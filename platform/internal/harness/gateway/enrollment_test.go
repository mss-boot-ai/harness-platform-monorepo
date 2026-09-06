package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	abaenrollment "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/enrollment"
)

func TestABADeviceEnrollmentHTTPFlow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, _, _, _, _, _, _ := gatewayFixture(t, now)
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082",
	}, persistence, deterministicGatewayBytes(2048), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	signingJWK, signingKey := gatewaySigningKey(7)
	kemJWK, _ := gatewaySigningKey(8)
	signingJKT, _ := signingJWK.Thumbprint()
	kemJKT, _ := kemJWK.Thumbprint()
	clientNonce := bytes.Repeat([]byte{31}, 32)
	transcript, err := abaenrollment.StartTranscript(clientNonce, signingJKT, kemJKT, "Test ABA", "0.1.0")
	if err != nil {
		t.Fatalf("StartTranscript: %v", err)
	}
	proof, err := awpcrypto.SignP1363LowS(signingKey, transcript)
	if err != nil {
		t.Fatalf("sign start transcript: %v", err)
	}
	startBody, _ := json.Marshal(map[string]any{
		"endpointName": "Test ABA", "softwareVersion": "0.1.0",
		"signingPublicJwk": signingJWK, "kemPublicJwk": kemJWK,
		"clientNonce": base64.RawURLEncoding.EncodeToString(clientNonce),
		"proof":       base64.RawURLEncoding.EncodeToString(proof),
	})
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, httptest.NewRequest(http.MethodPost, "/gateway/v1/enrollments", bytes.NewReader(startBody)))
	if startResponse.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var started struct {
		EnrollmentID string `json:"enrollmentId"`
		DeviceCode   string `json:"deviceCode"`
		UserCode     string `json:"userCode"`
	}
	if err := json.Unmarshal(startResponse.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	poll := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/gateway/v1/enrollments/"+started.EnrollmentID, nil)
		request.Header.Set("Authorization", "Device "+started.DeviceCode)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := poll(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "PENDING") {
		t.Fatalf("pending poll status=%d body=%s", response.Code, response.Body.String())
	}
	enrollmentID, err := domain.ParseID(started.EnrollmentID)
	if err != nil {
		t.Fatalf("parse enrollment ID: %v", err)
	}
	normalizedCode := strings.ReplaceAll(started.UserCode, "-", "")
	if _, err := persistence.ApproveEnrollmentWithCode(
		t.Context(), enrollmentID, sha256.Sum256([]byte(normalizedCode)), "owner", "tenant", now.Add(time.Second),
	); err != nil {
		t.Fatalf("approve enrollment: %v", err)
	}
	if response := poll(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "APPROVED") {
		t.Fatalf("approved poll status=%d body=%s", response.Code, response.Body.String())
	}
	deviceRaw, _ := base64.RawURLEncoding.Strict().DecodeString(started.DeviceCode)
	consumeNonce := bytes.Repeat([]byte{32}, 32)
	consumeTranscript, err := abaenrollment.ConsumeTranscript(enrollmentID, deviceRaw, consumeNonce)
	if err != nil {
		t.Fatalf("ConsumeTranscript: %v", err)
	}
	consumeProof, err := awpcrypto.SignP1363LowS(signingKey, consumeTranscript)
	if err != nil {
		t.Fatalf("sign consume transcript: %v", err)
	}
	consumeBody, _ := json.Marshal(map[string]any{
		"deviceCode":  started.DeviceCode,
		"clientNonce": base64.RawURLEncoding.EncodeToString(consumeNonce),
		"proof":       base64.RawURLEncoding.EncodeToString(consumeProof),
	})
	consumeResponse := httptest.NewRecorder()
	handler.ServeHTTP(consumeResponse, httptest.NewRequest(
		http.MethodPost, "/gateway/v1/enrollments/"+started.EnrollmentID+"/consume", bytes.NewReader(consumeBody),
	))
	if consumeResponse.Code != http.StatusCreated {
		t.Fatalf("consume status=%d body=%s", consumeResponse.Code, consumeResponse.Body.String())
	}
	var consumed struct {
		EndpointID  string `json:"endpointId"`
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(consumeResponse.Body.Bytes(), &consumed); err != nil {
		t.Fatalf("decode consume: %v", err)
	}
	accessRaw, err := base64.RawURLEncoding.Strict().DecodeString(consumed.AccessToken)
	if err != nil || len(accessRaw) != 32 {
		t.Fatalf("decode access token: %v", err)
	}
	endpoint, _, err := persistence.AuthenticateAccessToken(t.Context(), sha256.Sum256(accessRaw), now.Add(2*time.Second))
	if err != nil || endpoint.ID.String() != consumed.EndpointID {
		t.Fatalf("authenticate enrolled ABA endpoint=%#v error=%v", endpoint, err)
	}
}
