package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTicketEndpointRequiresNonceBoundDPoPAndCreatesSingleUseTicket(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, credential, accessToken, signingKey, publicJWK := gatewayFixture(t, now)
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082",
	}, persistence, deterministicGatewayBytes(512), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	first := gatewayRequest(accessToken, "")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusUnauthorized || firstResponse.Header().Get("DPoP-Nonce") == "" {
		t.Fatalf("nonce challenge status=%d headers=%v body=%s", firstResponse.Code, firstResponse.Header(), firstResponse.Body.String())
	}
	nonce := firstResponse.Header().Get("DPoP-Nonce")
	proof := signDPoP(t, signingKey, publicJWK, accessToken, nonce, now, "00000000-0000-4000-8000-000000000101")
	second := gatewayRequest(accessToken, proof)
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusCreated || secondResponse.Header().Get("DPoP-Nonce") == "" {
		t.Fatalf("ticket status=%d headers=%v body=%s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}
	var body struct {
		Ticket       string    `json:"ticket"`
		ExpiresAt    time.Time `json:"expiresAt"`
		Protocol     string    `json:"protocol"`
		WebsocketURL string    `json:"websocketUrl"`
	}
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ticket response: %v", err)
	}
	if body.Protocol != protocolName || body.WebsocketURL != "ws://127.0.0.1:8082/gateway/v1/ws" || body.ExpiresAt != now.Add(30*time.Second) {
		t.Fatalf("unexpected ticket response: %#v", body)
	}
	ticketRaw, err := base64.RawURLEncoding.Strict().DecodeString(body.Ticket)
	if err != nil || len(ticketRaw) != 32 {
		t.Fatalf("decode ticket: %v length=%d", err, len(ticketRaw))
	}
	if _, err := persistence.ConsumeTicket(
		context.Background(), sha256.Sum256(ticketRaw), endpoint.ID, credential.ID,
		ticketPurpose, "http://127.0.0.1:8001", protocolName, now.Add(time.Second),
	); err != nil {
		t.Fatalf("ConsumeTicket: %v", err)
	}
	if _, err := persistence.ConsumeTicket(
		context.Background(), sha256.Sum256(ticketRaw), endpoint.ID, credential.ID,
		ticketPurpose, "http://127.0.0.1:8001", protocolName, now.Add(2*time.Second),
	); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("ticket replay error=%v, want conflict", err)
	}
}

func TestGatewayRejectsAdminCookieAndUntrustedOrigin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, _, _, accessToken, _, _ := gatewayFixture(t, now)
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082",
	}, persistence, deterministicGatewayBytes(256), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	adminRequest := httptest.NewRequest(http.MethodPost, "/gateway/v1/ws/tickets", nil)
	adminRequest.Header.Set("Origin", "http://127.0.0.1:8001")
	adminRequest.AddCookie(&http.Cookie{Name: "mss_admin_session", Value: "admin-cookie"})
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Admin cookie status=%d body=%s", adminResponse.Code, adminResponse.Body.String())
	}

	originRequest := gatewayRequest(accessToken, "")
	originRequest.Header.Set("Origin", "https://evil.example")
	originResponse := httptest.NewRecorder()
	handler.ServeHTTP(originResponse, originRequest)
	if originResponse.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin status=%d body=%s", originResponse.Code, originResponse.Body.String())
	}
}

