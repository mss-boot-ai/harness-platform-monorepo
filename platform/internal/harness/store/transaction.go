package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// WithTransaction executes fn with a Store bound to one database transaction.
// Nested Store operations use GORM savepoints, while the outer transaction
// remains the atomic boundary for a management side effect, audit event, and
// idempotency completion.
func (store *Store) WithTransaction(ctx context.Context, fn func(*Store) error) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("harness transaction callback is required")
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Store{db: tx})
	})
}

// Only database metadata belongs in this callback. Retry rolled-back lock
// conflicts without replaying an Agent action or any external side effect.
func (store *Store) metadataTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	for attempt := 0; attempt < 8; attempt++ {
		err := store.db.WithContext(ctx).Transaction(fn)
		if err == nil || (!isSQLiteConcurrencyError(err) && !isPostgresConcurrencyError(err)) {
			return err
		}
		if attempt == 7 {
			return normalizeConcurrencyError("metadata update remains contended", err)
		}
		if err := waitForSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return errors.New("metadata transaction retry exhausted")
}
