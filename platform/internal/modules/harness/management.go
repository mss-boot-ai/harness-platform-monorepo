package harness

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
)

const maxManagementRequestBytes int64 = 16 * 1024

type managementContext struct {
	owner  string
	tenant string
	store  *store.Store
}

type enrollmentDecisionRequest struct {
	UserCode string `json:"userCode"`
}

func registerManagementRoutes(group *gin.RouterGroup, runtime business.Runtime) {
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	group.GET("/overview", withManagement(runtime, func(c *gin.Context, management managementContext) {
		value, err := management.store.Overview(c.Request.Context(), management.owner, management.tenant)
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"pendingEnrollments":   value.PendingEnrollments,
			"activeEndpoints":      value.ActiveEndpoints,
			"activeSessions":       value.ActiveSessions,
			"unacknowledgedFrames": value.Unacknowledged,
			"conflictFrames":       value.ConflictFrames,
		})
	}))
	group.GET("/enrollments", withManagement(runtime, func(c *gin.Context, management managementContext) {
		values, err := management.store.ListEnrollments(c.Request.Context(), management.owner, management.tenant, queryLimit(c))
		if err != nil {
			writeManagementError(c, err)
			return
		}
		items := make([]gin.H, 0, len(values))
		for _, value := range values {
			items = append(items, projectEnrollment(value))
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}))
	group.POST("/enrollments/:id/approve", enrollmentDecision(runtime, true))
	group.POST("/enrollments/:id/deny", enrollmentDecision(runtime, false))
	group.GET("/endpoints", withManagement(runtime, func(c *gin.Context, management managementContext) {
		values, err := management.store.ListEndpoints(c.Request.Context(), management.owner, management.tenant, queryLimit(c))
		if err != nil {
			writeManagementError(c, err)
			return
		}
		items := make([]gin.H, 0, len(values))
		for _, value := range values {
			items = append(items, projectEndpoint(value))
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}))
	group.POST("/endpoints/:id/suspend", endpointAction(runtime, func(value *domain.Endpoint, now time.Time) error {
		return value.Suspend(now)
	}))
	group.POST("/endpoints/:id/resume", endpointAction(runtime, func(value *domain.Endpoint, now time.Time) error {
		return value.Resume(now)
	}))
	group.POST("/endpoints/:id/revoke", withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		value, err := management.store.RevokeEndpointForOwner(c.Request.Context(), id, management.owner, management.tenant, time.Now().UTC())
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, projectEndpoint(value))
	}))
	group.GET("/sessions", withManagement(runtime, func(c *gin.Context, management managementContext) {
		values, err := management.store.ListSessions(c.Request.Context(), management.owner, management.tenant, queryLimit(c))
		if err != nil {
			writeManagementError(c, err)
			return
		}
		items := make([]gin.H, 0, len(values))
		for _, value := range values {
			items = append(items, projectSession(value))
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}))
	group.POST("/sessions/:id/close", withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		value, err := management.store.CloseSessionForOwner(c.Request.Context(), id, management.owner, management.tenant, time.Now().UTC())
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, projectSession(value))
	}))
	group.GET("/sessions/:id/delivery", withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		value, err := management.store.Delivery(c.Request.Context(), id, management.owner, management.tenant, queryLimit(c))
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, projectDelivery(value))
	}))
}

func withManagement(runtime business.Runtime, next func(*gin.Context, managementContext)) gin.HandlerFunc {
	return func(c *gin.Context) {
		management, ok := resolveManagement(c, runtime)
		if ok {
			next(c, management)
		}
	}
}

func withManagementResource(runtime business.Runtime, next func(*gin.Context, managementContext, domain.ID)) gin.HandlerFunc {
	return withManagement(runtime, func(c *gin.Context, management managementContext) {
		id, err := domain.ParseID(c.Param("id"))
		if err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "resource ID is invalid", err))
			return
		}
		next(c, management, id)
	})
}

func enrollmentDecision(runtime business.Runtime, approve bool) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		var request enrollmentDecisionRequest
		if err := decodeManagementJSON(c, &request); err != nil || normalizeUserCode(request.UserCode) == "" {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "valid userCode is required", err))
			return
		}
		now := time.Now().UTC()
		var value domain.Enrollment
		var err error
		if approve {
			value, err = management.store.ApproveEnrollmentWithCode(c.Request.Context(), id, hashUserCode(request.UserCode), management.owner, management.tenant, now)
		} else {
			value, err = management.store.DenyEnrollmentWithCode(c.Request.Context(), id, hashUserCode(request.UserCode), management.owner, management.tenant, now)
		}
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, projectEnrollment(value))
	})
}

func endpointAction(runtime business.Runtime, mutate func(*domain.Endpoint, time.Time) error) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		value, err := management.store.UpdateEndpointForOwner(c.Request.Context(), id, management.owner, management.tenant, func(endpoint *domain.Endpoint) error {
			return mutate(endpoint, time.Now().UTC())
		})
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusOK, projectEndpoint(value))
	})
}

func resolveManagement(c *gin.Context, runtime business.Runtime) (managementContext, bool) {
	principal := runtime.Principal(c)
	if principal == nil || strings.TrimSpace(principal.GetUserID()) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "HARNESS_UNAUTHENTICATED", "message": "authenticated principal is required"})
		return managementContext{}, false
	}
	db, ok := runtime.RequestDatabase(c.Request.Context())
	if !ok || db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "HARNESS_DATABASE_UNAVAILABLE", "message": "Harness persistence is unavailable"})
		return managementContext{}, false
	}
	persistence, err := store.New(db)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "HARNESS_DATABASE_UNAVAILABLE", "message": "Harness persistence is unavailable"})
		return managementContext{}, false
	}
	return managementContext{owner: principal.GetUserID(), tenant: principal.GetTenantID(), store: persistence}, true
}

func decodeManagementJSON(c *gin.Context, destination any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxManagementRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func queryLimit(c *gin.Context) int {
	value, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		return 50
	}
	return value
}

func normalizeUserCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	return strings.ReplaceAll(value, " ", "")
}

func hashUserCode(value string) [32]byte {
	return sha256.Sum256([]byte(normalizeUserCode(value)))
}

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

func writeManagementError(c *gin.Context, err error) {
	var problem *domain.Problem
	if !errors.As(err, &problem) {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "HARNESS_INTERNAL", "message": "Harness operation failed"})
		return
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
	c.JSON(status, gin.H{"code": problem.Code, "message": problem.Message})
}
