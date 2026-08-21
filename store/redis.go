package store

import (
	"context"
	"fmt"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/redis/go-redis/v9"
)

// RedisStore implements rate limiting state storage in Redis.
// Each algorithm has a dedicated Lua script that performs the entire
// check-and-update atomically in a single round-trip.
type RedisStore struct {
	client redis.UniversalClient
	prefix string

	// Pre-loaded Lua script SHA hashes for each algorithm.
	tokenBucketSHA          string
	leakyBucketSHA          string
	fixedWindowSHA          string
	slidingWindowLogSHA     string
	slidingWindowCounterSHA string
}

// RedisConfig configures the Redis store.
type RedisConfig struct {
	// Client is a pre-configured Redis client. Supports standalone,
	// Cluster, and Sentinel configurations.
	Client redis.UniversalClient

	// Prefix is prepended to all keys. Default: "rl:"
	Prefix string
}

// NewRedisStore creates a Redis-backed store and pre-loads all Lua scripts.
func NewRedisStore(ctx context.Context, cfg RedisConfig) (*RedisStore, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("%w: redis client is required", ratelimiter.ErrInvalidConfig)
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "rl:"
	}

	rs := &RedisStore{
		client: cfg.Client,
		prefix: cfg.Prefix,
	}

	// Pre-load all scripts (SCRIPT LOAD) so subsequent calls use EVALSHA.
	scripts := []struct {
		src string
		dst *string
	}{
		{tokenBucketScript, &rs.tokenBucketSHA},
		{leakyBucketScript, &rs.leakyBucketSHA},
		{fixedWindowScript, &rs.fixedWindowSHA},
		{slidingWindowLogScript, &rs.slidingWindowLogSHA},
		{slidingWindowCounterScript, &rs.slidingWindowCounterSHA},
	}

	for _, s := range scripts {
		sha, err := cfg.Client.ScriptLoad(ctx, s.src).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to load lua script: %w", err)
		}
		*s.dst = sha
	}

	return rs, nil
}

func (rs *RedisStore) key(k string) string {
	return rs.prefix + k
}

// TokenBucketAllow performs an atomic token bucket rate limit check.
// Returns allowed, remaining, retryAtUs, resetAtUs.
func (rs *RedisStore) TokenBucketAllow(ctx context.Context, key string, rate float64, burst, cost int64) (bool, int64, int64, int64, error) {
	result, err := rs.client.EvalSha(ctx, rs.tokenBucketSHA, []string{rs.key(key)},
		rate, burst, cost,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, 0, fmt.Errorf("token bucket script: %w", err)
	}
	return result[0] == 1, result[1], result[2], result[3], nil
}

// LeakyBucketAllow performs an atomic leaky bucket rate limit check.
func (rs *RedisStore) LeakyBucketAllow(ctx context.Context, key string, rate float64, burst, cost int64) (bool, int64, int64, int64, error) {
	result, err := rs.client.EvalSha(ctx, rs.leakyBucketSHA, []string{rs.key(key)},
		rate, burst, cost,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, 0, fmt.Errorf("leaky bucket script: %w", err)
	}
	return result[0] == 1, result[1], result[2], result[3], nil
}

// FixedWindowAllow performs an atomic fixed window rate limit check.
// Returns allowed, remaining, count, windowStartUs, prevCount.
func (rs *RedisStore) FixedWindowAllow(ctx context.Context, key string, windowMs, limit, cost int64) (bool, int64, int64, int64, int64, error) {
	result, err := rs.client.EvalSha(ctx, rs.fixedWindowSHA, []string{rs.key(key)},
		windowMs, limit, cost,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, 0, 0, fmt.Errorf("fixed window script: %w", err)
	}
	return result[0] == 1, result[1], result[2], result[3], result[4], nil
}

// SlidingWindowLogAllow performs an atomic sliding window log rate limit check.
// Returns allowed, remaining, oldestUs.
func (rs *RedisStore) SlidingWindowLogAllow(ctx context.Context, key string, windowMs, limit, cost int64) (bool, int64, int64, error) {
	result, err := rs.client.EvalSha(ctx, rs.slidingWindowLogSHA, []string{rs.key(key)},
		windowMs, limit, cost,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, fmt.Errorf("sliding window log script: %w", err)
	}
	return result[0] == 1, result[1], result[2], nil
}

// SlidingWindowCounterAllow performs an atomic sliding window counter rate limit check.
// Returns allowed, remaining, windowStartUs.
func (rs *RedisStore) SlidingWindowCounterAllow(ctx context.Context, key string, windowMs, limit, cost int64) (bool, int64, int64, error) {
	result, err := rs.client.EvalSha(ctx, rs.slidingWindowCounterSHA, []string{rs.key(key)},
		windowMs, limit, cost,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, fmt.Errorf("sliding window counter script: %w", err)
	}
	return result[0] == 1, result[1], result[2], nil
}

// --- Generic Store interface implementation (fallback for any backend) ---

// Get checks key existence. Algorithms use Lua scripts directly rather than this method.
func (rs *RedisStore) Get(ctx context.Context, key string) (State, bool, error) {
	// Not used when algorithms type-assert to *RedisStore directly.
	// Provided for Store interface compliance.
	exists, err := rs.client.Exists(ctx, rs.key(key)).Result()
	if err != nil {
		return State{}, false, err
	}
	return State{}, exists > 0, nil
}

// Set writes a placeholder value for key with the given TTL.
func (rs *RedisStore) Set(ctx context.Context, key string, state State, ttl time.Duration) error {
	return rs.client.Set(ctx, rs.key(key), "1", ttl).Err()
}

// CompareAndSwap is not supported on RedisStore; algorithms use Lua scripts instead.
func (rs *RedisStore) CompareAndSwap(_ context.Context, _ string, _, _ State, _ time.Duration) (bool, error) {
	// Not used — algorithms use the Lua-script methods instead.
	return false, fmt.Errorf("CompareAndSwap not supported on RedisStore; use algorithm-specific methods")
}

// Delete removes the key from Redis.
func (rs *RedisStore) Delete(ctx context.Context, key string) error {
	return rs.client.Del(ctx, rs.key(key)).Err()
}

// Close closes the underlying Redis client.
func (rs *RedisStore) Close() error {
	return rs.client.Close()
}
