package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var managementRequestSequence atomic.Uint64

func managementRouter(t *testing.T, principal security.Verifier) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	persistence, err := store.New(db)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	router := gin.New()
	if err := registerRoutes(router.Group("/api"), business.Runtime{
		RequestDatabase: func(context.Context) (*gorm.DB, bool) { return db, true },
		Principal:       func(*gin.Context) security.Verifier { return principal },
		Events:          testEvents{},
	}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return router, persistence
}

func managementID(value byte) domain.ID {
	var id domain.ID
	for index := range id {
		id[index] = value
	}
	return id
}

func managementJKT(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func managementJWK(value byte) json.RawMessage {
	return json.RawMessage(`{"kty":"EC","crv":"P-256","x":"` +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32)) +
		`","y":"` +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value + 1}, 32)) +
		`"}`)
}

func managementEndpoint(id, family, key byte, kind domain.EndpointType, now time.Time) domain.Endpoint {
	return domain.Endpoint{
		ID:                 managementID(id),
		OwnerUserID:        "owner",
		TenantID:           "tenant",
		Type:               kind,
		Name:               string(kind),
		SigningPublicJWK:   managementJWK(key),
		KEMPublicJWK:       managementJWK(key + 2),
		SigningJKT:         managementJKT(key),
		KEMJKT:             managementJKT(key + 1),
		Status:             domain.EndpointStatusActive,
		CredentialFamilyID: managementID(family),
		SoftwareVersion:    "0.1.0",
		PlatformName:       "test",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func requestJSON(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	key := ""
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		key = fmt.Sprintf("test-idempotency-%016x", managementRequestSequence.Add(1))
	}
	return requestJSONWithKey(router, method, path, body, key)
}

func requestJSONWithKey(router http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestManagementEnrollmentRequiresUserCodeAndProjectsSafeState(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	now := time.Now().UTC()
	code := "ABCD-1234"
	enrollment := domain.Enrollment{
		ID:               managementID(1),
		EndpointType:     domain.EndpointTypeABA,
		EndpointName:     "Laptop ABA",
		DeviceCodeHash:   sha256.Sum256([]byte("device-code")),
		UserCodeHash:     hashUserCode(code),
		SigningPublicJWK: managementJWK(10),
		KEMPublicJWK:     managementJWK(12),
		SigningJKT:       managementJKT(10),
		KEMJKT:           managementJKT(11),
		Status:           domain.EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := persistence.CreateEnrollment(context.Background(), enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}

	wrong := requestJSON(t, router, http.MethodPost,
		"/api/harness/v1/enrollments/"+enrollment.ID.String()+"/approve",
		`{"userCode":"WRONG"}`,
	)
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong code status = %d body=%s", wrong.Code, wrong.Body.String())
	}

	approved := requestJSON(t, router, http.MethodPost,
		"/api/harness/v1/enrollments/"+enrollment.ID.String()+"/approve",
		`{"userCode":"ABCD 1234"}`,
	)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", approved.Code, approved.Body.String())
	}
	if strings.Contains(approved.Body.String(), "deviceCodeHash") || strings.Contains(approved.Body.String(), "signingPublicJwk") {
		t.Fatalf("approval leaked sensitive enrollment fields: %s", approved.Body.String())
	}

	listed := requestJSON(t, router, http.MethodGet, "/api/harness/v1/enrollments", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), enrollment.ID.String()) {
		t.Fatalf("list status = %d body=%s", listed.Code, listed.Body.String())
	}

	unknown := requestJSON(t, router, http.MethodPost,
		"/api/harness/v1/enrollments/"+enrollment.ID.String()+"/deny",
		`{"userCode":"ABCD-1234","command":"rm -rf /"}`,
	)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestManagementEndpointSessionAndDeliveryLifecycle(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	ctx := context.Background()
	now := time.Now().UTC()
	aba := managementEndpoint(1, 2, 10, domain.EndpointTypeABA, now)
	hc := managementEndpoint(3, 4, 20, domain.EndpointTypeHCWeb, now)
	if err := persistence.CreateEndpoint(ctx, aba); err != nil {
		t.Fatalf("Create ABA: %v", err)
	}
	if err := persistence.CreateEndpoint(ctx, hc); err != nil {
		t.Fatalf("Create HC: %v", err)
	}

	suspend := requestJSON(t, router, http.MethodPost, "/api/harness/v1/endpoints/"+hc.ID.String()+"/suspend", "")
	if suspend.Code != http.StatusOK || !strings.Contains(suspend.Body.String(), string(domain.EndpointStatusSuspended)) {
		t.Fatalf("suspend status = %d body=%s", suspend.Code, suspend.Body.String())
	}
	resume := requestJSON(t, router, http.MethodPost, "/api/harness/v1/endpoints/"+hc.ID.String()+"/resume", "{}")
	if resume.Code != http.StatusOK || !strings.Contains(resume.Body.String(), string(domain.EndpointStatusActive)) {
		t.Fatalf("resume status = %d body=%s", resume.Code, resume.Body.String())
	}

	session := domain.Session{
		ID:                    managementID(5),
		OwnerUserID:           "owner",
		TenantID:              "tenant",
		ABAEndpointID:         aba.ID,
		HCEndpointID:          hc.ID,
		RuntimeProfileID:      "test-agent",
		WorkspaceID:           "workspace",
		RequestedCapabilities: []string{"prompt"},
		Status:                domain.SessionStatusCreating,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := persistence.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
		if err := value.WaitForKey(now.Add(time.Second)); err != nil {
			return err
		}
		return value.Activate(1, now.Add(2*time.Second))
	}); err != nil {
		t.Fatalf("activate session: %v", err)
	}

	contentHash := sha256.Sum256([]byte("ciphertext"))
	frame := domain.EncryptedFrame{
		MessageID:          managementID(6),
		ChannelID:          managementID(7),
		SessionID:          session.ID,
		SenderEndpointID:   hc.ID,
		ReceiverEndpointID: aba.ID,
		Direction:          domain.DirectionHCToABA,
		Sequence:           1,
		KeyGeneration:      1,
		KeyID:              managementID(8),
		CreatedAtMS:        now.UnixMilli(),
		AAD:                bytes.Repeat([]byte{0x11}, 148),
		Ciphertext:         []byte("secret-ciphertext"),
		Signature:          bytes.Repeat([]byte{0x22}, 64),
		ContentHash:        contentHash,
		Status:             domain.FrameStatusStored,
		ReceivedAt:         now,
		ExpiresAt:          now.Add(time.Hour),
	}
	if _, err := persistence.PutFrame(ctx, frame, 1024); err != nil {
		t.Fatalf("PutFrame: %v", err)
	}
	if _, err := persistence.AdvanceAck(ctx, domain.AckCursor{
		SessionID:                 session.ID,
		KeyGeneration:             1,
		Direction:                 domain.DirectionHCToABA,
		SenderEndpointID:          hc.ID,
		ReceiverEndpointID:        aba.ID,
		HighestContiguousSequence: 1,
		UpdatedAt:                 now,
	}); err != nil {
		t.Fatalf("AdvanceAck: %v", err)
	}

	delivery := requestJSON(t, router, http.MethodGet, "/api/harness/v1/sessions/"+session.ID.String()+"/delivery", "")
	if delivery.Code != http.StatusOK {
		t.Fatalf("delivery status = %d body=%s", delivery.Code, delivery.Body.String())
	}
	if strings.Contains(delivery.Body.String(), "secret-ciphertext") || strings.Contains(delivery.Body.String(), base64.StdEncoding.EncodeToString(frame.AAD)) {
		t.Fatalf("delivery projection leaked encrypted material: %s", delivery.Body.String())
	}
	if !strings.Contains(delivery.Body.String(), `"ciphertextBytes":17`) {
		t.Fatalf("delivery projection missing safe metadata: %s", delivery.Body.String())
	}

	overview := requestJSON(t, router, http.MethodGet, "/api/harness/v1/overview", "")
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), `"activeEndpoints":2`) {
		t.Fatalf("overview status = %d body=%s", overview.Code, overview.Body.String())
	}

	closed := requestJSON(t, router, http.MethodPost, "/api/harness/v1/sessions/"+session.ID.String()+"/close", "")
	if closed.Code != http.StatusOK || !strings.Contains(closed.Body.String(), string(domain.SessionStatusClosed)) {
		t.Fatalf("close status = %d body=%s", closed.Code, closed.Body.String())
	}
	revoked := requestJSON(t, router, http.MethodPost, "/api/harness/v1/endpoints/"+hc.ID.String()+"/revoke", "")
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), string(domain.EndpointStatusRevoked)) {
		t.Fatalf("revoke status = %d body=%s", revoked.Code, revoked.Body.String())
	}
	if revoked.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", revoked.Header().Get("Cache-Control"))
	}
}

func TestManagementDoesNotExposeAnotherOwner(t *testing.T) {
	router, persistence := managementRouter(t, otherPrincipal{})
	now := time.Now().UTC()
	endpoint := managementEndpoint(1, 2, 10, domain.EndpointTypeABA, now)
	if err := persistence.CreateEndpoint(context.Background(), endpoint); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	response := requestJSON(t, router, http.MethodGet, "/api/harness/v1/endpoints", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), endpoint.ID.String()) {
		t.Fatalf("cross-owner endpoint was exposed: %s", response.Body.String())
	}
}

type otherPrincipal struct{ testPrincipal }

func (otherPrincipal) GetUserID() string   { return "other" }
func (otherPrincipal) GetTenantID() string { return "other-tenant" }
