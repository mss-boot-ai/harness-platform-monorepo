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
	persistence, endpoint, credential, accessToken, _, signingKey, publicJWK := gatewayFixture(t, now)
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
	persistence, _, _, accessToken, _, _, _ := gatewayFixture(t, now)
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

func TestRefreshEndpointRotatesFamilyAndDetectsOldCredentialReuse(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, _, _, refreshToken, signingKey, publicJWK := gatewayFixture(t, now)
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082",
	}, persistence, deterministicGatewayBytes(1024), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	challengeResponse := httptest.NewRecorder()
	handler.ServeHTTP(challengeResponse, gatewayRefreshRequest(refreshToken, ""))
	nonce := challengeResponse.Header().Get("DPoP-Nonce")
	if challengeResponse.Code != http.StatusUnauthorized || nonce == "" {
		t.Fatalf("refresh nonce status=%d headers=%v body=%s", challengeResponse.Code, challengeResponse.Header(), challengeResponse.Body.String())
	}
	proof := signRefreshDPoP(t, signingKey, publicJWK, nonce, now, "00000000-0000-4000-8000-000000000201")
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, gatewayRefreshRequest(refreshToken, proof))
	if refreshResponse.Code != http.StatusOK || refreshResponse.Header().Get("DPoP-Nonce") == "" {
		t.Fatalf("refresh status=%d headers=%v body=%s", refreshResponse.Code, refreshResponse.Header(), refreshResponse.Body.String())
	}
	var response struct {
		AccessToken string `json:"accessToken"`
		EndpointID  string `json:"endpointId"`
	}
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	accessRaw, err := base64.RawURLEncoding.Strict().DecodeString(response.AccessToken)
	if err != nil || response.EndpointID != endpoint.ID.String() {
		t.Fatalf("rotated response=%#v decode=%v", response, err)
	}
	if _, _, err := persistence.AuthenticateAccessToken(context.Background(), sha256.Sum256(accessRaw), now); err != nil {
		t.Fatalf("rotated access token did not authenticate: %v", err)
	}
	cookies := refreshResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" || cookies[0].Value == refreshToken || !cookies[0].HttpOnly {
		t.Fatalf("rotated refresh cookie=%#v", cookies)
	}

	reuseNonce := refreshResponse.Header().Get("DPoP-Nonce")
	reuseProof := signRefreshDPoP(t, signingKey, publicJWK, reuseNonce, now, "00000000-0000-4000-8000-000000000202")
	reuseResponse := httptest.NewRecorder()
	handler.ServeHTTP(reuseResponse, gatewayRefreshRequest(refreshToken, reuseProof))
	if reuseResponse.Code != http.StatusForbidden {
		t.Fatalf("refresh reuse status=%d body=%s", reuseResponse.Code, reuseResponse.Body.String())
	}
	if _, _, err := persistence.AuthenticateAccessToken(context.Background(), sha256.Sum256(accessRaw), now); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("rotated family access error=%v, want revoked", err)
	}
}

