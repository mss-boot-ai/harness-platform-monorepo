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
		Migrations: registerHarnessMigrations,
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
			{Code: PermissionRead, DisplayName: "Read Harness state", Description: "View owned endpoints, enrollments, sessions, and delivery status."},
			{Code: PermissionOperate, DisplayName: "Operate Harness sessions", Description: "Create and close owned ACP sessions."},
			{Code: PermissionApprove, DisplayName: "Approve Harness enrollments", Description: "Approve or deny endpoint enrollment requests."},
			{Code: PermissionRevoke, DisplayName: "Revoke Harness endpoints", Description: "Suspend or permanently revoke endpoint credentials."},
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
	if err := business.RequireAppliedMigrations(
		ctx,
		db,
		store.SchemaMigrationID,
		store.M1PersistenceMigrationID,
		store.M2IdentityMigrationID,
		HarnessAuthorizationMigrationID,
		HarnessHCRegistrationAuthorizationMigrationID,
		store.M2GatewayMigrationID,
		store.M2ConnectionMigrationID,
		store.ReliabilityMigrationID,
	); err != nil {
		return err
	}
	if err := store.VerifyAllSchema(db); err != nil {
		return err
	}
	return verifyHarnessAuthorizationReadiness(db)
}

type healthResponse struct {
	Status           string   `json:"status"`
	Module           string   `json:"module"`
	UserID           string   `json:"userId"`
	TenantID         string   `json:"tenantId"`
	SchemaMigrations []string `json:"schemaMigrations"`
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
	group.Use(newRequestAuthorizer(runtime).Middleware())
	group.GET("/health", func(c *gin.Context) {
		principal := runtime.Principal(c)
		if nilVerifier(principal) || principal.GetUserID() == "" {
			c.JSON(401, gin.H{"code": "HARNESS_UNAUTHENTICATED", "message": "authenticated principal is required"})
			return
		}
		db, ok := runtime.RequestDatabase(c.Request.Context())
		if !ok || db == nil {
			c.JSON(503, gin.H{"code": "HARNESS_DATABASE_UNAVAILABLE", "message": "Harness persistence is unavailable"})
			return
		}
		readyDB := db.WithContext(c.Request.Context())
		if err := store.VerifyAllSchema(readyDB); err != nil {
			c.JSON(503, gin.H{"code": "HARNESS_NOT_READY", "message": "Harness schema is not ready"})
			return
		}
		if err := verifyHarnessAuthorizationReadiness(readyDB); err != nil {
			c.JSON(503, gin.H{"code": "HARNESS_AUTHORIZATION_UNAVAILABLE", "message": "Harness authorization is unavailable"})
			return
		}
		c.JSON(200, healthResponse{
			Status:   "ready",
			Module:   ModuleName,
			UserID:   principal.GetUserID(),
			TenantID: principal.GetTenantID(),
			SchemaMigrations: []string{
				store.SchemaMigrationID.String(),
				store.M1PersistenceMigrationID.String(),
				store.M2IdentityMigrationID.String(),
				HarnessAuthorizationMigrationID.String(),
				HarnessHCRegistrationAuthorizationMigrationID.String(),
				store.M2GatewayMigrationID.String(),
				store.M2ConnectionMigrationID.String(),
				store.ReliabilityMigrationID.String(),
			},
		})
	})
	registerManagementRoutes(group, runtime)
	return nil
}
