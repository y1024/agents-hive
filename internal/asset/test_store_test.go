package asset

import (
	"context"
	"time"
)

type captureSignedURLStore struct {
	ObjectStore
	lastTTL time.Duration
}

func (s *captureSignedURLStore) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	s.lastTTL = ttl
	return s.ObjectStore.SignedURL(ctx, key, ttl)
}
