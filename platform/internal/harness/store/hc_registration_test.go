package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestHCRegistrationConsumptionIsAtomicAndSingleUse(t *testing.T) {
	persistence := newTestStore(t)
	if err := CreateAllSchema(persistence.db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	challenge := testHCRegistrationChallenge(1, now)
	if err := persistence.CreateHCRegistrationChallenge(ctx, challenge, now, 5*time.Minute); err != nil {
		t.Fatalf("CreateHCRegistrationChallenge: %v", err)
	}
	hc := endpoint(2, 3, 10, domain.EndpointTypeHCWeb, "owner", now.Add(time.Second))
	hc.TenantID = "tenant"
	access := credential(4, hc, now.Add(time.Second))
	refresh := testRefreshCredential(5, hc, now.Add(time.Second))
	audit := testHCRegistrationAudit(6, hc, now.Add(time.Second))

	err := persistence.ConsumeHCRegistrationChallenge(
		ctx,
		challenge.ID,
		challenge.ChallengeHash,
		"owner",
		"tenant",
		challenge.Origin,
		hc,
		access,
		refresh,
		audit,
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("ConsumeHCRegistrationChallenge: %v", err)
	}

	for name, model := range map[string]any{
		"endpoint": &endpointRow{}, "access": &credentialRow{}, "refresh": &refreshCredentialRow{}, "audit": &auditRow{},
	} {
		var count int64
		if err := persistence.db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", name, count)
		}
	}
	var stored hcRegistrationChallengeRow
	if err := persistence.db.First(&stored, "id = ?", challenge.ID.String()).Error; err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	if stored.Status != string(domain.HCRegistrationChallengeConsumed) || stored.ConsumedAt == nil {
		t.Fatalf("unexpected challenge state: %#v", stored)
	}

	err = persistence.ConsumeHCRegistrationChallenge(
		ctx, challenge.ID, challenge.ChallengeHash, "owner", "tenant", challenge.Origin,
		hc, access, refresh, audit, now.Add(2*time.Second),
	)
	if !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("repeated consume error = %v, want conflict", err)
	}
}

func TestHCRegistrationScopeFailureCreatesNothing(t *testing.T) {
	persistence := newTestStore(t)
	if err := CreateAllSchema(persistence.db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	challenge := testHCRegistrationChallenge(20, now)
	if err := persistence.CreateHCRegistrationChallenge(ctx, challenge, now, 5*time.Minute); err != nil {
		t.Fatalf("CreateHCRegistrationChallenge: %v", err)
	}
	hc := endpoint(21, 22, 30, domain.EndpointTypeHCWeb, "owner", now.Add(time.Second))
	hc.TenantID = "tenant"
	err := persistence.ConsumeHCRegistrationChallenge(
		ctx,
		challenge.ID,
		challenge.ChallengeHash,
		"owner",
		"tenant",
		"https://other.example",
		hc,
		credential(23, hc, now.Add(time.Second)),
		testRefreshCredential(24, hc, now.Add(time.Second)),
		testHCRegistrationAudit(25, hc, now.Add(time.Second)),
		now.Add(time.Second),
	)
	if !domain.HasCode(err, domain.CodeNotFound) {
		t.Fatalf("scope failure error = %v, want not found", err)
	}
	for name, model := range map[string]any{
		"endpoint": &endpointRow{}, "access": &credentialRow{}, "refresh": &refreshCredentialRow{}, "audit": &auditRow{},
	} {
		var count int64
		if err := persistence.db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", name, count)
		}
	}
}

