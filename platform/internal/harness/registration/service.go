package registration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

const (
	challengeBytes = 32
	tokenBytes     = 32
)

type Persistence interface {
	CreateHCRegistrationChallenge(context.Context, domain.HCRegistrationChallenge, time.Time, time.Duration) error
	ConsumeHCRegistrationChallenge(
		context.Context,
		domain.ID,
		[32]byte,
		string,
		string,
		string,
		domain.Endpoint,
		domain.EndpointCredential,
		domain.RefreshCredential,
		domain.SecurityAuditEvent,
		time.Time,
	) error
}

type Service struct {
	Persistence  Persistence
	Random       io.Reader
	Now          func() time.Time
	ChallengeTTL time.Duration
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
}

type IssuedChallenge struct {
	ID        domain.ID
	Challenge string
	ExpiresAt time.Time
}

type RegisterInput struct {
	ChallengeID      domain.ID
	Challenge        string
	EndpointName     string
	SigningPublicJWK json.RawMessage
	KEMPublicJWK     json.RawMessage
	Assurance        Assurance
	SoftwareVersion  string
	Proof            string
}

type Registration struct {
	EndpointID       domain.ID
	CredentialID     domain.ID
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	SigningJKT       string
	KEMJKT           string
}

func (service Service) IssueChallenge(
	ctx context.Context,
	owner string,
	tenant string,
	origin string,
) (IssuedChallenge, error) {
	if service.Persistence == nil || ctx == nil {
		return IssuedChallenge{}, errors.New("HC registration service is unavailable")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return IssuedChallenge{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration owner is required", nil)
	}
	normalizedOrigin, err := NormalizeOrigin(origin)
	if err != nil {
		return IssuedChallenge{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration origin is invalid", err)
	}
	now := service.now()
	ttl := service.challengeTTL()
	id, err := domain.NewID(service.random())
	if err != nil {
		return IssuedChallenge{}, err
	}
	challenge := make([]byte, challengeBytes)
	if _, err := io.ReadFull(service.random(), challenge); err != nil {
		return IssuedChallenge{}, fmt.Errorf("generate HC registration challenge: %w", err)
	}
	expiresAt := now.Add(ttl)
	record := domain.HCRegistrationChallenge{
		ID: id, OwnerUserID: owner, TenantID: strings.TrimSpace(tenant), Origin: normalizedOrigin,
		ChallengeHash: sha256.Sum256(challenge), Status: domain.HCRegistrationChallengePending,
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.Persistence.CreateHCRegistrationChallenge(ctx, record, now, ttl); err != nil {
		return IssuedChallenge{}, err
	}
	return IssuedChallenge{ID: id, Challenge: base64.RawURLEncoding.EncodeToString(challenge), ExpiresAt: expiresAt}, nil
}

func (service Service) Register(
	ctx context.Context,
	owner string,
	tenant string,
	origin string,
	input RegisterInput,
) (Registration, error) {
	if service.Persistence == nil || ctx == nil {
		return Registration{}, errors.New("HC registration service is unavailable")
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)
	normalizedOrigin, err := NormalizeOrigin(origin)
	if err != nil || owner == "" {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration scope is invalid", err)
	}
	challenge, err := base64.RawURLEncoding.Strict().DecodeString(input.Challenge)
	if err != nil || len(challenge) != challengeBytes {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration challenge is invalid", err)
	}
	proof, err := base64.RawURLEncoding.Strict().DecodeString(input.Proof)
	if err != nil || len(proof) != 64 {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration proof is invalid", err)
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(input.SigningPublicJWK)
	if err != nil {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC signing JWK is invalid", err)
	}
	kemJWK, err := awpcrypto.ParseP256PublicJWK(input.KEMPublicJWK)
	if err != nil {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC KEM JWK is invalid", err)
	}
	signingJKT, err := signingJWK.Thumbprint()
	if err != nil {
		return Registration{}, err
	}
	kemJKT, err := kemJWK.Thumbprint()
	if err != nil {
		return Registration{}, err
	}
	transcript, err := BuildTranscript(TranscriptInput{
		ChallengeID: input.ChallengeID, Challenge: challenge, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Origin: normalizedOrigin, EndpointName: strings.TrimSpace(input.EndpointName),
		Assurance: input.Assurance, SoftwareVersion: strings.TrimSpace(input.SoftwareVersion),
	})
	if err != nil {
		return Registration{}, domain.NewProblem(domain.CodeInvalidArgument, "HC registration transcript is invalid", err)
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, transcript, proof) {
		return Registration{}, domain.NewProblem(domain.CodeSecurityViolation, "HC registration proof is invalid", err)
	}

	now := service.now()
	endpointID, familyID, accessID, refreshID, auditID, err := service.newIDs()
	if err != nil {
		return Registration{}, err
	}
	accessToken, err := service.newToken()
	if err != nil {
		return Registration{}, err
	}
	refreshToken, err := service.newToken()
	if err != nil {
		return Registration{}, err
	}
	signingJSON, err := json.Marshal(signingJWK)
	if err != nil {
		return Registration{}, fmt.Errorf("encode HC signing JWK: %w", err)
	}
	kemJSON, err := json.Marshal(kemJWK)
	if err != nil {
		return Registration{}, fmt.Errorf("encode HC KEM JWK: %w", err)
	}
	endpoint := domain.Endpoint{
		ID: endpointID, OwnerUserID: owner, TenantID: tenant, Type: domain.EndpointTypeHCWeb,
		Name: strings.TrimSpace(input.EndpointName), SigningPublicJWK: signingJSON, KEMPublicJWK: kemJSON,
		SigningJKT: signingJKT, KEMJKT: kemJKT, Status: domain.EndpointStatusActive,
		CredentialFamilyID: familyID, SoftwareVersion: strings.TrimSpace(input.SoftwareVersion),
		PlatformName: string(input.Assurance), CreatedAt: now, UpdatedAt: now,
	}
	accessExpiresAt := now.Add(service.accessTTL())
	accessCredential := domain.EndpointCredential{
		ID: accessID, EndpointID: endpointID, FamilyID: familyID, TokenHash: sha256.Sum256(accessToken),
		SigningJKT: signingJKT, Scopes: []string{"endpoint:connect", "relay:write", "session:manage"},
		Status: domain.CredentialStatusActive, ExpiresAt: accessExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	refreshExpiresAt := now.Add(service.refreshTTL())
	refreshCredential := domain.RefreshCredential{
		ID: refreshID, EndpointID: endpointID, FamilyID: familyID, TokenHash: sha256.Sum256(refreshToken),
		SigningJKT: signingJKT, Status: domain.CredentialStatusActive,
		ExpiresAt: refreshExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	audit := domain.SecurityAuditEvent{
		ID: auditID, OwnerUserID: owner, TenantID: tenant, ActorType: domain.AuditActorHuman,
		ActorID: owner, Action: "hc.endpoint.register", ObjectType: "endpoint", ObjectID: endpointID.String(),
		Result: "success", Metadata: map[string]string{"endpointType": string(domain.EndpointTypeHCWeb)}, CreatedAt: now,
	}
	if err := service.Persistence.ConsumeHCRegistrationChallenge(
		ctx, input.ChallengeID, sha256.Sum256(challenge), owner, tenant, normalizedOrigin,
		endpoint, accessCredential, refreshCredential, audit, now,
	); err != nil {
		return Registration{}, err
	}
	return Registration{
		EndpointID: endpointID, CredentialID: accessID, AccessToken: base64.RawURLEncoding.EncodeToString(accessToken),
		AccessExpiresAt: accessExpiresAt, RefreshToken: base64.RawURLEncoding.EncodeToString(refreshToken),
		RefreshExpiresAt: refreshExpiresAt, SigningJKT: signingJKT, KEMJKT: kemJKT,
	}, nil
}

func NormalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must contain only scheme and authority")
	}
	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if scheme != "https" {
		ip := net.ParseIP(hostname)
		if scheme != "http" || (hostname != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return "", errors.New("origin must use HTTPS except for loopback development")
		}
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, nil
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

func (service Service) challengeTTL() time.Duration {
	if service.ChallengeTTL > 0 && service.ChallengeTTL <= 5*time.Minute {
		return service.ChallengeTTL
	}
	return 2 * time.Minute
}

func (service Service) accessTTL() time.Duration {
	if service.AccessTTL > 0 && service.AccessTTL <= 15*time.Minute {
		return service.AccessTTL
	}
	return 10 * time.Minute
}

func (service Service) refreshTTL() time.Duration {
	if service.RefreshTTL > 0 && service.RefreshTTL <= 30*24*time.Hour {
		return service.RefreshTTL
	}
	return 24 * time.Hour
}

func (service Service) newIDs() (domain.ID, domain.ID, domain.ID, domain.ID, domain.ID, error) {
	values := make([]domain.ID, 5)
	for index := range values {
		value, err := domain.NewID(service.random())
		if err != nil {
			return domain.ID{}, domain.ID{}, domain.ID{}, domain.ID{}, domain.ID{}, err
		}
		values[index] = value
	}
	return values[0], values[1], values[2], values[3], values[4], nil
}

func (service Service) newToken() ([]byte, error) {
	value := make([]byte, tokenBytes)
	if _, err := io.ReadFull(service.random(), value); err != nil {
		return nil, fmt.Errorf("generate HC credential: %w", err)
	}
	return value, nil
}
