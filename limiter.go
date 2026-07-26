package ratelimiter

import (
	"context"
	"time"
)

// Result is returned by every Allow call. It carries enough information
// for middleware to set standard rate-limit response headers.
type Result struct {
	// Allowed indicates whether the request is permitted.
	Allowed bool

	// Limit is the configured maximum (tokens, count, etc.).
	Limit int64

	// Remaining is how many requests remain in the current window/bucket.
	Remaining int64

	// RetryAt is when the caller should retry (zero value if allowed).
	RetryAt time.Time

	// ResetAt is when the current window/bucket resets.
	ResetAt time.Time
}

// Limiter is the strategy interface. Each algorithm provides one implementation.
// All implementations must be safe for concurrent use.
type Limiter interface {
	// Allow checks whether a request identified by key should be permitted.
	Allow(ctx context.Context, key string) (Result, error)

	// AllowN is like Allow but attempts to consume n tokens/slots at once.
	AllowN(ctx context.Context, key string, n int64) (Result, error)

	// Reset clears the state for a specific key.
	Reset(ctx context.Context, key string) error

	// Close releases resources held by this limiter.
	Close() error
}
