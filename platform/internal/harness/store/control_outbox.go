package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxControlOutboxPayloadBytes = 64 << 10
	maxControlOutboxPacketBytes  = 1 << 20
	maxControlOutboxList         = 128
)

type PutControlOutboxOutcome string

const (
	PutControlOutboxStored    PutControlOutboxOutcome = "STORED"
	PutControlOutboxDuplicate PutControlOutboxOutcome = "DUPLICATE"
)

func (store *Store) PutControlOutbox(ctx context.Context, value domain.ControlOutbox) (PutControlOutboxOutcome, error) {
	if err := requireM1Scope(store, ctx, value.OwnerUserID); err != nil {
		return "", err
	}
	if err := value.ValidateNew(maxControlOutboxPayloadBytes, maxControlOutboxPacketBytes); err != nil {
		return "", err
	}
	row := controlOutboxToRow(value)
	result := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return "", classifyPersistence(result.Error, "store control outbox entry")
	}
	if result.RowsAffected == 1 {
		return PutControlOutboxStored, nil
	}
	var existing controlOutboxRow
	if err := store.db.WithContext(ctx).Where(
		"kind = ? AND correlation_id = ? AND recipient_endpoint_id = ?",
		row.Kind, row.CorrelationID, row.RecipientEndpointID,
	).Take(&existing).Error; err != nil {
		return "", notFoundOr("read control outbox conflict", "control outbox conflict could not be resolved", err)
	}
	if !sameControlOutbox(existing, row) {
		return "", domain.NewProblem(domain.CodeConflict, "control outbox idempotency key was reused with different content", nil)
	}
	return PutControlOutboxDuplicate, nil
}

func (store *Store) ListPendingControlOutbox(
	ctx context.Context,
	recipient domain.ID,
	now time.Time,
	limit int,
) ([]domain.ControlOutbox, error) {
	if err := requireStore(store, ctx); err != nil {
		return nil, err
	}
	if recipient.IsZero() || now.IsZero() {
		return nil, domain.NewProblem(domain.CodeInvalidArgument, "control outbox recipient and time are required", nil)
	}
	if limit <= 0 || limit > maxControlOutboxList {
		limit = maxControlOutboxList
	}
	var rows []controlOutboxRow
	if err := store.db.WithContext(ctx).Where(
		"recipient_endpoint_id = ? AND status = ? AND expires_at > ?",
		recipient.String(), string(domain.ControlOutboxPending), now,
	).Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list pending control outbox: %w", err)
	}
	values := make([]domain.ControlOutbox, 0, len(rows))
	for _, row := range rows {
		value, err := controlOutboxFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) CompleteControlOutbox(
	ctx context.Context,
	kind domain.ControlOutboxKind,
	correlationID domain.ID,
	recipient domain.ID,
	now time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if !kind.Valid() || correlationID.IsZero() || recipient.IsZero() || now.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "control outbox completion binding is invalid", nil)
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row controlOutboxRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"kind = ? AND correlation_id = ? AND recipient_endpoint_id = ?",
			string(kind), correlationID.String(), recipient.String(),
		).Take(&row).Error; err != nil {
			return notFoundOr("lock control outbox entry", "control outbox entry was not found", err)
		}
		value, err := controlOutboxFromRow(row)
		if err != nil {
			return err
		}
		if value.Status == domain.ControlOutboxCompleted {
			return nil
		}
		previous := value.RowVersion
		if err := value.Complete(now); err != nil {
			return err
		}
		result := tx.Model(new(controlOutboxRow)).Where(
			"id = ? AND row_version = ? AND status = ?", row.ID, previous, string(domain.ControlOutboxPending),
		).Updates(map[string]any{
			"status": string(value.Status), "completed_at": value.CompletedAt,
			"updated_at": value.UpdatedAt, "row_version": value.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "complete control outbox entry")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "control outbox entry changed concurrently", nil)
		}
		return nil
	})
}

func (store *Store) CancelSessionControlOutbox(ctx context.Context, sessionID domain.ID, now time.Time) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if sessionID.IsZero() || now.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "control outbox cancellation is invalid", nil)
	}
	return store.db.WithContext(ctx).Model(new(controlOutboxRow)).Where(
		"session_id = ? AND status = ?", sessionID.String(), string(domain.ControlOutboxPending),
	).Updates(map[string]any{
		"status": string(domain.ControlOutboxCancelled), "completed_at": now,
		"updated_at": now, "row_version": gorm.Expr("row_version + 1"),
	}).Error
}

func (store *Store) PutEndpointSessionKeyPackageWithOutbox(
	ctx context.Context,
	owner string,
	tenant string,
	value domain.SessionKeyPackage,
	outbox domain.ControlOutbox,
) (domain.SessionKeyPackage, bool, error) {
	if outbox.Kind != domain.ControlOutboxSessionKeyPackage || outbox.CorrelationID != value.ID ||
		outbox.SessionID != value.SessionID || outbox.RecipientEndpointID != value.RecipientHCEndpointID ||
		outbox.OwnerUserID != strings.TrimSpace(owner) || outbox.TenantID != strings.TrimSpace(tenant) {
		return domain.SessionKeyPackage{}, false, domain.NewProblem(domain.CodeInvalidArgument, "key package outbox binding is invalid", nil)
	}
	var stored domain.SessionKeyPackage
	var duplicate bool
	err := store.WithTransaction(ctx, func(transaction *Store) error {
		result, err := transaction.PutSessionKeyPackage(ctx, owner, tenant, value)
		if err != nil {
			return err
		}
		if _, err := transaction.PutControlOutbox(ctx, outbox); err != nil {
			return err
		}
		if err := transaction.CompleteControlOutbox(
			ctx, domain.ControlOutboxOpenTunnel, value.SessionID, value.IssuerABAEndpointID, value.CreatedAt,
		); err != nil && !domain.HasCode(err, domain.CodeNotFound) {
			return err
		}
		stored = result.Package
		duplicate = result.Outcome == PutKeyPackageDuplicate
		return nil
	})
	return stored, duplicate, err
}

