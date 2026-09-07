package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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
	if isSQLiteConcurrencyError(err) || isPostgresConcurrencyError(err) {
		return domain.NewProblem(domain.CodeConflict, message, err)
	}
	return err
}

func isPostgresConcurrencyError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40P01" || postgresError.Code == "40001"
}

func isSQLiteConcurrencyError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "database is locked") ||
		strings.Contains(value, "database table is locked") ||
		strings.Contains(value, "sqlite_busy") ||
		strings.Contains(value, "sqlite_locked")
}

func waitForSQLiteRetry(ctx context.Context, attempt int) error {
	backoff := time.Duration(1<<attempt) * time.Millisecond
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
