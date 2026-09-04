package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/dpop"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/registration"
)

const (
	protocolName     = "mss.awp.v1"
	ticketPurpose    = "awp-connect"
	nativeABAOrigin  = "app://aba"
	defaultNonceTTL  = 2 * time.Minute
	defaultTicketTTL = 30 * time.Second
	defaultReplayMax = 100_000
)

type Persistence interface {
	CreateEnrollment(context.Context, domain.Enrollment) error
	GetEnrollmentByDeviceCode(context.Context, domain.ID, [32]byte, time.Time) (domain.Enrollment, error)
	ConsumeABAEnrollment(context.Context, domain.ID, [32]byte, domain.Endpoint, domain.EndpointCredential, domain.RefreshCredential, domain.SecurityAuditEvent, time.Time) error
	AuthenticateAccessToken(context.Context, [32]byte, time.Time) (domain.Endpoint, domain.EndpointCredential, error)
	CreateTicket(context.Context, domain.WSTicket, time.Duration) error
	InspectTicket(context.Context, [32]byte, time.Time) (domain.WSTicket, error)
	GetEndpointCredential(context.Context, domain.ID, domain.ID, time.Time) (domain.Endpoint, domain.EndpointCredential, error)
	ConsumeTicket(context.Context, [32]byte, domain.ID, domain.ID, string, string, string, time.Time) (domain.WSTicket, error)
	GetEndpointNonceHash(context.Context, domain.ID, time.Time) ([32]byte, error)
	PutEndpointNonce(context.Context, domain.ID, [32]byte, time.Time, time.Time) error
	RotateEndpointNonce(context.Context, domain.ID, [32]byte, [32]byte, time.Time, time.Time) error
	InspectRefreshCredential(context.Context, [32]byte, time.Time) (domain.Endpoint, domain.RefreshCredential, error)
	RotateRefreshCredential(context.Context, [32]byte, string, domain.EndpointCredential, domain.RefreshCredential, domain.SecurityAuditEvent, time.Time) error
	NextConnectionGeneration(context.Context, domain.ID, time.Time) (uint64, error)
	MarkEndpointSeen(context.Context, domain.ID, time.Time) error
	CreateEndpointSession(context.Context, domain.Session, domain.IdempotencyRecord, domain.SecurityAuditEvent, int, []byte) (domain.Session, []byte, bool, error)
	GetSession(context.Context, domain.ID) (domain.Session, error)
	UpdateSession(context.Context, domain.ID, func(*domain.Session) error) (domain.Session, error)
	ListEndpoints(context.Context, string, string, int) ([]domain.Endpoint, error)
	PutEndpointSessionKeyPackage(context.Context, string, string, domain.SessionKeyPackage) (domain.SessionKeyPackage, bool, error)
	AcknowledgeAndActivateSessionKeyPackage(context.Context, domain.ID, domain.ID, string, string, time.Time) (domain.Session, error)
	PutEndpointFrame(context.Context, domain.EncryptedFrame) (bool, error)
	AdvanceAck(context.Context, domain.AckCursor) (domain.AckCursor, error)
	UseDPoPReplay(context.Context, string, string, time.Time, time.Time, int64) error
}

type Config struct {
	AllowedOrigin        string
	ExternalOrigin       string
	NativeExternalOrigin string
	NonceTTL             time.Duration
	TicketTTL            time.Duration
	ReplayMaxEntries     int64
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	Trust                *TrustBundle
	VerificationURI      string
}

type Server struct {
	config      Config
	persistence Persistence
	random      io.Reader
	now         func() time.Time
	trust       *TrustBundle
	connections connectionDirectory
}

