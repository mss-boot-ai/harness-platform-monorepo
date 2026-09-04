package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

const (
	maxResumeCursors = 256
	maxReplayFrames  = 1024
)

func (server *Server) processResumeState(
	ctx context.Context,
	endpoint domain.Endpoint,
	control *awpv1.ControlFrame,
) error {
	if control == nil || !bytes.Equal(control.GetReceiverEndpointId(), make([]byte, 16)) {
		return errors.New("ResumeState receiver is invalid")
	}
	resume := new(awpv1.ResumeState)
	if err := proto.Unmarshal(control.GetPayload(), resume); err != nil {
		return errors.New("ResumeState payload is invalid")
	}
	if len(resume.GetCursors()) == 0 || len(resume.GetCursors()) > maxResumeCursors {
		return errors.New("ResumeState cursor limit is invalid")
	}
	seen := make(map[string]struct{}, len(resume.GetCursors()))
	remaining := maxReplayFrames
	for _, cursor := range resume.GetCursors() {
		if cursor == nil || len(cursor.GetChannelId()) != 16 || cursor.GetKeyGeneration() == 0 {
			return errors.New("ResumeState cursor is invalid")
		}
		channelID, err := idFromWire(cursor.GetChannelId())
		if err != nil {
			return err
		}
		direction := domain.Direction(cursor.GetDirection())
		if !resumeDirectionAllowed(endpoint.Type, direction) {
			return errors.New("ResumeState direction is invalid")
		}
		ranges, err := resumeRanges(cursor.GetHighestContiguousSequence(), cursor.GetReceivedRanges())
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%s:%d:%d", channelID.String(), cursor.GetDirection(), cursor.GetKeyGeneration())
		if _, duplicate := seen[key]; duplicate {
			return errors.New("ResumeState cursor is duplicated")
		}
		seen[key] = struct{}{}
		frames, err := server.persistence.ReplayEndpointFrames(
			ctx, endpoint.ID, channelID, cursor.GetKeyGeneration(), direction,
			cursor.GetHighestContiguousSequence(), ranges, remaining,
		)
		if err != nil {
			return err
		}
		for _, frame := range frames {
			session, err := server.persistence.GetSession(ctx, frame.SessionID)
			if err != nil {
				return err
			}
			expectedChannel, channelErr := sessionChannelID(
				session.ID, session.ABAEndpointID, session.HCEndpointID,
			)
			if channelErr != nil || session.Status != domain.SessionStatusActive ||
				frame.ReceiverEndpointID != endpoint.ID || frame.ChannelID != expectedChannel ||
				!resumeFrameReceiverMatches(endpoint.Type, session, frame) {
				return errors.New("stored replay frame route is invalid")
			}
			encoded, err := server.replayPacket(frame)
			if err != nil {
				return err
			}
			if err := server.connections.send(endpoint.ID, encoded); err != nil {
				return err
			}
			remaining--
		}
		if remaining == 0 {
			break
		}
	}
	return nil
}

func resumeFrameReceiverMatches(
	endpointType domain.EndpointType,
	session domain.Session,
	frame domain.EncryptedFrame,
) bool {
	switch endpointType {
	case domain.EndpointTypeABA:
		return frame.Direction == domain.DirectionHCToABA &&
			frame.SenderEndpointID == session.HCEndpointID && frame.ReceiverEndpointID == session.ABAEndpointID
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return frame.Direction == domain.DirectionABAToHC &&
			frame.SenderEndpointID == session.ABAEndpointID && frame.ReceiverEndpointID == session.HCEndpointID
	default:
		return false
	}
}

func (server *Server) replayPacket(frame domain.EncryptedFrame) ([]byte, error) {
	wireFrame := &awpv1.EncryptedFrame{
		CryptoSuiteId: 1, FrameType: awpv1.FrameType_FRAME_TYPE_ACP_TRANSPORT_FRAME,
		Flags: 0, MessageId: frame.MessageID[:], ChannelId: frame.ChannelID[:], SessionId: frame.SessionID[:],
		SenderEndpointId: frame.SenderEndpointID[:], ReceiverEndpointId: frame.ReceiverEndpointID[:],
		Direction: awpv1.Direction(frame.Direction), Sequence: frame.Sequence,
		KeyGeneration: frame.KeyGeneration, KeyId: frame.KeyID[:], CreatedAtMs: frame.CreatedAtMS,
		Ciphertext: bytes.Clone(frame.Ciphertext), Signature: bytes.Clone(frame.Signature),
	}
	aad, err := frameAAD(wireFrame)
	if err != nil || !bytes.Equal(aad, frame.AAD) {
		return nil, errors.New("stored replay frame is inconsistent")
	}
	packetID, err := server.randomBytes(16)
	if err != nil {
		return nil, err
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.WirePacket{
		WireMajor: 1, WireMinor: 0, PacketId: packetID,
		Body: &awpv1.WirePacket_Encrypted{Encrypted: wireFrame},
	})
}

func resumeDirectionAllowed(endpointType domain.EndpointType, direction domain.Direction) bool {
	switch endpointType {
	case domain.EndpointTypeABA:
		return direction == domain.DirectionHCToABA
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return direction == domain.DirectionABAToHC
	default:
		return false
	}
}

func resumeRanges(highest uint64, input []*awpv1.SequenceRange) ([]domain.SequenceRange, error) {
	if len(input) > maxAckRanges {
		return nil, errors.New("ResumeState range limit exceeded")
	}
	output := make([]domain.SequenceRange, 0, len(input))
	previous := highest
	for _, value := range input {
		if value == nil || value.GetStart() == 0 || value.GetStart() > value.GetEnd() || value.GetStart() <= previous {
			return nil, errors.New("ResumeState ranges are invalid")
		}
		output = append(output, domain.SequenceRange{Start: value.GetStart(), End: value.GetEnd()})
		previous = value.GetEnd()
	}
	return output, nil
}
