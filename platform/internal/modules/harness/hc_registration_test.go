package harness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/registration"
	adminconfig "github.com/mss-boot-io/mss-boot-admin/admin/config"
	adminmiddleware "github.com/mss-boot-io/mss-boot-admin/admin/middleware"
)

type hcChallengeResponse struct {
	ChallengeID string `json:"challengeId"`
	Challenge   string `json:"challenge"`
}

func TestHCRegistrationRoutesRequireBrowserSession(t *testing.T) {
	router, _ := managementRouter(t, testPrincipal{})
	for _, authorization := range []string{"", "Bearer admin-token"} {
		request := httptest.NewRequest(http.MethodPost, "/api/harness/v1/hc/challenges", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://hc.example")
		request.AddCookie(&http.Cookie{Name: adminmiddleware.BrowserSessionCookieName, Value: "browser-session"})
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if authorization == "" {
			// Without trusted-origin configuration this request fails after the browser-session gate.
			if response.Code != http.StatusForbidden {
				t.Fatalf("browser request status = %d, body=%s", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusUnauthorized {
			t.Fatalf("bearer request status = %d, body=%s", response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/harness/v1/hc/challenges", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://hc.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing browser session status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHCRegistrationRoutesCreateEndpointAndHttpOnlyRefreshCookie(t *testing.T) {
	origin := "https://hc.example"
	previousOrigin := adminconfig.Cfg.Application.Origin
	previousAllowed := append([]string(nil), adminconfig.Cfg.CORS.AllowOrigins...)
	adminconfig.Cfg.Application.Origin = origin
	adminconfig.Cfg.CORS.AllowOrigins = []string{origin}
	t.Cleanup(func() {
		adminconfig.Cfg.Application.Origin = previousOrigin
		adminconfig.Cfg.CORS.AllowOrigins = previousAllowed
	})

	router, persistence := managementRouter(t, testPrincipal{})
	challengeRequest := hcBrowserRequest(http.MethodPost, "/api/harness/v1/hc/challenges", `{}`, origin)
	challengeResponse := httptest.NewRecorder()
	router.ServeHTTP(challengeResponse, challengeRequest)
	if challengeResponse.Code != http.StatusCreated {
		t.Fatalf("challenge status = %d, body=%s", challengeResponse.Code, challengeResponse.Body.String())
	}
	var challenge hcChallengeResponse
	if err := json.Unmarshal(challengeResponse.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	challengeID, err := domain.ParseID(challenge.ChallengeID)
	if err != nil {
		t.Fatalf("parse challenge ID: %v", err)
	}
	challengeBytes, err := base64.RawURLEncoding.Strict().DecodeString(challenge.Challenge)
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	signingJWK, signingKey := hcTestSigningKey()
	kemJWK := hcTestPublicJWK(2)
	signingJKT, _ := signingJWK.Thumbprint()
	kemJKT, _ := kemJWK.Thumbprint()
	transcript, err := registration.BuildTranscript(registration.TranscriptInput{
		ChallengeID: challengeID, Challenge: challengeBytes, SigningJKT: signingJKT, KEMJKT: kemJKT,
		Origin: origin, EndpointName: "H5 browser", Assurance: registration.AssuranceWebSoftware,
		SoftwareVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("BuildTranscript: %v", err)
	}
	proof, err := awpcrypto.SignP1363LowS(signingKey, transcript)
	if err != nil {
		t.Fatalf("sign registration proof: %v", err)
	}
	body, err := json.Marshal(ginRegistrationRequest{
		ChallengeID: challenge.ChallengeID, Challenge: challenge.Challenge, EndpointName: "H5 browser",
		SigningPublicJWK: signingJWK, KEMPublicJWK: kemJWK, Assurance: string(registration.AssuranceWebSoftware),
		SoftwareVersion: "0.1.0", Proof: base64.RawURLEncoding.EncodeToString(proof),
	})
	if err != nil {
		t.Fatalf("encode registration request: %v", err)
	}
	registerRequest := hcBrowserRequest(http.MethodPost, "/api/harness/v1/hc/endpoints", string(body), origin)
	registerResponse := httptest.NewRecorder()
	router.ServeHTTP(registerResponse, registerRequest)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, body=%s", registerResponse.Code, registerResponse.Body.String())
	}
	if bytes.Contains(registerResponse.Body.Bytes(), []byte("refresh")) {
		t.Fatalf("registration body exposed refresh credential: %s", registerResponse.Body.String())
	}
	var responseBody struct {
		EndpointID  string `json:"endpointId"`
		AccessToken string `json:"accessToken"`
		TokenType   string `json:"tokenType"`
	}
	if err := json.Unmarshal(registerResponse.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if responseBody.EndpointID == "" || len(responseBody.AccessToken) != 43 || responseBody.TokenType != "DPoP" {
		t.Fatalf("unexpected registration response: %#v", responseBody)
	}
	cookies := registerResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != hcRefreshCookieName || cookies[0].Path != hcRefreshCookiePath || !cookies[0].HttpOnly {
		t.Fatalf("unexpected refresh cookie: %#v", cookies)
	}
	endpoints, err := persistence.ListEndpoints(t.Context(), "owner", "tenant", 10)
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].ID.String() != responseBody.EndpointID {
		t.Fatalf("registered endpoints: %#v", endpoints)
	}

	replayResponse := httptest.NewRecorder()
	router.ServeHTTP(replayResponse, hcBrowserRequest(http.MethodPost, "/api/harness/v1/hc/endpoints", string(body), origin))
	if replayResponse.Code != http.StatusConflict {
		t.Fatalf("registration replay status = %d, body=%s", replayResponse.Code, replayResponse.Body.String())
	}
}

type ginRegistrationRequest struct {
	ChallengeID      string                  `json:"challengeId"`
	Challenge        string                  `json:"challenge"`
	EndpointName     string                  `json:"endpointName"`
	SigningPublicJWK awpcrypto.P256PublicJWK `json:"signingPublicJwk"`
	KEMPublicJWK     awpcrypto.P256PublicJWK `json:"kemPublicJwk"`
	Assurance        string                  `json:"assurance"`
	SoftwareVersion  string                  `json:"softwareVersion"`
	Proof            string                  `json:"proof"`
}

func hcBrowserRequest(method, path, body, origin string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.AddCookie(&http.Cookie{Name: adminmiddleware.BrowserSessionCookieName, Value: "browser-session"})
	return request
}

func hcTestSigningKey() (awpcrypto.P256PublicJWK, *ecdsa.PrivateKey) {
	d := big.NewInt(1)
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(hcFixedScalar(d))
	return hcPublicJWK(x, y), &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
}

func hcTestPublicJWK(scalar int64) awpcrypto.P256PublicJWK {
	x, y := elliptic.P256().ScalarBaseMult(hcFixedScalar(big.NewInt(scalar)))
	return hcPublicJWK(x, y)
}

func hcPublicJWK(x, y *big.Int) awpcrypto.P256PublicJWK {
	return awpcrypto.P256PublicJWK{
		Curve: "P-256", KeyType: "EC",
		X: base64.RawURLEncoding.EncodeToString(hcFixedScalar(x)),
		Y: base64.RawURLEncoding.EncodeToString(hcFixedScalar(y)),
	}
}

func hcFixedScalar(value *big.Int) []byte {
	result := make([]byte, 32)
	value.FillBytes(result)
	return result
}
