package algorithm

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/store"
)

func setupRedisStore(t *testing.T) *store.RedisStore {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rs, err := store.NewRedisStore(context.Background(), store.RedisConfig{
		Client: client,
		Prefix: "test:",
	})
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return rs
}

func TestRedisIntegration_TokenBucket(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	tb, err := NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: 5}, rs)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}

	// Allow up to burst.
	for i := 0; i < 5; i++ {
		result, err := tb.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Deny.
	result, err := tb.Allow(ctx, "key1")
	if err != nil {
		t.Fatalf("6th request: %v", err)
	}
	if result.Allowed {
		t.Fatal("6th should be denied")
	}

	// Reset and verify.
	tb.Reset(ctx, "key1")
	result, _ = tb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
}

func TestRedisIntegration_LeakyBucket(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	lb, err := NewLeakyBucket(ratelimiter.Config{Rate: 10, Burst: 3}, rs)
	if err != nil {
		t.Fatalf("NewLeakyBucket: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, _ := lb.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should overflow")
	}
}

func TestRedisIntegration_FixedWindow(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	fw, err := NewFixedWindow(ratelimiter.Config{Rate: 3, Window: time.Minute}, rs)
	if err != nil {
		t.Fatalf("NewFixedWindow: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, _ := fw.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := fw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}
}

func TestRedisIntegration_SlidingWindowLog(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	sw, err := NewSlidingWindowLog(ratelimiter.Config{Rate: 3, Window: time.Minute}, rs)
	if err != nil {
		t.Fatalf("NewSlidingWindowLog: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, _ := sw.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}
}

func TestRedisIntegration_SlidingWindowCounter(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	sc, err := NewSlidingWindowCounter(ratelimiter.Config{Rate: 3, Window: time.Minute}, rs)
	if err != nil {
		t.Fatalf("NewSlidingWindowCounter: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, _ := sc.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := sc.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}
}

func TestRedisIntegration_Factory(t *testing.T) {
	rs := setupRedisStore(t)
	defer rs.Close()
	ctx := context.Background()

	// Use the factory to create each algorithm against Redis.
	types := []struct {
		name string
		typ  Type
		cfg  ratelimiter.Config
	}{
		{"TokenBucket", TypeTokenBucket, ratelimiter.Config{Rate: 10, Burst: 3}},
		{"LeakyBucket", TypeLeakyBucket, ratelimiter.Config{Rate: 10, Burst: 3}},
		{"FixedWindow", TypeFixedWindow, ratelimiter.Config{Rate: 3, Window: time.Minute}},
		{"SlidingWindowLog", TypeSlidingWindowLog, ratelimiter.Config{Rate: 3, Window: time.Minute}},
		{"SlidingWindowCounter", TypeSlidingWindowCounter, ratelimiter.Config{Rate: 3, Window: time.Minute}},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			limiter, err := New(tt.typ, tt.cfg, rs)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer limiter.Close()

			key := "factory-" + tt.name
			for i := 0; i < 3; i++ {
				result, err := limiter.Allow(ctx, key)
				if err != nil {
					t.Fatalf("request %d: %v", i, err)
				}
				if !result.Allowed {
					t.Fatalf("request %d should be allowed", i)
				}
			}

			result, _ := limiter.Allow(ctx, key)
			if result.Allowed {
				t.Fatal("should be denied after exhausting limit")
			}
		})
	}
}
