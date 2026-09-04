package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		t.Name(),
	)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func tid(value byte) domain.ID {
	var id domain.ID
	for index := range id {
		id[index] = value
	}
	return id
}

func jkt(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func jwk(value byte) json.RawMessage {
	return json.RawMessage(`{"kty":"EC","crv":"P-256","x":"` +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32)) +
		`","y":"` +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value + 1}, 32)) +
		`"}`)
}

func endpoint(id, family, key byte, kind domain.EndpointType, owner string, now time.Time) domain.Endpoint {
	return domain.Endpoint{
		ID:                 tid(id),
		OwnerUserID:        owner,
		Type:               kind,
		Name:               string(kind),
		SigningPublicJWK:   jwk(key),
		KEMPublicJWK:       jwk(key + 2),
		SigningJKT:         jkt(key),
		KEMJKT:             jkt(key + 1),
		Status:             domain.EndpointStatusActive,
		CredentialFamilyID: tid(family),
		SoftwareVersion:    "0.1.0",
		PlatformName:       "test",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func credential(id byte, value domain.Endpoint, now time.Time) domain.EndpointCredential {
	return domain.EndpointCredential{
		ID:         tid(id),
		EndpointID: value.ID,
		FamilyID:   value.CredentialFamilyID,
		TokenHash:  sha256.Sum256([]byte{id}),
		SigningJKT: value.SigningJKT,
		Scopes:     []string{"endpoint:connect"},
		Status:     domain.CredentialStatusActive,
		ExpiresAt:  now.Add(time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestSchemaIsExplicitRepeatableAndComplete(t *testing.T) {
	store := newTestStore(t)
	if err := CreateSchema(store.db); err != nil {
		t.Fatalf("repeat CreateSchema: %v", err)
	}
	if err := VerifySchema(store.db); err != nil {
		t.Fatalf("VerifySchema: %v", err)
	}
}

func TestEnrollmentConsumptionIsAtomicAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	enrollment := domain.Enrollment{
		ID:               tid(1),
		EndpointType:     domain.EndpointTypeABA,
		EndpointName:     "ABA",
		DeviceCodeHash:   sha256.Sum256([]byte("device")),
		UserCodeHash:     sha256.Sum256([]byte("user")),
		SigningPublicJWK: jwk(10),
		KEMPublicJWK:     jwk(12),
		SigningJKT:       jkt(10),
		KEMJKT:           jkt(11),
		Status:           domain.EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.CreateEnrollment(ctx, enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	if _, err := store.ApproveEnrollment(ctx, enrollment.ID, "owner", now.Add(time.Second)); err != nil {
		t.Fatalf("ApproveEnrollment: %v", err)
	}
	aba := endpoint(2, 3, 10, domain.EndpointTypeABA, "owner", now.Add(2*time.Second))
	access := credential(4, aba, now.Add(2*time.Second))
	if err := store.ConsumeEnrollment(ctx, enrollment.ID, aba, access, now.Add(3*time.Second)); err != nil {
		t.Fatalf("ConsumeEnrollment: %v", err)
	}
	if err := store.ConsumeEnrollment(ctx, enrollment.ID, aba, access, now.Add(4*time.Second)); err != nil {
		t.Fatalf("idempotent ConsumeEnrollment: %v", err)
	}
	conflict := aba
	conflict.ID = tid(5)
	conflictingCredential := access
	conflictingCredential.ID = tid(6)
	conflictingCredential.EndpointID = conflict.ID
	if err := store.ConsumeEnrollment(
		ctx,
		enrollment.ID,
		conflict,
		conflictingCredential,
		now.Add(5*time.Second),
	); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("conflicting consume error = %v", err)
	}
	var endpoints int64
	var credentials int64
	if err := store.db.Model(&endpointRow{}).Count(&endpoints).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&credentialRow{}).Count(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if endpoints != 1 || credentials != 1 {
		t.Fatalf("atomic counts: endpoints=%d credentials=%d", endpoints, credentials)
	}
}

func TestTicketHasOneSuccessfulConsumer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	ticket := domain.WSTicket{
		ID:           tid(1),
		TokenHash:    sha256.Sum256([]byte("ticket")),
		EndpointID:   tid(2),
		CredentialID: tid(3),
		Purpose:      "aba-relay",
		Origin:       "app://aba",
		Protocol:     "mss.awp.v1",
		Status:       domain.TicketStatusIssued,
		ExpiresAt:    now.Add(30 * time.Second),
		CreatedAt:    now,
	}
	if err := store.CreateTicket(ctx, ticket, 30*time.Second); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := store.ConsumeTicket(
				ctx,
				ticket.TokenHash,
				ticket.EndpointID,
				ticket.CredentialID,
				ticket.Purpose,
				ticket.Origin,
				ticket.Protocol,
				now.Add(time.Second),
			)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("ticket outcomes: success=%d failure=%d", successes, failures)
	}
	if _, err := store.ConsumeTicket(
		ctx,
		ticket.TokenHash,
		ticket.EndpointID,
		ticket.CredentialID,
		ticket.Purpose,
		ticket.Origin,
		ticket.Protocol,
		now.Add(2*time.Second),
	); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("ticket replay error = %v", err)
	}
}

func testFrame(now time.Time) domain.EncryptedFrame {
	return domain.EncryptedFrame{
		MessageID:          tid(1),
		SessionID:          tid(2),
		ChannelID:          tid(3),
		KeyGeneration:      1,
		SenderEndpointID:   tid(4),
		ReceiverEndpointID: tid(5),
		Direction:          domain.DirectionHCToABA,
		Sequence:           1,
		KeyID:              tid(6),
		CreatedAtMS:        now.UnixMilli(),
		AAD:                bytes.Repeat([]byte{0x11}, 148),
		Ciphertext:         []byte{0xaa},
		Signature:          bytes.Repeat([]byte{0x22}, 64),
		ContentHash:        sha256.Sum256([]byte("frame")),
		Status:             domain.FrameStatusStored,
		ReceivedAt:         now,
		ExpiresAt:          now.Add(time.Hour),
	}
}

func TestFrameIdempotencyConflictAndMonotonicACK(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	frame := testFrame(now)
	if outcome, err := store.PutFrame(ctx, frame, 1024); err != nil || outcome != PutFrameStored {
		t.Fatalf("first PutFrame: outcome=%s err=%v", outcome, err)
	}
	if outcome, err := store.PutFrame(ctx, frame, 1024); err != nil || outcome != PutFrameDuplicate {
		t.Fatalf("duplicate PutFrame: outcome=%s err=%v", outcome, err)
	}
	different := frame
	different.Ciphertext = []byte{0xbb}
	different.ContentHash = sha256.Sum256([]byte("different"))
	if _, err := store.PutFrame(ctx, different, 1024); !domain.HasCode(err, domain.CodeConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	var row frameRow
	if err := store.db.First(&row, "message_id = ?", frame.MessageID.String()).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != string(domain.FrameStatusConflict) {
		t.Fatalf("frame status = %s", row.Status)
	}

	cursor := domain.AckCursor{
		SessionID:                 frame.SessionID,
		KeyGeneration:             1,
		Direction:                 frame.Direction,
		SenderEndpointID:          frame.SenderEndpointID,
		ReceiverEndpointID:        frame.ReceiverEndpointID,
		HighestContiguousSequence: 5,
		UpdatedAt:                 now,
	}
	current, err := store.AdvanceAck(ctx, cursor)
	if err != nil || current.HighestContiguousSequence != 5 {
		t.Fatalf("create ACK: %#v err=%v", current, err)
	}
	cursor.HighestContiguousSequence = 3
	current, err = store.AdvanceAck(ctx, cursor)
	if err != nil || current.HighestContiguousSequence != 5 {
		t.Fatalf("old ACK: %#v err=%v", current, err)
	}
	cursor.HighestContiguousSequence = 8
	cursor.UpdatedAt = now.Add(time.Second)
	current, err = store.AdvanceAck(ctx, cursor)
	if err != nil || current.HighestContiguousSequence != 8 {
		t.Fatalf("new ACK: %#v err=%v", current, err)
	}
}

func TestRevocationCascadesToCredentialTicketAndSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(1, 2, 10, domain.EndpointTypeABA, "owner", now)
	hc := endpoint(3, 4, 20, domain.EndpointTypeHCWeb, "owner", now)
	if err := store.CreateEndpoint(ctx, aba); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEndpoint(ctx, hc); err != nil {
		t.Fatal(err)
	}
	access := credential(5, aba, now)
	accessRow, err := credentialToRow(access)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&accessRow).Error; err != nil {
		t.Fatal(err)
	}
	session := domain.Session{
		ID:                    tid(6),
		OwnerUserID:           "owner",
		ABAEndpointID:         aba.ID,
		HCEndpointID:          hc.ID,
		RuntimeProfileID:      "test-agent",
		WorkspaceID:           "workspace",
		RequestedCapabilities: []string{"prompt"},
		Status:                domain.SessionStatusCreating,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
		if err := value.WaitForKey(now.Add(time.Second)); err != nil {
			return err
		}
		return value.Activate(1, now.Add(2*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	ticket := domain.WSTicket{
		ID:           tid(7),
		TokenHash:    sha256.Sum256([]byte("ticket")),
		EndpointID:   aba.ID,
		CredentialID: access.ID,
		Purpose:      "aba-relay",
		Origin:       "app://aba",
		Protocol:     "mss.awp.v1",
		Status:       domain.TicketStatusIssued,
		ExpiresAt:    now.Add(30 * time.Second),
		CreatedAt:    now,
	}
	if err := store.CreateTicket(ctx, ticket, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeEndpoint(ctx, aba.ID, "owner", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.SessionStatusABARevoked {
		t.Fatalf("session status = %s", stored.Status)
	}
	var credentialState credentialRow
	if err := store.db.First(&credentialState, "id = ?", access.ID.String()).Error; err != nil {
		t.Fatal(err)
	}
	if credentialState.Status != string(domain.CredentialStatusRevoked) {
		t.Fatalf("credential status = %s", credentialState.Status)
	}
	if _, err := store.ConsumeTicket(
		ctx,
		ticket.TokenHash,
		ticket.EndpointID,
		ticket.CredentialID,
		ticket.Purpose,
		ticket.Origin,
		ticket.Protocol,
		now.Add(4*time.Second),
	); !domain.HasCode(err, domain.CodeRevoked) {
		t.Fatalf("revoked ticket error = %v", err)
	}
}
