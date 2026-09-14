package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestEndpointStatusAndCreationCancellationUseExactScopedIDs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, hc, _, token, _, signing, jwk := gatewayFixture(t, now)
	handler, err := NewHandler(Config{AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082"}, persistence, deterministicGatewayBytes(32768), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	call := func(path, key string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(body)
		challenge := httptest.NewRecorder()
		request := gatewaySessionRequest(token, key, encoded, "")
		request.URL.Path = path
		handler.ServeHTTP(challenge, request)
		if challenge.Code != http.StatusUnauthorized {
			t.Fatalf("nonce status=%d", challenge.Code)
		}
		index++
		proof := signDPoPForMethodPath(t, signing, jwk, token, challenge.Header().Get("DPoP-Nonce"), now, fmt.Sprintf("00000000-0000-4000-8000-%012d", 2000+index), "POST", path)
		response := httptest.NewRecorder()
		request = gatewaySessionRequest(token, key, encoded, proof)
		request.URL.Path = path
		handler.ServeHTTP(response, request)
		return response
	}
	key := "cancel-creation-exact-key"
	operationPath := "/gateway/v1/session-operations/" + key
	if response := call(operationPath, key, map[string]any{}); response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"not-found"`) {
		t.Fatalf("absent creation: %d %s", response.Code, response.Body.String())
	}
	for i := 0; i < 2; i++ {
		if response := call(operationPath+"/cancel", key, map[string]any{}); response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"cancelled"`) {
			t.Fatalf("cancel creation: %d %s", response.Code, response.Body.String())
		}
	}
	if response := call("/gateway/v1/sessions", key, map[string]any{"abaEndpointId": gatewayID(70).String(), "runtimeProfileId": "agent", "workspaceId": "project", "requestedCapabilities": []string{"prompt", "session"}}); response.Code != 409 || !strings.Contains(response.Body.String(), "CREATION_CANCELLED") {
		t.Fatalf("late create: %d %s", response.Code, response.Body.String())
	}
	session := domain.Session{ID: gatewayID(71), OwnerUserID: hc.OwnerUserID, TenantID: hc.TenantID, ABAEndpointID: gatewayID(70), HCEndpointID: hc.ID,
		RuntimeProfileID: "agent", WorkspaceID: "project", RequestedCapabilities: []string{"prompt"}, Status: domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now}
	if err := persistence.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if response := call("/gateway/v1/sessions/"+session.ID.String()+"/status", key, map[string]any{}); response.Code != 200 || !strings.Contains(response.Body.String(), session.ID.String()) {
		t.Fatalf("exact session status: %d %s", response.Code, response.Body.String())
	}
	if _, err := persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
		value.StartupFailureCode = "WORKSPACE_BUSY"
		return value.Fail(now)
	}); err != nil {
		t.Fatal(err)
	}
	if response := call("/gateway/v1/sessions/"+session.ID.String()+"/status", key, map[string]any{}); response.Code != 200 || !strings.Contains(response.Body.String(), `"startupFailureCode":"WORKSPACE_BUSY"`) {
		t.Fatal("safe startup reason was not persisted and projected")
	}
	session.ID = gatewayID(72)
	session.HCEndpointID = gatewayID(73)
	if err := persistence.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if response := call("/gateway/v1/sessions/"+session.ID.String()+"/status", key, map[string]any{}); response.Code != 404 {
		t.Fatalf("foreign endpoint status exposed: %d", response.Code)
	}
	response := call("/gateway/v1/sessions/status", key, map[string]any{"sessionIds": []string{gatewayID(71).String(), gatewayID(72).String(), gatewayID(80).String()}})
	var batch struct {
		Items []endpointSessionResponse `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &batch); err != nil || response.Code != 200 || len(batch.Items) != 1 || batch.Items[0].SessionID != gatewayID(71).String() {
		t.Fatalf("scoped exact batch response: %d %s", response.Code, response.Body.String())
	}
}
