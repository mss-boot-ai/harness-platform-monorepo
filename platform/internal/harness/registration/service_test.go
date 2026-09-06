package registration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

type capturedRegistration struct {
	challengeHash [32]byte
	owner         string
	tenant        string
	origin        string
	endpoint      domain.Endpoint
	access        domain.EndpointCredential
	refresh       domain.RefreshCredential
	audit         domain.SecurityAuditEvent
	now           time.Time
}

type fakePersistence struct {
	challenge domain.HCRegistrationChallenge
	captured  *capturedRegistration
}

func (fake *fakePersistence) CreateHCRegistrationChallenge(
	_ context.Context,
	challenge domain.HCRegistrationChallenge,
	_ time.Time,
	_ time.Duration,
) error {
	fake.challenge = challenge
	return nil
}

func (fake *fakePersistence) ConsumeHCRegistrationChallenge(
	_ context.Context,
	_ domain.ID,
	challengeHash [32]byte,
	owner string,
	tenant string,
	origin string,
	endpoint domain.Endpoint,
	access domain.EndpointCredential,
	refresh domain.RefreshCredential,
	audit domain.SecurityAuditEvent,
	now time.Time,
) error {
	fake.captured = &capturedRegistration{
		challengeHash: challengeHash, owner: owner, tenant: tenant, origin: origin,
		endpoint: endpoint, access: access, refresh: refresh, audit: audit, now: now,
	}
	return nil
}

func TestServiceIssuesAndRegistersHCWithProofOfPossession(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence := &fakePersistence{}
	service := Service{
		Persistence: persistence,
		Random:      deterministicBytes(512),
		Now:         func() time.Time { return now },
	}
	issued, err := service.IssueChallenge(context.Background(), "owner", "tenant", "https://HC.Example:443")
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	if issued.ExpiresAt != now.Add(2*time.Minute) || issued.ID != persistence.challenge.ID {
		t.Fatalf("unexpected challenge: %#v", issued)
	}
	challenge, err := base64.RawURLEncoding.Strict().DecodeString(issued.Challenge)
	if err != nil || len(challenge) != 32 {
		t.Fatalf("decode challenge: %v, length=%d", err, len(challenge))
	}
	if persistence.challenge.ChallengeHash != sha256.Sum256(challenge) {
		t.Fatal("persisted challenge hash does not match returned challenge")
	}

	signingJWK, signingKey := testSigningKey(t)
	kemJWK := testPublicJWK(t, 2)
	signingJKT, err := signingJWK.Thumbprint()
	if err != nil {
		t.Fatalf("signing JKT: %v", err)
	}
	kemJKT, err := kemJWK.Thumbprint()
	if err != nil {
		t.Fatalf("KEM JKT: %v", err)
	}
	transcript, err := BuildTranscript(TranscriptInput{
		ChallengeID: issued.ID, Challenge: challenge, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Origin: "https://hc.example", EndpointName: "H5 browser", Assurance: AssuranceWebSoftware,
		SoftwareVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("BuildTranscript: %v", err)
	}
	proof, err := awpcrypto.SignP1363LowS(signingKey, transcript)
	if err != nil {
		t.Fatalf("sign transcript: %v", err)
	}
	signingJSON, _ := json.Marshal(signingJWK)
	kemJSON, _ := json.Marshal(kemJWK)
	registration, err := service.Register(context.Background(), "owner", "tenant", "https://hc.example", RegisterInput{
		ChallengeID: issued.ID, Challenge: issued.Challenge, EndpointName: "H5 browser",
		SigningPublicJWK: signingJSON, KEMPublicJWK: kemJSON, Assurance: AssuranceWebSoftware,
		SoftwareVersion: "0.1.0", Proof: base64.RawURLEncoding.EncodeToString(proof),
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if persistence.captured == nil {
		t.Fatal("registration was not persisted")
	}
	if registration.EndpointID != persistence.captured.endpoint.ID || registration.SigningJKT != signingJKT || registration.KEMJKT != kemJKT {
		t.Fatalf("unexpected registration: %#v", registration)
	}
	accessRaw, err := base64.RawURLEncoding.Strict().DecodeString(registration.AccessToken)
	if err != nil {
		t.Fatalf("decode access token: %v", err)
	}
	refreshRaw, err := base64.RawURLEncoding.Strict().DecodeString(registration.RefreshToken)
	if err != nil {
		t.Fatalf("decode refresh token: %v", err)
	}
	if persistence.captured.access.TokenHash != sha256.Sum256(accessRaw) || persistence.captured.refresh.TokenHash != sha256.Sum256(refreshRaw) {
		t.Fatal("persisted credential hashes do not match returned opaque values")
	}
	if bytes.Contains(signingJSON, accessRaw) || bytes.Contains(kemJSON, refreshRaw) {
		t.Fatal("credential value leaked into public JWK")
	}
}

func TestServiceRejectsInvalidProofBeforePersistence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence := &fakePersistence{}
	service := Service{Persistence: persistence, Random: deterministicBytes(256), Now: func() time.Time { return now }}
	issued, err := service.IssueChallenge(context.Background(), "owner", "tenant", "http://127.0.0.1:4173")
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	signingJWK, _ := testSigningKey(t)
	kemJWK := testPublicJWK(t, 2)
	signingJSON, _ := json.Marshal(signingJWK)
	kemJSON, _ := json.Marshal(kemJWK)
	_, err = service.Register(context.Background(), "owner", "tenant", "http://127.0.0.1:4173", RegisterInput{
		ChallengeID: issued.ID, Challenge: issued.Challenge, EndpointName: "H5 browser",
		SigningPublicJWK: signingJSON, KEMPublicJWK: kemJSON, Assurance: AssuranceWebEphemeral,
		SoftwareVersion: "0.1.0", Proof: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 64)),
	})
	if !domain.HasCode(err, domain.CodeSecurityViolation) {
		t.Fatalf("invalid proof error = %v", err)
	}
	if persistence.captured != nil {
		t.Fatal("invalid proof reached persistence")
	}
}

