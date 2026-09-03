package store

import (
	"context"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

// RevokeEndpointForOwner verifies the immutable owner/tenant scope before
// invoking the transactional revocation cascade.
func (store *Store) RevokeEndpointForOwner(
	ctx context.Context,
	id domain.ID,
	owner string,
	tenant string,
	now time.Time,
) (domain.Endpoint, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return domain.Endpoint{}, err
	}
	if id.IsZero() || now.IsZero() {
		return domain.Endpoint{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint revocation is invalid", nil)
	}
	query := store.db.WithContext(ctx).Model(new(endpointRow)).
		Where("id = ? AND owner_user_id = ?", id.String(), strings.TrimSpace(owner))
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		query = query.Where("tenant_id = ?", tenant)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return domain.Endpoint{}, err
	}
	if count != 1 {
		return domain.Endpoint{}, domain.NewProblem(domain.CodeNotFound, "endpoint was not found", nil)
	}
	return store.RevokeEndpoint(ctx, id, strings.TrimSpace(owner), now)
}