func NewHandler(config Config, persistence Persistence, random io.Reader, now func() time.Time) (http.Handler, error) {
	if persistence == nil {
		return nil, errors.New("Gateway persistence is required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	allowedOrigin, err := registration.NormalizeOrigin(config.AllowedOrigin)
	if err != nil {
		return nil, fmt.Errorf("normalize Gateway allowed origin: %w", err)
	}
	externalOrigin, err := registration.NormalizeOrigin(config.ExternalOrigin)
	if err != nil {
		return nil, fmt.Errorf("normalize Gateway external origin: %w", err)
	}
	config.AllowedOrigin = allowedOrigin
	config.ExternalOrigin = externalOrigin
	if strings.TrimSpace(config.NativeExternalOrigin) == "" {
		config.NativeExternalOrigin = config.ExternalOrigin
	}
	nativeExternalOrigin, err := registration.NormalizeOrigin(config.NativeExternalOrigin)
	if err != nil {
		return nil, fmt.Errorf("normalize Gateway native external origin: %w", err)
	}
	config.NativeExternalOrigin = nativeExternalOrigin
	if strings.TrimSpace(config.VerificationURI) == "" {
		config.VerificationURI = config.AllowedOrigin + "/harness/enrollments"
	}
	if config.NonceTTL <= 0 || config.NonceTTL > 5*time.Minute {
		config.NonceTTL = defaultNonceTTL
	}
	if config.TicketTTL <= 0 || config.TicketTTL > defaultTicketTTL {
		config.TicketTTL = defaultTicketTTL
	}
	if config.ReplayMaxEntries <= 0 {
		config.ReplayMaxEntries = defaultReplayMax
	}
	if config.AccessTTL <= 0 || config.AccessTTL > 15*time.Minute {
		config.AccessTTL = 10 * time.Minute
	}
	if config.RefreshTTL <= 0 || config.RefreshTTL > 30*24*time.Hour {
		config.RefreshTTL = 24 * time.Hour
	}
	if config.Trust == nil {
		config.Trust, err = NewEphemeralTrust(random, now().UTC())
		if err != nil {
			return nil, err
		}
	}
	if _, err := config.Trust.Manifest(); err != nil {
		return nil, err
	}
	server := &Server{
		config: config, persistence: persistence, random: random, now: now,
		trust: config.Trust, connections: newMemoryConnectionDirectory(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gateway/v1/health", server.health)
	mux.HandleFunc("GET /gateway/v1/trust-manifest", server.trustManifest)
	mux.HandleFunc("GET /gateway/v1/ws", server.websocket)
	mux.HandleFunc("POST /gateway/v1/enrollments", server.startEnrollment)
	mux.HandleFunc("GET /gateway/v1/enrollments/{id}", server.pollEnrollment)
	mux.HandleFunc("POST /gateway/v1/enrollments/{id}/consume", server.consumeEnrollment)
	mux.HandleFunc("POST /gateway/v1/ws/tickets", server.issueTicket)
	mux.HandleFunc("OPTIONS /gateway/v1/ws/tickets", server.preflight)
	mux.HandleFunc("POST /gateway/v1/tokens/refresh", server.refreshToken)
	mux.HandleFunc("OPTIONS /gateway/v1/tokens/refresh", server.preflight)
	mux.HandleFunc("POST /gateway/v1/sessions", server.createSession)
	mux.HandleFunc("OPTIONS /gateway/v1/sessions", server.preflight)
	mux.HandleFunc("POST /gateway/v1/endpoints/abas", server.listABAEndpoints)
	mux.HandleFunc("OPTIONS /gateway/v1/endpoints/abas", server.preflight)
	return server.cors(mux), nil
}

func (server *Server) trustManifest(writer http.ResponseWriter, _ *http.Request) {
	manifest, err := server.trust.Manifest()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "TRUST_MANIFEST_UNAVAILABLE", "trust manifest is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, manifest)
}

func (server *Server) refreshToken(writer http.ResponseWriter, request *http.Request) {
	now := server.now().UTC()
	refreshTokenValue, refreshHash, transport, ok := parseRefreshCredential(request)
	if !ok {
		writeGatewayError(writer, http.StatusUnauthorized, "REFRESH_CREDENTIAL_REQUIRED", "refresh credential is required")
		return
	}
	if refreshTokenValue == "" {
		if transport == refreshTransportCookie {
			clearRefreshCookie(writer, server.config.ExternalOrigin)
		}
		writeGatewayError(writer, http.StatusUnauthorized, "REFRESH_CREDENTIAL_INVALID", "refresh credential is invalid")
		return
	}
	endpoint, currentRefresh, err := server.persistence.InspectRefreshCredential(request.Context(), refreshHash, now)
	if err != nil {
		if transport == refreshTransportCookie {
			clearRefreshCookie(writer, server.config.ExternalOrigin)
		}
		writeDomainError(writer, err)
		return
	}
	if !validRefreshTransport(endpoint.Type, transport, request.Header.Get("Origin")) {
		if transport == refreshTransportCookie {
			clearRefreshCookie(writer, server.config.ExternalOrigin)
		}
		writeGatewayError(writer, http.StatusForbidden, "REFRESH_TRANSPORT_FORBIDDEN", "refresh credential transport is not allowed for this endpoint")
		return
	}
	nonceHash, err := server.persistence.GetEndpointNonceHash(request.Context(), endpoint.ID, now)
	proofHeaders := request.Header.Values("DPoP")
	if err != nil {
		if !domain.HasCode(err, domain.CodeNotFound) && !domain.HasCode(err, domain.CodeExpired) {
			writeDomainError(writer, err)
			return
		}
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return
	}
	if len(proofHeaders) == 0 || (len(proofHeaders) == 1 && strings.TrimSpace(proofHeaders[0]) == "") {
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return
	}
	if len(proofHeaders) != 1 {
		writeGatewayError(writer, http.StatusBadRequest, "DPOP_MALFORMED", "exactly one DPoP header is required")
		return
	}
	verifier := dpop.Verifier{Replay: storeReplayCache{persistence: server.persistence, maxEntries: server.config.ReplayMaxEntries}}
	externalOrigin := server.endpointExternalOrigin(endpoint.Type)
	_, err = verifier.Verify(request.Context(), proofHeaders[0], dpop.Requirements{
		ExpectedJKT: endpoint.SigningJKT, ExpectedNonceHash: nonceHash,
		HTM: request.Method, HTU: externalOrigin + request.URL.RequestURI(), Now: now,
	})
	if err != nil {
		server.writeDPoPError(writer, request.Context(), endpoint.ID, now, err)
		return
	}
	nextNonce, _, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	if err := server.persistence.RotateEndpointNonce(
		request.Context(), endpoint.ID, nonceHash, sha256.Sum256([]byte(nextNonce)), now, now.Add(server.config.NonceTTL),
	); err != nil {
		writeGatewayError(writer, http.StatusConflict, "DPOP_NONCE_CHANGED", "DPoP nonce changed concurrently")
		return
	}
	accessToken, accessHash, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	refreshToken, nextRefreshHash, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	accessID, err := domain.NewID(server.random)
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	refreshID, err := domain.NewID(server.random)
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	auditID, err := domain.NewID(server.random)
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	accessExpiresAt := now.Add(server.config.AccessTTL)
	refreshExpiresAt := now.Add(server.config.RefreshTTL)
	nextAccess := domain.EndpointCredential{
		ID: accessID, EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: accessHash, SigningJKT: endpoint.SigningJKT,
		Scopes: []string{"endpoint:connect", "relay:write", "session:manage"},
		Status: domain.CredentialStatusActive, ExpiresAt: accessExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	nextRefresh := domain.RefreshCredential{
		ID: refreshID, EndpointID: endpoint.ID, FamilyID: endpoint.CredentialFamilyID,
		TokenHash: nextRefreshHash, SigningJKT: endpoint.SigningJKT, Status: domain.CredentialStatusActive,
		ExpiresAt: refreshExpiresAt, RotatedFrom: currentRefresh.ID, CreatedAt: now, UpdatedAt: now,
	}
	audit := domain.SecurityAuditEvent{
		ID: auditID, OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID,
		ActorType: domain.AuditActorEndpoint, ActorID: endpoint.ID.String(), Action: "endpoint.token.refresh",
		ObjectType: "endpoint", ObjectID: endpoint.ID.String(), Result: "success", Metadata: map[string]string{}, CreatedAt: now,
	}
	if err := server.persistence.RotateRefreshCredential(
		request.Context(), refreshHash, endpoint.SigningJKT, nextAccess, nextRefresh, audit, now,
	); err != nil {
		if transport == refreshTransportCookie && domain.HasCode(err, domain.CodeRevoked) {
			clearRefreshCookie(writer, server.config.ExternalOrigin)
		}
		writeDomainError(writer, err)
		return
	}
	if transport == refreshTransportCookie {
		setRefreshCookie(writer, refreshToken, refreshExpiresAt, server.config.ExternalOrigin)
	}
	writer.Header().Set("DPoP-Nonce", nextNonce)
	response := map[string]any{
		"endpointId": endpoint.ID.String(), "credentialId": accessID.String(), "tokenType": "DPoP", "accessToken": accessToken,
		"accessExpiresAt": accessExpiresAt, "signingJkt": endpoint.SigningJKT, "kemJkt": endpoint.KEMJKT,
	}
	if transport == refreshTransportHeader {
		response["refreshToken"] = refreshToken
		response["refreshExpiresAt"] = refreshExpiresAt
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ready", "protocol": protocolName})
}

func (server *Server) preflight(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) issueTicket(writer http.ResponseWriter, request *http.Request) {
	now := server.now().UTC()
	token, tokenHash, ok := parseAuthorization(request)
	if !ok {
		writeGatewayError(writer, http.StatusUnauthorized, "ENDPOINT_CREDENTIAL_REQUIRED", "endpoint credential is required")
		return
	}
	endpoint, credential, err := server.persistence.AuthenticateAccessToken(request.Context(), tokenHash, now)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	ticketOrigin, ok := server.ticketOrigin(endpoint.Type, request.Header.Get("Origin"))
	if !ok {
		writeGatewayError(writer, http.StatusForbidden, "TICKET_ORIGIN_FORBIDDEN", "ticket origin is not allowed for this endpoint")
		return
	}
	nonceHash, err := server.persistence.GetEndpointNonceHash(request.Context(), endpoint.ID, now)
	proofHeaders := request.Header.Values("DPoP")
	if err != nil {
		if !domain.HasCode(err, domain.CodeNotFound) && !domain.HasCode(err, domain.CodeExpired) {
			writeDomainError(writer, err)
			return
		}
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return
	}
	if len(proofHeaders) == 0 || (len(proofHeaders) == 1 && strings.TrimSpace(proofHeaders[0]) == "") {
		server.writeNonceChallenge(writer, request.Context(), endpoint.ID, now)
		return
	}
	if len(proofHeaders) != 1 {
		writeGatewayError(writer, http.StatusBadRequest, "DPOP_MALFORMED", "exactly one DPoP header is required")
		return
	}
	verifier := dpop.Verifier{Replay: storeReplayCache{persistence: server.persistence, maxEntries: server.config.ReplayMaxEntries}}
	externalOrigin := server.endpointExternalOrigin(endpoint.Type)
	_, err = verifier.Verify(request.Context(), proofHeaders[0], dpop.Requirements{
		AccessToken: token, ExpectedJKT: endpoint.SigningJKT, ExpectedNonceHash: nonceHash,
		HTM: request.Method, HTU: externalOrigin + request.URL.RequestURI(), Now: now,
	})
	if err != nil {
		server.writeDPoPError(writer, request.Context(), endpoint.ID, now, err)
		return
	}
	nextNonce, _, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	if err := server.persistence.RotateEndpointNonce(
		request.Context(), endpoint.ID, nonceHash, sha256.Sum256([]byte(nextNonce)), now, now.Add(server.config.NonceTTL),
	); err != nil {
		writeGatewayError(writer, http.StatusConflict, "DPOP_NONCE_CHANGED", "DPoP nonce changed concurrently")
		return
	}
	ticketValue, ticketHash, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	ticketID, err := domain.NewID(server.random)
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	expiresAt := now.Add(server.config.TicketTTL)
	ticket := domain.WSTicket{
		ID: ticketID, TokenHash: ticketHash, EndpointID: endpoint.ID, CredentialID: credential.ID,
		Purpose: ticketPurpose, Origin: ticketOrigin, Protocol: protocolName,
		Status: domain.TicketStatusIssued, ExpiresAt: expiresAt, CreatedAt: now,
	}
	if err := server.persistence.CreateTicket(request.Context(), ticket, server.config.TicketTTL); err != nil {
		writeDomainError(writer, err)
		return
	}
	writer.Header().Set("DPoP-Nonce", nextNonce)
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ticket": ticketValue, "expiresAt": expiresAt, "protocol": protocolName,
		"websocketUrl": websocketURL(externalOrigin) + "/gateway/v1/ws",
	})
}

func (server *Server) writeNonceChallenge(
	writer http.ResponseWriter,
	ctx context.Context,
	endpointID domain.ID,
	now time.Time,
) {
	nonce, _, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	if err := server.persistence.PutEndpointNonce(ctx, endpointID, sha256.Sum256([]byte(nonce)), now, now.Add(server.config.NonceTTL)); err != nil {
		writeDomainError(writer, err)
		return
	}
	writer.Header().Set("DPoP-Nonce", nonce)
	writeGatewayError(writer, http.StatusUnauthorized, "DPOP_NONCE_REQUIRED", "a fresh DPoP proof is required")
}

func (server *Server) writeDPoPError(
	writer http.ResponseWriter,
	ctx context.Context,
	endpointID domain.ID,
	now time.Time,
	err error,
) {
	code := dpop.ErrorCodeOf(err)
	status := http.StatusUnauthorized
	if code == dpop.CodeReplayCacheFull {
		status = http.StatusServiceUnavailable
	}
	if code == dpop.CodeReplay {
		status = http.StatusConflict
	}
	if status == http.StatusUnauthorized {
		nonce, _, randomErr := server.newOpaqueValue()
		if randomErr == nil && server.persistence.PutEndpointNonce(ctx, endpointID, sha256.Sum256([]byte(nonce)), now, now.Add(server.config.NonceTTL)) == nil {
			writer.Header().Set("DPoP-Nonce", nonce)
		}
	}
	writeGatewayError(writer, status, string(code), "DPoP proof was rejected")
}

func (server *Server) newOpaqueValue() (string, [32]byte, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(server.random, value); err != nil {
		return "", [32]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(value), sha256.Sum256(value), nil
}

func (server *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin != "" {
			normalized, err := registration.NormalizeOrigin(origin)
			if err != nil || normalized != server.config.AllowedOrigin {
				writeGatewayError(writer, http.StatusForbidden, "GATEWAY_ORIGIN_FORBIDDEN", "request origin is not allowed")
				return
			}
			writer.Header().Set("Access-Control-Allow-Origin", server.config.AllowedOrigin)
			writer.Header().Set("Access-Control-Allow-Credentials", "true")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, DPoP, Idempotency-Key")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Access-Control-Expose-Headers", "DPoP-Nonce")
			writer.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(writer, request)
	})
}

type storeReplayCache struct {
	persistence Persistence
	maxEntries  int64
}

func (cache storeReplayCache) Use(ctx context.Context, jkt, jti string, now, expiresAt time.Time) error {
	err := cache.persistence.UseDPoPReplay(ctx, jkt, jti, now, expiresAt, cache.maxEntries)
	switch {
	case domain.HasCode(err, domain.CodeConflict):
		return dpop.ErrReplay
	case domain.HasCode(err, domain.CodeResourceLimit):
		return dpop.ErrReplayCapacity
	default:
		return err
	}
}

func parseAuthorization(request *http.Request) (string, [32]byte, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "DPoP ") {
		return "", [32]byte{}, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "DPoP "))
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", [32]byte{}, false
	}
	return token, sha256.Sum256(decoded), true
}