func TestNormalizeOriginRejectsRemotePlaintextAndPaths(t *testing.T) {
	if _, err := NormalizeOrigin("http://platform.example"); err == nil {
		t.Fatal("remote plaintext origin was accepted")
	}
	if _, err := NormalizeOrigin("https://platform.example/path"); err == nil {
		t.Fatal("origin with path was accepted")
	}
	if got, err := NormalizeOrigin("http://LOCALHOST:80/"); err != nil || got != "http://localhost" {
		t.Fatalf("loopback origin = %q, error=%v", got, err)
	}
}

func testSigningKey(t *testing.T) (awpcrypto.P256PublicJWK, *ecdsa.PrivateKey) {
	t.Helper()
	d := big.NewInt(1)
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(fixedScalar(d))
	return publicJWK(x, y), &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
}

func testPublicJWK(t *testing.T, scalar int64) awpcrypto.P256PublicJWK {
	t.Helper()
	x, y := elliptic.P256().ScalarBaseMult(fixedScalar(big.NewInt(scalar)))
	return publicJWK(x, y)
}

func publicJWK(x, y *big.Int) awpcrypto.P256PublicJWK {
	return awpcrypto.P256PublicJWK{
		Curve: "P-256", KeyType: "EC",
		X: base64.RawURLEncoding.EncodeToString(fixedScalar(x)),
		Y: base64.RawURLEncoding.EncodeToString(fixedScalar(y)),
	}
}

func fixedScalar(value *big.Int) []byte {
	result := make([]byte, 32)
	value.FillBytes(result)
	return result
}

func deterministicBytes(length int) *bytes.Reader {
	value := make([]byte, length)
	for index := range value {
		value[index] = byte(index%251 + 1)
	}
	return bytes.NewReader(value)
}
