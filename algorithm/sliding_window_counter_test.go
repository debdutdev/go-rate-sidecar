package algorithm

import (
	"context"
	"testing"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/internal/clock"
	"github.com/debdutdev/rate-limiter/store"
)

func newTestSlidingWindowCounter(rate int64, window time.Duration, clk *clock.Mock) (*SlidingWindowCounter, *store.Memory) {
	mem := store.NewMemory(time.Minute)
	sc, _ := NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   rate,
		Window: window,
	}, mem, WithClock(clk))
	return sc, mem
}

func TestSlidingWindowCounter_InvalidConfig(t *testing.T) {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	_, err := NewSlidingWindowCounter(ratelimiter.Config{Rate: 0, Window: time.Minute}, mem)
	if err == nil {
		t.Fatal("expected error for Rate=0")
	}

	_, err = NewSlidingWindowCounter(ratelimiter.Config{Rate: 10, Window: 0}, mem)
	if err == nil {
		t.Fatal("expected error for Window=0")
	}
}

func TestSlidingWindowCounter_BasicCounting(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sc, mem := newTestSlidingWindowCounter(5, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		result, err := sc.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := sc.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("6th request should be denied")
	}
}

func TestSlidingWindowCounter_WeightedOverlap(t *testing.T) {
	// Demonstrates the weighted calculation at the boundary.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sc, mem := newTestSlidingWindowCounter(10, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Send 8 requests in the first window.
	for i := 0; i < 8; i++ {
		sc.Allow(ctx, "key1")
	}

	// Move to 30s into the second window (50% overlap with previous).
	// weight = 1 - 0.5 = 0.5
	// estimated = 8 × 0.5 + 0 = 4.0
	// So we should be able to send 6 more (4.0 + 6 = 10).
	clk.Advance(time.Minute + 30*time.Second)

	allowed := 0
	for i := 0; i < 10; i++ {
		result, _ := sc.Allow(ctx, "key1")
		if result.Allowed {
			allowed++
		}
	}

	if allowed != 6 {
		t.Errorf("allowed=%d, want 6 (weighted estimate: 8×0.5 + current)", allowed)
	}
}

func TestSlidingWindowCounter_ReducesBoundaryBurst(t *testing.T) {
	// Compare with Fixed Window's boundary burst.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sc, mem := newTestSlidingWindowCounter(10, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Send 10 requests at t=50s (end of window 1).
	clk.Advance(50 * time.Second)
	for i := 0; i < 10; i++ {
		sc.Allow(ctx, "key1")
	}

	// Move to t=60s (start of window 2).
	// weight = 1 - (0/60) = 1.0 (just entered new window)
	// estimated = 10 × 1.0 + 0 = 10
	// No more requests should be allowed!
	clk.Advance(10 * time.Second)

	result, _ := sc.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied — weighted estimate is 10 (full)")
	}

	// Move 30s further into window 2 (t=90s).
	// weight = 1 - (30/60) = 0.5
	// estimated = 10 × 0.5 + 0 = 5.0
	// Should allow 5 more.
	clk.Advance(30 * time.Second)

	allowed := 0
	for i := 0; i < 10; i++ {
		result, _ := sc.Allow(ctx, "key1")
		if result.Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("at 50%% into window 2: allowed=%d, want 5", allowed)
	}
}

func TestSlidingWindowCounter_FullWindowTransition(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sc, mem := newTestSlidingWindowCounter(5, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Fill first window.
	for i := 0; i < 5; i++ {
		sc.Allow(ctx, "key1")
	}

	// Skip entirely past the next window (2 windows ahead).
	// Previous window count should be 0 (the window we skipped).
	clk.Advance(2*time.Minute + time.Second)

	// Should have full allowance.
	for i := 0; i < 5; i++ {
		result, _ := sc.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed after 2 full windows", i)
		}
	}
}

func TestSlidingWindowCounter_Reset(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sc, mem := newTestSlidingWindowCounter(3, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sc.Allow(ctx, "key1")
	}
	sc.Reset(ctx, "key1")

	result, _ := sc.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
}

func TestSlidingWindowCounter_ImplementsLimiter(t *testing.T) {
	var _ ratelimiter.Limiter = &SlidingWindowCounter{}
}
