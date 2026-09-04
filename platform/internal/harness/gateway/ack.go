package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
)

const maxAckRanges = 32

func (server *Server) processAckFrame(
	ctx context.Context,
	endpoint domain.Endpoint,
	packet *awpv1.WirePacket,
	now time.Time,
) error {
	ack := packet.GetAck()
	if packet.GetWireMajor() != 1 || packet.GetWireMinor() != 0 || len(packet.GetPacketId()) != 16 || ack == nil {
		return errors.New("ACK packet envelope is invalid")
	}
	if len(ack.GetAckId()) != 16 || len(ack.GetChannelId()) != 16 || len(ack.GetSessionId()) != 16 ||
		len(ack.GetEndpointId()) != 16 || len(ack.GetSignature()) != 64 ||
		!bytes.Equal(ack.GetEndpointId(), endpoint.ID[:]) || ack.GetKeyGeneration() == 0 ||
		(ack.GetHighestContiguousSequence() == 0 && len(ack.GetReceivedRanges()) == 0) {
		return errors.New("ACK fields are invalid")
	}
	if _, err := idFromWire(ack.GetAckId()); err != nil {
		return err
	}
	channelID, err := idFromWire(ack.GetChannelId())
	if err != nil {
		return err
	}
	sessionID, err := idFromWire(ack.GetSessionId())
	if err != nil {
		return err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status != domain.SessionStatusActive || ack.GetKeyGeneration() != session.CurrentKeyGeneration {
		return errors.New("ACK session is invalid")
	}
	expectedChannel, err := sessionChannelID(session.ID, session.ABAEndpointID, session.HCEndpointID)
	if err != nil || channelID != expectedChannel {
		return errors.New("ACK channel is invalid")
	}
	var senderID, receiverID domain.ID
	direction := domain.Direction(ack.GetAcknowledgedDirection())
	switch direction {
	case domain.DirectionHCToABA:
		senderID, receiverID = session.HCEndpointID, session.ABAEndpointID
	case domain.DirectionABAToHC:
		senderID, receiverID = session.ABAEndpointID, session.HCEndpointID
	default:
		return errors.New("ACK direction is invalid")
	}
	if receiverID != endpoint.ID {
		return errors.New("ACK sender is not the acknowledged receiver")
	}
	ranges, err := ackRanges(ack.GetHighestContiguousSequence(), ack.GetReceivedRanges())
	if err != nil {
		return err
	}
	createdAt := time.UnixMilli(ack.GetCreatedAtMs()).UTC()
	if createdAt.Before(now.Add(-5*time.Minute)) || createdAt.After(now.Add(5*time.Minute)) {
		return errors.New("ACK time is invalid")
	}
	transcript, err := ackTranscript(ack)
	if err != nil {
		return err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, transcript, ack.GetSignature()) {
		return errors.New("ACK signature is invalid")
	}
	_, err = server.persistence.AdvanceAck(ctx, domain.AckCursor{
		SessionID: session.ID, KeyGeneration: ack.GetKeyGeneration(), Direction: direction,
		SenderEndpointID: senderID, ReceiverEndpointID: receiverID,
		HighestContiguousSequence: ack.GetHighestContiguousSequence(), ReceivedRanges: ranges,
		UpdatedAt: now,
	})
	return err
}

func ackRanges(highest uint64, input []*awpv1.SequenceRange) ([]domain.SequenceRange, error) {
	if len(input) > maxAckRanges {
		return nil, errors.New("ACK range limit exceeded")
	}
	output := make([]domain.SequenceRange, 0, len(input))
	previous := highest
	for _, value := range input {
		if value == nil || value.GetStart() == 0 || value.GetStart() > value.GetEnd() || value.GetStart() <= previous {
			return nil, errors.New("ACK ranges are invalid")
		}
		output = append(output, domain.SequenceRange{Start: value.GetStart(), End: value.GetEnd()})
		previous = value.GetEnd()
	}
	return output, nil
}

func ackTranscript(ack *awpv1.AckFrame) ([]byte, error) {
	if ack == nil || len(ack.GetAckId()) != 16 || len(ack.GetChannelId()) != 16 ||
		len(ack.GetSessionId()) != 16 || len(ack.GetEndpointId()) != 16 ||
		len(ack.GetReceivedRanges()) > maxAckRanges {
		return nil, errors.New("ACK transcript input is invalid")
	}
	output := make([]byte, 0, 114+len(ack.GetReceivedRanges())*16)
	output = append(output, []byte("mss-awp-ack-v1")...)
	output = append(output, ack.GetAckId()...)
	output = append(output, ack.GetChannelId()...)
	output = append(output, ack.GetSessionId()...)
	output = append(output, ack.GetEndpointId()...)
	output = append(output, byte(ack.GetAcknowledgedDirection()))
	output = append(output, make([]byte, 7)...)
	output = binary.BigEndian.AppendUint64(output, ack.GetHighestContiguousSequence())
	output = binary.BigEndian.AppendUint64(output, ack.GetKeyGeneration())
	output = binary.BigEndian.AppendUint64(output, uint64(ack.GetCreatedAtMs()))
	output = binary.BigEndian.AppendUint32(output, uint32(len(ack.GetReceivedRanges())))
	for _, value := range ack.GetReceivedRanges() {
		if value == nil {
			return nil, errors.New("ACK transcript range is invalid")
		}
		output = binary.BigEndian.AppendUint64(output, value.GetStart())
		output = binary.BigEndian.AppendUint64(output, value.GetEnd())
	}
	return output, nil
}
