package algorithm

import (
	"context"
	"testing"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/internal/clock"
	"github.com/debdutdev/rate-limiter/store"
)

func newTestSlidingWindowLog(rate int64, window time.Duration, clk *clock.Mock) (*SlidingWindowLog, *store.Memory) {
	mem := store.NewMemory(time.Minute)
	sw, _ := NewSlidingWindowLog(ratelimiter.Config{
		Rate:   rate,
		Window: window,
	}, mem, WithClock(clk))
	return sw, mem
}

func TestSlidingWindowLog_InvalidConfig(t *testing.T) {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	_, err := NewSlidingWindowLog(ratelimiter.Config{Rate: 0, Window: time.Minute}, mem)
	if err == nil {
		t.Fatal("expected error for Rate=0")
	}

	_, err = NewSlidingWindowLog(ratelimiter.Config{Rate: 10, Window: 0}, mem)
	if err == nil {
		t.Fatal("expected error for Window=0")
	}
}

func TestSlidingWindowLog_BasicCounting(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sw, mem := newTestSlidingWindowLog(5, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		result, err := sw.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	result, _ := sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("6th request should be denied")
	}
}

func TestSlidingWindowLog_NoBoundaryBurst(t *testing.T) {
	// This is the key advantage over Fixed Window.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sw, mem := newTestSlidingWindowLog(10, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Send 10 requests at t=50s.
	clk.Advance(50 * time.Second)
	for i := 0; i < 10; i++ {
		sw.Allow(ctx, "key1")
	}

	// Move to t=60s (10 seconds later).
	clk.Advance(10 * time.Second)

	// Unlike Fixed Window, these should be DENIED because the sliding
	// window [0:00, 1:00) still contains all 10 requests from t=50s.
	result, _ := sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied — sliding window still contains 10 requests")
	}
}

func TestSlidingWindowLog_SlidingExpiration(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sw, mem := newTestSlidingWindowLog(3, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Send 3 requests at t=0.
	for i := 0; i < 3; i++ {
		sw.Allow(ctx, "key1")
	}

	// Denied at t=0.
	result, _ := sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}

	// Advance 61s — all timestamps from t=0 expire.
	clk.Advance(61 * time.Second)

	result, _ = sw.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after all timestamps expired")
	}
	if result.Remaining != 2 {
		t.Errorf("remaining=%d, want 2", result.Remaining)
	}
}

func TestSlidingWindowLog_PartialExpiration(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sw, mem := newTestSlidingWindowLog(3, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// t=0: 1 request.
	sw.Allow(ctx, "key1")

	// t=30s: 2 more requests.
	clk.Advance(30 * time.Second)
	sw.Allow(ctx, "key1")
	sw.Allow(ctx, "key1")

	// t=30s: denied (3 in window).
	result, _ := sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}

	// t=61s: the first timestamp (t=0) expires, but the two from t=30s remain.
	clk.Advance(31 * time.Second)

	result, _ = sw.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed (1 slot freed)")
	}

	result, _ = sw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied again (window has 3)")
	}
}

func TestSlidingWindowLog_Reset(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	sw, mem := newTestSlidingWindowLog(2, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	sw.Allow(ctx, "key1")
	sw.Allow(ctx, "key1")
	sw.Reset(ctx, "key1")

	result, _ := sw.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
}

func TestSlidingWindowLog_ImplementsLimiter(t *testing.T) {
	var _ ratelimiter.Limiter = &SlidingWindowLog{}
}
