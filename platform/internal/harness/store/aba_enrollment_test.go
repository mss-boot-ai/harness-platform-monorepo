package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestABAEnrollmentPollAndConsumeAreDeviceBoundAndAtomic(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	deviceHash := sha256.Sum256([]byte("device-code"))
	userHash := sha256.Sum256([]byte("USERCODE"))
	enrollment := domain.Enrollment{
		ID: tid(130), EndpointType: domain.EndpointTypeABA, EndpointName: "Local ABA",
		DeviceCodeHash: deviceHash, UserCodeHash: userHash,
		SigningPublicJWK: jwk(140), KEMPublicJWK: jwk(142), SigningJKT: jkt(140), KEMJKT: jkt(141),
		Status: domain.EnrollmentStatusPending, ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateEnrollment(ctx, enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	if _, err := persistence.GetEnrollmentByDeviceCode(ctx, enrollment.ID, sha256.Sum256([]byte("wrong")), now); !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("wrong device code error=%v, want not found", err)
	}
	if got, err := persistence.GetEnrollmentByDeviceCode(ctx, enrollment.ID, deviceHash, now); err != nil || got.Status != domain.EnrollmentStatusPending {
		t.Fatalf("pending enrollment=%#v error=%v", got, err)
	}
	approved, err := persistence.ApproveEnrollmentWithCode(ctx, enrollment.ID, userHash, "owner", "tenant", now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApproveEnrollmentWithCode: %v", err)
	}
	aba := endpoint(131, 132, 140, domain.EndpointTypeABA, "owner", now.Add(2*time.Second))
	aba.TenantID = "tenant"
	access := credential(133, aba, now.Add(2*time.Second))
	refresh := testRefreshCredential(134, aba, now.Add(2*time.Second))
	audit := domain.SecurityAuditEvent{
		ID: tid(135), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorEndpoint,
		ActorID: aba.ID.String(), Action: "aba.endpoint.enroll", ObjectType: "endpoint",
		ObjectID: aba.ID.String(), Result: "success", Metadata: map[string]string{"endpointType": string(domain.EndpointTypeABA)},
		CreatedAt: now.Add(2 * time.Second),
	}
	if approved.Status != domain.EnrollmentStatusApproved {
		t.Fatalf("approved state=%s", approved.Status)
	}
	if err := persistence.ConsumeABAEnrollment(
		ctx, enrollment.ID, deviceHash, aba, access, refresh, audit, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("ConsumeABAEnrollment: %v", err)
	}
	if got, err := persistence.GetEnrollmentByDeviceCode(ctx, enrollment.ID, deviceHash, now.Add(3*time.Second)); err != nil ||
		got.Status != domain.EnrollmentStatusConsumed || got.EndpointID != aba.ID {
		t.Fatalf("consumed enrollment=%#v error=%v", got, err)
	}
	if err := persistence.ConsumeABAEnrollment(
		ctx, enrollment.ID, deviceHash, aba, access, refresh, audit, now.Add(3*time.Second),
	); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("repeated consume error=%v, want conflict", err)
	}
}
