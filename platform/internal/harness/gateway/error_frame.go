package gateway

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
)

const uncertainSafeMessage = "Local agent dispatch result is uncertain"

func (server *Server) processEndpointError(
	ctx context.Context,
	endpoint domain.Endpoint,
	packet *awpv1.WirePacket,
	now time.Time,
) (domain.ID, error) {
	frame := packet.GetError()
	if packet.GetWireMajor() != 1 || packet.GetWireMinor() != 0 || len(packet.GetPacketId()) != 16 || frame == nil {
		return domain.ID{}, errors.New("ErrorFrame packet envelope is invalid")
	}
	if endpoint.Type != domain.EndpointTypeABA || len(frame.GetErrorId()) != 16 ||
		len(frame.GetRelatedMessageId()) != 16 || len(frame.GetSignature()) != 64 ||
		frame.GetCode() != awpv1.ErrorCode_ERROR_CODE_LOCAL_DISPATCH_UNCERTAIN ||
		frame.GetRetryable() || frame.GetRetryAfterMs() != 0 || frame.GetSafeMessage() != uncertainSafeMessage {
		return domain.ID{}, errors.New("ErrorFrame fields are invalid")
	}
	if _, err := idFromWire(frame.GetErrorId()); err != nil {
		return domain.ID{}, err
	}
	relatedID, err := idFromWire(frame.GetRelatedMessageId())
	if err != nil {
		return domain.ID{}, err
	}
	related, err := server.persistence.GetFrame(ctx, relatedID)
	if err != nil {
		return domain.ID{}, err
	}
	session, err := server.persistence.GetSession(ctx, related.SessionID)
	if err != nil {
		return domain.ID{}, err
	}
	if related.Direction != domain.DirectionHCToABA || related.ReceiverEndpointID != endpoint.ID ||
		session.ABAEndpointID != endpoint.ID || session.HCEndpointID != related.SenderEndpointID ||
		(session.Status != domain.SessionStatusActive && session.Status != domain.SessionStatusUncertain) {
		return domain.ID{}, errors.New("ErrorFrame session binding is invalid")
	}
	transcript, err := errorFrameTranscript(frame)
	if err != nil {
		return domain.ID{}, err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return domain.ID{}, err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, transcript, frame.GetSignature()) {
		return domain.ID{}, errors.New("ErrorFrame signature is invalid")
	}
	if session.Status == domain.SessionStatusActive {
		if _, err := server.persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
			return value.MarkUncertain(now)
		}); err != nil {
			return domain.ID{}, err
		}
	}
	return session.HCEndpointID, nil
}

func errorFrameTranscript(frame *awpv1.ErrorFrame) ([]byte, error) {
	if frame == nil || len(frame.GetErrorId()) != 16 || len(frame.GetRelatedMessageId()) != 16 ||
		frame.GetCode() <= 0 || len(frame.GetSafeMessage()) == 0 || len(frame.GetSafeMessage()) > 256 ||
		!utf8.ValidString(frame.GetSafeMessage()) || hasControl(frame.GetSafeMessage()) ||
		(!frame.GetRetryable() && frame.GetRetryAfterMs() != 0) {
		return nil, errors.New("ErrorFrame transcript input is invalid")
	}
	output := make([]byte, 0, 64+len(frame.GetSafeMessage()))
	output = append(output, []byte("mss-awp-error-v1")...)
	output = append(output, frame.GetErrorId()...)
	output = append(output, frame.GetRelatedMessageId()...)
	output = binary.BigEndian.AppendUint32(output, uint32(frame.GetCode()))
	if frame.GetRetryable() {
		output = append(output, 1)
	} else {
		output = append(output, 0)
	}
	output = append(output, 0, 0, 0)
	output = binary.BigEndian.AppendUint32(output, frame.GetRetryAfterMs())
	output = binary.BigEndian.AppendUint32(output, uint32(len(frame.GetSafeMessage())))
	output = append(output, frame.GetSafeMessage()...)
	return output, nil
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}
