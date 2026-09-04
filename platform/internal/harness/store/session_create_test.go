package store

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestCreateEndpointSessionAuthorizesEndpointsAndReplays(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(141, 142, 143, domain.EndpointTypeABA, "owner", now)
	aba.TenantID = "tenant"
	hc := endpoint(144, 145, 146, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	for _, value := range []domain.Endpoint{aba, hc} {
		if err := persistence.CreateEndpoint(t.Context(), value); err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
	}
	session := domain.Session{
		ID: tid(147), OwnerUserID: "owner", TenantID: "tenant", ABAEndpointID: aba.ID, HCEndpointID: hc.ID,
		RuntimeProfileID: "test-agent", WorkspaceID: "fixture", RequestedCapabilities: []string{"prompt", "session"},
		Status: domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now,
	}
	response := []byte(`{"sessionId":"` + session.ID.String() + `","status":"CREATING"}`)
	reservation := domain.IdempotencyRecord{
		ID: tid(148), OwnerUserID: "owner", TenantID: "tenant", ActorID: hc.ID.String(),
		Operation: "endpoint.session.create", Key: "session-create-key-0001", RequestHash: sha256.Sum256([]byte("request")),
		Status: domain.IdempotencyStatusInProgress, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	audit := domain.SecurityAuditEvent{
		ID: tid(149), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorEndpoint,
		ActorID: hc.ID.String(), Action: "session.create", ObjectType: "session", ObjectID: session.ID.String(),
		Result: "success", Metadata: map[string]string{"sessionStatus": string(session.Status)}, CreatedAt: now,
	}
	created, body, replayed, err := persistence.CreateEndpointSession(
		t.Context(), session, reservation, audit, 201, response,
	)
	if err != nil || replayed || created.ID != session.ID || !bytes.Equal(body, response) {
		t.Fatalf("create session=%#v body=%s replayed=%v error=%v", created, body, replayed, err)
	}
	_, replayBody, replayed, err := persistence.CreateEndpointSession(
		t.Context(), session, reservation, audit, 201, response,
	)
	if err != nil || !replayed || !bytes.Equal(replayBody, response) {
		t.Fatalf("replay body=%s replayed=%v error=%v", replayBody, replayed, err)
	}
	values, err := persistence.ListSessions(t.Context(), "owner", "tenant", 10)
	if err != nil || len(values) != 1 {
		t.Fatalf("sessions=%#v error=%v", values, err)
	}
	audits, err := persistence.ListAudit(t.Context(), "owner", "tenant", 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "session.create" {
		t.Fatalf("audits=%#v error=%v", audits, err)
	}
}

func TestCreateEndpointSessionRejectsCrossOwnerABA(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(151, 152, 153, domain.EndpointTypeABA, "other", now)
	aba.TenantID = "tenant"
	hc := endpoint(154, 155, 156, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	for _, value := range []domain.Endpoint{aba, hc} {
		if err := persistence.CreateEndpoint(t.Context(), value); err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
	}
	session := domain.Session{
		ID: tid(157), OwnerUserID: "owner", TenantID: "tenant", ABAEndpointID: aba.ID, HCEndpointID: hc.ID,
		RuntimeProfileID: "test-agent", WorkspaceID: "fixture", RequestedCapabilities: []string{"prompt"},
		Status: domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now,
	}
	response := []byte(`{"sessionId":"` + session.ID.String() + `"}`)
	reservation := domain.IdempotencyRecord{
		ID: tid(158), OwnerUserID: "owner", TenantID: "tenant", ActorID: hc.ID.String(),
		Operation: "endpoint.session.create", Key: "session-create-key-0002", RequestHash: sha256.Sum256([]byte("request")),
		Status: domain.IdempotencyStatusInProgress, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	audit := domain.SecurityAuditEvent{
		ID: tid(159), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorEndpoint,
		ActorID: hc.ID.String(), Action: "session.create", ObjectType: "session", ObjectID: session.ID.String(),
		Result: "success", Metadata: map[string]string{"sessionStatus": string(session.Status)}, CreatedAt: now,
	}
	if _, _, _, err := persistence.CreateEndpointSession(
		t.Context(), session, reservation, audit, 201, response,
	); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-owner session error=%v, want not found", err)
	}
	values, err := persistence.ListSessions(t.Context(), "owner", "tenant", 10)
	if err != nil || len(values) != 0 {
		t.Fatalf("sessions after rejected create=%#v error=%v", values, err)
	}
}
