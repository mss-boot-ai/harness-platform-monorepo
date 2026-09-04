package dpop

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrReplay = errors.New("DPoP proof was already used")
var ErrReplayCapacity = errors.New("DPoP replay cache reached its hard limit")

type MemoryReplayCache struct {
	mu         sync.Mutex
	maxEntries int
	entries    map[string]time.Time
}

func NewMemoryReplayCache(maxEntries int) (*MemoryReplayCache, error) {
	if maxEntries <= 0 {
		return nil, errors.New("DPoP replay cache limit must be positive")
	}
	return &MemoryReplayCache{maxEntries: maxEntries, entries: make(map[string]time.Time)}, nil
}

func (cache *MemoryReplayCache) Use(ctx context.Context, jkt, jti string, expiresAt time.Time) error {
	if cache == nil || ctx == nil || jkt == "" || jti == "" || expiresAt.IsZero() {
		return errors.New("DPoP replay cache input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	key := jkt + "\x00" + jti
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for candidate, expiry := range cache.entries {
		if !expiry.After(now) {
			delete(cache.entries, candidate)
		}
	}
	if _, exists := cache.entries[key]; exists {
		return ErrReplay
	}
	if len(cache.entries) >= cache.maxEntries {
		return ErrReplayCapacity
	}
	cache.entries[key] = expiresAt
	return nil
}
