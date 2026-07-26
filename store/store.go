package store

import (
	"context"
	"time"
)

// State holds the raw counters/timestamps that algorithms read and write.
// Each algorithm uses a subset of these fields.
type State struct {
	// Token Bucket
	Tokens     float64
	LastRefill time.Time

	// Leaky Bucket
	QueueSize int64
	LastDrain time.Time

	// Fixed Window
	WindowCount int64
	WindowStart time.Time

	// Sliding Window Counter
	PrevWindowCount int64

	// Sliding Window Log
	Timestamps []int64 // unix nanoseconds

	// Version is used for optimistic locking in CompareAndSwap.
	Version uint64
}

// Store abstracts the read/write of rate limiter state.
// Implementations must be goroutine-safe.
type Store interface {
	// Get retrieves the current state for a key.
	// Returns zero-value State and false if key does not exist.
	Get(ctx context.Context, key string) (State, bool, error)

	// Set atomically writes state for a key with a TTL.
	Set(ctx context.Context, key string, state State, ttl time.Duration) error

	// CompareAndSwap atomically updates state only if the current version matches.
	// Returns false if it did not match (optimistic locking).
	CompareAndSwap(ctx context.Context, key string, old, new State, ttl time.Duration) (bool, error)

	// Delete removes state for a key.
	Delete(ctx context.Context, key string) error

	// Close releases any resources.
	Close() error
}