func controlOutboxToRow(value domain.ControlOutbox) controlOutboxRow {
	return controlOutboxRow{
		ID: value.ID.String(), OwnerUserID: value.OwnerUserID, TenantID: value.TenantID,
		SessionID: value.SessionID.String(), CorrelationID: value.CorrelationID.String(),
		RecipientEndpointID: value.RecipientEndpointID.String(), Kind: string(value.Kind),
		Payload: bytes.Clone(value.Payload), Packet: bytes.Clone(value.Packet), Status: string(value.Status),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ExpiresAt: value.ExpiresAt,
		CompletedAt: cloneTime(value.CompletedAt), RowVersion: value.RowVersion,
	}
}

func controlOutboxFromRow(row controlOutboxRow) (domain.ControlOutbox, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.ControlOutbox{}, err
	}
	sessionID, err := parseID(row.SessionID)
	if err != nil {
		return domain.ControlOutbox{}, err
	}
	correlationID, err := parseID(row.CorrelationID)
	if err != nil {
		return domain.ControlOutbox{}, err
	}
	recipientID, err := parseID(row.RecipientEndpointID)
	if err != nil {
		return domain.ControlOutbox{}, err
	}
	value := domain.ControlOutbox{
		ID: id, OwnerUserID: row.OwnerUserID, TenantID: row.TenantID,
		SessionID: sessionID, CorrelationID: correlationID, RecipientEndpointID: recipientID,
		Kind: domain.ControlOutboxKind(row.Kind), Payload: bytes.Clone(row.Payload), Packet: bytes.Clone(row.Packet),
		Status: domain.ControlOutboxStatus(row.Status), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		ExpiresAt: row.ExpiresAt, CompletedAt: cloneTime(row.CompletedAt), RowVersion: row.RowVersion,
	}
	if err := value.ValidateStored(maxControlOutboxPayloadBytes, maxControlOutboxPacketBytes); err != nil {
		return domain.ControlOutbox{}, err
	}
	return value, nil
}

func sameControlOutbox(left, right controlOutboxRow) bool {
	return left.OwnerUserID == right.OwnerUserID && left.TenantID == right.TenantID &&
		left.SessionID == right.SessionID && left.CorrelationID == right.CorrelationID &&
		left.RecipientEndpointID == right.RecipientEndpointID && left.Kind == right.Kind &&
		bytes.Equal(left.Payload, right.Payload) && bytes.Equal(left.Packet, right.Packet)
}

func readControlOutboxByCorrelation(
	tx *gorm.DB,
	kind domain.ControlOutboxKind,
	correlationID domain.ID,
	recipient domain.ID,
) (controlOutboxRow, error) {
	var row controlOutboxRow
	if err := tx.Where(
		"kind = ? AND correlation_id = ? AND recipient_endpoint_id = ?",
		string(kind), correlationID.String(), recipient.String(),
	).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return controlOutboxRow{}, domain.NewProblem(domain.CodeNotFound, "control outbox entry was not found", err)
		}
		return controlOutboxRow{}, fmt.Errorf("read control outbox entry: %w", err)
	}
	return row, nil
}

func (store *Store) AcknowledgeAndActivateSessionKeyPackageWithOutbox(
	ctx context.Context,
	packageID domain.ID,
	recipient domain.ID,
	owner string,
	tenant string,
	now time.Time,
	outbox domain.ControlOutbox,
) (domain.Session, error) {
	if outbox.Kind != domain.ControlOutboxSessionKeyPackageACK || outbox.CorrelationID != packageID ||
		outbox.RecipientEndpointID.IsZero() || outbox.OwnerUserID != strings.TrimSpace(owner) ||
		outbox.TenantID != strings.TrimSpace(tenant) {
		return domain.Session{}, domain.NewProblem(domain.CodeInvalidArgument, "key package acknowledgment outbox binding is invalid", nil)
	}
	var session domain.Session
	err := store.WithTransaction(ctx, func(transaction *Store) error {
		updated, err := transaction.AcknowledgeAndActivateSessionKeyPackage(
			ctx, packageID, recipient, owner, tenant, now,
		)
		if err != nil {
			return err
		}
		if outbox.SessionID != updated.ID || outbox.RecipientEndpointID != updated.ABAEndpointID {
			return domain.NewProblem(domain.CodeInvalidArgument, "key package acknowledgment outbox session is invalid", nil)
		}
		if _, err := transaction.PutControlOutbox(ctx, outbox); err != nil {
			return err
		}
		if err := transaction.CompleteControlOutbox(
			ctx, domain.ControlOutboxSessionKeyPackage, packageID, recipient, now,
		); err != nil && !domain.HasCode(err, domain.CodeNotFound) {
			return err
		}
		session = updated
		return nil
	})
	return session, err
}
