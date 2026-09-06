package gateway

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
)

func TestTrustStatePersistsRootAndAdvancesRevision(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	path := filepath.Join(t.TempDir(), "private", "gateway-trust.json")
	first, err := LoadOrCreateTrustState(path, deterministicGatewayBytes(1024), now)
	if err != nil {
		t.Fatalf("create trust state: %v", err)
	}
	firstManifest, err := first.Manifest()
	if err != nil {
		t.Fatalf("first manifest: %v", err)
	}
	second, err := LoadOrCreateTrustState(path, deterministicGatewayBytes(1024), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("reload trust state: %v", err)
	}
	secondManifest, err := second.Manifest()
	if err != nil {
		t.Fatalf("second manifest: %v", err)
	}
	firstRoot, _ := firstManifest.RootPublicJWK.Thumbprint()
	secondRoot, _ := secondManifest.RootPublicJWK.Thumbprint()
	firstOnline, _ := awpcrypto.PublicJWK(&first.Online.PublicKey)
	secondOnline, _ := awpcrypto.PublicJWK(&second.Online.PublicKey)
	firstOnlineJKT, _ := firstOnline.Thumbprint()
	secondOnlineJKT, _ := secondOnline.Thumbprint()
	if firstRoot != secondRoot || firstOnlineJKT != secondOnlineJKT || first.Revision != 1 || second.Revision != 2 {
		t.Fatalf("trust continuity root=%v online=%v revisions=%d/%d", firstRoot == secondRoot, firstOnlineJKT == secondOnlineJKT, first.Revision, second.Revision)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat trust state: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("trust state permissions=%#o", info.Mode().Perm())
		}
	}
}

func TestTrustStateRejectsBroadPermissionsAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission contract")
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	directory := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(directory, "gateway-trust.json")
	if _, err := LoadOrCreateTrustState(path, deterministicGatewayBytes(1024), now); err != nil {
		t.Fatalf("create trust state: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("broaden trust state: %v", err)
	}
	if _, err := LoadOrCreateTrustState(path, deterministicGatewayBytes(1024), now); err == nil {
		t.Fatal("broad trust state permissions were accepted")
	}
	link := filepath.Join(t.TempDir(), "trust-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := LoadOrCreateTrustState(link, deterministicGatewayBytes(1024), now); err == nil {
		t.Fatal("trust state symlink was accepted")
	}
}
