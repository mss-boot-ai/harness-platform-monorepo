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
	if payload.RootJKT != rootJKT || onlineJKT == rootJKT || payload.Revision != 1 || payload.ExpiresAtMS != now.Add(trustManifestTTL).UnixMilli() {
		t.Fatalf("unexpected trust manifest payload: %#v", payload)
	}
}

func TestTrustManifestExpiryRefreshKeepsTrustIdentity(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	first, err := trust.ManifestAt(now)
	if err != nil {
		t.Fatalf("first ManifestAt: %v", err)
	}
	later := now.Add(48 * time.Hour)
	refreshed, err := trust.ManifestAt(later)
	if err != nil {
		t.Fatalf("refreshed ManifestAt: %v", err)
	}
	firstRoot, _ := first.RootPublicJWK.Thumbprint()
	refreshedRoot, _ := refreshed.RootPublicJWK.Thumbprint()
	if firstRoot != refreshedRoot || first.Revision != refreshed.Revision {
		t.Fatal("refreshing manifest expiry changed trust identity")
	}
	if !refreshed.ExpiresAt.Equal(later.Add(trustManifestTTL)) {
		t.Fatalf("refreshed expiry = %s, want %s", refreshed.ExpiresAt, later.Add(trustManifestTTL))
	}
	if !trust.ExpiresAt.Equal(now.Add(trustManifestTTL)) {
		t.Fatal("ManifestAt mutated the shared trust bundle")
	}
}

func TestServerCachesTrustManifestWithinRefreshWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	initial, err := trust.ManifestAt(now)
	if err != nil {
		t.Fatalf("initial ManifestAt: %v", err)
	}
	server := &Server{
		trust:                  trust,
		cachedTrustManifest:    initial,
		trustManifestRefreshAt: now.Add(trustManifestRefreshInterval),
	}
	cached, err := server.currentTrustManifest(now.Add(time.Hour))
	if err != nil {
		t.Fatalf("cached manifest: %v", err)
	}
	if cached.PayloadBase64URL != initial.PayloadBase64URL || cached.SignatureBase64URL != initial.SignatureBase64URL {
		t.Fatal("manifest changed inside the refresh window")
	}
	refreshAt := now.Add(trustManifestRefreshInterval)
	refreshed, err := server.currentTrustManifest(refreshAt)
	if err != nil {
		t.Fatalf("refreshed manifest: %v", err)
	}
	if refreshed.Revision != initial.Revision || refreshed.RootPublicJWK != initial.RootPublicJWK {
		t.Fatal("manifest refresh changed trust material or revision")
	}
	if refreshed.PayloadBase64URL == initial.PayloadBase64URL || !refreshed.ExpiresAt.Equal(refreshAt.Add(trustManifestTTL)) {
		t.Fatal("manifest refresh did not publish a new bounded validity window")
	}
}
