package algorithm

import (
	"context"
	"fmt"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/internal/clock"
	"github.com/debdutdev/rate-limiter/store"
)

// SlidingWindowLog implements rate limiting by tracking individual
// request timestamps within a sliding window.
//
// Config.Rate   = maximum requests allowed within the window.
// Config.Window = sliding window duration.
//
// This is the most precise algorithm — no boundary burst issues.
// However, it uses O(Rate) memory per key because it stores every
// timestamp. For high-rate limits (e.g. 10,000/sec), prefer
// SlidingWindowCounter instead.
type SlidingWindowLog struct {
	rate   int64
	window time.Duration
	store  store.Store
	clock  clock.Clock
}

func NewSlidingWindowLog(cfg ratelimiter.Config, s store.Store, opts ...Option) (*SlidingWindowLog, error) {
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("%w: rate must be > 0", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: window must be > 0", ratelimiter.ErrInvalidConfig)
	}

	o := applyOptions(opts)
	return &SlidingWindowLog{
		rate:   cfg.Rate,
		window: cfg.Window,
		store:  s,
		clock:  o.clock,
	}, nil
}

func (sw *SlidingWindowLog) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return sw.AllowN(ctx, key, 1)
}

func (sw *SlidingWindowLog) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	if rs, ok := sw.store.(*store.RedisStore); ok {
		return sw.allowRedis(ctx, rs, key, n)
	}

	now := sw.clock.Now()
	cutoff := now.Add(-sw.window).UnixNano()

	for {
		state, exists, err := sw.store.Get(ctx, key)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}

		var version uint64
		var timestamps []int64
		if exists {
			version = state.Version
			// Filter out expired timestamps.
			for _, ts := range state.Timestamps {
				if ts > cutoff {
					timestamps = append(timestamps, ts)
				}
			}
		}

		count := int64(len(timestamps))

		newState := store.State{
			Version: version + 1,
		}

		if count+n <= sw.rate {
			// Add n timestamps for the current request.
			for i := int64(0); i < n; i++ {
				timestamps = append(timestamps, now.UnixNano())
			}
			newState.Timestamps = timestamps
			remaining := sw.rate - count - n

			var resetAt time.Time
			if len(timestamps) > 0 {
				oldest := time.Unix(0, timestamps[0])
				resetAt = oldest.Add(sw.window)
			} else {
				resetAt = now.Add(sw.window)
			}

			swapped, err := sw.store.CompareAndSwap(ctx, key, state, newState, sw.window+time.Second)
			if err != nil {
				return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
			}
			if !swapped {
				continue
			}

			return ratelimiter.Result{
				Allowed:   true,
				Limit:     sw.rate,
				Remaining: remaining,
				ResetAt:   resetAt,
			}, nil
		}

		// Deny.
		newState.Timestamps = timestamps
		var retryAt time.Time
		if len(timestamps) > 0 {
			// Earliest timestamp will expire first, opening a slot.
			retryAt = time.Unix(0, timestamps[0]).Add(sw.window)
		} else {
			retryAt = now.Add(sw.window)
		}

		swapped, err := sw.store.CompareAndSwap(ctx, key, state, newState, sw.window+time.Second)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}
		if !swapped {
			continue
		}

		return ratelimiter.Result{
			Allowed:   false,
			Limit:     sw.rate,
			Remaining: 0,
			RetryAt:   retryAt,
			ResetAt:   retryAt,
		}, nil
	}
}

func (sw *SlidingWindowLog) allowRedis(ctx context.Context, rs *store.RedisStore, key string, n int64) (ratelimiter.Result, error) {
	windowMs := sw.window.Milliseconds()
	allowed, remaining, oldestUs, err := rs.SlidingWindowLogAllow(ctx, key, windowMs, sw.rate, n)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
	}

	result := ratelimiter.Result{
		Allowed:   allowed,
		Limit:     sw.rate,
		Remaining: remaining,
	}
	if !allowed && oldestUs > 0 {
		result.RetryAt = time.UnixMicro(oldestUs).Add(sw.window)
		result.ResetAt = result.RetryAt
	}
	return result, nil
}

func (sw *SlidingWindowLog) Reset(ctx context.Context, key string) error {
	return sw.store.Delete(ctx, key)
}

func (sw *SlidingWindowLog) Close() error {
	return nil
}
