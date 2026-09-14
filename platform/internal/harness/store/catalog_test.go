package store

import (
	"context"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestExecutionCatalogScopeGenerationExpiryAndRevocation(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(90, 91, 92, domain.EndpointTypeABA, "owner", now)
	aba.TenantID = "tenant"
	if err := persistence.CreateEndpoint(ctx, aba); err != nil {
		t.Fatal(err)
	}
	generation, err := persistence.NextConnectionGeneration(ctx, aba.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	document := domain.ExecutionCatalog{Version: 1, Runtimes: []domain.CatalogRuntime{{ID: "agent", DisplayName: "Agent"}},
		Workspaces: []domain.CatalogWorkspace{{ID: "project", DisplayName: "Project", RuntimeIDs: []string{"agent"}}}}
	revision, _ := document.Revision()
	catalog := domain.PublishedCatalog{EndpointID: aba.ID, OwnerUserID: aba.OwnerUserID, TenantID: aba.TenantID,
		ConnectionGeneration: generation, Revision: revision, Catalog: document, PublishedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := persistence.PublishExecutionCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetExecutionCatalog(ctx, "owner", "tenant", aba.ID, now); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{"other", "tenant"}, {"owner", "other"}} {
		if _, err := persistence.GetExecutionCatalog(ctx, scope[0], scope[1], aba.ID, now); !domain.HasCode(err, domain.CodeNotFound) {
			t.Fatalf("scope leak: %v", err)
		}
	}
	if _, err := persistence.GetExecutionCatalog(ctx, "owner", "tenant", aba.ID, catalog.ExpiresAt); !domain.HasCode(err, domain.CodeExpired) {
		t.Fatalf("expiry: %v", err)
	}
	generation, err = persistence.NextConnectionGeneration(ctx, aba.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.PublishExecutionCatalog(ctx, catalog); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("stale publication: %v", err)
	}
	if _, err := persistence.GetExecutionCatalog(ctx, "owner", "tenant", aba.ID, now); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("stale catalog remained available: %v", err)
	}
	catalog.ConnectionGeneration = generation
	if err := persistence.PublishExecutionCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := persistence.db.Model(new(endpointRow)).Where("id = ?", aba.ID.String()).Update("status", string(domain.EndpointStatusRevoked)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetExecutionCatalog(ctx, "owner", "tenant", aba.ID, now); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("revoked catalog remained available: %v", err)
	}
	if err := persistence.PublishExecutionCatalog(ctx, catalog); !domain.HasCode(err, domain.CodeSecurityViolation) {
		t.Fatalf("revoked publication: %v", err)
	}
}

func TestCatalogSchemaIsRepeatableAndDetectsMissingColumns(t *testing.T) {
	persistence := newTestStore(t)
	for i := 0; i < 2; i++ {
		if err := CreateCatalogSchema(persistence.db); err != nil {
			t.Fatal(err)
		}
	}
	if err := persistence.db.Migrator().DropColumn(new(catalogRow), "catalog_json"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCatalogSchema(persistence.db); err == nil {
		t.Fatal("incomplete schema accepted")
	}
}
