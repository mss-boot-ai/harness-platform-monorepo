package gateway

import (
	"errors"
	"net/http"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

const catalogTTL = 15 * time.Minute

func (server *Server) publishCatalog(writer http.ResponseWriter, request *http.Request) {
	endpoint, _, ok := server.authenticateProfileRequest(writer, request, true)
	if !ok {
		return
	}
	var input struct {
		ConnectionGeneration uint64                  `json:"connectionGeneration,string"`
		Catalog              domain.ExecutionCatalog `json:"catalog"`
	}
	if err := decodeGatewayJSON(writer, request, &input); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_CATALOG", "catalog must contain only the documented public profile fields")
		return
	}
	revision, err := input.Catalog.Revision()
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	now := server.now().UTC()
	catalog := domain.PublishedCatalog{EndpointID: endpoint.ID, OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID,
		ConnectionGeneration: input.ConnectionGeneration, Revision: revision, Catalog: input.Catalog, PublishedAt: now, ExpiresAt: now.Add(catalogTTL)}
	err = server.connections.withGeneration(endpoint.ID, input.ConnectionGeneration, func() error {
		return server.persistence.PublishExecutionCatalog(request.Context(), catalog)
	})
	if errors.Is(err, errConnectionFenced) {
		writeGatewayError(writer, http.StatusConflict, "CATALOG_GENERATION_STALE", "catalog publisher is not the active connection")
		return
	}
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"revision": revision, "expiresAt": catalog.ExpiresAt})
}

func (server *Server) executionCatalogView(request *http.Request, value domain.Endpoint) map[string]any {
	now := server.now().UTC()
	generation, online := server.connections.generation(value.ID)
	result := map[string]any{"status": "not-published", "revision": "", "runtimes": []domain.CatalogRuntime{}, "workspaces": []domain.CatalogWorkspace{}}
	catalog, err := server.persistence.GetExecutionCatalog(request.Context(), value.OwnerUserID, value.TenantID, value.ID, now)
	if err == nil {
		result["revision"] = catalog.Revision
		result["publishedAt"] = catalog.PublishedAt
		result["expiresAt"] = catalog.ExpiresAt
		result["runtimes"] = catalog.Catalog.Runtimes
		result["workspaces"] = catalog.Catalog.Workspaces
		if online && catalog.ConnectionGeneration == generation {
			result["status"] = "ready"
		}
	} else if domain.HasCode(err, domain.CodeExpired) {
		result["status"] = "stale"
	} else if !domain.HasCode(err, domain.CodeNotFound) {
		result["status"] = "unavailable"
	}
	if !online {
		result["status"] = "offline"
	}
	return result
}