type refreshTransport uint8

const (
	refreshTransportCookie refreshTransport = iota + 1
	refreshTransportHeader
)

func parseRefreshCredential(request *http.Request) (string, [32]byte, refreshTransport, bool) {
	cookie, cookieErr := request.Cookie("harness_hc_refresh")
	authorization := request.Header.Values("Authorization")
	hasCookie := cookieErr == nil
	hasHeader := len(authorization) != 0
	if hasCookie == hasHeader {
		return "", [32]byte{}, 0, false
	}
	var token string
	transport := refreshTransportCookie
	if hasHeader {
		if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Refresh ") {
			return "", [32]byte{}, 0, false
		}
		token = strings.TrimSpace(strings.TrimPrefix(authorization[0], "Refresh "))
		transport = refreshTransportHeader
	} else {
		token = cookie.Value
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", [32]byte{}, transport, true
	}
	return token, sha256.Sum256(decoded), transport, true
}

func validRefreshTransport(endpointType domain.EndpointType, transport refreshTransport, origin string) bool {
	switch endpointType {
	case domain.EndpointTypeABA:
		return transport == refreshTransportHeader && strings.TrimSpace(origin) == ""
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return transport == refreshTransportCookie
	default:
		return false
	}
}

func (server *Server) ticketOrigin(endpointType domain.EndpointType, origin string) (string, bool) {
	origin = strings.TrimSpace(origin)
	switch endpointType {
	case domain.EndpointTypeABA:
		return nativeABAOrigin, origin == ""
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return server.config.AllowedOrigin, origin == server.config.AllowedOrigin
	default:
		return "", false
	}
}

