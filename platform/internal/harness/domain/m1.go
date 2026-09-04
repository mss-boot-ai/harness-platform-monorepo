package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxKeyPackageEncapsulatedKeyBytes = 512
	MaxKeyPackageCiphertextBytes      = 16 * 1024
	MaxIdempotencyResponseBytes       = 64 * 1024
	MaxAuditMetadataEntries           = 16
	MaxAuditMetadataValueBytes        = 256

	// CryptoSuiteMSSAWPSuite0001 is the only key-package suite accepted by AWP v1.
	CryptoSuiteMSSAWPSuite0001 uint16 = 1
)

var (
	auditTokenPattern      = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
	auditCodePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,127}$`)
	idempotencyKeyPattern  = regexp.MustCompile(`^[\x21-\x7e]{16,128}$`)
	forbiddenMetadataValue = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

type AuditActorType string

const (
	AuditActorHuman    AuditActorType = "HUMAN"
	AuditActorEndpoint AuditActorType = "ENDPOINT"
	AuditActorSystem   AuditActorType = "SYSTEM"
)

func (value AuditActorType) Valid() bool {
	switch value {
	case AuditActorHuman, AuditActorEndpoint, AuditActorSystem:
		return true
	default:
		return false
	}
}

type SecurityAuditEvent struct {
	ID          ID
	OwnerUserID string
	TenantID    string
	ActorType   AuditActorType
	ActorID     string
	Action      string
	ObjectType  string
	ObjectID    string
	Result      string
	ErrorCode   string
	Metadata    map[string]string
	CreatedAt   time.Time
}

func (event SecurityAuditEvent) Validate() error {
	if event.ID.IsZero() {
		return NewProblem(CodeInvalidArgument, "audit event ID is required", nil)
	}
	if strings.TrimSpace(event.OwnerUserID) == "" {
		return NewProblem(CodeInvalidArgument, "audit owner is required", nil)
	}
	if !event.ActorType.Valid() || !safeAuditValue(event.ActorID, 128) {
		return NewProblem(CodeInvalidArgument, "audit actor is invalid", nil)
	}
	for label, value := range map[string]string{
		"action":     event.Action,
		"objectType": event.ObjectType,
		"result":     event.Result,
	} {
		if !auditTokenPattern.MatchString(value) {
			return NewProblem(CodeInvalidArgument, "audit "+label+" is invalid", nil)
		}
	}
	if !safeAuditValue(event.ObjectID, 256) {
		return NewProblem(CodeInvalidArgument, "audit object ID is invalid", nil)
	}
	if event.ErrorCode != "" && !auditCodePattern.MatchString(event.ErrorCode) {
		return NewProblem(CodeInvalidArgument, "audit error code is invalid", nil)
	}
	if event.CreatedAt.IsZero() {
		return NewProblem(CodeInvalidArgument, "audit creation time is required", nil)
	}
	if len(event.Metadata) > MaxAuditMetadataEntries {
		return NewProblem(CodeResourceLimit, "audit metadata has too many entries", nil)
	}
	for key, value := range event.Metadata {
		if _, allowed := allowedAuditMetadataKeys[key]; !allowed {
			return NewProblem(CodeSecurityViolation, "audit metadata key is not allowed: "+key, nil)
		}
		if !safeAuditValue(value, MaxAuditMetadataValueBytes) {
			return NewProblem(CoeInvalidArgument, "audit metadata value is invalid: "+key, nil)
		}
	}
	return nil
}

func (event SecurityAuditEvent) CanonicalMetadataJSON() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(event.Metadata))
	for key := range event.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(keys))
	for _, key := range keys {
		ordered[key] = event.Metadata[key]
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return nil, fmt.Errorf("encode audit metadata: %w", err)
	}
	return encoded, nil
}

var allowedAuditMetadataKeys = map[string]struct{}{
	"currentStatus":   {},
	"decision":        {},
	"endpointType":    {},
	"httpStatus":      {},
	"keyGeneration":   {},
	"previousStatus":  {},
	"replayed":        {},
	"requestOutcome":  {},
	"resourceVersion": {},
	"sessionStatus":   {},
}

type IdempotencyStatus string

const (
	IdempotencyStatusInProgress IdempotencyStatus = "IN_PROGRESS"
	IdempotencyStatusCompleted  IdempotencyStatus = "COMPLETED"
)

func (value IdempotencyStatus) Valid() bool {
	return value == IdempotencyStatusInProgress || value == IdempotencyStatusCompleted
}

type IdempotencyRecord struct {
	ID          ID
	OwnerUserID  string
	TenantID    string
	ActorID      string
	Operation    string
	Key          string
	RequestHash  [32]byte
	Status       IdempotencyStatus
	HTTPStatus   int
	ResponseJSON []byte
	ErrorCode    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
	RowVersion   uint64
}

func (record IdempotencyRecord) Validate(maxResponseBytes int) error {
	if record.ID.IsZero() {
		return NewProblem(CodeInvalidArgument, "idempotency record ID is required", nil)
	}
	if strings.TrimSpace(record.OwnerUserID) == "" || !safeAuditValue(record.ActorID, 128) {
		return NewProblem(CodeInvalidArgument, "idempotency actor scope is invalid", nil)
	}
	if !auditTokenPattern.MatchString(record.Operation) {
		return NewProblem(CodeInvalidArgument, "idempotency operation is invalid", nil)
	}
	if err := ValidateIdempotencyKey(record.Key); err != nil {
		return err
	}
	if allZero(record.RequestHash[:]) || !record.Status.Valid() {
		return NewProblem(CodeInvalidArgument, "idempotency request hash or status is invalid", nil)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || !record.ExpiresAt.After(record.CreatedAt) {
		return NewProblem(CodeInvalidArgument, "idempotency timestamps are invalid", nil)
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = MaxIdempotencyResponseBytes
	}
	switch record.Status {
	case IdempotencyStatusInProgress:
		if record.HTTPStatus != 0 || len(record.ResponseJSON) != 0 || record.ErrorCode != "" {
			return NewProblem(CodeInvalidState, "in-progress idempotency record cannot contain a result", nil)
		}
	case IdempotencyStatusCompleted:
		if record.HTTPStatus < 100 || record.HTTPStatus > 599 || len(record.ResponseJSON) == 0 || len(record.ResponseJSON) > maxResponseBytes || !json.Valid(record.ResponseJSON) {
			return NewProblem(CodeInvalidArgument, "completed idempotency response is invalid", nil)
		}
		if record.ErrorCode != "" && !auditCodePattern.MatchString(record.ErrorCode) {
			return NewProblem(CodeInvalidArgument, "idempotency error code is invalid", nil)
		}
	}
	return nil
}

func ValidateIdempotencyKey(value string) error {
	if !idempotencyKeyPattern.MatchString(value) {
		return NewProblem(CoeInvalidArgument, "Idempotency-Key must be 16-128 printable ASCII characters", nil)
	}
	return nil
}

func (value KeyPackageStatus) Valid() bool {
	switch value {
	case KeyPackageStatusPending, KeyPackageStatusDelivered, KeyPackageStatusAcknowledged, KeyPackageStatusExpired, KeyPackageStatusRevoked:
		return true
	default:
		return false
	}
}

// ValidateKeyPackageCryptoSuite rejects every suite other than the frozen AWP v1 suite.
// Unknown non-zero values are not treated as forward-compatible input.
func ValidateKeyPackageCryptoSuite(value uint16) error {
	switch value {
	case 0:
		return NewProblem(CodeInvalidArgument, "key package crypto suite is required", nil)
	case CryptoSuiteMSSAWPSuite0001:
		return nil
	default:
		return NewProblem(CodeSecurityViolation, "key package crypto suite is unsupported", nil)
	}
}

// AllowsSessionKeyPackage is deliberately an allow-list: any future or corrupt
// endpoint state remains ineligible until an Accepted design explicitly opts in.
func (value EndpointStatus) AllowsSessionKeyPackage() bool {
	return value == EndpointStatusActive
}

// ValidateNextKeyPackageGeneration binds package issuance to the Accepted
// WAITING_KEY and REKEY_REQUIRED states and to the next exact generation.
func (value Session) ValidateNextKeyPackageGeneration(generation uint64) error {
	if generation == 0 {
        return NewProblem(CodeInvalidArgument, "key package generation is required", nil)
    }
	switch value.Status {
	case SessionStatusWaitingKey:
		if value.CurrentKeyGeneration != 0 || generation != 1 {
			return NewProblem(CodeInvalidState, "initial key package must use generation 1", nil)
		}
	case SessionStatusRekeyRequired:
		if value.CurrentKeyGeneration == 0 || value.CurrentKeyGeneration == ^uint64(0) || generation != value.CurrentKeyGeneration+1 {
			return NewProblem(CodeInvalidState, "rekey package must use the next generation", nil)
		}
	default:
		return NewProblem(CodeInvalidState, "session does not accept a key package in status "+string(value.Status), nil)
	}
	return nil
}

func (value SessionKeyPackage) Validate(maxEncapsulatedKeyBytes, maxCiphertextBytes int) error {
	if value.ID.IsZero() || value.SessionID.IsZero() || value.IssuerABAEndpointID.IsZero() || value.RecipientHCEndpointID.IsZero() || value.IssuerCredentialID.IsZero() {
		return NewProblem(CodeInvalidArgument, "key package identifiers must be non-zero", nil)
	}
	if value.IssuerABAEndpointID)== value.RecipientHCEndpointID {
        return NewProblem(CodeSecurityViolation, "key package issuer and recipient must be distinct", nil)
    }
	if value.Generation == 0 || !value.Status.Valid() {
		return NewProblem(CodeInvalidArgument, "key package generation or status is invalid", nil)
	}
	if err := ValidateKeyPackageCryptoSuite(value.CryptoSuite); err != nil {
		return err
	}
	if maxEncapsulatedKeyBytes <= 0 {
		maxEncapsulatedKeyBytes = MaxKeyPackageEncapsulatedKeyBytes
	}
	if maxCiphertextBytes <= 0 {
		maxCiphertextBytes = MaxKeyPackageCiphertextBytes
	}
	if len(value.EncapsulatedKey) == 0 || len(value.EncapsulatedKey) > maxEncapsulatedKeyBytes || len(value.Ciphertext) == 0 || len(value.Ciphertext) > maxCiphertextBytes {
		return NewProblem(CodeResourceLimit, "key package encrypted material is outside the allowed range", nil)
	}
	if allZero(value.ContextHash[:]) || len(value.IssuerSignature) != 64 || allZero(value.IssuerSignature) {
		return NewProblem(CodeInvalidArgument, "key package context hash or signature is invalid", nil)
	}
	if value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) {
		return NewProblem(CodeInvalidArgument, "key package timestamps are invalid", nil)
	}
	if value.Status == KeyPackageStatusAcknowledged && value.AcknowledgedAt == nil {
		return NewProblem(CodeInvalidState, "acknowledged key package requires acknowledged_at", nil)
	}
	if value.AcknowledgedAt != nil && value.AcknowledgedAt.Before(value.CreatedAt) {
		return NewProblem(CodeInvalidArgument, "key package acknowledgment precedes creation", nil)
	}
	return nil
}

func (value *SessionKeyPackage) Acknowledge(recipient ID, now time.Time) error {
	if value == nil {
		return NewProblem(CodeInvalidArgument, "key package is required", nil)
	}
	if recipient.IsZero() || recipient != value.RecipientHCEndpointID {
		return NewProblem(CodeSecurityViolation, "key package recipient does not match", nil)
	}
	if value.Status == KeyPackageStatusAcknowledged {
		return nil
	}
	if value.Status != KeyPackageStatusPending && value.Status != KeyPackageStatusDelivered {
		return NewProblem(CodeInvalidState, "key package cannot be acknowledged from "+string(value.Status), nil)
	}
	if now.IsZero() || !now.Before(value.ExpiresAt) {
		return NewProblem(CodeExpired, "key package is expired", nil)
	}
	value.Status = KeyPackageStatusAcknowledged
	value.AcknowledgedAt = &now
	return nil
}

func safeAuditValue(value string, maxBytes int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && len(value) <= maxBytes && !forbiddenMetadataValue.MatchString(value)
}

func FormatAuditUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}
