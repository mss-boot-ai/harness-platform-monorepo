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
