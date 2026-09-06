package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func newM1TestStore(t *testing.T) *Store {
	t.Helper()
	persistence := newTestStore(t)
	if err := CreateM1Schema(persistence.db); err != nil {
		t.Fatalf("CreateM1Schema: %v", err)
	}
	return persistence
}

func TestM1SchemaIsExplicitRepeatableAndComplete(t *testing.T) {
	persistence := newM1TestStore(t)
	if err := CreateM1Schema(persistence.db); err != nil {
		t.Fatalf("repeat CreateM1Schema: %v", err)
	}
	if err := VerifyAllSchema(persistence.db); err != nil {
		t.Fatalf("VerifyAllSchema: %v", err)
	}
}

func seedKeyPackageSession(t *testing.T, persistence *Store, now time.Time) (domain.Endpoint, domain.Endpoint, domain.EndpointCredential, domain.Session) {
	t.Helper()
	ctx := context.Background()
	aba := endpoint(31, 32, 40, domain.EndpointTypeABA, "owner", now)
	aba.TenantID = "tenant"
	hc := endpoint(33, 34, 50, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	if err := persistence.CreateEndpoint(ctx, aba); err != nil {
		t.Fatalf("CreateEndpoint ABA: %v", err)
	}
	if err := persistence.CreateEndpoint(ctx, hc); err != nil {
		t.Fatalf("CreateEndpoint HC: %v", err)
	}
	issuerCredential := credential(35, aba, now)
	credentialRecord, err := credentialToRow(issuerCredential)
	if err != nil {
		t.Fatalf("credentialToRow: %v", err)
	}
	if err := persistence.db.Create(&credentialRecord).Error; err != nil {
		t.Fatalf("create issuer credential: %v", err)
	}
	session := domain.Session{
		ID:                    tid(36),
		OwnerUserID:           "owner",
		TenantID:              "tenant",
		ABAEndpointID:         aba.ID,
		HCEndpointID:          hc.ID,
		RuntimeProfileID:      "test-agent",
		WorkspaceID:           "workspace",
		RequestedCapabilities: []string{"prompt"},
		Status:                domain.SessionStatusCreating,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := persistence.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	session, err = persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
		return value.WaitForKey(now)
	})
	if err != nil {
		t.Fatalf("WaitForKey: %v", err)
	}
	return aba, hc, issuerCredential, session
}

func testKeyPackage(now time.Time, aba, hc domain.Endpoint, credential domain.EndpointCredential, session domain.Session) domain.SessionKeyPackage {
	return domain.SessionKeyPackage{
		ID:                    tid(37),
		SessionID:             session.ID,
		Generation:            1,
		IssuerABAEndpointID:   aba.ID,
		RecipientHCEndpointID: hc.ID,
		CryptoSuite:           domain.CryptoSuiteMSSAWPSuite0001,
		EncapsulatedKey:       []byte{1, 2, 3},
		Ciphertext:            []byte{4, 5, 6},
		ContextHash:           sha256.Sum256([]byte("key-package-context")),
		IssuerSignature:       bytes.Repeat([]byte{7}, 64),
		IssuerCredentialID:    credential.ID,
		Status:                domain.KeyPackageStatusPending,
		ExpiresAt:             now.Add(time.Hour),
		CreatedAt:             now,
	}
}

func TestKeyPackageUniqueScopeIsIdempotentAndTenantBound(t *testing.T) {
	persistence := newM1TestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
	value := testKeyPackage(now, aba, hc, issuerCredential, session)

	start := make(chan struct{})
	outcomes := make(chan PutKeyPackageOutcome, 2)
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := persistence.PutSessionKeyPackage(ctx, "owner", "tenant", value)
			if err != nil {
				errorsFound <- err
				return
			}
			outcomes <- result.Outcome
		}()
	}
	close(start)
	group.Wait()
	close(outcomes)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("PutSessionKeyPackage: %v", err)
	}
	counts := map[PutKeyPackageOutcome]int{}
	for outcome := range outcomes {
		counts[outcome]++
	}
	if counts[PutKeyPackageStored] != 1 || counts[PutKeyPackageDuplicate] != 1 {
		t.Fatalf("outcomes = %#v", counts)
	}

	conflict := value
	conflict.ID = tid(38)
	conflict.Ciphertext = []byte{9, 9, 9}
	if _, err := persistence.PutSessionKeyPackage(ctx, "owner", "tenant", conflict); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("conflicting package error = %v", err)
	}
	values, err := persistence.ListSessionKeyPackages(ctx, session.ID, "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].ID != value.ID {
		t.Fatalf("ListSessionKeyPackages = %#v err=%v", values, err)
	}
	if _, err := persistence.ListSessionKeyPackages(ctx, session.ID, "owner", "other-tenant", 10); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant list error = %v", err)
	}
	acknowledged, err := persistence.AcknowledgeSessionKeyPackage(ctx, value.ID, hc.ID, "owner", "tenant", now.Add(time.Second))
	if err != nil || acknowledged.Status != domain.KeyPackageStatusAcknowledged || acknowledged.AcknowledgedAt == nil {
		t.Fatalf("AcknowledgeSessionKeyPackage = %#v err=%v", acknowledged, err)
	}
}

