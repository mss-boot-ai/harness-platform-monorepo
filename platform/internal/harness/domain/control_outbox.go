package domain

import (
	"strings"
	"time"
)

type ControlOutboxKind string

const (
	ControlOutboxOpenTunnel           ControlOutboxKind = "OPEN_TUNNEL"
	ControlOutboxSessionKeyPackage    ControlOutboxKind = "SESSION_KEY_PACKAGE"
	ControlOutboxSessionKeyPackageACK ControlOutboxKind = "SESSION_KEY_PACKAGE_ACK"
	ControlOutboxCloseTunnel          ControlOutboxKind = "CLOSE_TUNNEL"
)

func (kind ControlOutboxKind) Valid() bool {
	switch kind {
	case ControlOutboxOpenTunnel, ControlOutboxSessionKeyPackage, ControlOutboxSessionKeyPackageACK, ControlOutboxCloseTunnel:
		return true
	default:
		return false
	}
}

type ControlOutboxStatus string

const (
	ControlOutboxPending   ControlOutboxStatus = "PENDING"
	ControlOutboxCompleted ControlOutboxStatus = "COMPLETED"
	ControlOutboxCancelled ControlOutboxStatus = "CANCELLED"
	ControlOutboxExpired   ControlOutboxStatus = "EXPIRED"
)

// ControlOutbox stores the minimum durable material needed to redeliver a
// control-plane operation. Platform-originated controls persist a semantic
// protobuf payload and are re-signed for the current connection generation.
// Endpoint-originated SessionKeyPackage controls persist the exact signed wire
// packet so replay never changes endpoint-authenticated bytes.
type ControlOutbox struct {
	ID                  ID
	OwnerUserID         string
	TenantID            string
	SessionID           ID
	CorrelationID       ID
	RecipientEndpointID ID
	Kind                ControlOutboxKind
	Payload             []byte
	Packet              []byte
	Status              ControlOutboxStatus
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ExpiresAt           time.Time
	CompletedAt         *time.Time
	RowVersion          uint64
}

func (value ControlOutbox) ValidateNew(maxPayloadBytes, maxPacketBytes int) error {
	if value.ID.IsZero() || value.SessionID.IsZero() || value.CorrelationID.IsZero() ||
		value.RecipientEndpointID.IsZero() || strings.TrimSpace(value.OwnerUserID) == "" ||
		!value.Kind.Valid() || value.Status != ControlOutboxPending || value.CreatedAt.IsZero() ||
		value.UpdatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) || value.CompletedAt != nil ||
		maxPayloadBytes <= 0 || maxPacketBytes <= 0 {
		return NewProblem(CodeInvalidArgument, "control outbox entry is invalid", nil)
	}
	switch value.Kind {
	case ControlOutboxOpenTunnel, ControlOutboxCloseTunnel:
		if len(value.Payload) == 0 || len(value.Payload) > maxPayloadBytes || len(value.Packet) != 0 {
			return NewProblem(CodeInvalidArgument, "platform control outbox payload is invalid", nil)
		}
	case ControlOutboxSessionKeyPackage, ControlOutboxSessionKeyPackageACK:
		if len(value.Packet) == 0 || len(value.Packet) > maxPacketBytes || len(value.Payload) != 0 {
			return NewProblem(CodeInvalidArgument, "endpoint control outbox packet is invalid", nil)
		}
	default:
		return NewProblem(CodeInvalidArgument, "control outbox kind is unsupported", nil)
	}
	return nil
}

func (value ControlOutbox) ValidateStored(maxPayloadBytes, maxPacketBytes int) error {
	if value.ID.IsZero() || value.SessionID.IsZero() || value.CorrelationID.IsZero() ||
		value.RecipientEndpointID.IsZero() || strings.TrimSpace(value.OwnerUserID) == "" ||
		!value.Kind.Valid() || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() ||
		!value.ExpiresAt.After(value.CreatedAt) || maxPayloadBytes <= 0 || maxPacketBytes <= 0 {
		return NewProblem(CodeInvalidArgument, "stored control outbox entry is invalid", nil)
	}
	switch value.Status {
	case ControlOutboxPending:
		if value.CompletedAt != nil {
			return NewProblem(CodeInvalidState, "pending control outbox entry cannot be completed", nil)
		}
	case ControlOutboxCompleted, ControlOutboxCancelled, ControlOutboxExpired:
		if value.CompletedAt == nil {
			return NewProblem(CodeInvalidState, "terminal control outbox entry requires completed_at", nil)
		}
	default:
		return NewProblem(CodeInvalidState, "control outbox status is invalid", nil)
	}
	candidate := value
	candidate.Status = ControlOutboxPending
	candidate.CompletedAt = nil
	return candidate.ValidateNew(maxPayloadBytes, maxPacketBytes)
}

func (value *ControlOutbox) Complete(now time.Time) error {
	if value == nil || now.IsZero() {
		return NewProblem(CodeInvalidArgument, "control outbox completion is invalid", nil)
	}
	if value.Status == ControlOutboxCompleted {
		return nil
	}
	if value.Status != ControlOutboxPending {
		return NewProblem(CodeInvalidState, "control outbox entry is not pending", nil)
	}
	value.Status = ControlOutboxCompleted
	value.CompletedAt = &now
	value.UpdatedAt = now
	value.RowVersion++
	return nil
}
