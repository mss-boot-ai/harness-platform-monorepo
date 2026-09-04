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
