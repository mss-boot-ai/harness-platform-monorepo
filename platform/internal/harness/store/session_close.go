package store

import (
	"bytes"
	"context"
	"net/http"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm/clause"
)

const (
	endpointSessionCloseOperation = "endpoint.session.close"
	endpointSessionCloseAction    = "session.close"
)

func (store *Store) CloseEndpointSession(
	ctx context.Context,
	session domain.Session,
	reservation domain.IdempotencyRecord,
	audit domain.SecurityAuditEvent,
	responseJSON []byte,
) (domain.Session, []byte, bool, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Session{}, nil, false, err
	}
	if session.ID.IsZero() || session.HCEndpointID.IsZero() ||
		reservation.Status != domain.IdempotencyStatusInProgress ||
		reservation.Operation != endpointSessionCloseOperation ||
		reservation.OwnerUserID != session.OwnerUserID || reservation.TenantID != session.TenantID ||
		reservation.ActorID != session.HCEndpointID.String() ||
		audit.OwnerUserID != session.OwnerUserID || audit.TenantID != session.TenantID ||
		audit.ActorType != domain.AuditActorEndpoint || audit.ActorID != session.HCEndpointID.String() ||
		audit.Action != endpointSessionCloseAction || audit.ObjectType != "session" || audit.ObjectID != session.ID.String() ||
		len(responseJSON) == 0 || !bytes.Contains(responseJSON, []byte(session.ID.String())) ||
		!bytes.Contains(responseJSON, []byte(`"status":"CLOSED"`)) {
		return domain.Session{}, nil, false, domain.NewProblem(domain.CodeInvalidArgument, "endpoint session close transaction input is invalid", nil)
	}
	var replayed bool
	var outputJSON []byte
	updated := session
	err := store.WithTransaction(ctx, func(transaction *Store) error {
		idempotency, err := transaction.ReserveIdempotency(ctx, reservation)
		if err != nil {
			return err
		}
		switch idempotency.Outcome {
		case IdempotencyReplay:
			replayed = true
			outputJSON = bytes.Clone(idempotency.Record.ResponseJSON)
			return nil
		case IdempotencyInProgress:
			return domain.NewProblem(domain.CodeConflict, "session close is already in progress", nil)
		case IdempotencyAcquired:
		default:
			return domain.NewProblem(domain.CodeInvalidState, "idempotency reservation state is invalid", nil)
		}
		var row sessionRow
		if err := transaction.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(
			&row,
			"id = ? AND hc_endpoint_id = ? AND owner_user_id = ? AND tenant_id = ?",
			session.ID.String(), session.HCEndpointID.String(), session.OwnerUserID, session.TenantID,
		).Error; err != nil {
			return notFoundOr("lock endpoint session", "session was not found", err)
		}
		current, err := sessionFromRow(row)
		if err != nil {
			return err
		}
		previous := current.RowVersion
		if err := current.Close(session.UpdatedAt); err != nil {
			return err
		}
		result := transaction.db.WithContext(ctx).Model(new(sessionRow)).Where(
			"id = ? AND row_version = ?", row.ID, previous,
		).Updates(map[string]any{
			"status": string(current.Status), "current_key_generation": current.CurrentKeyGeneration,
			"last_activity_at": current.LastActivityAt, "closed_at": current.ClosedAt,
			"updated_at": current.UpdatedAt, "row_version": current.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "close endpoint session")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "session changed concurrently", nil)
		}
		if err := transaction.AppendAudit(ctx, audit); err != nil {
			return err
		}
		completed := reservation
		completed.Status = domain.IdempotencyStatusCompleted
		completed.HTTPStatus = http.StatusOK
		completed.ResponseJSON = bytes.Clone(responseJSON)
		completed.UpdatedAt = session.UpdatedAt
		if _, err := transaction.CompleteIdempotency(ctx, completed); err != nil {
			return err
		}
		updated = current
		outputJSON = bytes.Clone(responseJSON)
		return nil
	})
	return updated, outputJSON, replayed, err
}