func TestABARefreshUsesNativeHeaderAndIssuesNativeTicket(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, _, _, refreshToken, signingKey, publicJWK := abaGatewayFixture(t, now)
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://localhost:8001",
		NativeExternalOrigin: "http://127.0.0.1:8082",
	}, persistence, deterministicGatewayBytes(1024), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	challengeResponse := httptest.NewRecorder()
	handler.ServeHTTP(challengeResponse, gatewayNativeRefreshRequest(refreshToken, ""))
	nonce := challengeResponse.Header().Get("DPoP-Nonce")
	if challengeResponse.Code != http.StatusUnauthorized || nonce == "" {
		t.Fatalf("native refresh nonce status=%d headers=%v body=%s", challengeResponse.Code, challengeResponse.Header(), challengeResponse.Body.String())
	}
	proof := signRefreshDPoP(t, signingKey, publicJWK, nonce, now, "00000000-0000-4000-8000-000000000301")
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, gatewayNativeRefreshRequest(refreshToken, proof))
	if refreshResponse.Code != http.StatusOK || refreshResponse.Header().Get("DPoP-Nonce") == "" {
		t.Fatalf("native refresh status=%d headers=%v body=%s", refreshResponse.Code, refreshResponse.Header(), refreshResponse.Body.String())
	}
	if cookies := refreshResponse.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("native refresh unexpectedly set cookies: %#v", cookies)
	}
	var refreshed struct {
		AccessToken     string    `json:"accessToken"`
		RefreshToken    string    `json:"refreshToken"`
		RefreshExpires  time.Time `json:"refreshExpiresAt"`
		EndpointID      string    `json:"endpointId"`
		AccessExpiresAt time.Time `json:"accessExpiresAt"`
	}
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("decode native refresh: %v", err)
	}
	if refreshed.EndpointID != endpoint.ID.String() || refreshed.AccessToken == "" ||
		refreshed.RefreshToken == "" || refreshed.RefreshToken == refreshToken || refreshed.RefreshExpires.IsZero() {
		t.Fatalf("native refresh response did not contain rotated credentials: %#v", refreshed)
	}

	ticketChallenge := httptest.NewRecorder()
	handler.ServeHTTP(ticketChallenge, gatewayNativeTicketRequest(refreshed.AccessToken, ""))
	ticketNonce := ticketChallenge.Header().Get("DPoP-Nonce")
	if ticketChallenge.Code != http.StatusUnauthorized || ticketNonce == "" {
		t.Fatalf("native ticket nonce status=%d headers=%v body=%s", ticketChallenge.Code, ticketChallenge.Header(), ticketChallenge.Body.String())
	}
	ticketProof := signDPoP(t, signingKey, publicJWK, refreshed.AccessToken, ticketNonce, now, "00000000-0000-4000-8000-000000000302")
	ticketResponse := httptest.NewRecorder()
	handler.ServeHTTP(ticketResponse, gatewayNativeTicketRequest(refreshed.AccessToken, ticketProof))
	if ticketResponse.Code != http.StatusCreated {
		t.Fatalf("native ticket status=%d body=%s", ticketResponse.Code, ticketResponse.Body.String())
	}
	var issued struct {
		Ticket       string `json:"ticket"`
		WebsocketURL string `json:"websocketUrl"`
	}
	if err := json.Unmarshal(ticketResponse.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decode native ticket: %v", err)
	}
	if issued.WebsocketURL != "ws://127.0.0.1:8082/gateway/v1/ws" {
		t.Fatalf("native WebSocket URL=%q", issued.WebsocketURL)
	}
	ticketRaw, err := base64.RawURLEncoding.Strict().DecodeString(issued.Ticket)
	if err != nil || len(ticketRaw) != 32 {
		t.Fatalf("decode native ticket: %v length=%d", err, len(ticketRaw))
	}
	ticket, err := persistence.InspectTicket(t.Context(), sha256.Sum256(ticketRaw), now)
	if err != nil || ticket.Origin != nativeABAOrigin {
		t.Fatalf("native ticket=%#v error=%v", ticket, err)
	}

	cookieRequest := gatewayRefreshRequest(refreshed.RefreshToken, "")
	cookieResponse := httptest.NewRecorder()
	handler.ServeHTTP(cookieResponse, cookieRequest)
	if cookieResponse.Code != http.StatusForbidden {
		t.Fatalf("ABA cookie refresh status=%d body=%s", cookieResponse.Code, cookieResponse.Body.String())
	}
}

