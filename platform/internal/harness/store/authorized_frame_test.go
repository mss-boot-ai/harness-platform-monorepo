package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func seedAuthorizedFrame(t *testing.T, now time.Time) (*Store, domain.Endpoint, domain.Endpoint, domain.EndpointCredential, domain.Session, domain.EncryptedFrame) {
	t.Helper()
	persistence := newTestStore(t)
	aba := endpoint(171, 172, 173, domain.EndpointTypeABA, "owner", now)
	aba.TenantID = "tenant"
	hc := endpoint(174, 175, 176, domain.EndpointTypeHCWeb, "owner", now)
	hc.TenantID = "tenant"
	for _, value := range []domain.Endpoint{aba, hc} {
		if err := persistence.CreateEndpoint(t.Context(), value); err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
	}
	hcCredential := credential(177, hc, now)
	row, err := credentialToRow(hcCredential)
	if err != nil {
		t.Fatalf("credentialToRow: %v", err)
	}
	if err := persistence.db.Create(&row).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	session := domain.Session{
		ID: tid(178), OwnerUserID: "owner", TenantID: "tenant",
		ABAEndpointID: aba.ID, HCEndpointID: hc.ID,
		RuntimeProfileID: "test-agent", WorkspaceID: "fixture", RequestedCapabilities: []string{"prompt"},
		Status: domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateSession(t.Context(), session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	session, err = persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
		return value.WaitForKey(now.Add(time.Millisecond))
	})
	if err != nil {
		t.Fatalf("WaitForKey: %v", err)
	}
	session, err = persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
		return value.Activate(1, now.Add(2*time.Millisecond))
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	channelID, err := authorizedFrameChannelID(session.ID, aba.ID, hc.ID)
	if err != nil {
		t.Fatalf("authorizedFrameChannelID: %v", err)
	}
	frame := testFrame(now.Add(3 * time.Millisecond))
	frame.MessageID = tid(179)
	frame.SessionID = session.ID
	frame.ChannelID = channelID
	frame.SenderEndpointID = hc.ID
	frame.ReceiverEndpointID = aba.ID
	frame.Direction = domain.DirectionHCToABA
	frame.KeyGeneration = 1
	frame.Sequence = 1
	frame.KeyID = tid(180)
	frame.ContentHash = sha256.Sum256([]byte("authorized-frame"))
	frame.ReceivedAt = now.Add(3 * time.Millisecond)
	frame.ExpiresAt = now.Add(time.Hour)
	return persistence, aba, hc, hcCredential, session, frame
}

