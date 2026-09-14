package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestCatalogPublicationRequiresABAProofAndCurrentConnection(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, aba, _, token, _, signing, jwk := abaGatewayFixture(t, now)
	generation, err := persistence.NextConnectionGeneration(t.Context(), aba.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	directory := newMemoryConnectionDirectory()
	active := newActiveConnection(aba.ID, generation, [16]byte{1}, [32]byte{1}, nil)
	directory.activate(active)
	server := &Server{config: Config{AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082", NativeExternalOrigin: "http://127.0.0.1:8082", NonceTTL: time.Minute, ReplayMaxEntries: 1000},
		persistence: persistence, connections: directory, random: deterministicGatewayBytes(8192), now: func() time.Time { return now }}
	document := domain.ExecutionCatalog{Version: 1, Runtimes: []domain.CatalogRuntime{{ID: "agent", DisplayName: "Agent"}}, Workspaces: []domain.CatalogWorkspace{{ID: "project", DisplayName: "Project", RuntimeIDs: []string{"agent"}}}}
	index := 0
	request := func(body any, origin string) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(body)
		build := func(proof string) *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/gateway/v1/catalog", bytes.NewReader(encoded))
			r.Header.Set("Authorization", "DPoP "+token)
			r.Header.Set("Content-Type", "application/json")
			if proof != "" {
				r.Header.Set("DPoP", proof)
			}
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			return r
		}
		challenge := httptest.NewRecorder()
		server.publishCatalog(challenge, build(""))
		if challenge.Code != http.StatusUnauthorized {
			return challenge
		}
		index++
		proof := signDPoPForMethodPath(t, signing, jwk, token, challenge.Header().Get("DPoP-Nonce"), now,
			fmt.Sprintf("00000000-0000-4000-8000-%012d", 800+index), "POST", "/gateway/v1/catalog")
		response := httptest.NewRecorder()
		server.publishCatalog(response, build(proof))
		return response
	}
	body := map[string]any{"connectionGeneration": fmt.Sprint(generation), "catalog": document}
	if response := request(body, "http://127.0.0.1:8001"); response.Code != http.StatusForbidden {
		t.Fatalf("browser publication status=%d", response.Code)
	}
	if response := request(body, ""); response.Code != http.StatusOK {
		t.Fatalf("publication status=%d body=%s", response.Code, response.Body.String())
	}
	if view := server.executionCatalogView(httptest.NewRequest(http.MethodPost, "/", nil), aba); view["status"] != "ready" {
		t.Fatalf("catalog state=%v", view["status"])
	}
	body["command"] = "/private/not-public"
	if response := request(body, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("prohibited field accepted: %d", response.Code)
	}
	delete(body, "command")
	body["connectionGeneration"] = fmt.Sprint(generation + 1)
	if response := request(body, ""); response.Code != http.StatusConflict {
		t.Fatalf("wrong generation accepted: %d", response.Code)
	}
	active.close(0, "test")
	if view := server.executionCatalogView(httptest.NewRequest(http.MethodPost, "/", nil), aba); view["status"] != "offline" {
		t.Fatalf("closed connection selectable: %v", view["status"])
	}
}
