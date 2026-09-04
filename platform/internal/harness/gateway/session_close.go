package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

const sessionCloseOperation = "endpoint.session.close"

func (server *Server) closeEndpointSession(writer http.ResponseWriter, request *http.Request) {
	endpoint, _, ok := server.authenticateHCRequest(writer, request)
	if !ok {
		return
	}
	var input struct{}
	if err := decodeGatewayJSON(writer, request, &input); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "session close request is invalid")
		return
	}
	sessionID, err := domain.ParseID(request.PathValue("sessionId"))
	if err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Session ID is invalid")
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
	session, err := server.persistence.GetSession(request.Context(), sessionID)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if session.HCEndpointID != endpoint.ID || session.OwnerUserID != endpoint.OwnerUserID || session.TenantID != endpoint.TenantID {
		writeDomainError(writer, domain.NewProblem(domain.CodeNotFound, "session was not found", nil))
		return
	}
	now := server.now().UTC()
	response := endpointSessionResponse{
		SessionID: session.ID.String(), ABAEndpointID: session.ABAEndpointID.String(),
		HCEndpointID: session.HCEndpointID.String(), RuntimeProfileID: session.RuntimeProfileID,
		WorkspaceID:           session.WorkspaceID,
		RequestedCapabilities: append([]string(nil), session.RequestedCapabilities...),
		Status:                domain.SessionStatusClosed, CreatedAt: session.CreatedAt,
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		writeGatewayError(writer, http.StatusInternalServerError, "GATEWAY_INTERNAL", "session response could not be encoded")
		return
	}
	ids := make([]domain.ID, 2)
	for index := range ids {
		ids[index], err = domain.NewID(server.random)
		if err != nil {
			writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
			return
		}
	}
	requestHash := sha256.Sum256([]byte(sessionCloseOperation + "\x00" + endpoint.ID.String() + "\x00" + session.ID.String()))
	reservation := domain.IdempotencyRecord{
		ID: ids[0], OwnerUserID: session.OwnerUserID, TenantID: session.TenantID,
		ActorID: endpoint.ID.String(), Operation: sessionCloseOperation, Key: idempotencyKey,
		RequestHash: requestHash, Status: domain.IdempotencyStatusInProgress,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(sessionIdempotencyTTL),
	}
	audit := domain.SecurityAuditEvent{
		ID: ids[1], OwnerUserID: session.OwnerUserID, TenantID: session.TenantID,
		ActorType: domain.AuditActorEndpoint, ActorID: endpoint.ID.String(), Action: "session.close",
		ObjectType: "session", ObjectID: session.ID.String(), Result: "success",
		Metadata: map[string]string{"sessionStatus": string(domain.SessionStatusClosed)}, CreatedAt: now,
	}
	session.UpdatedAt = now
	closed, output, replayed, err := server.persistence.CloseEndpointSession(
		request.Context(), session, reservation, audit, responseJSON,
	)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	} else if err := server.sendCloseTunnelRequest(closed, now); err != nil {
		writer.Header().Set("Harness-Close-Delivery", "pending")
	}
	writeRawJSON(writer, http.StatusOK, output)
}

func (server *Server) sendCloseTunnelRequest(session domain.Session, now time.Time) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.CloseTunnelRequest{
		SessionId: session.ID[:], StableReasonCode: "HC_REQUESTED",
		ExpiresAtMs: now.Add(openTunnelTTL).UnixMilli(),
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
		controlType := awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_REQUEST
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
		return proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.WirePacket{
			WireMajor: 1, WireMinor: 0, PacketId: packetID,
			Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
				MessageId: messageID, SenderEndpointId: sender, ReceiverEndpointId: session.ABAEndpointID[:],
				ControlSequence: sequence, CreatedAtMs: now.UnixMilli(), Type: controlType,
				Payload: payload, Signature: signature,
			}},
		})
	})
}

func (server *Server) processCloseTunnelResult(
	ctx context.Context,
	endpoint domain.Endpoint,
	control *awpv1.ControlFrame,
) (domain.ID, error) {
	result := new(awpv1.CloseTunnelResult)
	if err := proto.Unmarshal(control.GetPayload(), result); err != nil {
		return domain.ID{}, errors.New("CloseTunnelResult payload is invalid")
	}
	sessionID, err := idFromWire(result.GetSessionId())
	if err != nil {
		return domain.ID{}, err
	}
	receiverID, err := idFromWire(control.GetReceiverEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return domain.ID{}, err
	}
	status := result.GetStatus()
	if session.ABAEndpointID != endpoint.ID || session.HCEndpointID != receiverID ||
		session.Status != domain.SessionStatusClosed ||
		(status != awpv1.CloseTunnelStatus_CLOSE_TUNNEL_STATUS_ACCEPTED &&
			status != awpv1.CloseTunnelStatus_CLOSE_TUNNEL_STATUS_ALREADY_CLOSED) ||
		result.GetStableErrorCode() != "" {
		return domain.ID{}, errors.New("CloseTunnelResult binding is invalid")
	}
	return receiverID, nil
}
