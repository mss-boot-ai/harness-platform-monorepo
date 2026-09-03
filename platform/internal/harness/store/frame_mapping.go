package store

import (
	"bytes"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func frameFromRow(row frameRow) (domain.EncryptedFrame, error) {
	messageID, err := parseID(row.MessageID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	sessionID, err := parseID(row.SessionID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	channelID, err := parseID(row.ChannelID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	senderID, err := parseID(row.SenderEndpointID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	receiverID, err := parseID(row.ReceiverEndpointID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	keyID, err := parseID(row.KeyID)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	contentHash, err := parseHash(row.ContentHash)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	return domain.EncryptedFrame{
		MessageID:          messageID,
		ChannelID:          channelID,
		SessionID:          sessionID,
		SenderEndpointID:   senderID,
		ReceiverEndpointID: receiverID,
		Direction:          domain.Direction(row.Direction),
		Sequence:           row.Sequence,
		KeyGeneration:      row.KeyGeneration,
		KeyID:              keyID,
		CreatedAtMS:        row.CreatedAtMS,
		AAD:                bytes.Clone(row.AAD),
		Ciphertext:         bytes.Clone(row.Ciphertext),
		Signature:          bytes.Clone(row.Signature),
		ContentHash:        contentHash,
		Status:             domain.FrameStatus(row.Status),
		ReceivedAt:         row.ReceivedAt,
		RoutedAt:           cloneTime(row.RoutedAt),
		AcknowledgedAt:     cloneTime(row.AcknowledgedAt),
		ExpiresAt:          row.ExpiresAt,
	}, nil
}
