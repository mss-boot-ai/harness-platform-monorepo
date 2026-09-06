package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func testID(value byte) ID {
	var id ID
	for index := range id {
		id[index] = value
	}
	return id
}

func testJKT(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func testJWK(value byte) json.RawMessage {
	return json.RawMessage(`{"kty":"EC","crv":"P-256","x":"` + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32)) + `","y":"` + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value + 1}, 32)) + `"}`)
}

func TestIDRoundTripAndRejectsZero(t *testing.T) {
	input := bytes.Repeat([]byte{0x42}, IDSize)
	id, err := NewID(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	parsed, err := ParseID(id.String())
	if err != nil {
		t.Fatalf("ParseID: %v", err)
	}
	if parsed != id {
		t.Fatalf("round trip mismatch: got %v want %v", parsed, id)
	}
	if _, err := ParseID("00000000000000000000000000000000"); err == nil {
		t.Fatal("all-zero ID was accepted")
	}
}

func TestEndpointValidationAndTerminalRevocation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	endpoint := Endpoint{
		ID:               testID(1),
		OwnerUserID:      "owner",
		Type:             EndpointTypeABA,
		Name:             "ABA",
		SigningPublicJWK: testJWK(1),
		KEMPublicJWK:     testJWK(3),
		SigningJKT:       testJKT(1),
		KEMJKT:           testJKT(2),
		Status:           EndpointStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := endpoint.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := endpoint.Activate(now.Add(time.Second)); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := endpoint.Suspend(now.Add(2 * time.Second)); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if err := endpoint.Resume(now.Add(3 * time.Second)); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := endpoint.Revoke(now.Add(4 * time.Second)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := endpoint.Resume(now.Add(5 * time.Second)); !HasCode(err, CodeRevoked) {
		t.Fatalf("resume revoked code = %v", err)
	}
	if endpoint.RowVersion != 4 {
		t.Fatalf("row version = %d, want 4", endpoint.RowVersion)
	}
}

func TestEndpointRejectsPrivateOrSharedKeys(t *testing.T) {
	endpoint := Endpoint{
		ID:               testID(1),
		OwnerUserID:      "owner",
		Type:             EndpointTypeABA,
		Name:             "ABA",
		SigningPublicJWK: json.RawMessage(`{"kty":"EC","crv":"P-256","x":"x","y":"y","d":"private"}`),
		KEMPublicJWK:     testJWK(3),
		SigningJKT:       testJKT(1),
		KEMJKT:           testJKT(2),
		Status:           EndpointStatusPending,
	}
	if err := endpoint.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("private JWK error = %v", err)
	}
	endpoint.SigningPublicJWK = testJWK(1)
	endpoint.KEMJKT = endpoint.SigningJKT
	if err := endpoint.Validate(); !HasCode(err, CodeSecurityViolation) {
		t.Fatalf("shared key error = %v", err)
	}
}

func TestEnrollmentApprovalConsumptionAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	device := sha256.Sum256([]byte("device"))
	user := sha256.Sum256([]byte("user"))
	enrollment := Enrollment{
		ID:               testID(1),
		OwnerUserID:      "owner",
		EndpointType:     EndpointTypeABA,
		EndpointName:     "ABA",
		DeviceCodeHash:   device,
		UserCodeHash:     user,
		SigningPublicJWK: testJWK(1),
		KEMPublicJWK:     testJWK(3),
		SigningJKT:       testJKT(1),
		KEMJKT:           testJKT(2),
		Status:           EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := enrollment.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := enrollment.Approve("owner", now.Add(time.Second)); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	endpointID := testID(9)
	if err := enrollment.Consume(endpointID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := enrollment.Consume(endpointID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("idempotent Consume: %v", err)
	}
	if err := enrollment.Consume(testID(8), now.Add(4*time.Second)); !HasCode(err, CodeConflict) {
		t.Fatalf("conflicting Consume: %v", err)
	}

	expired := enrollment
	expired.ID = testID(2)
	expired.Status = EnrollmentStatusPending
	expired.EndpointID = ID{}
	expired.ConsumedAt = nil
	expired.ExpiresAt = now
	if err := expired.Approve("owner", now); !HasCode(err, CodeExpired) {
		t.Fatalf("expired approval: %v", err)
	}
}

func TestSessionRequestRejectsUnsafeIDsAndCapabilities(t *testing.T) {
	valid := SessionRequest{
		ABAEndpointID:         testID(1),
		HCEndpointID:          testID(2),
		RuntimeProfileID:      "test-acp",
		WorkspaceID:           "mss-boot-admin",
		RequestedCapabilities: []string{"permission", "prompt"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if valid.RequestedCapabilities[0] != "permission" || valid.RequestedCapabilities[1] != "prompt" {
		t.Fatalf("capabilities not canonical: %#v", valid.RequestedCapabilities)
	}
	unsafe := valid
	unsafe.WorkspaceID = "../../etc"
	if err := unsafe.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("unsafe workspace error = %v", err)
	}
	unsupported := valid
	unsupported.RequestedCapabilities = []string{"shell"}
	if err := unsupported.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("unsupported capability error = %v", err)
	}
}

func TestSessionLifecycleRequiresMonotonicGeneration(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	session := Session{ID: testID(1), Status: SessionStatusCreating, CreatedAt: now, UpdatedAt: now}
	if err := session.WaitForKey(now.Add(time.Second)); err != nil {
		t.Fatalf("WaitForKey: %v", err)
	}
	if err := session.Activate(1, now.Add(2*time.Second)); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := session.RequireRekey(now.Add(3 * time.Second)); err != nil {
		t.Fatalf("RequireRekey: %v", err)
	}
	if err := session.Activate(1, now.Add(4*time.Second)); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("non-monotonic generation: %v", err)
	}
	if err := session.Activate(2, now.Add(5*time.Second)); err != nil {
		t.Fatalf("Activate generation 2: %v", err)
	}
	if err := session.MarkUncertain(now.Add(6 * time.Second)); err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}
	if err := session.Close(now.Add(7 * time.Second)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := session.MarkABARevoked(now.Add(8 * time.Second)); !HasCode(err, CodeInvalidState) {
		t.Fatalf("closed -> ABA revoked: %v", err)
	}
}

func TestFrameValidationAndTransitions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	contentHash := sha256.Sum256([]byte("frame"))
	frame := EncryptedFrame{
		MessageID:          testID(1),
		ChannelID:          testID(2),
		SessionID:          testID(3),
		SenderEndpointID:   testID(4),
		ReceiverEndpointID: testID(5),
		Direction:          DirectionHCToABA,
		Sequence:           1,
		KeyGeneration:      1,
		KeyID:              testID(6),
		CreatedAtMS:        now.UnixMilli(),
		AAD:                make([]byte, 148),
		Ciphertext:         []byte{1},
		Signature:          make([]byte, 64),
		ContentHash:        contentHash,
		Status:             FrameStatusStored,
		ReceivedAt:         now,
		ExpiresAt:          now.Add(time.Hour),
	}
	if err := frame.Validate(1024); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := frame.MarkRouted(now.Add(time.Second)); err != nil {
		t.Fatalf("MarkRouted: %v", err)
	}
	if err := frame.Acknowledge(now.Add(2 * time.Second)); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := frame.Conflict(); !HasCode(err, CodeInvalidState) {
		t.Fatalf("acked frame conflict: %v", err)
	}
	frame.AAD = make([]byte, 147)
	if err := frame.Validate(1024); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("short AAD error = %v", err)
	}
}
