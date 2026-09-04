package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/registration"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	adminconfig "github.com/mss-boot-io/mss-boot-admin/admin/config"
	adminmiddleware "github.com/mss-boot-io/mss-boot-admin/admin/middleware"
)

const hcRefreshCookieName = "harness_hc_refresh"
const hcRefreshCookiePath = "/gateway/v1/tokens/refresh"

type hcRegistrationOperations interface {
	IssueChallenge(context.Context, string, string, string) (registration.IssuedChallenge, error)
	Register(context.Context, string, string, string, registration.RegisterInput) (registration.Registration, error)
}

type hcRegistrationFactory func(*store.Store) hcRegistrationOperations

type hcRegistrationRequest struct {
	ChallengeID      string          `json:"challengeId"`
	Challenge        string          `json:"challenge"`
	EndpointName     string          `json:"endpointName"`
	SigningPublicJWK json.RawMessage `json:"signingPublicJwk"`
	KEMPublicJWK     json.RawMessage `json:"kemPublicJwk"`
	Assurance        string          `json:"assurance"`
	SoftwareVersion  string          `json:"softwareVersion"`
	Proof            string          `json:"proof"`
}

func registerHCRegistrationRoutes(group *gin.RouterGroup, runtime business.Runtime) {
	registerHCRegistrationRoutesWithFactory(group, runtime, func(persistence *store.Store) hcRegistrationOperations {
		return registration.Service{Persistence: persistence}
	})
}

func registerHCRegistrationRoutesWithFactory(
	group *gin.RouterGroup,
	runtime business.Runtime,
	factory hcRegistrationFactory,
) {
	group.POST("/hc/challenges", withManagement(runtime, func(c *gin.Context, management managementContext) {
		if !requireHCBrowserSession(c) {
			return
		}
		if err := decodeOptionalEmptyManagementJSON(c); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "request body is invalid", err))
			return
		}
		issued, err := factory(management.store).IssueChallenge(
			c.Request.Context(), management.owner, management.tenant, c.GetHeader("Origin"),
		)
		if err != nil {
			writeManagementError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"challengeId": issued.ID.String(), "challenge": issued.Challenge,
			"expiresAt": issued.ExpiresAt, "transcriptVersion": "MSS-HC-REGISTER-V1",
		})
	}))

	group.POST("/hc/endpoints", withManagement(runtime, func(c *gin.Context, management managementContext) {
		if !requireHCBrowserSession(c) {
			return
		}
		var request hcRegistrationRequest
		if err := decodeManagementJSON(c, &request); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "request body is invalid", err))
			return
		}
		challengeID, err := domain.ParseID(request.ChallengeID)
		if err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "challenge ID is invalid", err))
			return
		}
		result, err := factory(management.store).Register(
			c.Request.Context(), management.owner, management.tenant, c.GetHeader("Origin"),
			registration.RegisterInput{
				ChallengeID: challengeID, Challenge: request.Challenge, EndpointName: request.EndpointName,
				SigningPublicJWK: request.SigningPublicJWK, KEMPublicJWK: request.KEMPublicJWK,
				Assurance: registration.Assurance(request.Assurance), SoftwareVersion: request.SoftwareVersion,
				Proof: request.Proof,
			},
		)
		if err != nil {
			writeManagementError(c, err)
			return
		}
		setHCRefreshCookie(c, result)
		c.JSON(http.StatusCreated, gin.H{
			"endpointId": result.EndpointID.String(), "tokenType": "DPoP",
			"accessToken": result.AccessToken, "accessExpiresAt": result.AccessExpiresAt,
			"signingJkt": result.SigningJKT, "kemJkt": result.KEMJKT,
		})
	}))
}

func requireHCBrowserSession(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if !adminmiddleware.RequestUsesBrowserSession(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": "HARNESS_BROWSER_SESSION_REQUIRED", "message": "Admin browser session is required",
		})
		return false
	}
	if !adminmiddleware.IsTrustedBrowserOrigin(c) {
		c.JSON(http.StatusForbidden, gin.H{
			"code": "HARNESS_ORIGIN_FORBIDDEN", "message": "trusted browser origin is required",
		})
		return false
	}
	return true
}

func setHCRefreshCookie(c *gin.Context, result registration.Registration) {
	maxAge := int(time.Until(result.RefreshExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: hcRefreshCookieName, Value: result.RefreshToken, Path: hcRefreshCookiePath,
		Expires: result.RefreshExpiresAt, MaxAge: maxAge, HttpOnly: true,
		Secure:   adminconfig.Cfg.Auth.BrowserSession.Secure || c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}
