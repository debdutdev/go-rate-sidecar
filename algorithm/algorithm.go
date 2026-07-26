package algorithm

import (
	"fmt"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/store"
)

// Type identifies a rate limiting algorithm.
type Type int

const (
	TypeTokenBucket Type = iota
	TypeLeakyBucket
	TypeFixedWindow
	TypeSlidingWindowLog
	TypeSlidingWindowCounter
)

var typeNames = map[Type]string{
	TypeTokenBucket:          "token_bucket",
	TypeLeakyBucket:          "leaky_bucket",
	TypeFixedWindow:          "fixed_window",
	TypeSlidingWindowLog:     "sliding_window_log",
	TypeSlidingWindowCounter: "sliding_window_counter",
}

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
func New(t Type, cfg ratelimiter.Config, s store.Store, opts ...Option) (ratelimiter.Limiter, error) {
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
