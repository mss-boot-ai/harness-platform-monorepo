package harness

import (
	"bytes"
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
	group.POST("/endpoints/:id/suspend", endpointAction(runtime, "endpoint.suspend", func(value *domain.Endpoint, now time.Time) error {
		return value.Suspend(now)
	}))
	group.POST("/endpoints/:id/resume", endpointAction(runtime, "endpoint.resume", func(value *domain.Endpoint, now time.Time) error {
		return value.Resume(now)
	}))
	group.POST("/endpoints/:id/revoke", revokeEndpoint(runtime))
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
	group.POST("/sessions/:id/close", closeSession(runtime))
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
	return managementContext{
		owner:  strings.TrimSpace(principal.GetUserID()),
		tenant: strings.TrimSpace(principal.GetTenantID()),
		store:  persistence,
	}, true
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

func decodeOptionalEmptyManagementJSON(c *gin.Context) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxManagementRequestBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	if bytes.Equal(body, []byte("null")) {
		return errors.New("null request body is not allowed")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var empty struct{}
	if err := decoder.Decode(&empty); err != nil {
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
