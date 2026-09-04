package store

import (
	"context"
	"strings"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func requireM1Scope(store *Store, ctx context.Context, owner string) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if strings.TrimSpace(owner) == "" {
		return domain.NewProblem(domain.CodeInvalidArgument, "owner is required", nil)
	}
	return nil
}

func normalizeConcurrencyError(message string, err error) error {
	if err == nil {
		return nil
	}
	value := strings.ToLower(err.Error())
	if strings.Contains(value, "database is locked") ||
		strings.Contains(value, "database table is locked") ||
		strings.Contains(value, "sqlite_busy") ||
		strings.Contains(value, "sqlite_locked") {
		return domain.NewProblem(domain.CodeConflict, message, err)
	}
	return err
}
