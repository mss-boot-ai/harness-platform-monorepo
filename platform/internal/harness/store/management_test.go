package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestEnrollmentDecisionRequiresCodeAndOwner(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	userCode := sha256.Sum256([]byte("ABCD1234"))
	enrollment := domain.Enrollment{
		ID:               tid(41),
		EndpointType:     domain.EndpointTypeABA,
		EndpointName:     "ABA",
		DeviceCodeHash:   sha256.Sum256([]byte("device")),
		UserCodeHash:     userCode,
		SigningPublicJWK: jwk(41),
		KEMPublicJWK:     jwk(43),
		SigningJKT:       jkt(41),
		KEMJKT:           jkt(42),
		Status:           domain.EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := persistence.CreateEnrollment(ctx, enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	wrong := sha256.Sum256([]byte("WRONG"))
	if _, err := persistence.ApproveEnrollmentWithCode(ctx, enrollment.ID, wrong, "owner", "tenant", now.Add(time.Second)); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("wrong code error = %v", err)
	}
	approved, err := persistence.ApproveEnrollmentWithCode(ctx, enrollment.ID, userCode, "owner", "tenant", now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApproveEnrollmentWithCode: %v", err)
	}
	if approved.OwnerUserID != "owner" || approved.TenantID != "tenant" || approved.Status != domain.EnrollmentStatusApproved {
		t.Fatalf("approved enrollment = %#v", approved)
	}
	values, err := persistence.ListEnrollments(ctx, "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].ID != enrollment.ID {
		t.Fatalf("ListEnrollments = %#v err=%v", values, err)
	}
}

func TestManagementQueriesRespectOwnerAndExposeDeliveryMetadata(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(51, 52, 51, domain.EndpointTypeABA, "owner", now)
	aba.TenantID = "tenant"
	hc := endpoint(53, 54, 61, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	other := endpoint(55, 56, 71, domain.EndpointTypeABA, "other", now)
	other.TenantID = "other-tenant"
	for _, value := range []domain.Endpoint{aba, hc, other} {
		if err := persistence.CreateEndpoint(ctx, value); err != nil {
			t.Fatalf("CreateEndpoint(%s): %v", value.ID, err)
		}
	}
	endpoints, err := persistence.ListEndpoints(ctx, "owner", "tenant", 10)
	if err != nil || len(endpoints) != 2 {
		t.Fatalf("ListEndpoints = %#v err=%v", endpoints, err)
	}

	session := domain.Session{
		ID:                    tid(57),
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
	if _, err := persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
		if err := value.WaitForKey(now.Add(time.Second)); err != nil {
			return err
		}
		return value.Activate(1, now.Add(2*time.Second))
	}); err != nil {
		t.Fatalf("activate session: %v", err)
	}
	frame := testFrame(now)
	frame.MessageID = tid(58)
	frame.SessionID = session.ID
	frame.SenderEndpointID = hc.ID
	frame.ReceiverEndpointID = aba.ID
	if _, err := persistence.PutFrame(ctx, frame, 1024); err != nil {
		t.Fatalf("PutFrame: %v", err)
	}
	cursor := domain.AckCursor{
		SessionID:                 session.ID,
		KeyGeneration:             1,
		Direction:                 domain.DirectionHCToABA,
		SenderEndpointID:          hc.ID,
		ReceiverEndpointID:        aba.ID,
		HighestContiguousSequence: 1,
		UpdatedAt:                 now,
	}
	if _, err := persistence.AdvanceAck(ctx, cursor); err != nil {
		t.Fatalf("AdvanceAck: %v", err)
	}
	delivery, err := persistence.Delivery(ctx, session.ID, "owner", "tenant", 10)
	if err != nil || len(delivery.Frames) != 1 || len(delivery.ACKs) != 1 {
		t.Fatalf("Delivery = %#v err=%v", delivery, err)
	}
	if _, err := persistence.Delivery(ctx, session.ID, "other", "other-tenant", 10); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-owner delivery error = %v", err)
	}
	overview, err := persistence.Overview(ctx, "owner", "tenant")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if overview.ActiveEndpoints != 2 || overview.ActiveSessions != 1 || overview.Unacknowledged != 1 {
		t.Fatalf("Overview = %#v", overview)
	}
	closed, err := persistence.CloseSessionForOwner(ctx, session.ID, "owner", "tenant", now.Add(3*time.Second))
	if err != nil || closed.Status != domain.SessionStatusClosed {
		t.Fatalf("CloseSessionForOwner = %#v err=%v", closed, err)
	}
}

func TestEndpointOwnerUpdateUsesDomainStateMachine(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	value := endpoint(61, 62, 81, domain.EndpointTypeHCWeb, "owner", now)
	value.TenantID = "tenant"
	if err := persistence.CreateEndpoint(ctx, value); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	updated, err := persistence.UpdateEndpointForOwner(ctx, value.ID, "owner", "tenant", func(endpoint *domain.Endpoint) error {
		return endpoint.Suspend(now.Add(time.Second))
	})
	if err != nil || updated.Status != domain.EndpointStatusSuspended {
		t.Fatalf("suspend = %#v err=%v", updated, err)
	}
	if _, err := persistence.UpdateEndpointForOwner(ctx, value.ID, "other", "tenant", func(endpoint *domain.Endpoint) error {
		return endpoint.Resume(now.Add(2 * time.Second))
	}); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("cross-owner update error = %v", err)
	}
}
