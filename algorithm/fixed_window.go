package algorithm

import (
	"context"
	"fmt"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/store"
)

// FixedWindow implements the fixed window counter rate limiting algorithm.
//
// Config.Rate  = maximum requests allowed per window.
// Config.Window = window duration (e.g. 1 minute).
//
// Time is divided into consecutive non-overlapping windows. Each window
// has an independent counter. When the window rolls over, the counter resets.
//
// Known weakness: boundary burst — a client can send Rate requests at the
// end of window N and Rate more at the start of window N+1, achieving
// 2×Rate in a span of Window centered on the boundary.
type FixedWindow struct {
	rate   int64
	window time.Duration
	store  store.Store
	clock  clock.Clock
}

func NewFixedWindow(cfg ratelimiter.Config, s store.Store, opts ...Option) (*FixedWindow, error) {
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("%w: rate must be > 0", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: window must be > 0", ratelimiter.ErrInvalidConfig)
	}

	o := applyOptions(opts)
	return &FixedWindow{
		rate:   cfg.Rate,
		window: cfg.Window,
		store:  s,
		clock:  o.clock,
	}, nil
}

func (fw *FixedWindow) windowStart(t time.Time) time.Time {
	windowNanos := fw.window.Nanoseconds()
	truncated := t.UnixNano() / windowNanos * windowNanos
	return time.Unix(0, truncated).UTC()
}

func (fw *FixedWindow) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return fw.AllowN(ctx, key, 1)
}

func (fw *FixedWindow) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	if rs, ok := fw.store.(*store.RedisStore); ok {
		return fw.allowRedis(ctx, rs, key, n)
	}

	now := fw.clock.Now()
	currentWindow := fw.windowStart(now)

	for {
		state, exists, err := fw.store.Get(ctx, key)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}

		var count int64
		var version uint64
		if exists {
			version = state.Version
			if state.WindowStart.Equal(currentWindow) {
				count = state.WindowCount
			}
			// If window has rolled over, count resets to 0.
		}

		resetAt := currentWindow.Add(fw.window)

		newState := store.State{
			WindowStart: currentWindow,
			WindowCount: count,
			Version:     version + 1,
		}

		// Keep previous window count for sliding window counter (Phase 5).
		if exists && !state.WindowStart.Equal(currentWindow) {
			newState.PrevWindowCount = state.WindowCount
		} else if exists {
			newState.PrevWindowCount = state.PrevWindowCount
		}

		ttl := 2 * fw.window // keep previous window available

		if count+n <= fw.rate {
			newState.WindowCount = count + n
			remaining := fw.rate - newState.WindowCount

			swapped, err := fw.store.CompareAndSwap(ctx, key, state, newState, ttl)
			if err != nil {
				return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
			}
			if !swapped {
				continue
			}

			return ratelimiter.Result{
				Allowed:   true,
				Limit:     fw.rate,
				Remaining: remaining,
				ResetAt:   resetAt,
			}, nil
		}

		// Deny — don't increment counter.
		swapped, err := fw.store.CompareAndSwap(ctx, key, state, newState, ttl)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}
		if !swapped {
			continue
		}

		return ratelimiter.Result{
			Allowed:   false,
			Limit:     fw.rate,
			Remaining: 0,
			RetryAt:   resetAt,
			ResetAt:   resetAt,
		}, nil
	}
}

func (fw *FixedWindow) allowRedis(ctx context.Context, rs *store.RedisStore, key string, n int64) (ratelimiter.Result, error) {
	windowMs := fw.window.Milliseconds()
	allowed, remaining, _, windowStartUs, _, err := rs.FixedWindowAllow(ctx, key, windowMs, fw.rate, n)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
	}

	resetAt := time.UnixMicro(windowStartUs).Add(fw.window)

	result := ratelimiter.Result{
		Allowed:   allowed,
		Limit:     fw.rate,
		Remaining: remaining,
		ResetAt:   resetAt,
	}
	if !allowed {
		result.RetryAt = resetAt
	}
	return result, nil
}

func (fw *FixedWindow) Reset(ctx context.Context, key string) error {
	return fw.store.Delete(ctx, key)
}

func (fw *FixedWindow) Close() error {
	return nil
}
