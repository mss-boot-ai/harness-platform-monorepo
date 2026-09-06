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
	now := time.Now()
	expiry := now.Add(time.Hour)
	if err := cache.Use(context.Background(), "jkt", "jti-1", now, expiry); err != nil {
		t.Fatalf("use first JTI: %v", err)
	}
	if err := cache.Use(context.Background(), "jkt", "jti-1", now, expiry); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate error = %v, want replay", err)
	}
	if err := cache.Use(context.Background(), "jkt", "jti-2", now, expiry); !errors.Is(err, ErrReplayCapacity) {
		t.Fatalf("capacity error = %v, want capacity", err)
	}
}