func (server *Server) endpointExternalOrigin(endpointType domain.EndpointType) string {
	if endpointType == domain.EndpointTypeABA {
		return server.config.NativeExternalOrigin
	}
	return server.config.ExternalOrigin
}

func websocketURL(origin string) string {
	if strings.HasPrefix(origin, "https://") {
		return "wss://" + strings.TrimPrefix(origin, "https://")
	}
	return "ws://" + strings.TrimPrefix(origin, "http://")
}

func setRefreshCookie(writer http.ResponseWriter, token string, expiresAt time.Time, externalOrigin string) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(writer, &http.Cookie{
		Name: "harness_hc_refresh", Value: token, Path: "/gateway/v1/tokens/refresh",
		Expires: expiresAt, MaxAge: maxAge, HttpOnly: true,
		Secure: strings.HasPrefix(externalOrigin, "https://"), SameSite: http.SameSiteStrictMode,
	})
}

func clearRefreshCookie(writer http.ResponseWriter, externalOrigin string) {
	http.SetCookie(writer, &http.Cookie{
		Name: "harness_hc_refresh", Value: "", Path: "/gateway/v1/tokens/refresh",
		Expires: time.Unix(1, 0), MaxAge: -1, HttpOnly: true,
		Secure: strings.HasPrefix(externalOrigin, "https://"), SameSite: http.SameSiteStrictMode,
	})
}

