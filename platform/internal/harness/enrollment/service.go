package enrollment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

type Persistence interface {
	CreateEnrollment(context.Context, domain.Enrollment) error
	GetEnrollmentByDeviceCode(context.Context, domain.ID, [32]byte, time.Time) (domain.Enrollment, error)
	ConsumeABAEnrollment(context.Context, domain.ID, [32]byte, domain.Endpoint, domain.EndpointCredential, domain.RefreshCredential, domain.SecurityAuditEvent, time.Time) error
}

type Service struct {
	Persistence     Persistence
	Random          io.Reader
	Now             func() time.Time
	VerificationURI string
}

type StartInput struct {
	EndpointName     string
	SoftwareVersion  string
	SigningPublicJWK json.RawMessage
	KEMPublicJWK     json.RawMessage
	ClientNonce      string
	Proof            string
}

type Started struct {
	EnrollmentID    domain.ID
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	IntervalSeconds int
}

type Consumed struct {
	EndpointID       domain.ID
	CredentialID     domain.ID
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

func (service Service) Start(ctx context.Context, input StartInput) (Started, error) {
	if service.Persistence == nil || ctx == nil {
		return Started{}, errors.New("ABA enrollment service is unavailable")
	}
	signingJWK, kemJWK, signingJKT, kemJKT, nonce, proof, err := parseStartInput(input)
	if err != nil {
		return Started{}, err
	}
	transcript, err := StartTranscript(nonce, signingJKT, kemJKT, strings.TrimSpace(input.EndpointName), strings.TrimSpace(input.SoftwareVersion))
	if err != nil {
		return Started{}, domain.NewProblem(domain.CodeInvalidArgument, "ABA enrollment transcript is invalid", err)
	}
	publicKey, _ := signingJWK.PublicKey()
	if !awpcrypto.VerifyP1363LowS(publicKey, transcript, proof) {
		return Started{}, domain.NewProblem(domain.CodeSecurityViolation, "ABA enrollment proof is invalid", nil)
	}
	now := service.now()
	enrollmentID, err := domain.NewID(service.random())
	if err != nil {
		return Started{}, err
	}
	deviceRaw, err := service.randomBytes(32)
	if err != nil {
		return Started{}, err
	}
	userRaw, err := service.randomBytes(5)
	if err != nil {
		return Started{}, err
	}
	userPlain := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(userRaw)
	userCode := userPlain[:4] + "-" + userPlain[4:]
	signingJSON, _ := json.Marshal(signingJWK)
	kemJSON, _ := json.Marshal(kemJWK)
	expiresAt := now.Add(10 * time.Minute)
	record := domain.Enrollment{
		ID: enrollmentID, EndpointType: domain.EndpointTypeABA, EndpointName: strings.TrimSpace(input.EndpointName),
		DeviceCodeHash: sha256.Sum256(deviceRaw), UserCodeHash: sha256.Sum256([]byte(userPlain)),
		SigningPublicJWK: signingJSON, KEMPublicJWK: kemJSON, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Status: domain.EnrollmentStatusPending, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.Persistence.CreateEnrollment(ctx, record); err != nil {
		return Started{}, err
	}
	return Started{
		EnrollmentID: enrollmentID, DeviceCode: base64.RawURLEncoding.EncodeToString(deviceRaw), UserCode: userCode,
		VerificationURI: service.VerificationURI, ExpiresAt: expiresAt, IntervalSeconds: 2,
	}, nil
}

func (service Service) Poll(ctx context.Context, enrollmentID domain.ID, deviceCode string) (domain.Enrollment, error) {
	deviceRaw, err := decodeFixed(deviceCode, 32)
	if err != nil {
		return domain.Enrollment{}, domain.NewProblem(domain.CodeInvalidArgument, "device code is invalid", err)
	}
	return service.Persistence.GetEnrollmentByDeviceCode(ctx, enrollmentID, sha256.Sum256(deviceRaw), service.now())
}

func (service Service) Consume(ctx context.Context, enrollmentID domain.ID, deviceCode, clientNonce, proofText string) (Consumed, error) {
	deviceRaw, err := decodeFixed(deviceCode, 32)
	if err != nil {
		return Consumed{}, domain.NewProblem(domain.CodeInvalidArgument, "device code is invalid", err)
	}
	nonce, err := decodeFixed(clientNonce, 32)
	if err != nil {
		return Consumed{}, domain.NewProblem(domain.CodeInvalidArgument, "client nonce is invalid", err)
	}
	proof, err := decodeFixed(proofText, 64)
	if err != nil {
		return Consumed{}, domain.NewProblem(domain.CodeInvalidArgument, "consume proof is invalid", err)
	}
	now := service.now()
	enrollment, err := service.Persistence.GetEnrollmentByDeviceCode(ctx, enrollmentID, sha256.Sum256(deviceRaw), now)
	if err != nil {
		return Consumed{}, err
	}
	if enrollment.Status != domain.EnrollmentStatusApproved {
		return Consumed{}, domain.NewProblem(domain.CodeInvalidState, "enrollment is not approved", nil)
	}
	transcript, err := ConsumeTranscript(enrollmentID, deviceRaw, nonce)
	if err != nil {
		return Consumed{}, err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(enrollment.SigningPublicJWK)
	if err != nil {
		return Consumed{}, err
	}
	publicKey, _ := signingJWK.PublicKey()
	if !awpcrypto.VerifyP1363LowS(publicKey, transcript, proof) {
		return Consumed{}, domain.NewProblem(domain.CodeSecurityViolation, "consume proof is invalid", nil)
	}
	ids := make([]domain.ID, 5)
	for index := range ids {
		ids[index], err = domain.NewID(service.random())
		if err != nil {
			return Consumed{}, err
		}
	}
	accessRaw, err := service.randomBytes(32)
	if err != nil {
		return Consumed{}, err
	}
	refreshRaw, err := service.randomBytes(32)
	if err != nil {
		return Consumed{}, err
	}
	endpoint := domain.Endpoint{
		ID: ids[0], OwnerUserID: enrollment.OwnerUserID, TenantID: enrollment.TenantID, Type: domain.EndpointTypeABA,
		Name: enrollment.EndpointName, SigningPublicJWK: enrollment.SigningPublicJWK, KEMPublicJWK: enrollment.KEMPublicJWK,
		SigningJKT: enrollment.SigningJKT, KEMJKT: enrollment.KEMJKT, Status: domain.EndpointStatusActive,
		CredentialFamilyID: ids[1], SoftwareVersion: "0.1.0", PlatformName: "aba", CreatedAt: now, UpdatedAt: now,
	}
	accessExpiry, refreshExpiry := now.Add(10*time.Minute), now.Add(24*time.Hour)
	access := domain.EndpointCredential{
		ID: ids[2], EndpointID: endpoint.ID, FamilyID: ids[1], TokenHash: sha256.Sum256(accessRaw), SigningJKT: endpoint.SigningJKT,
		Scopes: []string{"endpoint:connect", "relay:write", "session:manage"}, Status: domain.CredentialStatusActive,
		ExpiresAt: accessExpiry, CreatedAt: now, UpdatedAt: now,
	}
	refresh := domain.RefreshCredential{
		ID: ids[3], EndpointID: endpoint.ID, FamilyID: ids[1], TokenHash: sha256.Sum256(refreshRaw), SigningJKT: endpoint.SigningJKT,
		Status: domain.CredentialStatusActive, ExpiresAt: refreshExpiry, CreatedAt: now, UpdatedAt: now,
	}
	audit := domain.SecurityAuditEvent{
		ID: ids[4], OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID, ActorType: domain.AuditActorEndpoint,
		ActorID: endpoint.ID.String(), Action: "aba.endpoint.enroll", ObjectType: "endpoint", ObjectID: endpoint.ID.String(),
		Result: "success", Metadata: map[string]string{"endpointType": string(endpoint.Type)}, CreatedAt: now,
	}
	if err := service.Persistence.ConsumeABAEnrollment(ctx, enrollmentID, sha256.Sum256(deviceRaw), endpoint, access, refresh, audit, now); err != nil {
		return Consumed{}, err
	}
	return Consumed{
		EndpointID: endpoint.ID, CredentialID: access.ID, AccessToken: base64.RawURLEncoding.EncodeToString(accessRaw),
		AccessExpiresAt: accessExpiry, RefreshToken: base64.RawURLEncoding.EncodeToString(refreshRaw), RefreshExpiresAt: refreshExpiry,
	}, nil
}

func StartTranscript(nonce []byte, signingJKT, kemJKT, name, version string) ([]byte, error) {
	if len(nonce) != 32 || name == "" || len(name) > 120 || version == "" || len(version) > 64 {
		return nil, errors.New("start transcript input is invalid")
	}
	signing, err := decodeFixed(signingJKT, 32)
	if err != nil {
		return nil, err
	}
	kem, err := decodeFixed(kemJKT, 32)
	if err != nil || bytes.Equal(signing, kem) {
		return nil, errors.New("enrollment JKT is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-aba-enrollment-start-v1")
	output.Write(nonce)
	output.Write(signing)
	output.Write(kem)
	writeText(&output, name)
	writeText(&output, version)
	return output.Bytes(), nil
}

func ConsumeTranscript(id domain.ID, deviceCode, nonce []byte) ([]byte, error) {
	if id.IsZero() || len(deviceCode) != 32 || len(nonce) != 32 {
		return nil, errors.New("consume transcript input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-aba-enrollment-consume-v1")
	output.Write(id[:])
	output.Write(deviceCode)
	output.Write(nonce)
	return output.Bytes(), nil
}

func parseStartInput(input StartInput) (awpcrypto.P256PublicJWK, awpcrypto.P256PublicJWK, string, string, []byte, []byte, error) {
	signing, err := awpcrypto.ParseP256PublicJWK(input.SigningPublicJWK)
	if err != nil {
		return awpcrypto.P256PublicJWK{}, awpcrypto.P256PublicJWK{}, "", "", nil, nil, domain.NewProblem(domain.CodeInvalidArgument, "signing JWK is invalid", err)
	}
	kem, err := awpcrypto.ParseP256PublicJWK(input.KEMPublicJWK)
	if err != nil {
		return awpcrypto.P256PublicJWK{}, awpcrypto.P256PublicJWK{}, "", "", nil, nil, domain.NewProblem(domain.CodeInvalidArgument, "KEM JWK is invalid", err)
	}
	signingJKT, _ := signing.Thumbprint()
	kemJKT, _ := kem.Thumbprint()
	nonce, err := decodeFixed(input.ClientNonce, 32)
	if err != nil {
		return signing, kem, "", "", nil, nil, err
	}
	proof, err := decodeFixed(input.Proof, 64)
	if err != nil {
		return signing, kem, "", "", nil, nil, err
	}
	return signing, kem, signingJKT, kemJKT, nonce, proof, nil
}

func decodeFixed(value string, length int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != length {
		return nil, fmt.Errorf("base64url value must be %d bytes", length)
	}
	return decoded, nil
}

func writeText(output *bytes.Buffer, value string) {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	output.Write(length[:])
	output.WriteString(value)
}

func (service Service) random() io.Reader {
	if service.Random != nil {
		return service.Random
	}
	return rand.Reader
}
func (service Service) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}
func (service Service) randomBytes(length int) ([]byte, error) {
	value := make([]byte, length)
	_, err := io.ReadFull(service.random(), value)
	return value, err
}
