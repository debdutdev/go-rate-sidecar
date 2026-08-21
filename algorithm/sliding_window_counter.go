package algorithm

import (
	"context"
	"fmt"
	"math"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/internal/clock"
	"github.com/debdutdev/rate-limiter/store"
)

// SlidingWindowCounter implements rate limiting using a weighted
// combination of the current and previous fixed window counters.
//
// Config.Rate   = maximum requests allowed per window.
// Config.Window = window duration.
//
// The estimated count is:
//
//	weight = 1 - (elapsed_in_current_window / window)
//	estimated = prev_window_count × weight + current_window_count
//
// This achieves O(1) memory per key (just two counters) with much
// better precision than Fixed Window at boundaries. This is the
// algorithm used by Cloudflare and most production API gateways.
type SlidingWindowCounter struct {
	rate   int64
	window time.Duration
	store  store.Store
	clock  clock.Clock
}

// NewSlidingWindowCounter creates a sliding window counter limiter.
// Rate is the maximum requests per Window. The store must be non-nil.
func NewSlidingWindowCounter(cfg ratelimiter.Config, s store.Store, opts ...Option) (*SlidingWindowCounter, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: store must not be nil", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("%w: rate must be > 0", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: window must be > 0", ratelimiter.ErrInvalidConfig)
	}

	o := applyOptions(opts)
	return &SlidingWindowCounter{
		rate:   cfg.Rate,
		window: cfg.Window,
		store:  s,
		clock:  o.clock,
	}, nil
}

func (sc *SlidingWindowCounter) windowStart(t time.Time) time.Time {
	windowNanos := sc.window.Nanoseconds()
	truncated := t.UnixNano() / windowNanos * windowNanos
	return time.Unix(0, truncated).UTC()
}

// Allow checks whether a single request identified by key is permitted.
func (sc *SlidingWindowCounter) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return sc.AllowN(ctx, key, 1)
}

// AllowN checks whether n requests can be counted for the given key.
// n must be positive.
func (sc *SlidingWindowCounter) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	if n <= 0 {
		return ratelimiter.Result{}, fmt.Errorf("%w: n must be > 0, got %d", ratelimiter.ErrInvalidConfig, n)
	}
	if rs, ok := sc.store.(*store.RedisStore); ok {
		return sc.allowRedis(ctx, rs, key, n)
	}

	now := sc.clock.Now()
	currentWindow := sc.windowStart(now)

	for {
		state, exists, err := sc.store.Get(ctx, key)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}

		var currentCount, prevCount int64
		var version uint64

		if exists {
			version = state.Version
			if state.WindowStart.Equal(currentWindow) {
				currentCount = state.WindowCount
				prevCount = state.PrevWindowCount
			} else if state.WindowStart.Equal(currentWindow.Add(-sc.window)) {
				// Previous window is the stored window; current is fresh.
				prevCount = state.WindowCount
				currentCount = 0
			}
			// If the stored window is older than prev, both reset to 0.
		}

		// Calculate weighted estimate.
		elapsed := now.Sub(currentWindow)
		weight := 1.0 - float64(elapsed)/float64(sc.window)
		estimated := float64(prevCount)*weight + float64(currentCount)

		resetAt := currentWindow.Add(sc.window)

		newState := store.State{
			WindowStart:     currentWindow,
			WindowCount:     currentCount,
			PrevWindowCount: prevCount,
			Version:         version + 1,
		}

		ttl := 2 * sc.window

		if estimated+float64(n) <= float64(sc.rate) {
			newState.WindowCount = currentCount + n
			remainingEstimate := float64(sc.rate) - (float64(prevCount)*weight + float64(newState.WindowCount))
			remaining := int64(math.Max(0, remainingEstimate))

			swapped, err := sc.store.CompareAndSwap(ctx, key, state, newState, ttl)
			if err != nil {
				return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
			}
			if !swapped {
				continue
			}

			return ratelimiter.Result{
				Allowed:   true,
				Limit:     sc.rate,
				Remaining: remaining,
				ResetAt:   resetAt,
			}, nil
		}

		// Deny.
		swapped, err := sc.store.CompareAndSwap(ctx, key, state, newState, ttl)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}
		if !swapped {
			continue
		}

		return ratelimiter.Result{
			Allowed:   false,
			Limit:     sc.rate,
			Remaining: 0,
			RetryAt:   resetAt,
			ResetAt:   resetAt,
		}, nil
	}
}

func (sc *SlidingWindowCounter) allowRedis(ctx context.Context, rs *store.RedisStore, key string, n int64) (ratelimiter.Result, error) {
	windowMs := sc.window.Milliseconds()
	allowed, remaining, windowStartUs, err := rs.SlidingWindowCounterAllow(ctx, key, windowMs, sc.rate, n)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
	}

	resetAt := time.UnixMicro(windowStartUs).Add(sc.window)

	result := ratelimiter.Result{
		Allowed:   allowed,
		Limit:     sc.rate,
		Remaining: remaining,
		ResetAt:   resetAt,
	}
	if !allowed {
		result.RetryAt = resetAt
	}
	return result, nil
}

// Reset clears all state for the given key.
func (sc *SlidingWindowCounter) Reset(ctx context.Context, key string) error {
	return sc.store.Delete(ctx, key)
}

// Close is a no-op for SlidingWindowCounter (state lives in the store).
func (sc *SlidingWindowCounter) Close() error {
	return nil
}
