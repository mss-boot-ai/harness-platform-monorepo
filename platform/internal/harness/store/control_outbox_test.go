package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func testControlOutbox(now time.Time, kind domain.ControlOutboxKind, recipient, correlation, session domain.ID) domain.ControlOutbox {
	value := domain.ControlOutbox{
		ID: tid(211), OwnerUserID: "owner", TenantID: "tenant", SessionID: session,
		CorrelationID: correlation, RecipientEndpointID: recipient, Kind: kind,
		Status: domain.ControlOutboxPending, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if kind == domain.ControlOutboxOpenTunnel || kind == domain.ControlOutboxCloseTunnel {
		value.Payload = []byte{1, 2, 3}
	} else {
		value.Packet = []byte{4, 5, 6}
	}
	return value
}

func TestControlOutboxIsDurableIdempotentAndCompletable(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	entry := testControlOutbox(now, domain.ControlOutboxOpenTunnel, tid(212), tid(213), tid(213))
	if outcome, err := persistence.PutControlOutbox(ctx, entry); err != nil || outcome != PutControlOutboxStored {
		t.Fatalf("PutControlOutbox = %s, %v", outcome, err)
	}
	duplicate := entry
	duplicate.ID = tid(214)
	duplicate.CreatedAt = now.Add(time.Second)
	duplicate.UpdatedAt = duplicate.CreatedAt
	duplicate.ExpiresAt = now.Add(2 * time.Hour)
	if outcome, err := persistence.PutControlOutbox(ctx, duplicate); err != nil || outcome != PutControlOutboxDuplicate {
		t.Fatalf("duplicate PutControlOutbox = %s, %v", outcome, err)
	}
	conflict := duplicate
	conflict.Payload = []byte{9}
	if _, err := persistence.PutControlOutbox(ctx, conflict); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("conflicting outbox error = %v", err)
	}
	pending, err := persistence.ListPendingControlOutbox(ctx, entry.RecipientEndpointID, now, 10)
	if err != nil || len(pending) != 1 || pending[0].CorrelationID != entry.CorrelationID || !bytes.Equal(pending[0].Payload, entry.Payload) {
		t.Fatalf("pending = %#v, err=%v", pending, err)
	}
	if err := persistence.CompleteControlOutbox(ctx, entry.Kind, entry.CorrelationID, entry.RecipientEndpointID, now.Add(time.Minute)); err != nil {
		t.Fatalf("CompleteControlOutbox: %v", err)
	}
	if err := persistence.CompleteControlOutbox(ctx, entry.Kind, entry.CorrelationID, entry.RecipientEndpointID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("idempotent CompleteControlOutbox: %v", err)
	}
	pending, err = persistence.ListPendingControlOutbox(ctx, entry.RecipientEndpointID, now, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after completion = %#v, err=%v", pending, err)
	}
}
