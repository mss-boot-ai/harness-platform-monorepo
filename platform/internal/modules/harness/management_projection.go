package harness

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
)

func projectEnrollment(value domain.Enrollment) gin.H {
	endpointID := ""
	if !value.EndpointID.IsZero() {
		endpointID = value.EndpointID.String()
	}
	return gin.H{
		"id": value.ID.String(), "endpointType": value.EndpointType,
		"endpointName": value.EndpointName, "status": value.Status,
		"expiresAt": value.ExpiresAt, "approvedAt": value.ApprovedAt,
		"consumedAt": value.ConsumedAt, "endpointId": endpointID, "createdAt": value.CreatedAt,
	}
}

func projectEndpoint(value domain.Endpoint) gin.H {
	return gin.H{
		"id": value.ID.String(), "type": value.Type, "name": value.Name,
		"status": value.Status, "signingJkt": value.SigningJKT, "kemJkt": value.KEMJKT,
		"softwareVersion": value.SoftwareVersion, "platformName": value.PlatformName,
		"lastSeenAt": value.LastSeenAt, "revokedAt": value.RevokedAt, "createdAt": value.CreatedAt,
	}
}

func projectSession(value domain.Session) gin.H {
	return gin.H{
		"id": value.ID.String(), "abaEndpointId": value.ABAEndpointID.String(),
		"hcEndpointId": value.HCEndpointID.String(), "runtimeProfileId": value.RuntimeProfileID,
		"workspaceId": value.WorkspaceID, "requestedCapabilities": append([]string(nil), value.RequestedCapabilities...),
		"status": value.Status, "keyGeneration": value.CurrentKeyGeneration,
		"lastActivityAt": value.LastActivityAt, "closedAt": value.ClosedAt, "createdAt": value.CreatedAt,
	}
}

func projectDelivery(value store.DeliverySnapshot) gin.H {
	frames := make([]gin.H, 0, len(value.Frames))
	for _, frame := range value.Frames {
		frames = append(frames, gin.H{
			"messageId": frame.MessageID.String(), "channelId": frame.ChannelID.String(),
			"senderEndpointId": frame.SenderEndpointID.String(), "receiverEndpointId": frame.ReceiverEndpointID.String(),
			"direction": frame.Direction, "sequence": frame.Sequence, "keyGeneration": frame.KeyGeneration,
			"status": frame.Status, "ciphertextBytes": len(frame.Ciphertext),
			"receivedAt": frame.ReceivedAt, "acknowledgedAt": frame.AcknowledgedAt,
		})
	}
	acks := make([]gin.H, 0, len(value.ACKs))
	for _, ack := range value.ACKs {
		acks = append(acks, gin.H{
			"direction": ack.Direction, "senderEndpointId": ack.SenderEndpointID.String(),
			"receiverEndpointId": ack.ReceiverEndpointID.String(), "keyGeneration": ack.KeyGeneration,
			"highestContiguousSequence": ack.HighestContiguousSequence, "updatedAt": ack.UpdatedAt,
		})
	}
	return gin.H{"session": projectSession(value.Session), "frames": frames, "acks": acks}
}

func managementErrorPayload(err error) (int, gin.H, string) {
	var problem *domain.Problem
	if !errors.As(err, &problem) {
		return http.StatusInternalServerError, gin.H{
			"code":    "HARNESS_INTERNAL",
			"message": "Harness operation failed",
		}, "HARNESS_INTERNAL"
	}
	status := http.StatusInternalServerError
	switch problem.Code {
	case domain.CodeInvalidArgument:
		status = http.StatusBadRequest
	case domain.CodeNotFound:
		status = http.StatusNotFound
	case domain.CodeSecurityViolation, domain.CodeRevoked:
		status = http.StatusForbidden
	case domain.CodeConflict, domain.CodeInvalidState:
		status = http.StatusConflict
	case domain.CodeExpired:
		status = http.StatusGone
	case domain.CodeResourceLimit:
		status = http.StatusTooManyRequests
	}
	return status, gin.H{"code": problem.Code, "message": problem.Message}, string(problem.Code)
}

func writeManagementError(c *gin.Context, err error) {
	status, payload, _ := managementErrorPayload(err)
	c.JSON(status, payload)
}
