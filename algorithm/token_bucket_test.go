package algorithm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/store"
)

func newTestTokenBucket(rate, burst int64, clk *clock.Mock) (*TokenBucket, *store.Memory) {
	mem := store.NewMemory(time.Minute)
	tb, _ := NewTokenBucket(ratelimiter.Config{
		Rate:  rate,
		Burst: burst,
	}, mem, WithClock(clk))
	return tb, mem
}

func TestTokenBucket_InvalidConfig(t *testing.T) {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	_, err := NewTokenBucket(ratelimiter.Config{Rate: 0, Burst: 10}, mem)
	if err == nil {
		t.Fatal("expected error for Rate=0")
	}

	_, err = NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: 0}, mem)
	if err == nil {
		t.Fatal("expected error for Burst=0")
	}
}

func TestTokenBucket_AllowUpToBurst(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 5, clk)
	defer mem.Close()
	ctx := context.Background()

	// Should allow exactly Burst (5) requests.
	for i := 0; i < 5; i++ {
		result, err := tb.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		if result.Remaining != int64(4-i) {
			t.Errorf("request %d: remaining=%d, want %d", i, result.Remaining, 4-i)
		}
	}

	// 6th request should be denied.
	result, err := tb.Allow(ctx, "key1")
	if err != nil {
		t.Fatalf("6th request: %v", err)
	}
	if result.Allowed {
		t.Fatal("6th request should be denied")
	}
	if result.RetryAt.IsZero() {
		t.Error("RetryAt should be set on denial")
	}
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 5, clk) // 10 tokens/sec
	defer mem.Close()
	ctx := context.Background()

	// Consume all 5 tokens.
	for i := 0; i < 5; i++ {
		tb.Allow(ctx, "key1")
	}

	// Denied now.
	result, _ := tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied with 0 tokens")
	}

	// Advance 500ms → should refill 5 tokens (10/sec × 0.5s = 5).
	clk.Advance(500 * time.Millisecond)

	for i := 0; i < 5; i++ {
		result, err := tb.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("after refill, request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("after refill, request %d should be allowed", i)
		}
	}

	// Should be denied again.
	result, _ = tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied again after consuming refilled tokens")
	}
}

func TestTokenBucket_PartialRefill(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(2, 5, clk) // 2 tokens/sec
	defer mem.Close()
	ctx := context.Background()

	// Consume all tokens.
	for i := 0; i < 5; i++ {
		tb.Allow(ctx, "key1")
	}

	// Advance 1s → refill 2 tokens.
	clk.Advance(time.Second)

	result, _ := tb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after 1s refill (2 tokens)")
	}

	result, _ = tb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should still be allowed (1 token left)")
	}

	result, _ = tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied (0 tokens)")
	}
}

func TestTokenBucket_RefillCapsAtBurst(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(100, 5, clk) // 100/sec but max 5
	defer mem.Close()
	ctx := context.Background()

	// Consume 2 tokens (3 remaining).
	tb.Allow(ctx, "key1")
	tb.Allow(ctx, "key1")

	// Advance 10s → would add 1000 tokens, but capped at 5.
	clk.Advance(10 * time.Second)

	// Should allow 5, then deny.
	for i := 0; i < 5; i++ {
		result, _ := tb.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed (capped refill)", i)
		}
	}
	result, _ := tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("6th should be denied even after long wait (capped at burst)")
	}
}

func TestTokenBucket_AllowN(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 10, clk)
	defer mem.Close()
	ctx := context.Background()

	// AllowN with n=5 should consume 5 tokens.
	result, err := tb.AllowN(ctx, "key1", 5)
	if err != nil {
		t.Fatalf("AllowN: %v", err)
	}
	if !result.Allowed || result.Remaining != 5 {
		t.Errorf("AllowN(5): allowed=%v, remaining=%d", result.Allowed, result.Remaining)
	}

	// AllowN with n=6 should be denied (only 5 left).
	result, _ = tb.AllowN(ctx, "key1", 6)
	if result.Allowed {
		t.Fatal("AllowN(6) should be denied with only 5 tokens")
	}

	// AllowN with n=5 should still work.
	result, _ = tb.AllowN(ctx, "key1", 5)
	if !result.Allowed {
		t.Fatal("AllowN(5) should succeed with 5 tokens")
	}
}

func TestTokenBucket_MultipleKeys(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 3, clk)
	defer mem.Close()
	ctx := context.Background()

	// Exhaust key1.
	for i := 0; i < 3; i++ {
		tb.Allow(ctx, "key1")
	}
	result, _ := tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("key1 should be exhausted")
	}

	// key2 should still have its own bucket.
	result, _ = tb.Allow(ctx, "key2")
	if !result.Allowed {
		t.Fatal("key2 should be independent of key1")
	}
}

func TestTokenBucket_Reset(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 5, clk)
	defer mem.Close()
	ctx := context.Background()

	// Exhaust tokens.
	for i := 0; i < 5; i++ {
		tb.Allow(ctx, "key1")
	}
	result, _ := tb.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}

	// Reset and try again.
	tb.Reset(ctx, "key1")

	result, _ = tb.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
	if result.Remaining != 4 {
		t.Errorf("remaining=%d after reset, want 4", result.Remaining)
	}
}

func TestTokenBucket_ResultFields(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(10, 5, clk)
	defer mem.Close()
	ctx := context.Background()

	result, _ := tb.Allow(ctx, "key1")
	if result.Limit != 5 {
		t.Errorf("Limit=%d, want 5", result.Limit)
	}
	if result.ResetAt.IsZero() {
		t.Error("ResetAt should be set")
	}
}

func TestTokenBucket_ConcurrentSingleKey(t *testing.T) {
	clk := clock.NewMock(time.Now())
	tb, mem := newTestTokenBucket(1000, 100, clk) // burst=100
	defer mem.Close()
	ctx := context.Background()

	var allowed atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := tb.Allow(ctx, "key1")
			if err != nil {
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	got := allowed.Load()
	if got != 100 {
		t.Errorf("concurrent allowed=%d, want exactly 100 (burst capacity)", got)
	}
}

func TestTokenBucket_ImplementsLimiter(t *testing.T) {
	var _ ratelimiter.Limiter = &TokenBucket{}
}
