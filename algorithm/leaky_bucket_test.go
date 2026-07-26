package algorithm

import (
	"context"
	"testing"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/store"
)

func newTestLeakyBucket(rate, burst int64, clk *clock.Mock) (*LeakyBucket, *store.Memory) {
	mem := store.NewMemory(time.Minute)
	lb, _ := NewLeakyBucket(ratelimiter.Config{
		Rate:  rate,
		Burst: burst,
	}, mem, WithClock(clk))
	return lb, mem
}

func TestLeakyBucket_InvalidConfig(t *testing.T) {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	_, err := NewLeakyBucket(ratelimiter.Config{Rate: 0, Burst: 10}, mem)
	if err == nil {
		t.Fatal("expected error for Rate=0")
	}

	_, err = NewLeakyBucket(ratelimiter.Config{Rate: 10, Burst: 0}, mem)
	if err == nil {
		t.Fatal("expected error for Burst=0")
	}
}

func TestLeakyBucket_AllowUpToCapacity(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(10, 5, clk)
	defer mem.Close()
	ctx := context.Background()

	// Should allow up to capacity (5).
	for i := 0; i < 5; i++ {
		result, err := lb.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// 6th should overflow.
	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("6th request should overflow")
	}
}

func TestLeakyBucket_DrainOverTime(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(2, 4, clk) // drains 2/sec, capacity 4
	defer mem.Close()
	ctx := context.Background()

	// Fill the bucket.
	for i := 0; i < 4; i++ {
		lb.Allow(ctx, "key1")
	}
	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("bucket should be full")
	}

	// Advance 1s → drain 2 slots.
	clk.Advance(time.Second)

	result, _ = lb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after draining 2 slots")
	}

	result, _ = lb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should still be allowed (1 more drained slot)")
	}

	// Now full again.
	result, _ = lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should overflow again")
	}
}

func TestLeakyBucket_SmoothRate(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(1, 1, clk) // 1/sec, capacity 1
	defer mem.Close()
	ctx := context.Background()

	// First request fills the single slot.
	result, _ := lb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("first request should be allowed")
	}

	// Immediately, second should be denied.
	result, _ = lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("second request should be denied immediately")
	}

	// After 1s, bucket drains → one more allowed.
	clk.Advance(time.Second)
	result, _ = lb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("after 1s drain, should be allowed")
	}
}

func TestLeakyBucket_NoInstantBurst(t *testing.T) {
	// This test demonstrates the key difference from Token Bucket:
	// Leaky Bucket does NOT allow instant burst beyond capacity.
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(10, 5, clk)
	defer mem.Close()
	ctx := context.Background()

	allowed := 0
	for i := 0; i < 20; i++ {
		result, _ := lb.Allow(ctx, "key1")
		if result.Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("allowed=%d, want exactly 5 (capacity, no extra burst)", allowed)
	}
}

func TestLeakyBucket_MultipleKeys(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(10, 2, clk)
	defer mem.Close()
	ctx := context.Background()

	// Fill key1.
	lb.Allow(ctx, "key1")
	lb.Allow(ctx, "key1")
	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("key1 should be full")
	}

	// key2 is independent.
	result, _ = lb.Allow(ctx, "key2")
	if !result.Allowed {
		t.Fatal("key2 should be independent")
	}
}

func TestLeakyBucket_Reset(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(10, 3, clk)
	defer mem.Close()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		lb.Allow(ctx, "key1")
	}
	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be full")
	}

	lb.Reset(ctx, "key1")
	result, _ = lb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
}

func TestLeakyBucket_RetryAt(t *testing.T) {
	clk := clock.NewMock(time.Now())
	lb, mem := newTestLeakyBucket(2, 2, clk) // 2/sec
	defer mem.Close()
	ctx := context.Background()

	lb.Allow(ctx, "key1")
	lb.Allow(ctx, "key1")
	result, _ := lb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}
	if result.RetryAt.IsZero() {
		t.Error("RetryAt should be set on denial")
	}
}

func TestLeakyBucket_ImplementsLimiter(t *testing.T) {
	var _ ratelimiter.Limiter = &LeakyBucket{}
}
