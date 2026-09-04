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
	MaxAuditMetadataEntries    = 16
	MaxAuditMetadataValueBytes = 256
)

var (
	auditTokenPattern      = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
	auditCodePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,127}$`)
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
			return NewProblem(CodeInvalidArgument, "audit metadata value is invalid: "+key, nil)
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

func safeAuditValue(value string, maxBytes int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && len(value) <= maxBytes && !forbiddenMetadataValue.MatchString(value)
}

func FormatAuditUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}
