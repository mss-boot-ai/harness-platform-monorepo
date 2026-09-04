package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IdempotencyReserveOutcome string

const (
	IdempotencyAcquired   IdempotencyReserveOutcome = "ACQUIRED"
	IdempotencyInProgress IdempotencyReserveOutcome = "IN_PROGRESS"
	IdempotencyReplay     IdempotencyReserveOutcome = "REPLAY"
)

type IdempotencyReservation struct {
	Outcome IdempotencyReserveOutcome
	Record  domain.IdempotencyRecord
}

func (store *Store) ReserveIdempotency(
	ctx context.Context,
	record domain.IdempotencyRecord,
) (IdempotencyReservation, error) {
	if err := requireM1Scope(store, ctx, record.OwnerUserID); err != nil {
		return IdempotencyReservation{}, err
	}
	if record.Status != domain.IdempotencyStatusInProgress {
		return IdempotencyReservation{}, domain.NewProblem(domain.CodeInvalidState, "new idempotency record must be in progress", nil)
	}
	if err := record.Validate(domain.MaxIdempotencyResponseBytes); err != nil {
		return IdempotencyReservation{}, err
	}
	row := idempotencyToRow(record)
	insert := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if insert.Error != nil {
		return IdempotencyReservation{}, classifyPersistence(insert.Error, "reserve idempotency record")
	}
	if insert.RowsAffected == 1 {
		return IdempotencyReservation{Outcome: IdempotencyAcquired, Record: record}, nil
	}
	var existing idempotencyRow
	if err := store.db.WithContext(ctx).First(&existing,
		"owner_user_id = ? AND tenant_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?",
		strings.TrimSpace(record.OwnerUserID), strings.TrimSpace(record.TenantID), strings.TrimSpace(record.ActorID), record.Operation, record.Key,
	).Error; err != nil {
		return IdempotencyReservation{}, notFoundOr("read existing idempotency record", "idempotency conflict could not be resolved", err)
	}
	value, err := idempotencyFromRow(existing)
	if err != nil {
		return IdempotencyReservation{}, err
	}
	if !bytes.Equal(value.RequestHash[:], record.RequestHash[:]) {
		return IdempotencyReservation{}, domain.NewProblem(domain.CodeConflict, "Idempotency-Key was reused with a different request", nil)
	}
	if value.Status == domain.IdempotencyStatusCompleted {
		return IdempotencyReservation{Outcome: IdempotencyReplay, Record: value}, nil
	}
	if !record.CreatedAt.Before(value.ExpiresAt) {
		return IdempotencyReservation{}, domain.NewProblem(domain.CodeExpired, "idempotency reservation is expired", nil)
	}
	return IdempotencyReservation{Outcome: IdempotencyInProgress, Record: value}, nil
}

func (store *Store) CompleteIdempotency(
	ctx context.Context,
	completed domain.IdempotencyRecord,
) (domain.IdempotencyRecord, error) {
	if err := requireM1Scope(store, ctx, completed.OwnerUserID); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if completed.Status != domain.IdempotencyStatusCompleted {
		return domain.IdempotencyRecord{}, domain.NewProblem(domain.CodeInvalidState, "idempotency completion must be completed", nil)
	}
	if err := completed.Validate(domain.MaxIdempotencyResponseBytes); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	var output domain.IdempotencyRecord
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row idempotencyRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row,
			"id = ? AND owner_user_id = ? AND tenant_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?",
			completed.ID.String(), strings.TrimSpace(completed.OwnerUserID), strings.TrimSpace(completed.TenantID), strings.TrimSpace(completed.ActorID), completed.Operation, completed.Key,
		).Error; err != nil {
			return notFoundOr("lock idempotency record", "idempotency record was not found", err)
		}
		current, err := idempotencyFromRow(row)
		if err != nil {
			return err
		}
		if !bytes.Equal(current.RequestHash[:], completed.RequestHash[:]) {
			return domain.NewProblem(domain.CodeConflict, "idempotency completion request hash does not match", nil)
		}
		if current.Status == domain.IdempotencyStatusCompleted {
			if current.HTTPStatus == completed.HTTPStatus && current.ErrorCode == completed.ErrorCode && bytes.Equal(current.ResponseJSON, completed.ResponseJSON) {
				output = current
				return nil
			}
			return domain.NewProblem(domain.CodeConflict, "idempotency record was completed with a different result", nil)
		}
		result := tx.Model(new(idempotencyRow)).Where("id = ? AND row_version = ? AND status = ?", row.ID, row.RowVersion, string(domain.IdempotencyStatusInProgress)).Updates(map[string]any{
			"status":        string(domain.IdempotencyStatusCompleted),
			"http_status":   completed.HTTPStatus,
			"response_json": bytes.Clone(completed.ResponseJSON),
			"error_code":    completed.ErrorCode,
			"updated_at":    completed.UpdatedAt,
			"row_version":   row.RowVersion + 1,
		})
		if result.Error != nil {
			return fmt.Errorf("complete idempotency record: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "idempotency record changed concurrently", nil)
		}
		completed.RowVersion = row.RowVersion + 1
		output = completed
		return nil
	})
	return output, err
}

func idempotencyToRow(value domain.IdempotencyRecord) idempotencyRow {
	return idempotencyRow{
		ID:           value.ID.String(),
		OwnerUserID:  strings.TrimSpace(value.OwnerUserID),
		TenantID:     strings.TrimSpace(value.TenantID),
		ActorID:      strings.TrimSpace(value.ActorID),
		Operation:    value.Operation,
		Key:          value.Key,
		RequestHash:  hashString(value.RequestHash),
		Status:       string(value.Status),
		HTTPStatus:   value.HTTPStatus,
		ResponseJSON: bytes.Clone(value.ResponseJSON),
		ErrorCode:    value.ErrorCode,
		CreatedAt:    value.CreatedAt,
		UpdatedAt:    value.UpdatedAt,
		ExpiresAt:    value.ExpiresAt,
		RowVersion:   value.RowVersion,
	}
}

func idempotencyFromRow(row idempotencyRow) (domain.IdempotencyRecord, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	requestHash, err := parseHash(row.RequestHash)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	value := domain.IdempotencyRecord{
		ID:           id,
		OwnerUserID:  row.OwnerUserID,
		TenantID:     row.TenantID,
		ActorID:      row.ActorID,
		Operation:    row.Operation,
		Key:          row.Key,
		RequestHash:  requestHash,
		Status:       domain.IdempotencyStatus(row.Status),
		HTTPStatus:   row.HTTPStatus,
		ResponseJSON: bytes.Clone(row.ResponseJSON),
		ErrorCode:    row.ErrorCode,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		ExpiresAt:    row.ExpiresAt,
		RowVersion:   row.RowVersion,
	}
	if err := value.Validate(domain.MaxIdempotencyResponseBytes); err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("decode stored idempotency record: %w", err)
	}
	return value, nil
}
