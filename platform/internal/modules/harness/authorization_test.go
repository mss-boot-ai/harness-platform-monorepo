package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type authorizationPrincipal struct {
	testPrincipal
	roleID string
	root   bool
}

func (value authorizationPrincipal) GetRoleID() string { return value.roleID }
func (value authorizationPrincipal) Root() bool        { return value.root }
func (value authorizationPrincipal) Verify(context.Context) (bool, security.Verifier, error) {
	return true, value, nil
}

func openAuthorizationTestDB(t *testing.T, withPolicyTable bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "authorization.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	modelsToMigrate := []any{
		new(models.Role),
		new(models.Menu),
		new(models.ConfigRevision),
		new(migrationmodels.Migration),
	}
	if withPolicyTable {
		modelsToMigrate = append(modelsToMigrate, new(models.CasbinRule))
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("migrate Admin authorization prerequisites: %v", err)
	}
	return db
}

func seedAuthorizationPolicy(t *testing.T, db *gorm.DB, roleID string, route harnessAuthorizationRoute) {
	t.Helper()
	if err := db.Create(&models.CasbinRule{
		PType: "p",
		V0:    roleID,
		V1:    adminpkg.APIAccessType.String(),
		V2:    route.path,
		V3:    route.method,
	}).Error; err != nil {
		t.Fatalf("seed authorization policy: %v", err)
	}
}

func concreteAuthorizationPath(path string) string {
	return strings.ReplaceAll(path, ":id", "11111111111111111111111111111111")
}

