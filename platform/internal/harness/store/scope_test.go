package store

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestNormalizeConcurrencyErrorRecognizesPostgresRetryStates(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"40P01", "40001"} {
		err := normalizeConcurrencyError("changed concurrently", &pgconn.PgError{Code: code})
		if !domain.HasCode(err, domain.CodeConflict) {
			t.Fatalf("SQLSTATE %s normalized to %v", code, err)
		}
	}
	if isPostgresConcurrencyError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique violation was classified as retryable concurrency")
	}
}
