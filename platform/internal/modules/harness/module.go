package harness

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"gorm.io/gorm"
)

const ModuleName = "harness"

type Module struct{}

func New() business.Module { return Module{} }

func (Module) Name() string { return ModuleName }

func (Module) Register(registry *business.Registry) error {
	if registry == nil {
		return fmt.Errorf("%s registry is required", ModuleName)
	}
	return registry.Register(business.Registration{
		Descriptor: descriptor(),
		Migrations: store.RegisterMigrations,
		Readiness:  readiness,
		Routes:     registerRoutes,
	})
}

func descriptor() business.Descriptor {
	return business.Descriptor{
		Name:        ModuleName,
		DisplayName: "Harness Platform",
		Description: "Endpoint identity, ACP sessions, opaque relay delivery, and operations.",
		Version:     "0.1.0",
		Model:       new(domain.Endpoint),
		Permissions: []business.Permission{
			{
				Code:        "harness:read",
				DisplayName: "Read Harness state",
				Description: "View owned endpoints, enrollments, sessions, and delivery status.",
			},
			{
				Code:        "harness:operate",
				DisplayName: "Operate Harness sessions",
				Description: "Create and close owned ACP sessions.",
			},
			{
				Code:        "harness:approve",
				DisplayName: "Approve Harness enrollments",
				Description: "Approve or deny endpoint enrollment requests.",
			},
			{
				Code:        "harness:revoke",
				DisplayName: "Revoke Harness endpoints",
				Description: "Suspend or permanently revoke endpoint credentials.",
			},
		},
		Menu: business.Menu{
			Path:          "/harness",
			DisplayName:   "Harness 平台",
			DisplayNameEn: "Harness Platform",
			Icon:          "RobotOutlined",
			Order:         45,
		},
	}
}

func readiness(ctx context.Context, db *gorm.DB) error {
	if err := business.RequireAppliedMigrations(ctx, db, store.SchemaMigrationID); err != nil {
		return err
	}
	return store.VerifySchema(db)
}

type healthResponse struct {
	Status          string `json:"status"`
	Module          string `json:"module"`
	UserID          string `json:"userId"`
	TenantID        string `json:"tenantId"`
	SchemaMigration string `json:"schemaMigration"`
}

func registerRoutes(protectedAPI *gin.RouterGroup, runtime business.Runtime) error {
	if protectedAPI == nil {
		return fmt.Errorf("%s protected API group is required", ModuleName)
	}
	if runtime.RequestDatabase == nil {
		return fmt.Errorf("%s request database resolver is required", ModuleName)
	}
	if runtime.Principal == nil {
		return fmt.Errorf("%s principal resolver is required", ModuleName)
	}

	group := protectedAPI.Group("/harness/v1")
	group.GET("/health", func(c *gin.Context) {
		principal := runtime.Principal(c)
		if principal == nil || principal.GetUserID() == "" {
			c.JSON(401, gin.H{
				"code":    "HARNESS_UNAUTHENTICATED",
				"message": "authenticated principal is required",
			})
			return
		}
		db, ok := runtime.RequestDatabase(c.Request.Context())
		if !ok || db == nil {
			c.JSON(503, gin.H{
				"code":    "HARNESS_DATABASE_UNAVAILABLE",
				"message": "Harness persistence is unavailable",
			})
			return
		}
		if err := store.VerifySchema(db.WithContext(c.Request.Context())); err != nil {
			c.JSON(503, gin.H{
				"code":    "HARNESS_NOT_READY",
				"message": "Harness schema is not ready",
			})
			return
		}
		c.JSON(200, healthResponse{
			Status:          "ready",
			Module:          ModuleName,
			UserID:          principal.GetUserID(),
			TenantID:        principal.GetTenantID(),
			SchemaMigration: store.SchemaMigrationID.String(),
		})
	})
	registerManagementRoutes(group, runtime)
	return nil
}