func TestPutAuthorizedEndpointFrameValidatesCurrentSecurityState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()

	t.Run("stores and replays exact frame", func(t *testing.T) {
		persistence, _, _, credential, _, frame := seedAuthorizedFrame(t, now)
		duplicate, err := persistence.PutAuthorizedEndpointFrame(t.Context(), "owner", "tenant", credential.ID, frame, frame.ReceivedAt)
		if err != nil || duplicate {
			t.Fatalf("first authorized frame duplicate=%v error=%v", duplicate, err)
		}
		duplicate, err = persistence.PutAuthorizedEndpointFrame(t.Context(), "owner", "tenant", credential.ID, frame, frame.ReceivedAt)
		if err != nil || !duplicate {
			t.Fatalf("duplicate authorized frame duplicate=%v error=%v", duplicate, err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, domain.Endpoint, domain.Endpoint, domain.EndpointCredential, domain.Session, *domain.EncryptedFrame)
		codes  []domain.ErrorCode
	}{
		{
			name: "wrong tenant",
			mutate: func(_ *testing.T, _ *Store, _ domain.Endpoint, _ domain.Endpoint, _ domain.EndpointCredential, _ domain.Session, _ *domain.EncryptedFrame) {
			},
			codes: []domain.ErrorCode{domain.CodeNotFound},
		},
		{
			name: "closed session",
			mutate: func(t *testing.T, persistence *Store, _ domain.Endpoint, _ domain.Endpoint, _ domain.EndpointCredential, session domain.Session, _ *domain.EncryptedFrame) {
				if _, err := persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
					return value.Close(now.Add(time.Second))
				}); err != nil {
					t.Fatalf("close session: %v", err)
				}
			},
			codes: []domain.ErrorCode{domain.CodeInvalidState},
		},
		{
			name: "suspended sender",
			mutate: func(t *testing.T, persistence *Store, _ domain.Endpoint, hc domain.Endpoint, _ domain.EndpointCredential, _ domain.Session, _ *domain.EncryptedFrame) {
				if err := persistence.db.Model(new(endpointRow)).Where("id = ?", hc.ID.String()).Update("status", string(domain.EndpointStatusSuspended)).Error; err != nil {
					t.Fatalf("suspend sender: %v", err)
				}
			},
			codes: []domain.ErrorCode{domain.CodeSecurityViolation},
		},
		{
			name: "revoked receiver",
			mutate: func(t *testing.T, persistence *Store, aba domain.Endpoint, _ domain.Endpoint, _ domain.EndpointCredential, _ domain.Session, _ *domain.EncryptedFrame) {
				when := now.Add(time.Second)
				if err := persistence.db.Model(new(endpointRow)).Where("id = ?", aba.ID.String()).Updates(map[string]any{
					"status": string(domain.EndpointStatusRevoked), "revoked_at": when,
				}).Error; err != nil {
					t.Fatalf("revoke receiver: %v", err)
				}
			},
			codes: []domain.ErrorCode{domain.CodeRevoked},
		},
		{
			name: "revoked credential",
			mutate: func(t *testing.T, persistence *Store, _ domain.Endpoint, _ domain.Endpoint, credential domain.EndpointCredential, _ domain.Session, _ *domain.EncryptedFrame) {
				when := now.Add(time.Second)
				if err := persistence.db.Model(new(credentialRow)).Where("id = ?", credential.ID.String()).Updates(map[string]any{
					"status": string(domain.CredentialStatusRevoked), "revoked_at": when,
				}).Error; err != nil {
					t.Fatalf("revoke credential: %v", err)
				}
			},
			codes: []domain.ErrorCode{domain.CodeSecurityViolation},
		},
		{
			name: "wrong generation",
			mutate: func(_ *testing.T, _ *Store, _ domain.Endpoint, _ domain.Endpoint, _ domain.EndpointCredential, _ domain.Session, frame *domain.EncryptedFrame) {
				frame.KeyGeneration = 2
			},
			codes: []domain.ErrorCode{domain.CodeInvalidState},
		},
		{
			name: "wrong route",
			mutate: func(_ *testing.T, _ *Store, _ domain.Endpoint, _ domain.Endpoint, _ domain.EndpointCredential, _ domain.Session, frame *domain.EncryptedFrame) {
				frame.Direction = domain.DirectionABAToHC
			},
			codes: []domain.ErrorCode{domain.CodeSecurityViolation},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			persistence, aba, hc, credential, session, frame := seedAuthorizedFrame(t, now)
			test.mutate(t, persistence, aba, hc, credential, session, &frame)
			tenant := "tenant"
			if test.name == "wrong tenant" {
				tenant = "other"
			}
			_, err := persistence.PutAuthorizedEndpointFrame(t.Context(), "owner", tenant, credential.ID, frame, frame.ReceivedAt)
			for _, code := range test.codes {
				if domain.HasCode(err, code) {
					return
				}
			}
			t.Fatalf("authorized frame error=%v, want one of %v", err, test.codes)
		})
	}
}

func TestAuthorizedFrameCannotCommitAfterEndpointRevocation(t *testing.T) {
	for attempt := 0; attempt < 8; attempt++ {
		t.Run(fmt.Sprintf("attempt-%02d", attempt), func(t *testing.T) {
			now := time.Unix(1_800_000_000+int64(attempt*10), 0).UTC()
			persistence, _, hc, credential, _, frame := seedAuthorizedFrame(t, now)
			start := make(chan struct{})
			frameResult := make(chan error, 1)
			revokeResult := make(chan error, 1)
			var group sync.WaitGroup
			group.Add(2)
			go func() {
				defer group.Done()
				<-start
				_, err := persistence.PutAuthorizedEndpointFrame(context.Background(), "owner", "tenant", credential.ID, frame, frame.ReceivedAt)
				frameResult <- err
			}()
			go func() {
				defer group.Done()
				<-start
				_, err := persistence.RevokeEndpointForOwner(context.Background(), hc.ID, "owner", "tenant", now.Add(time.Second))
				revokeResult <- err
			}()
			close(start)
			group.Wait()
			close(frameResult)
			close(revokeResult)

			if err := <-revokeResult; err != nil {
				if !domain.HasCode(err, domain.CodeConflict) {
					t.Fatalf("revoke error=%v", err)
				}
				if _, retryErr := persistence.RevokeEndpointForOwner(t.Context(), hc.ID, "owner", "tenant", now.Add(2*time.Second)); retryErr != nil {
					t.Fatalf("revoke retry: %v", retryErr)
				}
			}
			if err := <-frameResult; err != nil &&
				!domain.HasCode(err, domain.CodeConflict) && !domain.HasCode(err, domain.CodeRevoked) &&
				!domain.HasCode(err, domain.CodeInvalidState) && !domain.HasCode(err, domain.CodeSecurityViolation) {
				t.Fatalf("concurrent frame error=%v", err)
			}

			post := frame
			post.MessageID = tid(byte(181 + attempt))
			post.Sequence = 2
			post.ContentHash = sha256.Sum256([]byte(fmt.Sprintf("post-revoke-%d", attempt)))
			post.ReceivedAt = now.Add(3 * time.Second)
			if _, err := persistence.PutAuthorizedEndpointFrame(t.Context(), "owner", "tenant", credential.ID, post, post.ReceivedAt); err == nil {
				t.Fatal("frame committed after endpoint revocation")
			}
		})
	}
}
