package algorithm

import (
	"fmt"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/store"
)

// Type identifies a rate limiting algorithm.
type Type int

const (
	// TypeTokenBucket is the token bucket algorithm.
	TypeTokenBucket Type = iota
	// TypeLeakyBucket is the leaky bucket algorithm.
	TypeLeakyBucket
	// TypeFixedWindow is the fixed window counter algorithm.
	TypeFixedWindow
	// TypeSlidingWindowLog is the sliding window log algorithm.
	TypeSlidingWindowLog
	// TypeSlidingWindowCounter is the sliding window counter algorithm.
	TypeSlidingWindowCounter
)

var typeNames = map[Type]string{
	TypeTokenBucket:          "token_bucket",
	TypeLeakyBucket:          "leaky_bucket",
	TypeFixedWindow:          "fixed_window",
	TypeSlidingWindowLog:     "sliding_window_log",
	TypeSlidingWindowCounter: "sliding_window_counter",
}

// String returns the snake_case name of the algorithm type.
func (t Type) String() string {
	if name, ok := typeNames[t]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", int(t))
}

// ParseType converts a string name to an algorithm Type.
func ParseType(name string) (Type, error) {
	for t, n := range typeNames {
		if n == name {
			return t, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown algorithm %q", ratelimiter.ErrInvalidConfig, name)
}

// New creates a Limiter of the given algorithm type.
// The store must be non-nil; all algorithms require a backing store.
func New(t Type, cfg ratelimiter.Config, s store.Store, opts ...Option) (ratelimiter.Limiter, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: store must not be nil", ratelimiter.ErrInvalidConfig)
	}
	switch t {
	case TypeTokenBucket:
		return NewTokenBucket(cfg, s, opts...)
	case TypeLeakyBucket:
		return NewLeakyBucket(cfg, s, opts...)
	case TypeFixedWindow:
		return NewFixedWindow(cfg, s, opts...)
	case TypeSlidingWindowLog:
		return NewSlidingWindowLog(cfg, s, opts...)
	case TypeSlidingWindowCounter:
		return NewSlidingWindowCounter(cfg, s, opts...)
	default:
		return nil, fmt.Errorf("%w: unknown algorithm type %d", ratelimiter.ErrInvalidConfig, int(t))
	}
}
