package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"
)

func TestSessionKeyPackageValidationAndAcknowledgement(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	value := SessionKeyPackage{
		ID:                    testID(1),
		SessionID:             testID(2),
		Generation:            1,
		IssuerABAEndpointID:   testID(3),
		RecipientHCEndpointID: testID(4),
		CryptoSuite:           1,
		EncapsulatedKey:       []byte{1, 2, 3},
		Ciphertext:            []byte{4, 5, 6},
		ContextHash:           sha256.Sum256([]byte("context")),
		IssuerSignature:       bytes.Repeat([]byte{7}, 64),
		IssuerCredentialID:    testID(5),
		Status:                KeyPackageStatusPending,
		ExpiresAt:             now.Add(time.Minute),
		CreatedAt:             now,
	}
	if err := value.Validate(0, 0); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := value.Acknowledge(testID(9), now.Add(time.Second)); !HasCode(err, CodeSecurityViolation) {
		t.Fatalf("wrong recipient error = %v", err)
	}
	if err := value.Acknowledge(value.RecipientHCEndpointID, now.Add(time.Second)); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if value.Status != KeyPackageStatusAcknowledged || value.AcknowledgedAt == nil {
		t.Fatalf("acknowledged value = %#v", value)
	}
	if err := value.Validate(0, 0); err != nil {
		t.Fatalf("Validate acknowledged: %v", err)
	}
}

func TestSessionKeyPackageRejectsOversizedOrPlaintextLikeMaterial(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	value := SessionKeyPackage{
		ID:                    testID(1),
		SessionID:             testID(2),
		Generation:            1,
		IssuerABAEndpointID:   testID(3),
		RecipientHCEndpointID: testID(4),
		CryptoSuite:           1,
		EncapsulatedKey:       bytes.Repeat([]byte{1}, 513),
		Ciphertext:            []byte{2},
		ContextHash:           sha256.Sum256([]byte("context")),
		IssuerSignature:       bytes.Repeat([]byte{3}, 64),
		IssuerCredentialID:    testID(5),
		Status:                KeyPackageStatusPending,
		ExpiresAt:             now.Add(time.Minute),
		CreatedAt:             now,
	}
	if err := value.Validate(0, 0); !HasCode(err, CodeResourceLimit) {
		t.Fatalf("oversized material error = %v", err)
	}
	value.EncapsulatedKey = []byte{1}
	value.Ciphertext = nil
	if err := value.Validate(0, 0); !HasCode(err, CodeResourceLimit) {
		t.Fatalf("empty ciphertext error = %v", err)
	}
}

func TestSecurityAuditEventOnlyAllowsSafeMetadata(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	event := SecurityAuditEvent{
		ID:          testID(1),
		OwnerUserID: "owner",
		TenantID:    "tenant",
		ActorType:   AuditActorHuman,
		ActorID:     "owner",
		Action:      "endpoint.revoke",
		ObjectType:  "endpoint",
		ObjectID:    testID(2).String(),
		Result:      "success",
		Metadata: map[string]string{
			"previousStatus": "ACTIVE",
			"currentStatus":  "REVOKED",
			"endpointType":   "HC_WEB",
		},
		CreatedAt: now,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	encoded, err := event.CanonicalMetadataJSON()
	if err != nil {
		t.Fatalf("CanonicalMetadataJSON: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded) != 3 {
		t.Fatalf("metadata = %#v err=%v", decoded, err)
	}

	unsafe := event
	unsafe.ID = testID(3)
	unsafe.Metadata = map[string]string{"token": "secret"}
	if err := unsafe.Validate(); !HasCode(err, CodeSecurityViolation) {
		t.Fatalf("unsafe metadata error = %v", err)
	}

	control := event
	control.ID = testID(4)
	control.Metadata = map[string]string{"decision": "allow\x00secret"}
	if err := control.Validate(); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("control metadata error = %v", err)
	}
}

func TestIdempotencyRecordValidation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	record := IdempotencyRecord{
		ID:          testID(1),
		OwnerUserID: "owner",
		TenantID:    "tenant",
		ActorID:     "owner",
		Operation:   "enrollment.approve",
		Key:         "request-key-0000000001",
		RequestHash: sha256.Sum256([]byte("request")),
		Status:      IdempotencyStatusInProgress,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	if err := record.Validate(0); err != nil {
		t.Fatalf("Validate in progress: %v", err)
	}
	record.Status = IdempotencyStatusCompleted
	record.HTTPStatus = 200
	record.ResponseJSON = []byte(`{"status":"APPROVED"}`)
	record.UpdatedAt = now.Add(time.Second)
	if err := record.Validate(0); err != nil {
		t.Fatalf("Validate completed: %v", err)
	}
	record.ResponseJSON = []byte("not-json")
	if err := record.Validate(0); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("invalid response error = %v", err)
	}
	if err := ValidateIdempotencyKey("short"); !HasCode(err, CodeInvalidArgument) {
		t.Fatalf("short key error = %v", err)
	}
}
