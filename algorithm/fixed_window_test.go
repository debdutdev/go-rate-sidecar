package algorithm

import (
	"context"
	"testing"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/internal/clock"
	"github.com/debdutdev/rate-limiter/store"
)

func newTestFixedWindow(rate int64, window time.Duration, clk *clock.Mock) (*FixedWindow, *store.Memory) {
	mem := store.NewMemory(time.Minute)
	fw, _ := NewFixedWindow(ratelimiter.Config{
		Rate:   rate,
		Window: window,
	}, mem, WithClock(clk))
	return fw, mem
}

func TestFixedWindow_InvalidConfig(t *testing.T) {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	_, err := NewFixedWindow(ratelimiter.Config{Rate: 0, Window: time.Minute}, mem)
	if err == nil {
		t.Fatal("expected error for Rate=0")
	}

	_, err = NewFixedWindow(ratelimiter.Config{Rate: 10, Window: 0}, mem)
	if err == nil {
		t.Fatal("expected error for Window=0")
	}
}

func TestFixedWindow_BasicCounting(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(5, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Allow exactly 5.
	for i := 0; i < 5; i++ {
		result, err := fw.Allow(ctx, "key1")
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

	// 6th should be denied.
	result, _ := fw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("6th request should be denied")
	}
	if result.Remaining != 0 {
		t.Errorf("remaining=%d, want 0", result.Remaining)
	}
}

func TestFixedWindow_ResetOnNewWindow(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(3, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Exhaust window.
	for i := 0; i < 3; i++ {
		fw.Allow(ctx, "key1")
	}
	result, _ := fw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied in this window")
	}

	// Advance to next window.
	clk.Advance(time.Minute)

	// Counter should reset.
	result, _ = fw.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed in new window")
	}
	if result.Remaining != 2 {
		t.Errorf("remaining=%d, want 2 (fresh window)", result.Remaining)
	}
}

func TestFixedWindow_BoundaryBurst(t *testing.T) {
	// Demonstrates the known weakness of Fixed Window.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(10, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	// Move to 50s into the first window.
	clk.Advance(50 * time.Second)

	// Send 10 requests at the end of window 1.
	for i := 0; i < 10; i++ {
		result, _ := fw.Allow(ctx, "key1")
		if !result.Allowed {
			t.Fatalf("end-of-window request %d should be allowed", i)
		}
	}

	// Move to the start of window 2 (10 seconds later).
	clk.Advance(10 * time.Second)

	// Send 10 more requests at the start of window 2.
	allowed := 0
	for i := 0; i < 10; i++ {
		result, _ := fw.Allow(ctx, "key1")
		if result.Allowed {
			allowed++
		}
	}

	// All 10 should be allowed — this is the boundary burst:
	// 20 requests in 20 seconds, even though the limit is 10/minute.
	if allowed != 10 {
		t.Errorf("boundary burst: allowed=%d in new window, want 10 (demonstrating the weakness)", allowed)
	}
}

func TestFixedWindow_RetryAtPointsToWindowEnd(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(1, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	fw.Allow(ctx, "key1")
	result, _ := fw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("should be denied")
	}

	expectedRetry := start.Add(time.Minute)
	if !result.RetryAt.Equal(expectedRetry) {
		t.Errorf("RetryAt=%v, want %v (next window)", result.RetryAt, expectedRetry)
	}
}

func TestFixedWindow_MultipleKeys(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(2, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	fw.Allow(ctx, "key1")
	fw.Allow(ctx, "key1")
	result, _ := fw.Allow(ctx, "key1")
	if result.Allowed {
		t.Fatal("key1 should be exhausted")
	}

	result, _ = fw.Allow(ctx, "key2")
	if !result.Allowed {
		t.Fatal("key2 should be independent")
	}
}

func TestFixedWindow_Reset(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(2, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	fw.Allow(ctx, "key1")
	fw.Allow(ctx, "key1")
	fw.Reset(ctx, "key1")

	result, _ := fw.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
}

func TestFixedWindow_PreservesPrevWindowCount(t *testing.T) {
	// Verify that when the window rolls over, PrevWindowCount is set
	// (needed for Sliding Window Counter in Phase 5).
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewMock(start)
	fw, mem := newTestFixedWindow(10, time.Minute, clk)
	defer mem.Close()
	ctx := context.Background()

	for i := 0; i < 7; i++ {
		fw.Allow(ctx, "key1")
	}

	// Roll to next window.
	clk.Advance(time.Minute)
	fw.Allow(ctx, "key1") // triggers rollover

	// Check stored state.
	state, exists, _ := mem.Get(ctx, "key1")
	if !exists {
		t.Fatal("key should exist")
	}
	if state.PrevWindowCount != 7 {
		t.Errorf("PrevWindowCount=%d, want 7", state.PrevWindowCount)
	}
}

func TestFixedWindow_ImplementsLimiter(t *testing.T) {
	var _ ratelimiter.Limiter = &FixedWindow{}
}
