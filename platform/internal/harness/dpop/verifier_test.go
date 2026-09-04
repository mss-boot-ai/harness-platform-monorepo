package dpop

import (
	"context"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

type dpopVector struct {
	JKT  string `json:"jkt"`
	DPoP struct {
		AccessToken string `json:"accessToken"`
		ATH         string `json:"ath"`
		Claims      struct {
			HTTPMethod string `json:"htm"`
			HTTPURI    string `json:"htu"`
			IssuedAt   int64  `json:"iat"`
			JTI        string `json:"jti"`
			Nonce      string `json:"nonce"`
		} `json:"claims"`
		Proof string `json:"proof"`
	} `json:"dpop"`
}

func TestVerifierAcceptsSharedDPoPVectorOnce(t *testing.T) {
	vector := readDPoPVector(t)
	cache, err := NewMemoryReplayCache(16)
	if err != nil {
		t.Fatalf("create replay cache: %v", err)
	}
	verifier := Verifier{Replay: cache}
	requirements := vectorRequirements(vector)
	requirements.HTU += "?ignored=query#ignored-fragment"

	result, err := verifier.Verify(context.Background(), vector.DPoP.Proof, requirements)
	if err != nil {
		t.Fatalf("verify shared proof: %v", err)
	}
	if result.JKT != vector.JKT || result.JTI != vector.DPoP.Claims.JTI {
		t.Fatalf("unexpected verification result: %#v", result)
	}
	if _, err := verifier.Verify(context.Background(), vector.DPoP.Proof, requirements); ErrorCodeOf(err) != CodeReplay {
		t.Fatalf("second verification error = %v, want %s", err, CodeReplay)
	}
}

func TestVerifierRejectsBindingAndFreshnessMismatches(t *testing.T) {
	vector := readDPoPVector(t)
	tests := []struct {
		name   string
		mutate func(*Requirements)
		code   ErrorCode
	}{
		{"method", func(value *Requirements) { value.HTM = "GET" }, CodeRequestMismatch},
		{"uri", func(value *Requirements) { value.HTU = "https://platform.example/gateway/v1/sessions" }, CodeRequestMismatch},
		{"token", func(value *Requirements) { value.AccessToken += "-wrong" }, CodeRequestMismatch},
		{"key", func(value *Requirements) { value.ExpectedJKT = strings.Repeat("x", 43) }, CodeRequestMismatch},
		{"nonce", func(value *Requirements) { value.ExpectedNonce += "-wrong" }, CodeRequestMismatch},
		{"missing nonce", func(value *Requirements) { value.ExpectedNonce = "" }, CodeNonceRequired},
		{"expired", func(value *Requirements) { value.Now = value.Now.Add(6 * time.Minute) }, CodeProofExpired},
		{"future", func(value *Requirements) { value.Now = value.Now.Add(-time.Minute) }, CodeProofExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache, err := NewMemoryReplayCache(4)
			if err != nil {
				t.Fatalf("create replay cache: %v", err)
			}
			requirements := vectorRequirements(vector)
			test.mutate(&requirements)
			_, err = (Verifier{Replay: cache}).Verify(context.Background(), vector.DPoP.Proof, requirements)
			if ErrorCodeOf(err) != test.code {
				t.Fatalf("verification error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestVerifierRejectsHighSSignature(t *testing.T) {
	vector := readDPoPVector(t)
	parts := strings.Split(vector.DPoP.Proof, ".")
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	s := new(big.Int).SetBytes(signature[32:])
	s.Sub(elliptic.P256().Params().N, s).FillBytes(signature[32:])
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	cache, err := NewMemoryReplayCache(4)
	if err != nil {
		t.Fatalf("create replay cache: %v", err)
	}
	_, err = (Verifier{Replay: cache}).Verify(context.Background(), strings.Join(parts, "."), vectorRequirements(vector))
	if ErrorCodeOf(err) != CodeSignatureInvalid {
		t.Fatalf("verification error = %v, want %s", err, CodeSignatureInvalid)
	}
}

func TestStrictJSONRejectsDuplicateAndUnknownNames(t *testing.T) {
	var claims proofClaims
	if err := decodeStrictObject([]byte(`{"ath":"a","ath":"b"}`), &claims); err == nil {
		t.Fatal("duplicate claim was accepted")
	}
	if err := decodeStrictObject([]byte(`{"unknown":true}`), &claims); err == nil {
		t.Fatal("unknown claim was accepted")
	}
}

func TestNormalizeHTU(t *testing.T) {
	got, err := NormalizeHTU("HTTPS://Platform.Example:443/gateway/v1/ws/tickets?secret=no#fragment")
	if err != nil {
		t.Fatalf("normalize HTU: %v", err)
	}
	if got != "https://platform.example/gateway/v1/ws/tickets" {
		t.Fatalf("normalized HTU = %q", got)
	}
	if _, err := NormalizeHTU("http://platform.example/gateway"); err == nil {
		t.Fatal("remote plaintext HTU was accepted")
	}
	if _, err := NormalizeHTU("http://127.0.0.1:8082/gateway"); err != nil {
		t.Fatalf("loopback development HTU was rejected: %v", err)
	}
}

func vectorRequirements(vector dpopVector) Requirements {
	return Requirements{
		AccessToken:   vector.DPoP.AccessToken,
		ExpectedJKT:   vector.JKT,
		ExpectedNonce: vector.DPoP.Claims.Nonce,
		HTM:           vector.DPoP.Claims.HTTPMethod,
		HTU:           vector.DPoP.Claims.HTTPURI,
		Now:           time.Unix(vector.DPoP.Claims.IssuedAt, 0),
	}
}

func readDPoPVector(t *testing.T) dpopVector {
	t.Helper()
	contents, err := os.ReadFile("../../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json")
	if err != nil {
		t.Fatalf("read shared vector: %v", err)
	}
	var vector dpopVector
	if err := json.Unmarshal(contents, &vector); err != nil {
		t.Fatalf("decode shared vector: %v", err)
	}
	return vector
}
