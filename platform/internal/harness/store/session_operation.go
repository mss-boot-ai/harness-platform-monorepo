package store

import (
	"context"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

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
