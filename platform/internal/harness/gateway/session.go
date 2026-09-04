package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/dpop"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

const (
	sessionCreateOperation = "endpoint.session.create"
	sessionIdempotencyTTL  = 24 * time.Hour
	openTunnelTTL          = 30 * time.Second
)

type createSessionRequest struct {
	ABAEndpointID         string   `json:"abaEndpointId"`
	RuntimeProfileID      string   `json:"runtimeProfileId"`
	WorkspaceID           string   `json:"workspaceId"`
	RequestedCapabilities []string `json:"requestedCapabilities"`
}

type endpointSessionResponse struct {
	SessionID             string               `json:"sessionId"`
	ABAEndpointID         string               `json:"abaEndpointId"`
	HCEndpointID          string               `json:"hcEndpointId"`
	RuntimeProfileID      string               `json:"runtimeProfileId"`
	WorkspaceID           string               `json:"workspaceId"`
	RequestedCapabilities []string             `json:"requestedCapabilities"`
	Status                domain.SessionStatus `json:"status"`
	CreatedAt             time.Time            `json:"createdAt"`
}

func (server *Server) createSession(writer http.ResponseWriter, request *http.Request) {
	endpoint, _, ok := server.authenticateHCRequest(writer, request)
	if !ok {
		return
	}
	var input createSessionRequest
	if err := decodeGatewayJSON(writer, request, &input); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "session request is invalid")
		return
	}
	abaID, err := domain.ParseID(input.ABAEndpointID)
	if err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "ABA endpoint ID is invalid")
		return
	}
	sessionRequest := domain.SessionRequest{
		ABAEndpointID: abaID, HCEndpointID: endpoint.ID,
		RuntimeProfileID: input.RuntimeProfileID, WorkspaceID: input.WorkspaceID,
		RequestedCapabilities: append([]string(nil), input.RequestedCapabilities...),
	}
	if err := sessionRequest.Validate(); err != nil {
		writeDomainError(writer, err)
		return
	}
	if !server.connections.online(abaID) {
		writeGatewayError(writer, http.StatusServiceUnavailable, "ABA_OFFLINE", "selected ABA endpoint is offline")
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key is invalid")
		return
	}
	if err := domain.ValidateIdempotencyKey(idempotencyKey); err != nil {
		writeDomainError(writer, err)
		return
	}
	requestHash, err := endpointSessionRequestHash(endpoint.ID, sessionRequest)
	if err != nil {
		writeGatewayError(writer, http.StatusInternalServerError, "GATEWAY_INTERNAL", "session request could not be encoded")
		return
	}
	now := server.now().UTC()
	ids := make([]domain.ID, 3)
	for index := range ids {
		ids[index], err = domain.NewID(server.random)
		if err != nil {
			writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
			return
		}
	}
	session := domain.Session{
		ID: ids[0], OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID,
		ABAEndpointID: sessionRequest.ABAEndpointID, HCEndpointID: sessionRequest.HCEndpointID,
		RuntimeProfileID: sessionRequest.RuntimeProfileID, WorkspaceID: sessionRequest.WorkspaceID,
		RequestedCapabilities: append([]string(nil), sessionRequest.RequestedCapabilities...),
		Status:                domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now,
	}
	response := endpointSessionResponse{
		SessionID: session.ID.String(), ABAEndpointID: session.ABAEndpointID.String(), HCEndpointID: session.HCEndpointID.String(),
		RuntimeProfileID: session.RuntimeProfileID, WorkspaceID: session.WorkspaceID,
		RequestedCapabilities: append([]string(nil), session.RequestedCapabilities...), Status: session.Status, CreatedAt: now,
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		writeGatewayError(writer, http.StatusInternalServerError, "GATEWAY_INTERNAL", "session response could not be encoded")
		return
	}
	reservation := domain.IdempotencyRecord{
		ID: ids[1], OwnerUserID: session.OwnerUserID, TenantID: session.TenantID,
		ActorID: endpoint.ID.String(), Operation: sessionCreateOperation, Key: idempotencyKey,
		RequestHash: requestHash, Status: domain.IdempotencyStatusInProgress,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(sessionIdempotencyTTL),
	}
	audit := domain.SecurityAuditEvent{
		ID: ids[2], OwnerUserID: session.OwnerUserID, TenantID: session.TenantID,
		ActorType: domain.AuditActorEndpoint, ActorID: endpoint.ID.String(), Action: "session.create",
		ObjectType: "session", ObjectID: session.ID.String(), Result: "success",
		Metadata: map[string]string{"sessionStatus": string(session.Status)}, CreatedAt: now,
	}
	_, storedJSON, replayed, err := server.persistence.CreateEndpointSession(
		request.Context(), session, reservation, audit, http.StatusCreated, responseJSON,
	)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
		writeRawJSON(writer, http.StatusCreated, storedJSON)
		return
	}
	if err := server.sendOpenTunnelRequest(session, now); err != nil {
		_, _ = server.persistence.UpdateSession(request.Context(), session.ID, func(value *domain.Session) error {
			return value.Fail(server.now().UTC())
		})
		writeGatewayError(writer, http.StatusServiceUnavailable, "ABA_DELIVERY_UNAVAILABLE", "session was created but ABA delivery failed")
		return
	}
	writeRawJSON(writer, http.StatusCreated, storedJSON)
}