func serveAuthorizedRouteProbe(
	t *testing.T,
	dbResolver business.RequestDatabase,
	principal security.Verifier,
	route harnessAuthorizationRoute,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authorizer := newRequestAuthorizer(business.Runtime{
		RequestDatabase: dbResolver,
		Principal: func(*gin.Context) security.Verifier {
			return principal
		},
	})
	router.Handle(route.method, route.path, authorizer.Middleware(), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(route.method, concreteAuthorizationPath(route.path), nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestHarnessAuthorizationRouteMatrixIsExact(t *testing.T) {
	expected := map[string]string{
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/health":                   PermissionRead,
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/overview":                 PermissionRead,
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/enrollments":              PermissionRead,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/enrollments/:id/approve": PermissionApprove,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/enrollments/:id/deny":    PermissionApprove,
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/endpoints":                PermissionRead,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/hc/challenges":           PermissionOperate,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/hc/endpoints":            PermissionOperate,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/suspend":   PermissionRevoke,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/resume":    PermissionRevoke,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/revoke":    PermissionRevoke,
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/sessions":                 PermissionRead,
		http.MethodPost + " " + canonicalAdminAPIBasePath + "/harness/v1/sessions/:id/close":      PermissionOperate,
		http.MethodGet + " " + canonicalAdminAPIBasePath + "/harness/v1/sessions/:id/delivery":    PermissionRead,
	}
	if len(harnessAuthorizationRoutes) != len(expected) {
		t.Fatalf("authorization routes = %d, want %d", len(harnessAuthorizationRoutes), len(expected))
	}
	for _, route := range harnessAuthorizationRoutes {
		key := authorizationRouteKey(route.method, route.path)
		if expected[key] != route.permission {
			t.Fatalf("route %s permission = %q, want %q", key, route.permission, expected[key])
		}
	}
}

func TestHarnessAuthorizationAllowsEveryExactRouteForGrantedRole(t *testing.T) {
	db := openAuthorizationTestDB(t, true)
	database := func(context.Context) (*gorm.DB, bool) { return db, true }
	const roleID = "operator"
	for _, route := range harnessAuthorizationRoutes {
		seedAuthorizationPolicy(t, db, roleID, route)
		response := serveAuthorizedRouteProbe(t, database, authorizationPrincipal{roleID: roleID}, route)
		if response.Code != http.StatusNoContent {
			t.Fatalf("authorized %s %s status = %d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestHarnessAuthorizationRootAuthorizedDeniedAndUnavailable(t *testing.T) {
	db := openAuthorizationTestDB(t, true)
	database := func(context.Context) (*gorm.DB, bool) { return db, true }
	overview := harnessAuthorizationRouteIndex[authorizationRouteKey(
		http.MethodGet,
		canonicalAdminAPIBasePath+"/harness/v1/overview",
	)]

	root := serveAuthorizedRouteProbe(t, database, authorizationPrincipal{roleID: "root", root: true}, overview)
	if root.Code != http.StatusNoContent {
		t.Fatalf("root status = %d body=%s", root.Code, root.Body.String())
	}
	rootUnavailable := serveAuthorizedRouteProbe(
		t,
		func(context.Context) (*gorm.DB, bool) { return nil, false },
		authorizationPrincipal{roleID: "root", root: true},
		overview,
	)
	if rootUnavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("root unavailable status = %d body=%s", rootUnavailable.Code, rootUnavailable.Body.String())
	}

	seedAuthorizationPolicy(t, db, "operator", overview)
	authorized := serveAuthorizedRouteProbe(t, database, authorizationPrincipal{roleID: "operator"}, overview)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d body=%s", authorized.Code, authorized.Body.String())
	}
	denied := serveAuthorizedRouteProbe(t, database, authorizationPrincipal{roleID: "viewer"}, overview)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d body=%s", denied.Code, denied.Body.String())
	}
	missingRole := serveAuthorizedRouteProbe(t, database, authorizationPrincipal{}, overview)
	if missingRole.Code != http.StatusUnauthorized {
		t.Fatalf("missing role status = %d body=%s", missingRole.Code, missingRole.Body.String())
	}

	missingTableDB := openAuthorizationTestDB(t, false)
	missingTable := serveAuthorizedRouteProbe(
		t,
		func(context.Context) (*gorm.DB, bool) { return missingTableDB, true },
		authorizationPrincipal{roleID: "operator"},
		overview,
	)
	if missingTable.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing policy table status = %d body=%s", missingTable.Code, missingTable.Body.String())
	}
	rootMissingTable := serveAuthorizedRouteProbe(
		t,
		func(context.Context) (*gorm.DB, bool) { return missingTableDB, true },
		authorizationPrincipal{roleID: "root", root: true},
		overview,
	)
	if rootMissingTable.Code != http.StatusServiceUnavailable {
		t.Fatalf("root missing policy table status = %d body=%s", rootMissingTable.Code, rootMissingTable.Body.String())
	}
}

func TestHarnessAuthorizationRejectsWrongMethodPathAndPermission(t *testing.T) {
	db := openAuthorizationTestDB(t, true)
	database := func(context.Context) (*gorm.DB, bool) { return db, true }
	principal := authorizationPrincipal{roleID: "root", root: true}
	overviewPath := canonicalAdminAPIBasePath + "/harness/v1/overview"

	wrongMethod := serveAuthorizedRouteProbe(t, database, principal, harnessAuthorizationRoute{
		method: http.MethodPost,
		path:   overviewPath,
	})
	if wrongMethod.Code != http.StatusForbidden {
		t.Fatalf("wrong method status = %d body=%s", wrongMethod.Code, wrongMethod.Body.String())
	}
	wrongPath := serveAuthorizedRouteProbe(t, database, principal, harnessAuthorizationRoute{
		method: http.MethodGet,
		path:   canonicalAdminAPIBasePath + "/harness/v1/not-declared",
	})
	if wrongPath.Code != http.StatusForbidden {
		t.Fatalf("wrong path status = %d body=%s", wrongPath.Code, wrongPath.Body.String())
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	authorizer := newRequestAuthorizer(business.Runtime{
		RequestDatabase: database,
		Principal:       func(*gin.Context) security.Verifier { return principal },
	})
	router.GET(overviewPath, func(ctx *gin.Context) {
		if err := authorizer.Authorize(ctx, PermissionApprove); err == nil {
			ctx.Status(http.StatusNoContent)
			return
		}
		writeAuthorizationError(ctx, ErrHarnessAuthorizationDenied)
	})
	request := httptest.NewRequest(http.MethodGet, overviewPath, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("permission mismatch status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestHarnessAuthorizationMapCoversEveryRegisteredRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := registerRoutes(router.Group(canonicalAdminAPIBasePath), business.Runtime{
		RequestDatabase: func(context.Context) (*gorm.DB, bool) { return nil, false },
		Principal:       func(*gin.Context) security.Verifier { return authorizationPrincipal{roleID: "root", root: true} },
		Events:          testEvents{},
	}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	registered := make(map[string]struct{})
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, canonicalAdminAPIBasePath+"/harness/v1/") {
			registered[authorizationRouteKey(route.Method, route.Path)] = struct{}{}
		}
	}
	if len(registered) != len(harnessAuthorizationRoutes) {
		t.Fatalf("registered Harness routes = %d, authorization routes = %d", len(registered), len(harnessAuthorizationRoutes))
	}
	for _, route := range harnessAuthorizationRoutes {
		if _, ok := registered[authorizationRouteKey(route.method, route.path)]; !ok {
			t.Fatalf("authorization route is not registered: %s %s", route.method, route.path)
		}
	}
}
