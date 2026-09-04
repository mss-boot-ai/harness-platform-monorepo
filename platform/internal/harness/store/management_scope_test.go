package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestManagementReadsAndMutationsRequireExactTenant(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	endpointA := endpoint(101, 102, 103, domain.EndpointTypeABA, "owner", now)
	endpointA.TenantID = "tenant-a"
	endpointB := endpoint(104, 105, 106, domain.EndpointTypeABA, "owner", now)
	endpointB.TenantID = "tenant-b"
	for _, value := range []domain.Endpoint{endpointA, endpointB} {
		if err := persistence.CreateEndpoint(ctx, value); err != nil {
			t.Fatalf("CreateEndpoint(%s): %v", value.TenantID, err)
		}
	}

	values, err := persistence.ListEndpoints(ctx, "owner", "tenant-a", 10)
	if err != nil || len(values) != 1 || values[0].ID != endpointA.ID {
		t.Fatalf("ListEndpoints tenant-a = %#v err=%v", values, err)
	}
	if _, err := persistence.UpdateEndpointForOwner(ctx, endpointA.ID, "owner", "tenant-b", func(value *domain.Endpoint) error {
		return value.Suspend(now.Add(time.Second))
	}); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant endpoint update error = %v", err)
	}
	values, err = persistence.ListEndpoints(ctx, "owner", "tenant-a", 10)
	if err != nil || len(values) != 1 || values[0].Status != domain.EndpointStatusActive {
		t.Fatalf("cross-tenant update changed endpoint: %#v err=%v", values, err)
	}

	hc := endpoint(107, 108, 109, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant-a"
	if err := persistence.CreateEndpoint(ctx, hc); err != nil {
		t.Fatalf("CreateEndpoint HC: %v", err)
	}
	session := domain.Session{
		ID:                    tid(110),
		OwnerUserID:           "owner",
		TenantID:              "tenant-a",
		ABAEndpointID:         endpointA.ID,
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
	if _, err := persistence.CloseSessionForOwner(ctx, session.ID, "owner", "tenant-b", now.Add(time.Second)); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant close error = %v", err)
	}
	sessions, err := persistence.ListSessions(ctx, "owner", "tenant-a", 10)
	if err != nil || len(sessions) != 1 || sessions[0].Status != domain.SessionStatusCreating {
		t.Fatalf("cross-tenant close changed session: %#v err=%v", sessions, err)
	}
	if _, err := persistence.Delivery(ctx, session.ID, "owner", "tenant-b", 10); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant delivery error = %v", err)
	}

	overviewA, err := persistence.Overview(ctx, "owner", "tenant-a")
	if err != nil {
		t.Fatalf("Overview tenant-a: %v", err)
	}
	overviewB, err := persistence.Overview(ctx, "owner", "tenant-b")
	if err != nil {
		t.Fatalf("Overview tenant-b: %v", err)
	}
	if overviewA.ActiveEndpoints != 2 || overviewA.ActiveSessions != 1 || overviewB.ActiveEndpoints != 1 || overviewB.ActiveSessions != 0 {
		t.Fatalf("overview tenant-a=%#v tenant-b=%#v", overviewA, overviewB)
	}
}

func TestEnrollmentDecisionCannotRebindAnotherTenant(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	userCode := sha256.Sum256([]byte("TENANT-CODE"))
	enrollment := domain.Enrollment{
		ID:               tid(121),
		OwnerUserID:      "owner",
		TenantID:         "tenant-a",
		EndpointType:     domain.EndpointTypeABA,
		EndpointName:     "Tenant A ABA",
		DeviceCodeHash:   sha256.Sum256([]byte("device")),
		UserCodeHash:     userCode,
		SigningPublicJWK: jwk(121),
		KEMPublicJWK:     jwk(123),
		SigningJKT:       jkt(121),
		KEMJKT:           jkt(122),
		Status:           domain.EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := persistence.CreateEnrollment(ctx, enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	if _, err := persistence.ApproveEnrollmentWithCode(ctx, enrollment.ID, userCode, "owner", "tenant-b", now.Add(time.Second)); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-tenant approval error = %v", err)
	}
	values, err := persistence.ListEnrollments(ctx, "owner", "tenant-a", 10)
	if err != nil || len(values) != 1 || values[0].Status != domain.EnrollmentStatusPending {
		t.Fatalf("cross-tenant approval changed enrollment: %#v err=%v", values, err)
	}
}