func (server *Server) authenticateHCRequest(
	writer http.ResponseWriter,
	request *http.Request,
) (domain.Endpoint, domain.EndpointCredential, bool) {
	now := server.now().UTC()
	token, tokenHash, ok := parseAuthorization(request)
	if !ok {
		writeGatewayError(writer, http.StatusUnauthorized, "ENDPOINT_CREDENTIAL_REQUIRED", "endpoint credential is required")
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	endpoint, credential, err := server.persistence.AuthenticateAccessToken(request.Context(), tokenHash, now)
	if err != nil {
		writeDomainError(writer, err)
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	if (endpoint.Type != domain.EndpointTypeHCWeb && endpoint.Type != domain.EndpointTypeHCReference) ||
		strings.TrimSpace(request.Header.Get("Origin")) != server.config.AllowedOrigin ||
		!slices.Contains(credential.Scopes, "session:manage") {
		writeGatewayError(writer, http.StatusForbidden, "SESSION_CREATE_FORBIDDEN", "endpoint cannot create sessions")
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	nonceHash, err := server.persistence.GetEndpointNonceHash(request.Context(), endpoint.ID, now)
	proofHeaders := request.Header.Values("DPoP")
	if err != nil {
		if !domain.HasCode(err, domain.CodeNotFound) && !domain.HasCode(err, domain.CodeExpired) {
			writeDomainError(writer, err)
			return domain.Endpoint{}, domain.EndpointCredential{}, false
		}
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	if len(proofHeaders) == 0 || (len(proofHeaders) == 1 && strings.TrimSpace(proofHeaders[0]) == "") {
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	if len(proofHeaders) != 1 {
		writeGatewayError(writer, http.StatusBadRequest, "DPOP_MALFORMED", "exactly one DPoP header is required")
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	verifier := dpop.Verifier{Replay: storeReplayCache{persistence: server.persistence, maxEntries: server.config.ReplayMaxEntries}}
	_, err = verifier.Verify(request.Context(), proofHeaders[0], dpop.Requirements{
		AccessToken: token, ExpectedJKT: endpoint.SigningJKT, ExpectedNonceHash: nonceHash,
		HTM: request.Method, HTU: server.config.ExternalOrigin + request.URL.RequestURI(), Now: now,
	})
	if err != nil {
		server.writeDPoPError(writer, request.Context(), endpoint.ID, now, err)
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	nextNonce, _, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	if err := server.persistence.RotateEndpointNonce(
		request.Context(), endpoint.ID, nonceHash, sha256.Sum256([]byte(nextNonce)), now, now.Add(server.config.NonceTTL),
	); err != nil {
		writeGatewayError(writer, http.StatusConflict, "DPOP_NONCE_CHANGED", "DPoP nonce changed concurrently")
		return domain.Endpoint{}, domain.EndpointCredential{}, false
	}
	writer.Header().Set("DPoP-Nonce", nextNonce)
	return endpoint, credential, true
}

func endpointSessionRequestHash(endpointID domain.ID, request domain.SessionRequest) ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		EndpointID domain.ID             `json:"endpointId"`
		Request    domain.SessionRequest `json:"request"`
	}{EndpointID: endpointID, Request: request})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func (server *Server) sendOpenTunnelRequest(session domain.Session, now time.Time) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.OpenTunnelRequest{
		SessionId: session.ID[:], HcEndpointId: session.HCEndpointID[:],
		RuntimeProfileId: session.RuntimeProfileID, WorkspaceId: session.WorkspaceID,
		AuthorizationRevision: 1, RequestedKeyGeneration: 1,
		RequestedAcpCapabilities: append([]string(nil), session.RequestedCapabilities...),
		ExpiresAtMs:              now.Add(openTunnelTTL).UnixMilli(),
	})
	if err != nil {
		return err
	}
	messageID, err := server.randomBytes(16)
	if err != nil {
		return err
	}
	packetID, err := server.randomBytes(16)
	if err != nil {
		return err
	}
	return server.connections.sendNextControl(session.ABAEndpointID, func(sequence uint64) ([]byte, error) {
		sender := make([]byte, 16)
		controlType := awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_REQUEST
		transcript, err := controlTranscript(
			messageID, sender, session.ABAEndpointID[:], sequence, now.UnixMilli(), uint32(controlType), payload,
		)
		if err != nil {
			return nil, err
		}
		signature, err := awpcrypto.SignP1363LowS(server.trust.Online, transcript)
		if err != nil {
			return nil, err
		}
		packet := &awpv1.WirePacket{
			WireMajor: 1, WireMinor: 0, PacketId: packetID,
			Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
				MessageId: messageID, SenderEndpointId: sender, ReceiverEndpointId: session.ABAEndpointID[:],
				ControlSequence: sequence, CreatedAtMs: now.UnixMilli(), Type: controlType,
				Payload: payload, Signature: signature,
			}},
		}
		return proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	})
}

func writeRawJSON(writer http.ResponseWriter, status int, value []byte) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(value)
}
