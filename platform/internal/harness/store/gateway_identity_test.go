package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestAuthenticateAccessTokenRequiresActiveBoundCredential(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	value := endpoint(60, 61, 70, domain.EndpointTypeHCWeb, "owner", now)
	value.TenantID = "tenant"
	if err := persistence.CreateEndpoint(ctx, value); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	access := credential(62, value, now)
	row, err := credentialToRow(access)
	if err != nil {
		t.Fatalf("credentialToRow: %v", err)
	}
	if err := persistence.db.Create(&row).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}

	gotEndpoint, gotCredential, err := persistence.AuthenticateAccessToken(ctx, access.TokenHash, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("AuthenticateAccessToken: %v", err)
	}
	if gotEndpoint.ID != value.ID || gotCredential.ID != access.ID || gotCredential.SigningJKT != value.SigningJKT {
		t.Fatalf("unexpected endpoint/credential: %#v %#v", gotEndpoint, gotCredential)
	}
	if _, _, err := persistence.AuthenticateAccessToken(ctx, sha256.Sum256([]byte("wrong")), now); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("unknown token error = %v, want not found", err)
	}
	if _, _, err := persistence.AuthenticateAccessToken(ctx, access.TokenHash, access.ExpiresAt); !domain.HasCode(err, domain.CodeExpired) {
		t.Fatalf("expired token error = %v, want expired", err)
	}
}

func TestDPoPReplayPersistenceIsSharedAndBounded(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	if err := persistence.UseDPoPReplay(ctx, "jkt", "jti-1", now, now.Add(time.Minute), 1); err != nil {
		t.Fatalf("store first replay key: %v", err)
	}
	if err := persistence.UseDPoPReplay(ctx, "jkt", "jti-1", now, now.Add(time.Minute), 1); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("duplicate replay error = %v, want conflict", err)
	}
	if err := persistence.UseDPoPReplay(ctx, "jkt", "jti-2", now, now.Add(time.Minute), 1); !domain.HasCode(err, domain.CodeResourceLimit) {
		t.Fatalf("capacity error = %v, want resource limit", err)
	}
	if err := persistence.UseDPoPReplay(ctx, "jkt", "jti-2", now.Add(2*time.Minute), now.Add(3*time.Minute), 1); err != nil {
		t.Fatalf("expired entry was not cleaned: %v", err)
	}
}

func TestEndpointNonceUsesCompareAndSwapRotation(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	endpointID := tid(80)
	first := sha256.Sum256([]byte("first nonce"))
	second := sha256.Sum256([]byte("second nonce"))
	third := sha256.Sum256([]byte("third nonce"))
	if err := persistence.PutEndpointNonce(ctx, endpointID, first, now, now.Add(time.Minute)); err != nil {
		t.Fatalf("PutEndpointNonce: %v", err)
	}
	if got, err := persistence.GetEndpointNonceHash(ctx, endpointID, now); err != nil || got != first {
		t.Fatalf("GetEndpointNonceHash = %x, error=%v", got, err)
	}
	if err := persistence.RotateEndpointNonce(ctx, endpointID, first, second, now.Add(time.Second), now.Add(time.Minute)); err != nil {
		t.Fatalf("RotateEndpointNonce: %v", err)
	}
	if err := persistence.RotateEndpointNonce(ctx, endpointID, first, third, now.Add(2*time.Second), now.Add(time.Minute)); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("stale nonce rotation error = %v, want conflict", err)
	}
	if got, err := persistence.GetEndpointNonceHash(ctx, endpointID, now.Add(2*time.Second)); err != nil || got != second {
		t.Fatalf("rotated nonce = %x, error=%v", got, err)
	}
}

func TestM2GatewaySchemaIsRepeatable(t *testing.T) {
	persistence := newTestStore(t)
	if err := CreateM2GatewaySchema(persistence.db); err != nil {
		t.Fatalf("CreateM2GatewaySchema: %v", err)
	}
	if err := VerifyM2GatewaySchema(persistence.db); err != nil {
		t.Fatalf("VerifyM2GatewaySchema: %v", err)
	}
}

func TestConnectionGenerationRemainsMonotonicAcrossStoreInstances(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	endpointID := tid(120)
	first, err := persistence.NextConnectionGeneration(context.Background(), endpointID, now)
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	second, err := persistence.NextConnectionGeneration(context.Background(), endpointID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}
	restarted, err := New(persistence.db)
	if err != nil {
		t.Fatalf("restart Store: %v", err)
	}
	third, err := restarted.NextConnectionGeneration(context.Background(), endpointID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("third generation: %v", err)
	}
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("connection generations = %d, %d, %d", first, second, third)
	}
}
