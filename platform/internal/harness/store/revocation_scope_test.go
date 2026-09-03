package store

import (
	"context"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestScopedRevocationCannotCrossTenant(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	value := endpoint(91, 92, 93, domain.EndpointTypeHCWeb, "owner", now)
	value.TenantID = "tenant-a"
	if err := persistence.CreateEndpoint(ctx, value); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	if _, err := persistence.RevokeEndpointForOwner(ctx, value.ID, "owner", "tenant-b", now.Add(time.Second)); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant revoke error = %v", err)
	}
	values, err := persistence.ListEndpoints(ctx, "owner", "tenant-a", 10)
	if err != nil {
		t.Fatalf("ListEndpoints after denied revoke: %v", err)
	}
	if len(values) != 1 || values[0].Status != domain.EndpointStatusActive {
		t.Fatalf("cross-tenant revoke changed endpoint: %#v", values)
	}
	stored, err := persistence.RevokeEndpointForOwner(ctx, value.ID, "owner", "tenant-a", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("RevokeEndpointForOwner: %v", err)
	}
	if stored.Status != domain.EndpointStatusRevoked {
		t.Fatalf("status = %s", stored.Status)
	}
}
