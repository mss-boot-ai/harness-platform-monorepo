package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func requireKeyPackageProblem(t *testing.T, err error, codes ...domain.ErrorCode) {
	t.Helper()
	for _, code := range codes {
		if domain.HasCode(err, code) {
			return
		}
	}
	t.Fatalf("key package error = %v, want one of %v", err, codes)
}

func TestPutSessionKeyPackageRejectsInactiveEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		issuer     bool
		status     domain.EndpointStatus
		expectCode domain.ErrorCode
	}{
		{name: "revoked HC", status: domain.EndpointStatusRevoked, expectCode: domain.CodeRevoked},
		{name: "suspended HC", status: domain.EndpointStatusSuspended, expectCode: domain.CodeSecurityViolation},
		{name: "expired HC", status: domain.EndpointStatus("EXPIRED"), expectCode: domain.CodeSecurityViolation},
		{name: "compromised HC", status: domain.EndpointStatus("COMPROMISED"), expectCode: domain.CodeSecurityViolation},
		{name: "suspended ABA", issuer: true, status: domain.EndpointStatusSuspended, expectCode: domain.CodeSecurityViolation},
		{name: "revoked ABA", issuer: true, status: domain.EndpointStatusRevoked, expectCode: domain.CodeRevoked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence := newM1TestStore(t)
			now := time.Unix(1_800_000_000, 0).UTC()
			aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
			id := hc.ID
			if test.issuer {
				id = aba.ID
			}
			updates := map[string]any{"status": string(test.status), "row_version": 1}
			if test.status == domain.EndpointStatusRevoked {
				updates["revoked_at"] = now
			}
			if err := persistence.db.Model(new(endpointRow)).Where("id = ?", id.String()).Updates(updates).Error; err != nil {
				t.Fatalf("set endpoint status: %v", err)
			}
			_, err := persistence.PutSessionKeyPackage(
				context.Background(), "owner", "tenant",
				testKeyPackage(now, aba, hc, issuerCredential, session),
			)
			requireKeyPackageProblem(t, err, test.expectCode)
		})
	}
}

func TestPutSessionKeyPackageRejectsTerminalOrIneligibleSessions(t *testing.T) {
	for _, status := range []domain.SessionStatus{
		domain.SessionStatusCreating,
		domain.SessionStatusActive,
		domain.SessionStatusClosed,
		domain.SessionStatusABARevoked,
		domain.SessionStatusFailed,
		domain.SessionStatusDraining,
		domain.SessionStatusUncertain,
	} {
		t.Run(string(status), func(t *testing.T) {
			persistence := newM1TestStore(t)
			now := time.Unix(1_800_000_000, 0).UTC()
			aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
			updates := map[string]any{"status": string(status), "row_version": 2}
			if status == domain.SessionStatusClosed || status == domain.SessionStatusABARevoked {
				updates["closed_at"] = now
			}
			if err := persistence.db.Model(new(sessionRow)).Where("id = ?", session.ID.String()).Updates(updates).Error; err != nil {
				t.Fatalf("set session status: %v", err)
			}
			_, err := persistence.PutSessionKeyPackage(
				context.Background(), "owner", "tenant",
				testKeyPackage(now, aba, hc, issuerCredential, session),
			)
			requireKeyPackageProblem(t, err, domain.CodeInvalidState)
		})
	}
}

func TestPutSessionKeyPackageRejectsWrongTenantAndInvalidCredential(t *testing.T) {
	t.Run("wrong tenant", func(t *testing.T) {
		persistence := newM1TestStore(t)
		now := time.Unix(1_800_000_000, 0).UTC()
		aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
		_, err := persistence.PutSessionKeyPackage(
			context.Background(), "owner", "other-tenant",
			testKeyPackage(now, aba, hc, issuerCredential, session),
		)
		requireKeyPackageProblem(t, err, domain.CodeNotFound)
	})

	tests := []struct {
		name   string
		update map[string]any
	}{
		{name: "revoked", update: map[string]any{"status": string(domain.CredentialStatusRevoked), "revoked_at": time.Unix(1_800_000_001, 0).UTC()}},
		{name: "expired", update: map[string]any{"status": string(domain.CredentialStatusExpired)}},
		{name: "past expiry", update: map[string]any{"expires_at": time.Unix(1_799_999_999, 0).UTC()}},
		{name: "wrong family", update: map[string]any{"family_id": tid(99).String()}},
		{name: "wrong signing key", update: map[string]any{"signing_jkt": jkt(99)}},
		{name: "not yet issued", update: map[string]any{"created_at": time.Unix(1_800_000_001, 0).UTC()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence := newM1TestStore(t)
			now := time.Unix(1_800_000_000, 0).UTC()
			aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
			if err := persistence.db.Model(new(credentialRow)).Where("id = ?", issuerCredential.ID.String()).Updates(test.update).Error; err != nil {
				t.Fatalf("update credential: %v", err)
			}
			_, err := persistence.PutSessionKeyPackage(
				context.Background(), "owner", "tenant",
				testKeyPackage(now, aba, hc, issuerCredential, session),
			)
			requireKeyPackageProblem(t, err, domain.CodeSecurityViolation)
		})
	}
}

