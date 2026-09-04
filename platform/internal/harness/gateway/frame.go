package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
)

const frameRetention = 24 * time.Hour

func (server *Server) processEncryptedFrame(
	ctx context.Context,
	endpoint domain.Endpoint,
	packet *awpv1.WirePacket,
	encoded []byte,
	now time.Time,
) error {
	frame := packet.GetEncrypted()
	if packet.GetWireMajor() != 1 || packet.GetWireMinor() != 0 || len(packet.GetPacketId()) != 16 || frame == nil {
		return errors.New("encrypted packet envelope is invalid")
	}
	messageID, err := idFromWire(frame.GetMessageId())
	if err != nil {
		return err
	}
	channelID, err := idFromWire(frame.GetChannelId())
	if err != nil {
		return err
	}
	sessionID, err := idFromWire(frame.GetSessionId())
	if err != nil {
		return err
	}
	senderID, err := idFromWire(frame.GetSenderEndpointId())
	if err != nil {
		return err
	}
	receiverID, err := idFromWire(frame.GetReceiverEndpointId())
	if err != nil {
		return err
	}
	keyID, err := idFromWire(frame.GetKeyId())
	if err != nil {
		return err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status != domain.SessionStatusActive || frame.GetKeyGeneration() != session.CurrentKeyGeneration ||
		senderID != endpoint.ID || !validFrameRoute(endpoint, session, receiverID, frame.GetDirection()) {
		return errors.New("encrypted frame route is invalid")
	}
	expectedChannel, err := sessionChannelID(session.ID, session.ABAEndpointID, session.HCEndpointID)
	if err != nil || channelID != expectedChannel {
		return errors.New("encrypted frame channel is invalid")
	}
	if frame.GetCryptoSuiteId() != 1 || frame.GetFrameType() != awpv1.FrameType_FRAME_TYPE_ACP_TRANSPORT_FRAME ||
		frame.GetFlags()&0xffff0000 != 0 || frame.GetSequence() == 0 ||
		len(frame.GetCiphertext()) < 16 || len(frame.GetCiphertext()) > maxWirePacketBytes || len(frame.GetSignature()) != 64 {
		return errors.New("encrypted frame fields are invalid")
	}
	createdAt := time.UnixMilli(frame.GetCreatedAtMs()).UTC()
	if createdAt.Before(now.Add(-5*time.Minute)) || createdAt.After(now.Add(5*time.Minute)) {
		return errors.New("encrypted frame time is invalid")
	}
	aad, err := frameAAD(frame)
	if err != nil {
		return err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(
		publicKey, frameSignatureInput(aad, frame.GetCiphertext()), frame.GetSignature(),
	) {
		return errors.New("encrypted frame signature is invalid")
	}
	contentHash := sha256.New()
	_, _ = contentHash.Write(aad)
	_, _ = contentHash.Write(frame.GetCiphertext())
	_, _ = contentHash.Write(frame.GetSignature())
	var contentHashValue [32]byte
	copy(contentHashValue[:], contentHash.Sum(nil))
	value := domain.EncryptedFrame{
		MessageID: messageID, ChannelID: channelID, SessionID: sessionID,
		SenderEndpointID: senderID, ReceiverEndpointID: receiverID,
		Direction: domain.Direction(frame.GetDirection()), Sequence: frame.GetSequence(),
		KeyGeneration: frame.GetKeyGeneration(), KeyID: keyID, CreatedAtMS: frame.GetCreatedAtMs(),
		AAD: bytes.Clone(aad), Ciphertext: bytes.Clone(frame.GetCiphertext()),
		Signature: bytes.Clone(frame.GetSignature()), ContentHash: contentHashValue,
		Status: domain.FrameStatusStored, ReceivedAt: now, ExpiresAt: now.Add(frameRetention),
	}
	if _, err := server.persistence.PutEndpointFrame(ctx, value); err != nil {
		return err
	}
	if err := server.connections.send(receiverID, encoded); err != nil &&
		!errors.Is(err, errConnectionOffline) && !errors.Is(err, errConnectionBackpressure) {
		return err
	}
	return nil
}

func validFrameRoute(
	endpoint domain.Endpoint,
	session domain.Session,
	receiverID domain.ID,
	direction awpv1.Direction,
) bool {
	switch endpoint.Type {
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return endpoint.ID == session.HCEndpointID && receiverID == session.ABAEndpointID &&
			direction == awpv1.Direction_DIRECTION_HC_TO_ABA
	case domain.EndpointTypeABA:
		return endpoint.ID == session.ABAEndpointID && receiverID == session.HCEndpointID &&
			direction == awpv1.Direction_DIRECTION_ABA_TO_HC
	default:
		return false
	}
}

func frameAAD(frame *awpv1.EncryptedFrame) ([]byte, error) {
	if frame == nil || len(frame.GetMessageId()) != 16 || len(frame.GetChannelId()) != 16 ||
		len(frame.GetSessionId()) != 16 || len(frame.GetSenderEndpointId()) != 16 ||
		len(frame.GetReceiverEndpointId()) != 16 || len(frame.GetKeyId()) != 16 {
		return nil, errors.New("frame AAD identifiers are invalid")
	}
	output := make([]byte, 148)
	copy(output[:4], []byte("AWP1"))
	putUint16(output[4:6], 1)
	putUint16(output[6:8], 0)
	putUint16(output[8:10], uint16(frame.GetCryptoSuiteId()))
	putUint16(output[10:12], uint16(frame.GetFrameType()))
	putUint32(output[12:16], frame.GetFlags())
	copy(output[16:32], frame.GetMessageId())
	copy(output[32:48], frame.GetChannelId())
	copy(output[48:64], frame.GetSessionId())
	copy(output[64:80], frame.GetSenderEndpointId())
	copy(output[80:96], frame.GetReceiverEndpointId())
	output[96] = byte(frame.GetDirection())
	putUint64(output[104:112], frame.GetSequence())
	putUint64(output[112:120], frame.GetKeyGeneration())
	copy(output[120:136], frame.GetKeyId())
	putUint64(output[136:144], uint64(frame.GetCreatedAtMs()))
	putUint32(output[144:148], uint32(len(frame.GetCiphertext())))
	return output, nil
}

func sessionChannelID(sessionID, abaEndpointID, hcEndpointID domain.ID) (domain.ID, error) {
	if sessionID.IsZero() || abaEndpointID.IsZero() || hcEndpointID.IsZero() || abaEndpointID == hcEndpointID {
		return domain.ID{}, errors.New("session channel input is invalid")
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("mss-awp-channel-v1"))
	_, _ = digest.Write(sessionID[:])
	_, _ = digest.Write(abaEndpointID[:])
	_, _ = digest.Write(hcEndpointID[:])
	var id domain.ID
	copy(id[:], digest.Sum(nil)[:16])
	return id, nil
}

func frameSignatureInput(aad, ciphertext []byte) []byte {
	input := make([]byte, 0, len("mss-awp-frame-signature-v1")+len(aad)+len(ciphertext))
	input = append(input, []byte("mss-awp-frame-signature-v1")...)
	input = append(input, aad...)
	return append(input, ciphertext...)
}

func putUint16(output []byte, value uint16) { output[0], output[1] = byte(value>>8), byte(value) }

func putUint32(output []byte, value uint32) {
	output[0], output[1], output[2], output[3] = byte(value>>24), byte(value>>16), byte(value>>8), byte(value)
}

func putUint64(output []byte, value uint64) {
	for index := 7; index >= 0; index-- {
		output[index] = byte(value)
		value >>= 8
	}
}
