package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const MaxIdempotencyResponseBytes = 64 * 1024

var idempotencyKeyPattern = regexp.MustCompile(`^[\x21-\x7e]{16,128}$`)

type IdempotencyStatus string

const (
	IdempotencyStatusInProgress IdempotencyStatus = "IN_PROGRESS"
	IdempotencyStatusCompleted  IdempotencyStatus = "COMPLETED"
)

func (value IdempotencyStatus) Valid() bool {
	return value == IdempotencyStatusInProgress || value == IdempotencyStatusCompleted
}

type IdempotencyRecord struct {
	ID           ID
	OwnerUserID  string
	TenantID     string
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
		return NewProblem(CodeInvalidArgument, "Idempotency-Key must be 16-128 printable ASCII characters", nil)
	}
	return nil
}
