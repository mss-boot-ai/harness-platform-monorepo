package store

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestCreationCancellationFencesLateCreationAndIsIdempotent(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	hc := endpoint(180, 181, 182, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	if err := persistence.CreateEndpoint(t.Context(), hc); err != nil {
		t.Fatal(err)
	}
	cancelled := domain.IdempotencyRecord{ID: tid(183), OwnerUserID: "owner", TenantID: "tenant", ActorID: hc.ID.String(), Operation: "endpoint.session.create",
		Key: "creation-cancelled-key", RequestHash: sha256.Sum256([]byte("cancel")), Status: domain.IdempotencyStatusCompleted,
		HTTPStatus: 409, ResponseJSON: []byte(`{"code":"CREATION_CANCELLED"}`), ErrorCode: "CREATION_CANCELLED", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	first, err := persistence.CancelEndpointSessionCreation(t.Context(), cancelled)
	if err != nil {
		t.Fatal(err)
	}
	cancelled.ID = tid(184)
	second, err := persistence.CancelEndpointSessionCreation(t.Context(), cancelled)
	if err != nil || first.ID != second.ID {
		t.Fatalf("cancellation replay: %v", err)
	}
	late := cancelled
	late.ID = tid(185)
	late.Status = domain.IdempotencyStatusInProgress
	late.HTTPStatus = 0
	late.ResponseJSON = nil
	late.ErrorCode = ""
	late.RequestHash = sha256.Sum256([]byte("original create"))
	if _, err := persistence.ReserveIdempotency(t.Context(), late); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("late create passed cancellation fence: %v", err)
	}
	if record, err := persistence.GetEndpointSessionCreation(t.Context(), "owner", "tenant", hc.ID, cancelled.Key); err != nil || record.ErrorCode != "CREATION_CANCELLED" {
		t.Fatalf("lookup cancellation: %v", err)
	}
	if _, err := persistence.GetEndpointSessionCreation(t.Context(), "other", "tenant", hc.ID, cancelled.Key); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatal("creation scope leaked")
	}
	audits, err := persistence.ListAudit(t.Context(), "owner", "tenant", 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "session.create.cancel" {
		t.Fatalf("cancellation audit: %v %v", audits, err)
	}
}

func TestCancellationCannotReplaceAnAlreadyCreatedResult(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	hc := endpoint(190, 191, 192, domain.EndpointTypeHCWeb, "owner", now)
	if err := persistence.CreateEndpoint(t.Context(), hc); err != nil {
		t.Fatal(err)
	}
	original := domain.IdempotencyRecord{ID: tid(193), OwnerUserID: "owner", ActorID: hc.ID.String(), Operation: "endpoint.session.create", Key: "creation-completed-key",
		RequestHash: sha256.Sum256([]byte("original")), Status: domain.IdempotencyStatusInProgress, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if _, err := persistence.ReserveIdempotency(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	original.Status = domain.IdempotencyStatusCompleted
	original.HTTPStatus = 201
	original.ResponseJSON = []byte(`{"sessionId":"` + tid(194).String() + `"}`)
	if _, err := persistence.CompleteIdempotency(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	cancelled := original
	cancelled.ID = tid(195)
	cancelled.HTTPStatus = 409
	cancelled.ErrorCode = "CREATION_CANCELLED"
	cancelled.RequestHash = sha256.Sum256([]byte("cancel"))
	cancelled.ResponseJSON = []byte(`{"code":"CREATION_CANCELLED"}`)
	got, err := persistence.CancelEndpointSessionCreation(t.Context(), cancelled)
	if err != nil || got.ID != original.ID || got.HTTPStatus != 201 || string(got.ResponseJSON) != string(original.ResponseJSON) {
		t.Fatalf("existing creation was replaced: %v", err)
	}
}