func TestPutSessionKeyPackageRejectsUnknownSuiteBeforePersistence(t *testing.T) {
	persistence := newM1TestStore(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
	value := testKeyPackage(now, aba, hc, issuerCredential, session)
	value.CryptoSuite = 2
	_, err := persistence.PutSessionKeyPackage(context.Background(), "owner", "tenant", value)
	requireKeyPackageProblem(t, err, domain.CodeSecurityViolation)
	var count int64
	if err := persistence.db.Model(new(keyPackageRow)).Count(&count).Error; err != nil {
		t.Fatalf("count key packages: %v", err)
	}
	if count != 0 {
		t.Fatalf("unknown suite persisted %d rows", count)
	}
}

func TestConcurrentHCRevocationAndKeyPackageWriteLeavesNoUsablePackage(t *testing.T) {
	for attempt := 0; attempt < 8; attempt++ {
		t.Run(fmt.Sprintf("attempt-%02d", attempt), func(t *testing.T) {
			persistence := newM1TestStore(t)
			ctx := context.Background()
			now := time.Unix(1_800_000_000+int64(attempt*10), 0).UTC()
			aba, hc, issuerCredential, session := seedKeyPackageSession(t, persistence, now)
			value := testKeyPackage(now, aba, hc, issuerCredential, session)

			start := make(chan struct{})
			putResult := make(chan error, 1)
			revokeResult := make(chan error, 1)
			var group sync.WaitGroup
			group.Add(2)
			go func() {
				defer group.Done()
				<-start
				_, err := persistence.PutSessionKeyPackage(ctx, "owner", "tenant", value)
				putResult <- err
			}()
			go func() {
				defer group.Done()
				<-start
				_, err := persistence.RevokeEndpointForOwner(ctx, hc.ID, "owner", "tenant", now.Add(time.Second))
				revokeResult <- err
			}()
			close(start)
			group.Wait()
			close(putResult)
			close(revokeResult)

			if err := <-revokeResult; err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "locked") {
					t.Fatalf("concurrent revoke leaked database lock error: %v", err)
				}
				requireKeyPackageProblem(t, err, domain.CodeConflict)
				if _, retryErr := persistence.RevokeEndpointForOwner(
					ctx, hc.ID, "owner", "tenant", now.Add(2*time.Second),
				); retryErr != nil {
					t.Fatalf("RevokeEndpointForOwner retry: %v", retryErr)
				}
			}
			if err := <-putResult; err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "locked") {
					t.Fatalf("concurrent write leaked database lock error: %v", err)
				}
				requireKeyPackageProblem(
					t, err,
					domain.CodeRevoked,
					domain.CodeSecurityViolation,
					domain.CodeInvalidState,
					domain.CodeConflict,
				)
			}

			var usable int64
			if err := persistence.db.Model(new(keyPackageRow)).Where(
				"session_id = ? AND status IN ?",
				session.ID.String(), []string{
					string(domain.KeyPackageStatusPending),
					string(domain.KeyPackageStatusDelivered),
					string(domain.KeyPackageStatusAcknowledged),
				},
			).Count(&usable).Error; err != nil {
				t.Fatalf("count usable key packages: %v", err)
			}
			if usable != 0 {
				t.Fatalf("revocation left %d usable key packages", usable)
			}
			var endpoint endpointRow
			if err := persistence.db.First(&endpoint, "id = ?", hc.ID.String()).Error; err != nil {
				t.Fatalf("read revoked endpoint: %v", err)
			}
			if endpoint.Status != string(domain.EndpointStatusRevoked) {
				t.Fatalf("endpoint status = %s", endpoint.Status)
			}
		})
	}
}
