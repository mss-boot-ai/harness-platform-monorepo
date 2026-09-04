package dpop

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryReplayCacheIsBoundedAndRejectsReuse(t *testing.T) {
	cache, err := NewMemoryReplayCache(1)
	if err != nil {
		t.Fatalf("create replay cache: %v", err)
	}
	expiry := time.Now().Add(time.Hour)
	if err := cache.Use(context.Background(), "jkt", "jti-1", expiry); err != nil {
		t.Fatalf("use first JTI: %v", err)
	}
	if err := cache.Use(context.Background(), "jkt", "jti-1", expiry); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate error = %v, want replay", err)
	}
	if err := cache.Use(context.Background(), "jkt", "jti-2", expiry); !errors.Is(err, ErrReplayCapacity) {
		t.Fatalf("capacity error = %v, want capacity", err)
	}
}
