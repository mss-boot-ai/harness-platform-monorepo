package crypto

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type suite0001Vector struct {
	FixtureUse string        `json:"fixtureUse"`
	JKT        string        `json:"jkt"`
	PublicJWK  P256PublicJWK `json:"publicJwk"`
	Signature  struct {
		HighSSignature string `json:"highSP1363Base64Url"`
		Message        string `json:"messageUtf8"`
		MessageDigest  string `json:"messageSha256Base64Url"`
		SignatureP1363 string `json:"p1363Base64Url"`
	} `json:"signature"`
	DPoP struct {
		AccessToken    string `json:"accessToken"`
		ATH            string `json:"ath"`
		Proof          string `json:"proof"`
		SigningInput   string `json:"signingInput"`
		SignatureP1363 string `json:"signatureP1363Base64Url"`
	} `json:"dpop"`
}

func TestSuite0001JWKAndES256Vector(t *testing.T) {
	vector := readSuite0001Vector(t)
	if !strings.HasPrefix(vector.FixtureUse, "TEST ONLY") {
		t.Fatal("shared private key fixture is not explicitly marked test-only")
	}
	thumbprint, err := vector.PublicJWK.Thumbprint()
	if err != nil {
		t.Fatalf("compute JKT: %v", err)
	}
	if thumbprint != vector.JKT {
		t.Fatalf("JKT = %q, want %q", thumbprint, vector.JKT)
	}
	publicKey, err := vector.PublicJWK.PublicKey()
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(vector.Signature.SignatureP1363)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !VerifyP1363LowS(publicKey, []byte(vector.Signature.Message), signature) {
		t.Fatal("shared low-S signature did not verify")
	}
	if VerifyP1363LowS(publicKey, []byte(vector.Signature.Message+"!"), signature) {
		t.Fatal("tampered message verified")
	}
	if VerifyP1363LowS(publicKey, []byte(vector.Signature.Message), signature[:63]) {
		t.Fatal("short P1363 signature verified")
	}

	highS, err := base64.RawURLEncoding.Strict().DecodeString(vector.Signature.HighSSignature)
	if err != nil {
		t.Fatalf("decode high-S signature: %v", err)
	}
	if VerifyP1363LowS(publicKey, []byte(vector.Signature.Message), highS) {
		t.Fatal("high-S signature verified")
	}
}

func TestSuite0001DPoPVectorPrimitives(t *testing.T) {
	vector := readSuite0001Vector(t)
	if got := AccessTokenHash(vector.DPoP.AccessToken); got != vector.DPoP.ATH {
		t.Fatalf("ath = %q, want %q", got, vector.DPoP.ATH)
	}
	parts := strings.Split(vector.DPoP.Proof, ".")
	if len(parts) != 3 || strings.Join(parts[:2], ".") != vector.DPoP.SigningInput {
		t.Fatal("DPoP compact JWS does not match its signing input")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode DPoP signature: %v", err)
	}
	publicKey, err := vector.PublicJWK.PublicKey()
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if !VerifyP1363LowS(publicKey, []byte(vector.DPoP.SigningInput), signature) {
		t.Fatal("shared DPoP signature did not verify")
	}
}

func TestParseP256PublicJWKRejectsPrivateOrUnknownFields(t *testing.T) {
	if _, err := ParseP256PublicJWK([]byte(`{"crv":"P-256","kty":"EC","x":"a","y":"b","d":"private"}`)); err == nil {
		t.Fatal("private JWK field was accepted")
	}
}

func readSuite0001Vector(t *testing.T) suite0001Vector {
	t.Helper()
	contents, err := os.ReadFile("../../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json")
	if err != nil {
		t.Fatalf("read shared vector: %v", err)
	}
	var vector suite0001Vector
	if err := json.Unmarshal(contents, &vector); err != nil {
		t.Fatalf("decode shared vector: %v", err)
	}
	return vector
}
