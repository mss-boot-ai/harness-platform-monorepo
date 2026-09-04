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
	defaultNonceTTL  = 2 * time.Minute
	defaultTicketTTL = 30 * time.Second
	defaultReplayMax = 100_000
)

type Persistence interface {
	AuthenticateAccessToken(context.Context, [32]byte, time.Time) (domain.Endpoint, domain.EndpointCredential, error)
	CreateTicket(context.Context, domain.WSTicket, time.Duration) error
	GetEndpointNonceHash(context.Context, domain.ID, time.Time) ([32]byte, error)
	PutEndpointNonce(context.Context, domain.ID, [32]byte, time.Time, time.Time) error
	RotateEndpointNonce(context.Context, domain.ID, [32]byte, [32]byte, time.Time, time.Time) error
	UseDPoPReplay(context.Context, string, string, time.Time, time.Time, int64) error
}

type Config struct {
	AllowedOrigin    string
	ExternalOrigin   string
	NonceTTL         time.Duration
	TicketTTL        time.Duration
	ReplayMaxEntries int64
}

type Server struct {
	config      Config
	persistence Persistence
	random      io.Reader
	now         func() time.Time
}

func NewHandler(config Config, persistence Persistence, random io.Reader, now func() time.Time) (http.Handler, error) {
	if persistence == nil {
		return nil, errors.New("Gateway persistence is required")
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
	if config.NonceTTL <= 0 || config.NonceTTL > 5*time.Minute {
		config.NonceTTL = defaultNonceTTL
	}
	if config.TicketTTL <= 0 || config.TicketTTL > defaultTicketTTL {
		config.TicketTTL = defaultTicketTTL
	}
	if config.ReplayMaxEntries <= 0 {
		config.ReplayMaxEntries = defaultReplayMax
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	server := &Server{config: config, persistence: persistence, random: random, now: now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gateway/v1/health", server.health)
	mux.HandleFunc("POST /gateway/v1/ws/tickets", server.issueTicket)
	mux.HandleFunc("OPTIONS /gateway/v1/ws/tickets", server.preflight)
	return server.cors(mux), nil
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
	_, err = verifier.Verify(request.Context(), proofHeaders[0], dpop.Requirements{
		AccessToken: token, ExpectedJKT: endpoint.SigningJKT, ExpectedNonceHash: nonceHash,
		HTM: request.Method, HTU: server.config.ExternalOrigin + request.URL.RequestURI(), Now: now,
	})
	if err != nil {
		server.writeDPoPError(writer, request.Context(), endpoint.ID, now, err)
		return
	}
	nextNonce, nextHash, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	if err := server.persistence.RotateEndpointNonce(
		request.Context(), endpoint.ID, nonceHash, nextHash, now, now.Add(server.config.NonceTTL),
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
		Purpose: ticketPurpose, Origin: server.config.AllowedOrigin, Protocol: protocolName,
		Status: domain.TicketStatusIssued, ExpiresAt: expiresAt, CreatedAt: now,
	}
	if err := server.persistence.CreateTicket(request.Context(), ticket, defaultTicketTTL); err != nil {
		writeDomainError(writer, err)
		return
	}
	writer.Header().Set("DPoP-Nonce", nextNonce)
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ticket": ticketValue, "expiresAt": expiresAt, "protocol": protocolName,
		"websocketUrl": websocketURL(server.config.ExternalOrigin) + "/gateway/v1/ws",
	})
}

func (server *Server) writeNonceChallenge(
	writer http.ResponseWriter,
	ctx context.Context,
	endpointID domain.ID,
	now time.Time,
) {
	nonce, hash, err := server.newOpaqueValue()
	if err != nil {
		writeGatewayError(writer, http.StatusServiceUnavailable, "GATEWAY_RANDOM_UNAVAILABLE", "Gateway is temporarily unavailable")
		return
	}
	if err := server.persistence.PutEndpointNonce(ctx, endpointID, hash, now, now.Add(server.config.NonceTTL)); err != nil {
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
		nonce, hash, randomErr := server.newOpaqueValue()
		if randomErr == nil && server.persistence.PutEndpointNonce(ctx, endpointID, hash, now, now.Add(server.config.NonceTTL)) == nil {
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
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, DPoP")
			writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
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

func websocketURL(origin string) string {
	if strings.HasPrefix(origin, "https://") {
		return "wss://" + strings.TrimPrefix(origin, "https://")
	}
	return "ws://" + strings.TrimPrefix(origin, "http://")
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
