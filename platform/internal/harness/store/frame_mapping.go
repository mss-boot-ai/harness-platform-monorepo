package store

import "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"

func frameFromRow(row frameRow) (domain.EncryptedFrame, error) {
	decoded, err := decodeFrameRow(row)
	if err != nil {
		return domain.EncryptedFrame{}, err
	}
	return domain.EncryptedFrame{
		MessageID:          decoded.MessageID,
		ChannelID:          decoded.ChannelID,
		SessionID:          decoded.SessionID,
		SenderEndpointID:   decoded.SenderEndpointID,
		ReceiverEndpointID: decoded.ReceiverEndpointID,
		Direction:          domain.Direction(row.Direction),
		Sequence:           row.Sequence,
		KeyGeneration:      row.KeyGeneration,
		KeyID:              decoded.KeyID,
		CreatedAtMS:        row.CreatedAtMS,
		AAD:                clone(row.AAD),
		Ciphertext:         clone(row.Ciphertext),
		Signature:          clone(row.Signature),
		ContentHash:        decoded.ContentHash,
		Status:             domain.FrameStatus(row.Status),
		ReceivedAt:         row.ReceivedAt,
		RoutedAt:           row.RoutedAt,
		AcknowledgedAt:     row.AcknowledgedAt,
		ExpiresAt:          row.ExpiresAt,
	}, nil
}
