package gateway

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
)

func TestEphemeralTrustManifestIsRootSignedAndSeparatesOnlineKey(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	manifest, err := trust.Manifest()
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	payloadBytes, err := base64.RawURLEncoding.Strict().DecodeString(manifest.PayloadBase64URL)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(manifest.SignatureBase64URL)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	rootKey, err := manifest.RootPublicJWK.PublicKey()
	if err != nil {
		t.Fatalf("parse root key: %v", err)
	}
	if !awpcrypto.VerifyP1363LowS(rootKey, payloadBytes, signature) {
		t.Fatal("trust manifest root signature did not verify")
	}
	var payload trustManifestPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	rootJKT, _ := manifest.RootPublicJWK.Thumbprint()
	onlineJKT, _ := payload.OnlinePublicJWK.Thumbprint()
	if payload.RootJKT != rootJKT || onlineJKT == rootJKT || payload.Revision != 1 || payload.ExpiresAtMS != now.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("unexpected trust manifest payload: %#v", payload)
	}
}