func gatewayFixture(
	t *testing.T,
	now time.Time,
) (*store.Store, domain.Endpoint, domain.EndpointCredential, string, *ecdsa.PrivateKey, awpcrypto.P256PublicJWK) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open SQL database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	persistence, err := store.New(db)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	publicJWK, signingKey := gatewaySigningKey(1)
	kemJWK, _ := gatewaySigningKey(2)
	publicJSON, _ := json.Marshal(publicJWK)
	kemJSON, _ := json.Marshal(kemJWK)
	signingJKT, _ := publicJWK.Thumbprint()
	kemJKT, _ := kemJWK.Thumbprint()
	endpoint := domain.Endpoint{
		ID: gatewayID(1), OwnerUserID: "owner", TenantID: "tenant", Type: domain.EndpointTypeHCWeb,
		Name: "H5", SigningPublicJWK: publicJSON, KEMPublicJWK: kemJSON, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Status: domain.EndpointStatusActive, CredentialFamilyID: gatewayID(2), SoftwareVersion: "0.1.0",
		PlatformName: "web-software", CreatedAt: now, UpdatedAt: now,
	}
	accessRaw := bytes.Repeat([]byte{9}, 32)
	accessToken := base64.RawURLEncoding.EncodeToString(accessRaw)
	credential := domain.EndpointCredential{
		ID: gatewayID(3), EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: sha256.Sum256(accessRaw), SigningJKT: signingJKT,
		Scopes: []string{"endpoint:connect"}, Status: domain.CredentialStatusActive,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	refresh := domain.RefreshCredential{
		ID: gatewayID(4), EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: sha256.Sum256([]byte("refresh")), SigningJKT: signingJKT,
		Status: domain.CredentialStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	challenge := domain.HCRegistrationChallenge{
		ID: gatewayID(5), OwnerUserID: "owner", TenantID: "tenant", Origin: "http://127.0.0.1:8001",
		ChallengeHash: sha256.Sum256([]byte("challenge")), Status: domain.HCRegistrationChallengePending,
		ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateHCRegistrationChallenge(context.Background(), challenge, now, time.Minute); err != nil {
		t.Fatalf("CreateHCRegistrationChallenge: %v", err)
	}
	audit := domain.SecurityAuditEvent{
		ID: gatewayID(6), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorHuman,
		ActorID: "owner", Action: "hc.endpoint.register", ObjectType: "endpoint", ObjectID: endpoint.ID.String(),
		Result: "success", Metadata: map[string]string{"endpointType": string(domain.EndpointTypeHCWeb)}, CreatedAt: now,
	}
	if err := persistence.ConsumeHCRegistrationChallenge(
		context.Background(), challenge.ID, challenge.ChallengeHash, "owner", "tenant", challenge.Origin,
		endpoint, credential, refresh, audit, now,
	); err != nil {
		t.Fatalf("ConsumeHCRegistrationChallenge: %v", err)
	}
	return persistence, endpoint, credential, accessToken, signingKey, publicJWK
}

func gatewayRequest(accessToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/ws/tickets", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.Header.Set("Authorization", "DPoP "+accessToken)
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}

func signDPoP(
	t *testing.T,
	key *ecdsa.PrivateKey,
	publicJWK awpcrypto.P256PublicJWK,
	accessToken string,
	nonce string,
	now time.Time,
	jti string,
) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "ES256", "jwk": publicJWK, "typ": "dpop+jwt"})
	claims, _ := json.Marshal(map[string]any{
		"ath": awpcrypto.AccessTokenHash(accessToken), "htm": "POST",
		"htu": "http://127.0.0.1:8082/gateway/v1/ws/tickets", "iat": now.Unix(), "jti": jti, "nonce": nonce,
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	signature, err := awpcrypto.SignP1363LowS(key, []byte(signingInput))
	if err != nil {
		t.Fatalf("sign DPoP proof: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func gatewaySigningKey(scalar int64) (awpcrypto.P256PublicJWK, *ecdsa.PrivateKey) {
	curve := elliptic.P256()
	d := big.NewInt(scalar)
	x, y := curve.ScalarBaseMult(gatewayFixed(d))
	public := awpcrypto.P256PublicJWK{
		Curve: "P-256", KeyType: "EC",
		X: base64.RawURLEncoding.EncodeToString(gatewayFixed(x)),
		Y: base64.RawURLEncoding.EncodeToString(gatewayFixed(y)),
	}
	return public, &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
}

func gatewayID(value byte) domain.ID {
	var id domain.ID
	for index := range id {
		id[index] = value
	}
	return id
}

func gatewayFixed(value *big.Int) []byte {
	result := make([]byte, 32)
	value.FillBytes(result)
	return result
}

func deterministicGatewayBytes(length int) *bytes.Reader {
	value := make([]byte, length)
	for index := range value {
		value[index] = byte(index%251 + 1)
	}
	return bytes.NewReader(value)
}

func TestNewHandlerRejectsMissingPersistence(t *testing.T) {
	_, err := NewHandler(Config{AllowedOrigin: "https://hc.example", ExternalOrigin: "http://gateway.example"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "persistence") {
		t.Fatalf("nil persistence error = %v", err)
	}
}
