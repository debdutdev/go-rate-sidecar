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

// LeakyBucket implements the leaky bucket rate limiting algorithm.
//
// Config.Rate = drain rate (requests leaked per second).
// Config.Burst = bucket capacity (max queue depth).
//
// Unlike Token Bucket, Leaky Bucket enforces a smooth output rate
// with no bursts. The queue drains lazily on each Allow call.
type LeakyBucket struct {
	rate  float64 // drain rate (requests per second)
	burst int64   // max queue depth
	store store.Store
	clock clock.Clock
}

func NewLeakyBucket(cfg ratelimiter.Config, s store.Store, opts ...Option) (*LeakyBucket, error) {
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("%w: rate must be > 0", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Burst <= 0 {
		return nil, fmt.Errorf("%w: burst must be > 0", ratelimiter.ErrInvalidConfig)
	}

	o := applyOptions(opts)
	return &LeakyBucket{
		rate:  float64(cfg.Rate),
		burst: cfg.Burst,
		store: s,
		clock: o.clock,
	}, nil
}

func (lb *LeakyBucket) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return lb.AllowN(ctx, key, 1)
}

func (lb *LeakyBucket) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	if rs, ok := lb.store.(*store.RedisStore); ok {
		return lb.allowRedis(ctx, rs, key, n)
	}

	now := lb.clock.Now()
	cost := float64(n)

	for {
		state, exists, err := lb.store.Get(ctx, key)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}

		var queue float64
		var version uint64
		if !exists {
			queue = 0
		} else {
			version = state.Version
			elapsed := now.Sub(state.LastDrain).Seconds()
			leaked := lb.rate * elapsed
			queue = math.Max(0, float64(state.QueueSize)-leaked)
		}

		newState := store.State{
			LastDrain: now,
			Version:   version + 1,
		}

		ttl := time.Duration(float64(lb.burst)/lb.rate*1000)*time.Millisecond + time.Second

		if queue+cost <= float64(lb.burst) {
			newState.QueueSize = int64(math.Ceil(queue + cost))
			remaining := lb.burst - newState.QueueSize
			resetAt := now.Add(time.Duration(float64(newState.QueueSize)/lb.rate*1000) * time.Millisecond)

			swapped, err := lb.store.CompareAndSwap(ctx, key, state, newState, ttl)
			if err != nil {
				return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
			}
			if !swapped {
				continue
			}

			return ratelimiter.Result{
				Allowed:   true,
				Limit:     lb.burst,
				Remaining: remaining,
				ResetAt:   resetAt,
			}, nil
		}

		// Overflow — deny.
		newState.QueueSize = int64(math.Ceil(queue))
		retryAfter := time.Duration((queue+cost-float64(lb.burst))/lb.rate*1000) * time.Millisecond
		resetAt := now.Add(time.Duration(queue/lb.rate*1000) * time.Millisecond)

		swapped, err := lb.store.CompareAndSwap(ctx, key, state, newState, ttl)
		if err != nil {
			return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
		}
		if !swapped {
			continue
		}

		return ratelimiter.Result{
			Allowed:   false,
			Limit:     lb.burst,
			Remaining: 0,
			RetryAt:   now.Add(retryAfter),
			ResetAt:   resetAt,
		}, nil
	}
}

func (lb *LeakyBucket) allowRedis(ctx context.Context, rs *store.RedisStore, key string, n int64) (ratelimiter.Result, error) {
	allowed, remaining, retryAtUs, resetAtUs, err := rs.LeakyBucketAllow(ctx, key, lb.rate, lb.burst, n)
	if err != nil {
		return ratelimiter.Result{}, fmt.Errorf("%w: %v", ratelimiter.ErrStoreFailed, err)
	}

	result := ratelimiter.Result{
		Allowed:   allowed,
		Limit:     lb.burst,
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

func (lb *LeakyBucket) Reset(ctx context.Context, key string) error {
	return lb.store.Delete(ctx, key)
}

func (lb *LeakyBucket) Close() error {
	return nil
}
