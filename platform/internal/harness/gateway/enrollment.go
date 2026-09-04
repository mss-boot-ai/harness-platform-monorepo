package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	abaenrollment "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/enrollment"
)

type enrollmentStartRequest struct {
	EndpointName     string          `json:"endpointName"`
	SoftwareVersion  string          `json:"softwareVersion"`
	SigningPublicJWK json.RawMessage `json:"signingPublicJwk"`
	KEMPublicJWK     json.RawMessage `json:"kemPublicJwk"`
	ClientNonce      string          `json:"clientNonce"`
	Proof            string          `json:"proof"`
}

type enrollmentConsumeRequest struct {
	DeviceCode  string `json:"deviceCode"`
	ClientNonce string `json:"clientNonce"`
	Proof       string `json:"proof"`
}

func (server *Server) startEnrollment(writer http.ResponseWriter, request *http.Request) {
	var input enrollmentStartRequest
	if err := decodeGatewayJSON(writer, request, &input); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "enrollment request is invalid")
		return
	}
	result, err := (abaenrollment.Service{
		Persistence: server.persistence, Random: server.random, Now: server.now,
		VerificationURI: server.config.VerificationURI,
	}).Start(request.Context(), abaenrollment.StartInput{
		EndpointName: input.EndpointName, SoftwareVersion: input.SoftwareVersion,
		SigningPublicJWK: input.SigningPublicJWK, KEMPublicJWK: input.KEMPublicJWK,
		ClientNonce: input.ClientNonce, Proof: input.Proof,
	})
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"enrollmentId": result.EnrollmentID.String(), "deviceCode": result.DeviceCode,
		"userCode": result.UserCode, "verificationUri": result.VerificationURI,
		"expiresAt": result.ExpiresAt, "intervalSeconds": result.IntervalSeconds,
	})
}

func (server *Server) pollEnrollment(writer http.ResponseWriter, request *http.Request) {
	id, deviceCode, ok := enrollmentCredential(request)
	if !ok {
		writeGatewayError(writer, http.StatusUnauthorized, "DEVICE_CODE_INVALID", "enrollment credential is invalid")
		return
	}
	value, err := (abaenrollment.Service{Persistence: server.persistence, Now: server.now}).Poll(request.Context(), id, deviceCode)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"enrollmentId": value.ID.String(), "status": value.Status, "expiresAt": value.ExpiresAt,
	})
}

func (server *Server) consumeEnrollment(writer http.ResponseWriter, request *http.Request) {
	id, err := domain.ParseID(request.PathValue("id"))
	if err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "enrollment ID is invalid")
		return
	}
	var input enrollmentConsumeRequest
	if err := decodeGatewayJSON(writer, request, &input); err != nil {
		writeGatewayError(writer, http.StatusBadRequest, "INVALID_REQUEST", "consume request is invalid")
		return
	}
	result, err := (abaenrollment.Service{Persistence: server.persistence, Random: server.random, Now: server.now}).Consume(
		request.Context(), id, input.DeviceCode, input.ClientNonce, input.Proof,
	)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"endpointId": result.EndpointID.String(), "credentialId": result.CredentialID.String(),
		"tokenType": "DPoP", "accessToken": result.AccessToken, "accessExpiresAt": result.AccessExpiresAt,
		"refreshToken": result.RefreshToken, "refreshExpiresAt": result.RefreshExpiresAt,
	})
}

func enrollmentCredential(request *http.Request) (domain.ID, string, bool) {
	id, err := domain.ParseID(request.PathValue("id"))
	values := request.Header.Values("Authorization")
	if err != nil || len(values) != 1 || !strings.HasPrefix(values[0], "Device ") {
		return domain.ID{}, "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(values[0], "Device "))
	return id, value, value != ""
}

func decodeGatewayJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 16*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}
