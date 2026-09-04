package store

import (
	"strings"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestVerifyM1SchemaRejectsMalformedCriticalIndexes(t *testing.T) {
	tests := []struct {
		name      string
		model     any
		indexName string
		createSQL string
	}{
		{
			name:      "key package same name non unique",
			model:     new(keyPackageRow),
			indexName: "ux_harness_key_package_recipient",
			createSQL: "CREATE INDEX ux_harness_key_package_recipient ON harness_session_key_packages(session_id, generation, recipient_hc_endpoint_id)",
		},
		{
			name:      "key package missing column",
			model:     new(keyPackageRow),
			indexName: "ux_harness_key_package_recipient",
			createSQL: "CREATE UNIQUE INDEX ux_harness_key_package_recipient ON harness_session_key_packages(session_id, generation)",
		},
		{
			name:      "key package wrong order",
			model:     new(keyPackageRow),
			indexName: "ux_harness_key_package_recipient",
			createSQL: "CREATE UNIQUE INDEX ux_harness_key_package_recipient ON harness_session_key_packages(recipient_hc_endpoint_id, session_id, generation)",
		},
		{
			name:      "key package wrong column",
			model:     new(keyPackageRow),
			indexName: "ux_harness_key_package_recipient",
			createSQL: "CREATE UNIQUE INDEX ux_harness_key_package_recipient ON harness_session_key_packages(session_id, generation, issuer_aba_endpoint_id)",
		},
		{
			name:      "idempotency same name non unique",
			model:     new(idempotencyRow),
			indexName: "ux_harness_idempotency_scope",
			createSQL: "CREATE INDEX ux_harness_idempotency_scope ON harness_idempotency_records(owner_user_id, tenant_id, actor_id, operation, idempotency_key)",
		},
		{
			name:      "idempotency missing column",
			model:     new(idempotencyRow),
			indexName: "ux_harness_idempotency_scope",
			createSQL: "CREATE UNIQUE INDEX ux_harness_idempotency_scope ON harness_idempotency_records(owner_user_id, tenant_id, actor_id, operation)",
		},
		{
			name:      "idempotency wrong order",
			model:     new(idempotencyRow),
			indexName: "ux_harness_idempotency_scope",
			createSQL: "CREATE UNIQUE INDEX ux_harness_idempotency_scope ON harness_idempotency_records(tenant_id, owner_user_id, actor_id, operation, idempotency_key)",
		},
		{
			name:      "idempotency wrong column",
			model:     new(idempotencyRow),
			indexName: "ux_harness_idempotency_scope",
			createSQL: "CREATE UNIQUE INDEX ux_harness_idempotency_scope ON harness_idempotency_records(owner_user_id, tenant_id, request_hash, operation, idempotency_key)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence := newM1TestStore(t)
			if err := persistence.db.Migrator().DropIndex(test.model, test.indexName); err != nil {
				t.Fatalf("DropIndex(%s): %v", test.indexName, err)
			}
			if err := persistence.db.Exec(test.createSQL).Error; err != nil {
				t.Fatalf("create malformed index: %v", err)
			}
			if err := VerifyM1Schema(persistence.db); err == nil {
				t.Fatalf("VerifyM1Schema accepted malformed index %s", test.indexName)
			}
		})
	}
}

func TestM1CriticalUniqueConstraintsRejectDuplicateRows(t *testing.T) {
	persistence := newM1TestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
	packageValue := testKeyPackage(now, aba, hc, issuerCredential, session)
	packageRow := keyPackageToRow(packageValue)
	if err := persistence.db.Create(&packageRow).Error; err != nil {
		t.Fatalf("create key package row: %v", err)
	}
	duplicatePackage := packageRow
	duplicatePackage.ID = tid(99).String()
	if err := persistence.db.Create(&duplicatePackage).Error; err == nil {
		t.Fatal("key package unique scope accepted a duplicate row")
	}

	idempotency := idempotencyRow{
		ID:          tid(100).String(),
		OwnerUserID: "owner",
		TenantID:    "tenant",
		ActorID:     "owner",
		Operation:   "endpoint.revoke",
		Key:         "idempotency-key-00000001",
		RequestHash: strings.Repeat("a", 64),
		Status:      string(domain.IdempotencyStatusInProgress),
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	if err := persistence.db.Create(&idempotency).Error; err != nil {
		t.Fatalf("create idempotency row: %v", err)
	}
	duplicateIdempotency := idempotency
	duplicateIdempotency.ID = tid(101).String()
	if err := persistence.db.Create(&duplicateIdempotency).Error; err == nil {
		t.Fatal("idempotency unique scope accepted a duplicate row")
	}
}
