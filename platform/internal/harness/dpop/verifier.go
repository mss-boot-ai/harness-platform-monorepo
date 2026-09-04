package dpop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
)

const maxProofBytes = 12 * 1024

var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ErrorCode string

const (
	CodeMalformedProof       ErrorCode = "DPOP_MALFORMED"
	CodeUnsupportedAlgorithm ErrorCode = "DPOP_ALGORITHM_UNSUPPORTED"
	CodeSignatureInvalid     ErrorCode = "DPOP_SIGNATURE_INVALID"
	CodeRequestMismatch      ErrorCode = "DPOP_REQUEST_MISMATCH"
	CodeProofExpired         ErrorCode = "DPOP_PROOF_EXPIRED"
	CodeNonceRequired        ErrorCode = "DPOP_NONCE_REQUIRED"
	CodeReplay               ErrorCode = "DPOP_REPLAYED"
	CodeReplayCacheFull      ErrorCode = "DPOP_REPLAY_CACHE_FULL"
)

type Error struct {
	Code  ErrorCode
	cause error
}

func (err *Error) Error() string { return string(err.Code) }
func (err *Error) Unwrap() error { return err.cause }

func ErrorCodeOf(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

type ReplayCache interface {
	Use(context.Context, string, string, time.Time, time.Time) error
}

type Requirements struct {
	AccessToken       string
	ExpectedJKT       string
	ExpectedNonce     string
	ExpectedNonceHash [32]byte
	HTM               string
	HTU               string
	Now               time.Time
}

type Result struct {
	JTI       string
	JKT       string
	IssuedAt  time.Time
	PublicJWK awpcrypto.P256PublicJWK
}

type Verifier struct {
	Replay     ReplayCache
	MaxAge     time.Duration
	FutureSkew time.Duration
}

type proofHeader struct {
	Algorithm string          `json:"alg"`
	JWK       json.RawMessage `json:"jwk"`
	Type      string          `json:"typ"`
}

type proofClaims struct {
	AccessTokenHash string `json:"ath"`
	HTTPMethod      string `json:"htm"`
	HTTPURI         string `json:"htu"`
	IssuedAt        int64  `json:"iat"`
	JTI             string `json:"jti"`
	Nonce           string `json:"nonce"`
}

func (verifier Verifier) Verify(ctx context.Context, proof string, requirements Requirements) (Result, error) {
	if ctx == nil {
		return Result{}, fail(CodeMalformedProof, errors.New("context is required"))
	}
	if len(proof) == 0 || len(proof) > maxProofBytes {
		return Result{}, fail(CodeMalformedProof, errors.New("proof length is invalid"))
	}
	if verifier.Replay == nil {
		return Result{}, fail(CodeReplayCacheFull, errors.New("replay cache is required"))
	}
	maxAge := verifier.MaxAge
	if maxAge <= 0 {
		maxAge = 5 * time.Minute
	}
	futureSkew := verifier.FutureSkew
	if futureSkew < 0 {
		return Result{}, fail(CodeMalformedProof, errors.New("future skew is invalid"))
	}
	if futureSkew == 0 {
		futureSkew = 30 * time.Second
	}

	parts := strings.Split(proof, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Result{}, fail(CodeMalformedProof, errors.New("proof must be a compact JWS"))
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	claimsBytes, err := decodeSegment(parts[1])
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	signature, err := decodeSegment(parts[2])
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}

	var header proofHeader
	if err := decodeStrictObject(headerBytes, &header); err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	if header.Type != "dpop+jwt" || header.Algorithm != "ES256" {
		return Result{}, fail(CodeUnsupportedAlgorithm, errors.New("only dpop+jwt with ES256 is accepted"))
	}
	publicJWK, err := awpcrypto.ParseP256PublicJWK(header.JWK)
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	publicKey, err := publicJWK.PublicKey()
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if !awpcrypto.VerifyP1363LowS(publicKey, signingInput, signature) {
		return Result{}, fail(CodeSignatureInvalid, errors.New("proof signature is invalid"))
	}
	jkt, err := publicJWK.Thumbprint()
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	if !equalSecret(jkt, requirements.ExpectedJKT) {
		return Result{}, fail(CodeRequestMismatch, errors.New("proof key does not match token binding"))
	}

	var claims proofClaims
	if err := decodeStrictObject(claimsBytes, &claims); err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	if !uuidV4Pattern.MatchString(claims.JTI) {
		return Result{}, fail(CodeMalformedProof, errors.New("jti must be a canonical UUID v4"))
	}
	if claims.HTTPMethod != requirements.HTM {
		return Result{}, fail(CodeRequestMismatch, errors.New("htm does not match request"))
	}
	claimHTU, err := NormalizeHTU(claims.HTTPURI)
	if err != nil {
		return Result{}, fail(CodeMalformedProof, err)
	}
	expectedHTU, err := NormalizeHTU(requirements.HTU)
	if err != nil {
		return Result{}, fail(CodeMalformedProof, fmt.Errorf("invalid expected HTU: %w", err))
	}
	if claimHTU != expectedHTU {
		return Result{}, fail(CodeRequestMismatch, errors.New("htu does not match request"))
	}
	if (requirements.ExpectedNonce == "" && zeroHash(requirements.ExpectedNonceHash)) || claims.Nonce == "" {
		return Result{}, fail(CodeNonceRequired, errors.New("server nonce is required"))
	}
	if !zeroHash(requirements.ExpectedNonceHash) {
		presentedHash := sha256.Sum256([]byte(claims.Nonce))
		if subtle.ConstantTimeCompare(presentedHash[:], requirements.ExpectedNonceHash[:]) != 1 {
			return Result{}, fail(CodeRequestMismatch, errors.New("nonce does not match"))
		}
	} else if !equalSecret(claims.Nonce, requirements.ExpectedNonce) {
		return Result{}, fail(CodeRequestMismatch, errors.New("nonce does not match"))
	}
	if requirements.AccessToken == "" || !equalSecret(claims.AccessTokenHash, awpcrypto.AccessTokenHash(requirements.AccessToken)) {
		return Result{}, fail(CodeRequestMismatch, errors.New("ath does not match access token"))
	}

	now := requirements.Now
	if now.IsZero() {
		now = time.Now()
	}
	issuedAt := time.Unix(claims.IssuedAt, 0)
	if issuedAt.After(now.Add(futureSkew)) || issuedAt.Before(now.Add(-maxAge)) {
		return Result{}, fail(CodeProofExpired, errors.New("proof iat is outside the accepted window"))
	}
	expiresAt := issuedAt.Add(maxAge + futureSkew)
	if err := verifier.Replay.Use(ctx, jkt, claims.JTI, now, expiresAt); err != nil {
		if errors.Is(err, ErrReplay) {
			return Result{}, fail(CodeReplay, err)
		}
		return Result{}, fail(CodeReplayCacheFull, err)
	}

	return Result{JTI: claims.JTI, JKT: jkt, IssuedAt: issuedAt, PublicJWK: publicJWK}, nil
}

func NormalizeHTU(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("htu must be an absolute HTTP URI without userinfo")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if parsed.Scheme != "https" {
		ip := net.ParseIP(hostname)
		local := hostname == "localhost" || (ip != nil && ip.IsLoopback())
		if parsed.Scheme != "http" || !local {
			return "", errors.New("htu must use HTTPS except for loopback development")
		}
	}
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	parsed.Host = hostname
	if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func decodeSegment(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode compact JWS segment: %w", err)
	}
	return decoded, nil
}

func decodeStrictObject(input []byte, target any) error {
	if err := rejectDuplicateNames(input); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode compact JWS JSON: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("compact JWS JSON must contain one object")
	}
	return nil
}

func rejectDuplicateNames(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := scanJSONValue(decoder); err != nil {
		return fmt.Errorf("validate compact JWS JSON names: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("compact JWS JSON contains trailing data")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object name is not a string")
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("duplicate JSON name %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func equalSecret(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func fail(code ErrorCode, cause error) error { return &Error{Code: code, cause: cause} }

func zeroHash(value [32]byte) bool {
	var zero [32]byte
	return subtle.ConstantTimeCompare(value[:], zero[:]) == 1
}
