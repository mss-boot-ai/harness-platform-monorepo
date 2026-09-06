package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestRefreshRotationReplacesBothCredentialsAndReuseRevokesFamily(t *testing.T) {
	persistence := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	challenge := testHCRegistrationChallenge(90, now)
	if err := persistence.CreateHCRegistrationChallenge(ctx, challenge, now, 5*time.Minute); err != nil {
		t.Fatalf("CreateHCRegistrationChallenge: %v", err)
	}
	endpoint := endpoint(91, 92, 100, domain.EndpointTypeHCWeb, "owner", now)
	endpoint.TenantID = "tenant"
	access := credential(93, endpoint, now)
	refresh := testRefreshCredential(94, endpoint, now)
	if err := persistence.ConsumeHCRegistrationChallenge(
		ctx, challenge.ID, challenge.ChallengeHash, "owner", "tenant", challenge.Origin,
		endpoint, access, refresh, testHCRegistrationAudit(95, endpoint, now), now,
	); err != nil {
		t.Fatalf("ConsumeHCRegistrationChallenge: %v", err)
	}
	if inspectedEndpoint, inspected, err := persistence.InspectRefreshCredential(ctx, refresh.TokenHash, now); err != nil ||
		inspectedEndpoint.ID != endpoint.ID || inspected.ID != refresh.ID {
		t.Fatalf("InspectRefreshCredential endpoint=%#v refresh=%#v error=%v", inspectedEndpoint, inspected, err)
	}

	nextTime := now.Add(time.Minute)
	nextAccess := credential(96, endpoint, nextTime)
	nextAccess.TokenHash = sha256.Sum256([]byte("next-access"))
	nextRefresh := testRefreshCredential(97, endpoint, nextTime)
	nextRefresh.TokenHash = sha256.Sum256([]byte("next-refresh"))
	nextRefresh.RotatedFrom = refresh.ID
	audit := domain.SecurityAuditEvent{
		ID: tid(98), OwnerUserID: "owner", TenantID: "tenant", ActorType: domain.AuditActorEndpoint,
		ActorID: endpoint.ID.String(), Action: "endpoint.token.refresh", ObjectType: "endpoint",
		ObjectID: endpoint.ID.String(), Result: "success", Metadata: map[string]string{}, CreatedAt: nextTime,
	}
	if err := persistence.RotateRefreshCredential(
		ctx, refresh.TokenHash, endpoint.SigningJKT, nextAccess, nextRefresh, audit, nextTime,
	); err != nil {
		t.Fatalf("RotateRefreshCredential: %v", err)
	}
	if _, _, err := persistence.AuthenticateAccessToken(ctx, access.TokenHash, nextTime); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("old access error=%v, want revoked", err)
	}
	if got, _, err := persistence.AuthenticateAccessToken(ctx, nextAccess.TokenHash, nextTime); err != nil || got.ID != endpoint.ID {
		t.Fatalf("new access endpoint=%#v error=%v", got, err)
	}
	if _, got, err := persistence.InspectRefreshCredential(ctx, nextRefresh.TokenHash, nextTime); err != nil || got.ID != nextRefresh.ID {
		t.Fatalf("new refresh=%#v error=%v", got, err)
	}

	reuseErr := persistence.RotateRefreshCredential(
		ctx, refresh.TokenHash, endpoint.SigningJKT, nextAccess, nextRefresh, audit, nextTime.Add(time.Second),
	)
	if !domain.HasCode(reuseErr, domain.CodeRevoked) {
		t.Fatalf("refresh reuse error=%v, want revoked", reuseErr)
	}
	if _, _, err := persistence.AuthenticateAccessToken(ctx, nextAccess.TokenHash, nextTime.Add(time.Second)); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("family access after reuse error=%v, want revoked", err)
	}
	if _, _, err := persistence.InspectRefreshCredential(ctx, nextRefresh.TokenHash, nextTime.Add(time.Second)); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("family refresh after reuse error=%v, want revoked", err)
	}
}