func gatewayFixture(
	t *testing.T,
	now time.Time,
) (*store.Store, domain.Endpoint, domain.EndpointCredential, string, string, *ecdsa.PrivateKey, awpcrypto.P256PublicJWK) {
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
		Scopes: []string{"endpoint:connect", "session:manage"}, Status: domain.CredentialStatusActive,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	refreshRaw := bytes.Repeat([]byte{8}, 32)
	refreshToken := base64.RawURLEncoding.EncodeToString(refreshRaw)
	refresh := domain.RefreshCredential{
		ID: gatewayID(4), EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: sha256.Sum256(refreshRaw), SigningJKT: signingJKT,
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
	return persistence, endpoint, credential, accessToken, refreshToken, signingKey, publicJWK
}

func abaGatewayFixture(
	t *testing.T,
	now time.Time,
) (*store.Store, domain.Endpoint, domain.EndpointCredential, string, string, *ecdsa.PrivateKey, awpcrypto.P256PublicJWK) {
	t.Helper()
	persistence, _, _, _, _, _, _ := gatewayFixture(t, now)
	publicJWK, signingKey := gatewaySigningKey(7)
	kemJWK, _ := gatewaySigningKey(8)
	publicJSON, _ := json.Marshal(publicJWK)
	kemJSON, _ := json.Marshal(kemJWK)
	signingJKT, _ := publicJWK.Thumbprint()
	kemJKT, _ := kemJWK.Thumbprint()
	deviceHash := sha256.Sum256([]byte("aba-device-code"))
	userHash := sha256.Sum256([]byte("ABACODE1"))
	enrollment := domain.Enrollment{
		ID: gatewayID(11), EndpointType: domain.EndpointTypeABA, EndpointName: "Test ABA",
		DeviceCodeHash: deviceHash, UserCodeHash: userHash,
		SigningPublicJWK: publicJSON, KEMPublicJWK: kemJSON, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Status: domain.EnrollmentStatusPending, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateEnrollment(t.Context(), enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	if _, err := persistence.ApproveEnrollmentWithCode(t.Context(), enrollment.ID, userHash, "owner", "tenant", now); err != nil {
		t.Fatalf("ApproveEnrollmentWithCode: %v", err)
	}
	endpoint := domain.Endpoint{
		ID: gatewayID(12), OwnerUserID: "owner", TenantID: "tenant", Type: domain.EndpointTypeABA,
		Name: "Test ABA", SigningPublicJWK: publicJSON, KEMPublicJWK: kemJSON, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Status: domain.EndpointStatusActive, CredentialFamilyID: gatewayID(13), SoftwareVersion: "0.1.0",
		PlatformName: "aba", CreatedAt: now, UpdatedAt: now,
	}
	accessRaw := bytes.Repeat([]byte{19}, 32)
	accessToken := base64.RawURLEncoding.EncodeToString(accessRaw)
	credential := domain.EndpointCredential{
		ID: gatewayID(14), EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: sha256.Sum256(accessRaw), SigningJKT: signingJKT,
		Scopes: []string{"endpoint:connect"}, Status: domain.CredentialStatusActive,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	refreshRaw := bytes.Repeat([]byte{18}, 32)
	refreshToken := base64.RawURLEncoding.EncodeToString(refreshRaw)
	refresh := domain.RefreshCredential{
		ID: gatewayID(15), EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: sha256.Sum256(refreshRaw), SigningJKT: signingJKT,
		Status: domain.CredentialStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	audit := domain.SecurityAuditEvent{
		ID: gatewayID(16), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorEndpoint,
		ActorID: endpoint.ID.String(), Action: "aba.endpoint.enroll", ObjectType: "endpoint", ObjectID: endpoint.ID.String(),
		Result: "success", Metadata: map[string]string{"endpointType": string(domain.EndpointTypeABA)}, CreatedAt: now,
	}
	if err := persistence.ConsumeABAEnrollment(t.Context(), enrollment.ID, deviceHash, endpoint, credential, refresh, audit, now); err != nil {
		t.Fatalf("ConsumeABAEnrollment: %v", err)
	}
	return persistence, endpoint, credential, accessToken, refreshToken, signingKey, publicJWK
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

func gatewayRefreshRequest(refreshToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/tokens/refresh", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.AddCookie(&http.Cookie{Name: "harness_hc_refresh", Value: refreshToken})
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}

func gatewayNativeRefreshRequest(refreshToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/tokens/refresh", nil)
	request.Header.Set("Authorization", "Refresh "+refreshToken)
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}

func gatewayNativeTicketRequest(accessToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/ws/tickets", nil)
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
	return signDPoPForPath(t, key, publicJWK, accessToken, nonce, now, jti, "/gateway/v1/ws/tickets")
}

func signDPoPForPath(
	t *testing.T,
	key *ecdsa.PrivateKey,
	publicJWK awpcrypto.P256PublicJWK,
	accessToken string,
	nonce string,
	now time.Time,
	jti string,
	path string,
) string {
	return signDPoPForMethodPath(t, key, publicJWK, accessToken, nonce, now, jti, "POST", path)
}

func signDPoPForMethodPath(
	t *testing.T,
	key *ecdsa.PrivateKey,
	publicJWK awpcrypto.P256PublicJWK,
	accessToken string,
	nonce string,
	now time.Time,
	jti string,
	method string,
	path string,
) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "ES256", "jwk": publicJWK, "typ": "dpop+jwt"})
	claims, _ := json.Marshal(map[string]any{
		"ath": awpcrypto.AccessTokenHash(accessToken), "htm": method,
		"htu": "http://127.0.0.1:8082" + path, "iat": now.Unix(), "jti": jti, "nonce": nonce,
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	signature, err := awpcrypto.SignP1363LowS(key, []byte(signingInput))
	if err != nil {
		t.Fatalf("sign DPoP proof: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func signRefreshDPoP(
	t *testing.T,
	key *ecdsa.PrivateKey,
	publicJWK awpcrypto.P256PublicJWK,
	nonce string,
	now time.Time,
	jti string,
) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "ES256", "jwk": publicJWK, "typ": "dpop+jwt"})
	claims, _ := json.Marshal(map[string]any{
		"htm": "POST", "htu": "http://127.0.0.1:8082/gateway/v1/tokens/refresh",
		"iat": now.Unix(), "jti": jti, "nonce": nonce,
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	signature, err := awpcrypto.SignP1363LowS(key, []byte(signingInput))
	if err != nil {
		t.Fatalf("sign refresh DPoP proof: %v", err)
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
