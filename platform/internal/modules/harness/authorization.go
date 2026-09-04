package harness

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
)

const (
	PermissionRead    = "harness:read"
	PermissionOperate = "harness:operate"
	PermissionApprove = "harness:approve"
	PermissionRevoke  = "harness:revoke"

	canonicalAdminAPIBasePath = "/admin/api"
)

var (
	ErrHarnessAuthenticationRequired   = errors.New("harness authentication required")
	ErrHarnessAuthorizationDenied      = errors.New("harness authorization denied")
	ErrHarnessAuthorizationUnavailable = errors.New("harness authorization unavailable")
)

type harnessAuthorizationRoute struct {
	permission string
	method     string
	path       string
}

// Policies always use the production Thin Host path. Tests that mount the
// protected group directly at /api are normalized by canonicalAdminRoutePath.
var harnessAuthorizationRoutes = []harnessAuthorizationRoute{
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/health"},
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/overview"},
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/enrollments"},
	{permission: PermissionApprove, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/enrollments/:id/approve"},
	{permission: PermissionApprove, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/enrollments/:id/deny"},
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/endpoints"},
	{permission: PermissionRevoke, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/suspend"},
	{permission: PermissionRevoke, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/resume"},
	{permission: PermissionRevoke, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/endpoints/:id/revoke"},
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/sessions"},
	{permission: PermissionOperate, method: http.MethodPost, path: canonicalAdminAPIBasePath + "/harness/v1/sessions/:id/close"},
	{permission: PermissionRead, method: http.MethodGet, path: canonicalAdminAPIBasePath + "/harness/v1/sessions/:id/delivery"},
}

var harnessAuthorizationRouteIndex = func() map[string]harnessAuthorizationRoute {
	index := make(map[string]harnessAuthorizationRoute, len(harnessAuthorizationRoutes))
	for _, route := range harnessAuthorizationRoutes {
		key := authorizationRouteKey(route.method, route.path)
		if _, exists := index[key]; exists {
			panic("duplicate Harness authorization route: " + key)
		}
		index[key] = route
	}
	return index
}()

type requestAuthorizer struct {
	database  business.RequestDatabase
	principal business.PrincipalResolver
}

func newRequestAuthorizer(runtime business.Runtime) *requestAuthorizer {
	return &requestAuthorizer{
		database:  runtime.RequestDatabase,
		principal: runtime.Principal,
	}
}

func (authorizer *requestAuthorizer) Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		route, declared := harnessRouteForRequest(ctx)
		if !declared {
			writeAuthorizationError(ctx, ErrHarnessAuthorizationDenied)
			ctx.Abort()
			return
		}
		if err := authorizer.Authorize(ctx, route.permission); err != nil {
			writeAuthorizationError(ctx, err)
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}

// Authorize binds a permission to the exact HTTP method and production Admin
// path. It resolves the authoritative request database before the Root shortcut
// so an unavailable authorization store always fails closed.
func (authorizer *requestAuthorizer) Authorize(ctx *gin.Context, permission string) error {
	if authorizer == nil || authorizer.database == nil || authorizer.principal == nil || ctx == nil || ctx.Request == nil {
		return ErrHarnessAuthorizationUnavailable
	}
	route, declared := harnessRouteForRequest(ctx)
	if !declared || route.permission != permission {
		return ErrHarnessAuthorizationDenied
	}
	principal := authorizer.principal(ctx)
	if nilVerifier(principal) || strings.TrimSpace(principal.GetRoleID()) == "" {
		return ErrHarnessAuthenticationRequired
	}
	db, ok := authorizer.database(ctx.Request.Context())
	if !ok || db == nil {
		return ErrHarnessAuthorizationUnavailable
	}
	// The Root shortcut is an authorization decision, not a storage fallback.
	// Losing the canonical Casbin table must fail closed for every principal.
	if !db.Migrator().HasTable(new(models.CasbinRule)) {
		return ErrHarnessAuthorizationUnavailable
	}
	if principal.Root() {
		return nil
	}

	var count int64
	if err := db.WithContext(ctx.Request.Context()).Model(new(models.CasbinRule)).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p",
		strings.TrimSpace(principal.GetRoleID()),
		adminpkg.APIAccessType.String(),
		route.path,
		route.method,
	).Count(&count).Error; err != nil {
		return fmt.Errorf("%w: read Admin policy", ErrHarnessAuthorizationUnavailable)
	}
	if count != 1 {
		return ErrHarnessAuthorizationDenied
	}
	return nil
}

func harnessRouteForRequest(ctx *gin.Context) (harnessAuthorizationRoute, bool) {
	if ctx == nil || ctx.Request == nil {
		return harnessAuthorizationRoute{}, false
	}
	path := canonicalAdminRoutePath(ctx.FullPath())
	route, ok := harnessAuthorizationRouteIndex[authorizationRouteKey(ctx.Request.Method, path)]
	return route, ok
}

func canonicalAdminRoutePath(path string) string {
	if strings.HasPrefix(path, "/api/harness/v1/") {
		return "/admin" + path
	}
	return path
}

func authorizationRouteKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func writeAuthorizationError(ctx *gin.Context, err error) {
	ctx.Header("Cache-Control", "no-store")
	switch {
	case errors.Is(err, ErrHarnessAuthenticationRequired):
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"code":    "HARNESS_UNAUTHENTICATED",
			"message": "authenticated principal is required",
		})
	case errors.Is(err, ErrHarnessAuthorizationDenied):
		ctx.JSON(http.StatusForbidden, gin.H{
			"code":    "HARNESS_FORBIDDEN",
			"message": "Harness permission is required",
		})
	default:
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    "HARNESS_AUTHORIZATION_UNAVAILABLE",
			"message": "Harness authorization is unavailable",
		})
	}
}

func nilVerifier(value security.Verifier) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
