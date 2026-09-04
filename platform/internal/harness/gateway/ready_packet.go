package gateway

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"slices"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

var stableControlErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (server *Server) handleReadyPacket(
	ctx context.Context,
	connection *activeConnection,
	endpoint domain.Endpoint,
	encoded []byte,
	now time.Time,
) error {
	if connection == nil || len(encoded) == 0 || len(encoded) > maxWirePacketBytes {
		return errors.New("ready packet input is invalid")
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, packet); err != nil {
		return errors.New("ready packet is not valid AWP protobuf")
	}
	control := packet.GetControl()
	if packet.GetWireMajor() != 1 || packet.GetWireMinor() != 0 || len(packet.GetPacketId()) != 16 ||
		control == nil || control.GetType() != awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_RESULT ||
		endpoint.Type != domain.EndpointTypeABA {
		return errors.New("ready packet type is not allowed")
	}
	if len(control.GetMessageId()) != 16 || !bytes.Equal(control.GetSenderEndpointId(), endpoint.ID[:]) ||
		len(control.GetReceiverEndpointId()) != 16 || control.GetControlSequence() == 0 ||
		len(control.GetPayload()) == 0 || len(control.GetSignature()) != 64 ||
		!connection.acceptInboundControlSequence(control.GetControlSequence()) {
		return errors.New("control frame binding is invalid")
	}
	createdAt := time.UnixMilli(control.GetCreatedAtMs()).UTC()
	if createdAt.Before(now.Add(-time.Minute)) || createdAt.After(now.Add(time.Minute)) {
		return errors.New("control frame time is invalid")
	}
	transcript, err := controlTranscript(
		control.GetMessageId(), control.GetSenderEndpointId(), control.GetReceiverEndpointId(),
		control.GetControlSequence(), control.GetCreatedAtMs(), uint32(control.GetType()), control.GetPayload(),
	)
	if err != nil {
		return err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, transcript, control.GetSignature()) {
		return errors.New("control frame signature is invalid")
	}
	result := new(awpv1.OpenTunnelResult)
	if err := proto.Unmarshal(control.GetPayload(), result); err != nil {
		return errors.New("OpenTunnelResult payload is invalid")
	}
	sessionID, err := idFromWire(result.GetSessionId())
	if err != nil {
		return err
	}
	receiverID, err := idFromWire(control.GetReceiverEndpointId())
	if err != nil {
		return err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.ABAEndpointID != endpoint.ID || session.HCEndpointID != receiverID ||
		session.Status != domain.SessionStatusCreating || result.GetActiveKeyGeneration() != 0 ||
		!capabilitySubset(result.GetNegotiatedCapabilityHints(), session.RequestedCapabilities) {
		return errors.New("OpenTunnelResult session binding is invalid")
	}
	switch result.GetStatus() {
	case awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_ACCEPTED, awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_ALREADY_OPEN:
		if result.GetStableErrorCode() != "" || result.GetAcceptedAuthorizationRevision() != 1 {
			return errors.New("accepted OpenTunnelResult is invalid")
		}
		_, err = server.persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
			return value.WaitForKey(now)
		})
	case awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_REJECTED, awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_RESOURCE_BUSY:
		if !stableControlErrorCode.MatchString(result.GetStableErrorCode()) || result.GetAcceptedAuthorizationRevision() > 1 {
			return errors.New("rejected OpenTunnelResult is invalid")
		}
		_, err = server.persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
			return value.Fail(now)
		})
	default:
		return errors.New("OpenTunnelResult status is invalid")
	}
	if err != nil {
		return err
	}
	if err := server.connections.send(session.HCEndpointID, encoded); err != nil &&
		!errors.Is(err, errConnectionOffline) && !errors.Is(err, errConnectionBackpressure) {
		return err
	}
	return nil
}

func idFromWire(value []byte) (domain.ID, error) {
	if len(value) != 16 {
		return domain.ID{}, errors.New("wire identifier length is invalid")
	}
	var id domain.ID
	copy(id[:], value)
	if id.IsZero() {
		return domain.ID{}, errors.New("wire identifier is zero")
	}
	return id, nil
}

func capabilitySubset(values, allowed []string) bool {
	if len(values) > len(allowed) {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