func TestAuditPersistenceRejectsUnsafeMetadataAndScopesReads(t *testing.T) {
	persistence := newM1TestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	event := domain.SecurityAuditEvent{
		ID:          tid(41),
		OwnerUserID: "owner",
		TenantID:    "tenant",
		ActorType:   domain.AuditActorHuman,
		ActorID:     "owner",
		Action:      "endpoint.revoke",
		ObjectType:  "endpoint",
		ObjectID:    tid(42).String(),
		Result:      "success",
		Metadata: map[string]string{
			"previousStatus": "ACTIVE",
			"currentStatus":  "REVOKED",
			"endpointType":   "HC_WEB",
		},
		CreatedAt: now,
	}
	if err := persistence.AppendAudit(ctx, event); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	unsafe := event
	unsafe.ID = tid(43)
	unsafe.Metadata = map[string]string{"token": "must-not-be-stored"}
	if err := persistence.AppendAudit(ctx, unsafe); !domain.HasCode(err, domain.CodeSecurityViolation) {
		t.Fatalf("unsafe audit error = %v", err)
	}
	values, err := persistence.ListAudit(ctx, "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].ID != event.ID {
		t.Fatalf("ListAudit = %#v err=%v", values, err)
	}
	other, err := persistence.ListAudit(ctx, "owner", "other-tenant", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant ListAudit = %#v err=%v", other, err)
	}
	encoded, err := json.Marshal(values[0])
	if err != nil {
		t.Fatalf("marshal audit: %v", err)
	}
	if bytes.Contains(encoded, []byte("must-not-be-stored")) {
		t.Fatalf("unsafe value leaked into persisted audit: %s", encoded)
	}
}

func TestIdempotencyScopeAllowsOneReservationAndStableReplay(t *testing.T) {
	persistence := newM1TestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	record := domain.IdempotencyRecord{
		ID:          tid(51),
		OwnerUserID: "owner",
		TenantID:    "tenant",
		ActorID:     "owner",
		Operation:   "enrollment.approve",
		Key:         "idempotency-key-00000001",
		RequestHash: sha256.Sum256([]byte("request")),
		Status:      domain.IdempotencyStatusInProgress,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}

	start := make(chan struct{})
	outcomes := make(chan IdempotencyReserveOutcome, 2)
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := persistence.ReserveIdempotency(ctx, record)
			if err != nil {
				errorsFound <- err
				return
			}
			outcomes <- result.Outcome
		}()
	}
	close(start)
	group.Wait()
	close(outcomes)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("ReserveIdempotency: %v", err)
	}
	counts := map[IdempotencyReserveOutcome]int{}
	for outcome := range outcomes {
		counts[outcome]++
	}
	if counts[IdempotencyAcquired] != 1 || counts[IdempotencyInProgress] != 1 {
		t.Fatalf("reservation outcomes = %#v", counts)
	}

	completed := record
	completed.Status = domain.IdempotencyStatusCompleted
	completed.HTTPStatus = 200
	completed.ResponseJSON = []byte(`{"id":"result","status":"APPROVED"}`)
	completed.UpdatedAt = now.Add(time.Second)
	stored, err := persistence.CompleteIdempotency(ctx, completed)
	if err != nil || stored.Status != domain.IdempotencyStatusCompleted || stored.RowVersion != 1 {
		t.Fatalf("CompleteIdempotency = %#v err=%v", stored, err)
	}

	retry := record
	retry.ID = tid(52)
	retry.CreatedAt = now.Add(2 * time.Second)
	retry.UpdatedAt = retry.CreatedAt
	reservation, err := persistence.ReserveIdempotency(ctx, retry)
	if err != nil || reservation.Outcome != IdempotencyReplay || !bytes.Equal(reservation.Record.ResponseJSON, completed.ResponseJSON) {
		t.Fatalf("replay reservation = %#v err=%v", reservation, err)
	}
	conflict := retry
	conflict.ID = tid(53)
	conflict.RequestHash = sha256.Sum256([]byte("different request"))
	if _, err := persistence.ReserveIdempotency(ctx, conflict); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("conflicting request error = %v", err)
	}
	var count int64
	if err := persistence.db.Model(new(idempotencyRow)).Count(&count).Error; err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if count != 1 {
		t.Fatalf("idempotency record count = %d, want 1", count)
	}
}
