package algorithm

import (
	"context"
	"fmt"
	"math"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/store"
)

// TokenBucket implements the token bucket rate limiting algorithm.
//
// Config.Rate = tokens added per second.
// Config.Burst = maximum bucket capacity (also the initial token count).
//
// Tokens refill lazily on each Allow call — no background goroutines.
type TokenBucket struct {
	rate  float64 // tokens per second
	burst int64   // bucket capacity
	store store.Store
	clock clock.Clock
}

func NewTokenBucket(cfg ratelimiter.Config, s store.Store, opts ...Option) (*TokenBucket, error) {
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("%w: rate must be > 0", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Burst <= 0 {
		return nil, fmt.Errorf("%w: burst must be > 0", ratelimiter.ErrInvalidConfig)
	}

	o := applyOptions(opts)
	return &TokenBucket{
		rate:  float64(cfg.Rate),
		burst: cfg.Burst,
		store: s,
		clock: o.clock,
	}, nil
}

func (tb *TokenBucket) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return tb.AllowN(ctx, key, 1)
}

func (tb *TokenBucket) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	// Optimized Redis path — single atomic Lua script call.
	if rs, ok := tb.store.(*store.RedisStore); ok {
		return tb.allowRedis(ctx, rs, key, n)
	}

	now := tb.clock.Now()
	cost := float64(n)

	for {
		state, exists, err := tb.store.Get(ctx, key)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}

		var tokens float64
		var version uint64
		if !exists {
			tokens = float64(tb.burst)
		} else {
			version = state.Version
			elapsed := now.Sub(state.LastRefill).Seconds()
			tokens = math.Min(float64(tb.burst), state.Tokens+tb.rate*elapsed)
		}

		newState := store.State{
			LastRefill: now,
			Version:    version + 1,
		}

		ttl := time.Duration(float64(tb.burst)/tb.rate*1000)*time.Millisecond + time.Second

		if tokens >= cost {
			newState.Tokens = tokens - cost
			remaining := int64(newState.Tokens)
			resetAt := now.Add(time.Duration(float64(tb.burst-remaining)/tb.rate*1000) * time.Millisecond)

			swapped, err := tb.store.CompareAndSwap(ctx, key, state, newState, ttl)
			if err != nil {
				return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
			}
			if !swapped {
				continue // CAS retry
			}

			return ratelimiter.Result{
				Allowed:   true,
				Limit:     tb.burst,
				Remaining: remaining,
				ResetAt:   resetAt,
			}, nil
		}

		// Not enough tokens — deny.
		newState.Tokens = tokens
		retryAfter := time.Duration((cost-tokens)/tb.rate*1000) * time.Millisecond
		resetAt := now.Add(time.Duration(float64(tb.burst)/tb.rate*1000) * time.Millisecond)

		swapped, err := tb.store.CompareAndSwap(ctx, key, state, newState, ttl)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}
		if !swapped {
			continue
		}

		return ratelimiter.Result{
			Allowed:   false,
			Limit:     tb.burst,
			Remaining: int64(tokens),
			RetryAt:   now.Add(retryAfter),
			ResetAt:   resetAt,
		}, nil
	}
}

func (tb *TokenBucket) allowRedis(ctx context.Context, rs *store.RedisStore, key string, n int64) (ratelimiter.Result, error) {
	allowed, remaining, retryAtUs, resetAtUs, err := rs.TokenBucketAllow(ctx, key, tb.rate, tb.burst, n)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
	}

	result := ratelimiter.Result{
		Allowed:   allowed,
		Limit:     tb.burst,
		Remaining: remaining,
	}
	if resetAtUs > 0 {
		result.ResetAt = time.UnixMicro(resetAtUs)
	}
	if !allowed && retryAtUs > 0 {
		result.RetryAt = time.UnixMicro(retryAtUs)
	}
	return result, nil
}

func (tb *TokenBucket) Reset(ctx context.Context, key string) error {
	return tb.store.Delete(ctx, key)
}

func (tb *TokenBucket) Close() error {
	return nil
}
