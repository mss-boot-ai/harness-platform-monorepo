package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func endpointSessionView(session domain.Session) endpointSessionResponse {
	return endpointSessionResponse{SessionID: session.ID.String(), ABAEndpointID: session.ABAEndpointID.String(), HCEndpointID: session.HCEndpointID.String(),
		RuntimeProfileID: session.RuntimeProfileID, WorkspaceID: session.WorkspaceID, RequestedCapabilities: append([]string(nil), session.RequestedCapabilities...), Status: session.Status, CreatedAt: session.CreatedAt}
}

func (server *Server) endpointSessionStatus(writer http.ResponseWriter, request *http.Request) {
	endpoint, _, ok := server.authenticateHCRequest(writer, request)
	if !ok {
		return
	}
	var empty struct{}
	if err := decodeGatewayJSON(writer, request, &empty); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "empty status request required")
		return
	}
	id, err := domain.ParseID(request.PathValue("sessionId"))
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	session, err := server.persistence.GetSession(request.Context(), id)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if session.HCEndpointID != endpoint.ID || session.OwnerUserID != endpoint.OwnerUserID || session.TenantID != endpoint.TenantID {
		writeDomainError(writer, domain.NewProblem(domain.CodeNotFound, "session was not found", nil))
		return
	}
	writeJSON(writer, http.StatusOK, endpointSessionView(session))
}

func (server *Server) sessionCreationStatus(writer http.ResponseWriter, request *http.Request) {
	server.sessionCreationOperation(writer, request, false)
}

func (server *Server) cancelSessionCreation(writer http.ResponseWriter, request *http.Request) {
	server.sessionCreationOperation(writer, request, true)
}

func (server *Server) sessionCreationOperation(writer http.ResponseWriter, request *http.Request, cancel bool) {
	endpoint, _, ok := server.authenticateHCRequest(writer, request)
	if !ok {
		return
	}
	var empty struct{}
	if err := decodeGatewayJSON(writer, request, &empty); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "empty operation request required")
		return
	}
	key := request.PathValue("operationKey")
	if err := domain.ValidateIdempotencyKey(key); err != nil {
		writeDomainError(writer, err)
		return
	}
	var operation domain.IdempotencyRecord
	var err error
	if cancel {
		id, randomErr := domain.NewID(server.random)
		if randomErr != nil {
			writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "temporarily unavailable")
			return
		}
		now := server.now().UTC()
		response, _ := json.Marshal(map[string]string{"code": "CREATION_CANCELLED", "message": "creation was cancelled before execution"})
		operation, err = server.persistence.CancelEndpointSessionCreation(request.Context(), domain.IdempotencyRecord{ID: id, OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID,
			ActorID: endpoint.ID.String(), Operation: sessionCreateOperation, Key: key, RequestHash: sha256.Sum256([]byte("cancel-creation/" + endpoint.ID.String() + "/" + key)),
			Status: domain.IdempotencyStatusCompleted, HTTPStatus: http.StatusConflict, ResponseJSON: response, ErrorCode: "CREATION_CANCELLED", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(sessionIdempotencyTTL)})
	} else {
		operation, err = server.persistence.GetEndpointSessionCreation(request.Context(), endpoint.OwnerUserID, endpoint.TenantID, endpoint.ID, key)
	}
	if domain.HasCode(err, domain.CodeNotFound) {
		writeJSON(writer, http.StatusOK, map[string]any{"state": "not-found"})
		return
	}
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if operation.ErrorCode == "CREATION_CANCELLED" {
		writeJSON(writer, http.StatusOK, map[string]any{"state": "cancelled"})
		return
	}
	if operation.Status != domain.IdempotencyStatusCompleted {
		writeJSON(writer, http.StatusOK, map[string]any{"state": "pending"})
		return
	}
	var created endpointSessionResponse
	if err := json.Unmarshal(operation.ResponseJSON, &created); err != nil {
		writeGatewayError(writer, http.StatusInternalServerError, "CREATION_STATE_INVALID", "creation state is unavailable")
		return
	}
	id, err := domain.ParseID(created.SessionID)
	if err != nil {
		writeGatewayError(writer, http.StatusInternalServerError, "CREATION_STATE_INVALID", "creation state is unavailable")
		return
	}
	session, err := server.persistence.GetSession(request.Context(), id)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if session.HCEndpointID != endpoint.ID || session.OwnerUserID != endpoint.OwnerUserID || session.TenantID != endpoint.TenantID {
		writeDomainError(writer, domain.NewProblem(domain.CodeNotFound, "session was not found", nil))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"state": "created", "session": endpointSessionView(session)})
}
