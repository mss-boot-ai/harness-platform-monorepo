package store

import (
	"bytes"
	"context"
	"strings"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxActiveSessionsPerABA = 4

func (store *Store) CreateEndpointSession(
	ctx context.Context,
	session domain.Session,
	reservation domain.IdempotencyRecord,
	audit domain.SecurityAuditEvent,
	httpStatus int,
	responseJSON []byte,
) (domain.Session, []byte, bool, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Session{}, nil, false, err
	}
	if reservation.Status != domain.IdempotencyStatusInProgress ||
		reservation.OwnerUserID != session.OwnerUserID || reservation.TenantID != session.TenantID ||
		reservation.ActorID != session.HCEndpointID.String() ||
		audit.OwnerUserID != session.OwnerUserID || audit.TenantID != session.TenantID ||
		audit.ActorType != domain.AuditActorEndpoint || audit.ActorID != session.HCEndpointID.String() ||
		audit.ObjectType != "session" || audit.ObjectID != session.ID.String() ||
		httpStatus < 200 || httpStatus > 299 || len(responseJSON) == 0 || !bytes.Contains(responseJSON, []byte(session.ID.String())) {
		return domain.Session{}, nil, false, domain.NewProblem(domain.CodeInvalidArgument, "endpoint session transaction input is invalid", nil)
	}
	var replayed bool
	var outputJSON []byte
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
			return domain.NewProblem(domain.CodeConflict, "session creation is already in progress", nil)
		case IdempotencyAcquired:
		default:
			return domain.NewProblem(domain.CodeInvalidState, "idempotency reservation state is invalid", nil)
		}
		if err := transaction.authorizeAndCreateEndpointSession(ctx, session); err != nil {
			return err
		}
		if err := transaction.AppendAudit(ctx, audit); err != nil {
			return err
		}
		completed := reservation
		completed.Status = domain.IdempotencyStatusCompleted
		completed.HTTPStatus = httpStatus
		completed.ResponseJSON = bytes.Clone(responseJSON)
		completed.UpdatedAt = session.UpdatedAt
		if _, err := transaction.CompleteIdempotency(ctx, completed); err != nil {
			return err
		}
		outputJSON = bytes.Clone(responseJSON)
		return nil
	})
	return session, outputJSON, replayed, err
}

func (store *Store) authorizeAndCreateEndpointSession(ctx context.Context, session domain.Session) error {
	aba, err := lockSessionCreationEndpoint(store.db.WithContext(ctx), session.ABAEndpointID)
	if err != nil {
		return err
	}
	hc, err := lockSessionCreationEndpoint(store.db.WithContext(ctx), session.HCEndpointID)
	if err != nil {
		return err
	}
	if aba.OwnerUserID != strings.TrimSpace(session.OwnerUserID) || aba.TenantID != strings.TrimSpace(session.TenantID) ||
		hc.OwnerUserID != strings.TrimSpace(session.OwnerUserID) || hc.TenantID != strings.TrimSpace(session.TenantID) {
		return domain.NewProblem(domain.CodeNotFound, "session endpoint was not found", nil)
	}
	if aba.Type != string(domain.EndpointTypeABA) ||
		(hc.Type != string(domain.EndpointTypeHCWeb) && hc.Type != string(domain.EndpointTypeHCReference)) {
		return domain.NewProblem(domain.CodeSecurityViolation, "session endpoint roles are invalid", nil)
	}
	if aba.Status != string(domain.EndpointStatusActive) || aba.RevokedAt != nil ||
		hc.Status != string(domain.EndpointStatusActive) || hc.RevokedAt != nil {
		return domain.NewProblem(domain.CodeRevoked, "session endpoint is unavailable", nil)
	}
	var active int64
	if err := store.db.WithContext(ctx).Model(new(sessionRow)).Where(
		"aba_endpoint_id = ? AND status IN ?",
		aba.ID,
		[]string{
			string(domain.SessionStatusCreating), string(domain.SessionStatusWaitingKey),
			string(domain.SessionStatusActive), string(domain.SessionStatusRekeyRequired),
			string(domain.SessionStatusDraining), string(domain.SessionStatusUncertain),
		},
	).Count(&active).Error; err != nil {
		return classifyPersistence(err, "count active ABA sessions")
	}
	if active >= maxActiveSessionsPerABA {
		return domain.NewProblem(domain.CodeResourceLimit, "ABA active session limit reached", nil)
	}
	row, err := sessionToRow(session)
	if err != nil {
		return err
	}
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create endpoint session")
	}
	return nil
}

func lockSessionCreationEndpoint(tx *gorm.DB, id domain.ID) (endpointRow, error) {
	var endpoint endpointRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
		&endpoint, "id = ?", id.String(),
	).Error; err != nil {
		return endpointRow{}, notFoundOr("authorize session endpoint", "session endpoint was not found", err)
	}
	return endpoint, nil
}
