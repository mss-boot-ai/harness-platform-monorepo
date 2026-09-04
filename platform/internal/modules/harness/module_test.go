package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestModuleComposesWithThinHostRegistry(t *testing.T) {
	registry, err := business.Compose(migration.New(), New())
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 {
		t.Fatalf("descriptor count = %d, want 1", len(descriptors))
	}
	if descriptors[0].Name != ModuleName || descriptors[0].Version != "0.1.0" {
		t.Fatalf("unexpected descriptor: %#v", descriptors[0])
	}
	phases, err := registry.MigrationPhaseRunners()
	if err != nil {
		t.Fatalf("MigrationPhaseRunners: %v", err)
	}
	if err := phases.Business.ValidateRegistrations(); err != nil {
		t.Fatalf("business migrations: %v", err)
	}
}

func TestProtectedHealthRouteUsesCurrentPrincipalAndSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:harness-module?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	if err := db.AutoMigrate(
		new(models.Role),
		new(models.Menu),
		new(models.CasbinRule),
		new(models.ConfigRevision),
		new(migrationmodels.Migration),
	); err != nil {
		t.Fatalf("create Admin authorization schema: %v", err)
	}
	if err := applyHarnessAuthorizationMigration(db, HarnessAuthorizationMigrationID.String()); err != nil {
		t.Fatalf("applyHarnessAuthorizationMigration: %v", err)
	}

	router := gin.New()
	if err := registerRoutes(router.Group("/api"), business.Runtime{
		RequestDatabase: func(context.Context) (*gorm.DB, bool) { return db, true },
		Principal:       func(*gin.Context) security.Verifier { return testPrincipal{} },
		Events:          testEvents{},
	}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/harness/v1/health", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ready" || body.UserID != "owner" || body.TenantID != "tenant" {
		t.Fatalf("unexpected response: %#v", body)
	}
	if len(body.SchemaMigrations) != 3 || body.SchemaMigrations[2] != HarnessAuthorizationMigrationID.String() {
		t.Fatalf("unexpected schema migrations: %#v", body.SchemaMigrations)
	}
}

func TestProtectedHealthRouteRejectsMissingPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := registerRoutes(router.Group("/api"), business.Runtime{
		RequestDatabase: func(context.Context) (*gorm.DB, bool) { return nil, false },
		Principal:       func(*gin.Context) security.Verifier { return nil },
		Events:          testEvents{},
	}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/harness/v1/health", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type testEvents struct{}

func (testEvents) Collect(context.Context, business.Event) {}

type testPrincipal struct{}

func (testPrincipal) GetUserID() string                        { return "owner" }
func (testPrincipal) GetTenantID() string                      { return "tenant" }
func (testPrincipal) GetRoleID() string                        { return "role" }
func (testPrincipal) GetEmail() string                         { return "owner@example.test" }
func (testPrincipal) GetUsername() string                      { return "owner" }
func (testPrincipal) GetRefreshTokenDisable() bool             { return false }
func (testPrincipal) SetRefreshTokenDisable(bool)              {}
func (testPrincipal) CheckToken(context.Context, string) error { return nil }
func (testPrincipal) Root() bool                               { return true }
func (testPrincipal) Verify(context.Context) (bool, security.Verifier, error) {
	return true, testPrincipal{}, nil
}
func (testPrincipal) GetPersonAccessToken() string { return "" }
func (testPrincipal) SetPersonAccessToken(string)  {}