func writeDomainError(writer http.ResponseWriter, err error) {
	var problem *domain.Problem
	if !errors.As(err, &problem) {
		writeGatewayError(writer, http.StatusInternalServerError, "GATEWAY_INTERNAL", "Gateway operation failed")
		return
	}
	switch problem.Code {
	case domain.CodeNotFound:
		writeGatewayError(writer, http.StatusUnauthorized, "ENDPOINT_CREDENTIAL_INVALID", "endpoint credential is unavailable")
	case domain.CodeExpired:
		writeGatewayError(writer, http.StatusUnauthorized, "ENDPOINT_CREDENTIAL_EXPIRED", "endpoint credential is expired")
	case domain.CodeRevoked, domain.CodeSecurityViolation:
		writeGatewayError(writer, http.StatusForbidden, "ENDPOINT_CREDENTIAL_REVOKED", "endpoint credential is unavailable")
	case domain.CodeConflict:
		writeGatewayError(writer, http.StatusConflict, string(problem.Code), problem.Message)
	case domain.CodeResourceLimit:
		writeGatewayError(writer, http.StatusTooManyRequests, string(problem.Code), problem.Message)
	default:
		writeGatewayError(writer, http.StatusBadRequest, string(problem.Code), problem.Message)
	}
}

func writeGatewayError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{"code": code, "message": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