func TestHCRegistrationExpiredChallengeIsRejected(t *testing.T) {
	persistence := newTestStore(t)
	if err := CreateAllSchema(persistence.db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	challenge := testHCRegistrationChallenge(40, now)
	if err := persistence.CreateHCRegistrationChallenge(ctx, challenge, now, 5*time.Minute); err != nil {
		t.Fatalf("CreateHCRegistrationChallenge: %v", err)
	}
	hc := endpoint(41, 42, 50, domain.EndpointTypeHCWeb, "owner", challenge.ExpiresAt)
	hc.TenantID = "tenant"
	err := persistence.ConsumeHCRegistrationChallenge(
		ctx,
		challenge.ID,
		challenge.ChallengeHash,
		"owner",
		"tenant",
		challenge.Origin,
		hc,
		credential(43, hc, challenge.ExpiresAt),
		testRefreshCredential(44, hc, challenge.ExpiresAt),
		testHCRegistrationAudit(45, hc, challenge.ExpiresAt),
		challenge.ExpiresAt,
	)
	if !domain.HasCode(err, domain.CodeExpired) {
		t.Fatalf("expired challenge error = %v, want expired", err)
	}
}

func TestM2IdentitySchemaIsRepeatable(t *testing.T) {
	persistence := newTestStore(t)
	if err := CreateM2IdentitySchema(persistence.db); err != nil {
		t.Fatalf("CreateM2IdentitySchema: %v", err)
	}
	if err := CreateM2IdentitySchema(persistence.db); err != nil {
		t.Fatalf("repeat CreateM2IdentitySchema: %v", err)
	}
	if err := VerifyM2IdentitySchema(persistence.db); err != nil {
		t.Fatalf("VerifyM2IdentitySchema: %v", err)
	}
}

func TestHCReauthenticationIsScopedAndCannotRaceRevocation(t *testing.T) {
	persistence := newTestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	hc := endpoint(102, 103, 110, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	consume := func(challengeID, accessID byte) error {
		challenge := testHCRegistrationChallenge(challengeID, now)
		if err := persistence.CreateHCRegistrationChallenge(t.Context(), challenge, now, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		return persistence.ConsumeHCRegistrationChallenge(t.Context(), challenge.ID, challenge.ChallengeHash, "owner", "tenant", challenge.Origin,
			hc, credential(accessID, hc, now), testRefreshCredential(accessID+1, hc, now), testHCRegistrationAudit(accessID+2, hc, now), now)
	}
	if err := consume(100, 120); err != nil {
		t.Fatal(err)
	}
	existing, found, err := persistence.FindHCRegistrationEndpoint(t.Context(), "owner", "tenant", hc.SigningJKT, hc.KEMJKT)
	if err != nil || !found || existing.ID != hc.ID {
		t.Fatalf("identity lookup: %v %v", found, err)
	}
	if _, found, err := persistence.FindHCRegistrationEndpoint(t.Context(), "owner", "other", hc.SigningJKT, hc.KEMJKT); err != nil || found {
		t.Fatal("cross-tenant identity was visible")
	}
	if _, _, err := persistence.FindHCRegistrationEndpoint(t.Context(), "owner", "tenant", hc.SigningJKT, "different"); !domain.HasCode(err, domain.CodeSecurityViolation) {
		t.Fatal("partial key replacement accepted")
	}
	if err := consume(101, 130); err != nil {
		t.Fatal(err)
	}
	var count int64
	persistence.db.Model(new(endpointRow)).Count(&count)
	if count != 1 {
		t.Fatalf("endpoint duplicated: %d", count)
	}
	if err := persistence.db.Model(new(endpointRow)).Where("id = ?", hc.ID.String()).Update("status", string(domain.EndpointStatusRevoked)).Error; err != nil {
		t.Fatal(err)
	}
	if err := consume(104, 140); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("revocation race accepted: %v", err)
	}
	persistence.db.Model(new(credentialRow)).Count(&count)
	if count != 2 {
		t.Fatalf("failed reauthentication issued credentials: %d", count)
	}
	var challenge hcRegistrationChallengeRow
	if err := persistence.db.First(&challenge, "id = ?", tid(104).String()).Error; err != nil {
		t.Fatal(err)
	}
	if challenge.Status != string(domain.HCRegistrationChallengePending) {
		t.Fatal("failed reauthentication consumed its challenge")
	}
}

func testHCRegistrationChallenge(id byte, now time.Time) domain.HCRegistrationChallenge {
	return domain.HCRegistrationChallenge{
		ID: tid(id), OwnerUserID: "owner", TenantID: "tenant", Origin: "https://hc.example",
		ChallengeHash: sha256.Sum256([]byte{id, 1}), Status: domain.HCRegistrationChallengePending,
		ExpiresAt: now.Add(2 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
}

func testRefreshCredential(id byte, hc domain.Endpoint, now time.Time) domain.RefreshCredential {
	return domain.RefreshCredential{
		ID: tid(id), EndpointID: hc.ID, FamilyID: hc.CredentialFamilyID,
		TokenHash: sha256.Sum256([]byte{id, 2}), SigningJKT: hc.SigningJKT,
		Status: domain.CredentialStatusActive, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	}
}

func testHCRegistrationAudit(id byte, hc domain.Endpoint, now time.Time) domain.SecurityAuditEvent {
	return domain.SecurityAuditEvent{
		ID: tid(id), OwnerUserID: hc.OwnerUserID, TenantID: hc.TenantID,
		ActorType: domain.AuditActorHuman, ActorID: hc.OwnerUserID, Action: "hc.endpoint.register",
		ObjectType: "endpoint", ObjectID: hc.ID.String(), Result: "success",
		Metadata: map[string]string{"endpointType": string(hc.Type)}, CreatedAt: now,
	}
}
