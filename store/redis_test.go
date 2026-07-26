package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupRedis(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	rs, err := NewRedisStore(context.Background(), RedisConfig{
		Client: client,
		Prefix: "test:",
	})
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return rs, mr
}

func TestRedisStore_TokenBucket(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	rate := 10.0
	burst := int64(5)

	// First request: bucket starts full → should allow.
	allowed, remaining, _, _, err := rs.TokenBucketAllow(ctx, "tb1", rate, burst, 1)
	if err != nil {
		t.Fatalf("TokenBucketAllow: %v", err)
	}
	if !allowed {
		t.Fatal("first request should be allowed")
	}
	if remaining != 4 {
		t.Errorf("remaining=%d, want 4", remaining)
	}

	// Exhaust all tokens.
	for i := 0; i < 4; i++ {
		rs.TokenBucketAllow(ctx, "tb1", rate, burst, 1)
	}

	// Should be denied now.
	allowed, _, retryAt, _, err := rs.TokenBucketAllow(ctx, "tb1", rate, burst, 1)
	if err != nil {
		t.Fatalf("TokenBucketAllow: %v", err)
	}
	if allowed {
		t.Fatal("should be denied after exhausting tokens")
	}
	if retryAt == 0 {
		t.Error("retryAt should be set on denial")
	}
}

func TestRedisStore_LeakyBucket(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	rate := 10.0
	burst := int64(3)

	// Fill bucket up to capacity.
	for i := 0; i < 3; i++ {
		allowed, _, _, _, err := rs.LeakyBucketAllow(ctx, "lb1", rate, burst, 1)
		if err != nil {
			t.Fatalf("LeakyBucketAllow request %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Overflow.
	allowed, _, _, _, err := rs.LeakyBucketAllow(ctx, "lb1", rate, burst, 1)
	if err != nil {
		t.Fatalf("LeakyBucketAllow: %v", err)
	}
	if allowed {
		t.Fatal("should overflow")
	}
}

func TestRedisStore_FixedWindow(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	windowMs := int64(60000) // 1 minute
	limit := int64(5)

	for i := int64(0); i < 5; i++ {
		allowed, remaining, _, _, _, err := rs.FixedWindowAllow(ctx, "fw1", windowMs, limit, 1)
		if err != nil {
			t.Fatalf("FixedWindowAllow request %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		if remaining != limit-i-1 {
			t.Errorf("request %d: remaining=%d, want %d", i, remaining, limit-i-1)
		}
	}

	allowed, _, _, _, _, err := rs.FixedWindowAllow(ctx, "fw1", windowMs, limit, 1)
	if err != nil {
		t.Fatalf("FixedWindowAllow: %v", err)
	}
	if allowed {
		t.Fatal("6th request should be denied")
	}
}

func TestRedisStore_SlidingWindowLog(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	windowMs := int64(60000)
	limit := int64(3)

	for i := int64(0); i < 3; i++ {
		allowed, remaining, _, err := rs.SlidingWindowLogAllow(ctx, "swl1", windowMs, limit, 1)
		if err != nil {
			t.Fatalf("SlidingWindowLogAllow request %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		if remaining != limit-i-1 {
			t.Errorf("request %d: remaining=%d, want %d", i, remaining, limit-i-1)
		}
	}

	allowed, _, _, err := rs.SlidingWindowLogAllow(ctx, "swl1", windowMs, limit, 1)
	if err != nil {
		t.Fatalf("SlidingWindowLogAllow: %v", err)
	}
	if allowed {
		t.Fatal("4th request should be denied")
	}
}

func TestRedisStore_SlidingWindowCounter(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	windowMs := int64(60000)
	limit := int64(5)

	for i := int64(0); i < 5; i++ {
		allowed, _, _, err := rs.SlidingWindowCounterAllow(ctx, "swc1", windowMs, limit, 1)
		if err != nil {
			t.Fatalf("SlidingWindowCounterAllow request %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	allowed, _, _, err := rs.SlidingWindowCounterAllow(ctx, "swc1", windowMs, limit, 1)
	if err != nil {
		t.Fatalf("SlidingWindowCounterAllow: %v", err)
	}
	if allowed {
		t.Fatal("6th request should be denied")
	}
}

func TestRedisStore_Delete(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	// Create a key via token bucket.
	rs.TokenBucketAllow(ctx, "del1", 10, 5, 5)

	// Delete it.
	if err := rs.Delete(ctx, "del1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Next call should start fresh (full bucket).
	allowed, remaining, _, _, err := rs.TokenBucketAllow(ctx, "del1", 10, 5, 1)
	if err != nil {
		t.Fatalf("after delete: %v", err)
	}
	if !allowed || remaining != 4 {
		t.Errorf("after delete: allowed=%v, remaining=%d; want allowed, 4", allowed, remaining)
	}
}

func TestRedisStore_KeyPrefix(t *testing.T) {
	rs, mr := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	rs.TokenBucketAllow(ctx, "mykey", 10, 5, 1)

	// Verify the key in Redis has the prefix.
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if k == "test:mykey" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected key 'test:mykey' in Redis, got keys: %v", keys)
	}
}

func TestRedisStore_MultipleKeysIndependent(t *testing.T) {
	rs, _ := setupRedis(t)
	defer rs.Close()
	ctx := context.Background()

	// Exhaust key1.
	for i := 0; i < 3; i++ {
		rs.TokenBucketAllow(ctx, "k1", 10, 3, 1)
	}
	allowed, _, _, _, _ := rs.TokenBucketAllow(ctx, "k1", 10, 3, 1)
	if allowed {
		t.Fatal("k1 should be denied")
	}

	// key2 should be independent.
	allowed, _, _, _, _ = rs.TokenBucketAllow(ctx, "k2", 10, 3, 1)
	if !allowed {
		t.Fatal("k2 should be allowed (independent)")
	}
}
