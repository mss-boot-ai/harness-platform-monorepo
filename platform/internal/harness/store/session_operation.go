package store

import (
	"context"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The same unique creation key serializes cancellation with an in-flight create.
// A cancellation inserted first is a durable terminal result, never a new run.
func (store *Store) CancelEndpointSessionCreation(ctx context.Context, cancellation domain.IdempotencyRecord) (domain.IdempotencyRecord, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if err := cancellation.Validate(domain.MaxIdempotencyResponseBytes); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if cancellation.Operation != "endpoint.session.create" || cancellation.Status != domain.IdempotencyStatusCompleted || cancellation.ErrorCode != "CREATION_CANCELLED" || cancellation.HTTPStatus != 409 {
		return domain.IdempotencyRecord{}, domain.NewProblem(domain.CodeInvalidArgument, "creation cancellation is invalid", nil)
	}
	hcID, err := domain.ParseID(cancellation.ActorID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	var result domain.IdempotencyRecord
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := idempotencyToRow(cancellation)
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if insert.Error != nil {
			return classifyPersistence(insert.Error, "cancel session creation")
		}
		endpoint, err := lockSessionCreationEndpoint(tx, hcID)
		if err != nil {
			return err
		}
		if endpoint.OwnerUserID != cancellation.OwnerUserID || endpoint.TenantID != cancellation.TenantID ||
			(endpoint.Type != string(domain.EndpointTypeHCWeb) && endpoint.Type != string(domain.EndpointTypeHCReference)) || endpoint.Status != string(domain.EndpointStatusActive) || endpoint.RevokedAt != nil {
			return domain.NewProblem(domain.CodeNotFound, "creation endpoint is unavailable", nil)
		}
		if insert.RowsAffected == 1 {
			event := domain.SecurityAuditEvent{ID: cancellation.ID, OwnerUserID: cancellation.OwnerUserID, TenantID: cancellation.TenantID,
				ActorType: domain.AuditActorEndpoint, ActorID: cancellation.ActorID, Action: "session.create.cancel", ObjectType: "session_creation", ObjectID: cancellation.Key,
				Result: "success", CreatedAt: cancellation.CreatedAt, Metadata: map[string]string{}}
			metadata, err := event.CanonicalMetadataJSON()
			if err != nil {
				return err
			}
			audit := auditToRow(event, metadata)
			if err := tx.Create(&audit).Error; err != nil {
				return classifyPersistence(err, "audit creation cancellation")
			}
		}
		var saved idempotencyRow
		if err := tx.Where("owner_user_id = ? AND tenant_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?", cancellation.OwnerUserID, cancellation.TenantID, cancellation.ActorID, cancellation.Operation, cancellation.Key).Take(&saved).Error; err != nil {
			return classifyPersistence(err, "resolve creation cancellation")
		}
		result, err = idempotencyFromRow(saved)
		return err
	})
	return result, err
}

// Inspect the exact creation key, including historical completed records. A page
// of sessions is not evidence that an interrupted creation never existed.
func (store *Store) GetEndpointSessionCreation(ctx context.Context, owner, tenant string, endpoint domain.ID, key string) (domain.IdempotencyRecord, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if endpoint.IsZero() || owner == "" {
		return domain.IdempotencyRecord{}, domain.NewProblem(domain.CodeInvalidArgument, "creation scope is invalid", nil)
	}
	if err := domain.ValidateIdempotencyKey(key); err != nil {
		return domain.IdempotencyRecord{}, err
	}
	var row idempotencyRow
	if err := store.db.WithContext(ctx).Where("owner_user_id = ? AND tenant_id = ? AND actor_id = ? AND operation = ? AND idempotency_key = ?", owner, tenant, endpoint.String(), "endpoint.session.create", key).Take(&row).Error; err != nil {
		return domain.IdempotencyRecord{}, notFoundOr("inspect session creation", "session creation is not available", err)
	}
	return idempotencyFromRow(row)
}
