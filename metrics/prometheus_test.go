package metrics

import (
	"context"
	"strings"
	"testing"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	"github.com/12345debdut/rate-limiter/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newInstrumented(t *testing.T, burst int64) (*InstrumentedLimiter, *prometheus.Registry) {
	t.Helper()
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: burst}, mem)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}

	reg := prometheus.NewRegistry()
	il := NewInstrumentedLimiter(limiter, InstrumentedConfig{
		RuleName:   "test-rule",
		Algorithm:  "token_bucket",
		Registerer: reg,
	})
	return il, reg
}

func TestInstrumented_ImplementsLimiter(t *testing.T) {
	il, _ := newInstrumented(t, 5)
	var _ ratelimiter.Limiter = il
}

func TestInstrumented_AllowedCounter(t *testing.T) {
	il, reg := newInstrumented(t, 5)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		result, err := il.Allow(ctx, "key1")
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	expected := `
# HELP ratelimiter_requests_total Total number of rate limit checks partitioned by decision.
# TYPE ratelimiter_requests_total counter
ratelimiter_requests_total{algorithm="token_bucket",decision="allowed",rule="test-rule"} 3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_requests_total"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_DeniedCounter(t *testing.T) {
	il, reg := newInstrumented(t, 2)
	ctx := context.Background()

	// Exhaust bucket.
	for i := 0; i < 2; i++ {
		il.Allow(ctx, "key1")
	}

	// This one is denied.
	result, err := il.Allow(ctx, "key1")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if result.Allowed {
		t.Fatal("should be denied")
	}

	expected := `
# HELP ratelimiter_requests_total Total number of rate limit checks partitioned by decision.
# TYPE ratelimiter_requests_total counter
ratelimiter_requests_total{algorithm="token_bucket",decision="allowed",rule="test-rule"} 2
ratelimiter_requests_total{algorithm="token_bucket",decision="denied",rule="test-rule"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_requests_total"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_UsageRatio(t *testing.T) {
	il, reg := newInstrumented(t, 10)
	ctx := context.Background()

	// Use 5 out of 10 tokens → usage ratio = 0.5.
	for i := 0; i < 5; i++ {
		il.Allow(ctx, "key1")
	}

	expected := `
# HELP ratelimiter_current_usage_ratio Current usage as a ratio of limit (0.0 to 1.0+).
# TYPE ratelimiter_current_usage_ratio gauge
ratelimiter_current_usage_ratio{algorithm="token_bucket",rule="test-rule"} 0.5
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_current_usage_ratio"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_CheckDuration(t *testing.T) {
	il, reg := newInstrumented(t, 5)
	ctx := context.Background()

	il.Allow(ctx, "key1")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "ratelimiter_check_duration_seconds" {
			found = true
			for _, m := range f.GetMetric() {
				h := m.GetHistogram()
				if h.GetSampleCount() != 1 {
					t.Errorf("sample_count=%d, want 1", h.GetSampleCount())
				}
				if h.GetSampleSum() <= 0 {
					t.Error("sample_sum should be > 0")
				}
			}
		}
	}
	if !found {
		t.Error("ratelimiter_check_duration_seconds not found in gathered metrics")
	}
}

func TestInstrumented_StoreErrors(t *testing.T) {
	reg := prometheus.NewRegistry()
	il := NewInstrumentedLimiter(&failingLimiter{}, InstrumentedConfig{
		RuleName:   "failing-rule",
		Algorithm:  "token_bucket",
		Registerer: reg,
	})
	ctx := context.Background()

	_, err := il.Allow(ctx, "key1")
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = il.Allow(ctx, "key2")
	if err == nil {
		t.Fatal("expected error")
	}

	expected := `
# HELP ratelimiter_store_errors_total Total number of store errors during rate limit checks.
# TYPE ratelimiter_store_errors_total counter
ratelimiter_store_errors_total{algorithm="token_bucket",rule="failing-rule"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_store_errors_total"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_AllowN(t *testing.T) {
	il, reg := newInstrumented(t, 10)
	ctx := context.Background()

	result, err := il.AllowN(ctx, "key1", 3)
	if err != nil {
		t.Fatalf("AllowN: %v", err)
	}
	if !result.Allowed {
		t.Fatal("should be allowed")
	}
	if result.Remaining != 7 {
		t.Errorf("remaining=%d, want 7", result.Remaining)
	}

	// Usage ratio after consuming 3/10 = 0.3.
	expected := `
# HELP ratelimiter_current_usage_ratio Current usage as a ratio of limit (0.0 to 1.0+).
# TYPE ratelimiter_current_usage_ratio gauge
ratelimiter_current_usage_ratio{algorithm="token_bucket",rule="test-rule"} 0.3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_current_usage_ratio"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_Reset(t *testing.T) {
	il, _ := newInstrumented(t, 5)
	ctx := context.Background()

	// Use some tokens.
	il.Allow(ctx, "key1")
	il.Allow(ctx, "key1")

	// Reset.
	if err := il.Reset(ctx, "key1"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// After reset, should get full burst again.
	result, _ := il.Allow(ctx, "key1")
	if !result.Allowed {
		t.Fatal("should be allowed after reset")
	}
	if result.Remaining != 4 {
		t.Errorf("remaining=%d, want 4", result.Remaining)
	}
}

func TestInstrumented_MultipleRulesSharedRegistry(t *testing.T) {
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	reg := prometheus.NewRegistry()

	limiter1, _ := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: 5}, mem)
	il1 := NewInstrumentedLimiter(limiter1, InstrumentedConfig{
		RuleName:   "rule-a",
		Algorithm:  "token_bucket",
		Registerer: reg,
	})

	limiter2, _ := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: 3}, mem)
	il2 := NewInstrumentedLimiter(limiter2, InstrumentedConfig{
		RuleName:   "rule-b",
		Algorithm:  "token_bucket",
		Registerer: reg,
	})

	ctx := context.Background()
	il1.Allow(ctx, "a-key")
	il2.Allow(ctx, "b-key")

	expected := `
# HELP ratelimiter_requests_total Total number of rate limit checks partitioned by decision.
# TYPE ratelimiter_requests_total counter
ratelimiter_requests_total{algorithm="token_bucket",decision="allowed",rule="rule-a"} 1
ratelimiter_requests_total{algorithm="token_bucket",decision="allowed",rule="rule-b"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "ratelimiter_requests_total"); err != nil {
		t.Error(err)
	}
}

func TestInstrumented_CustomNamespace(t *testing.T) {
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	reg := prometheus.NewRegistry()
	limiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: 5}, mem)
	il := NewInstrumentedLimiter(limiter, InstrumentedConfig{
		RuleName:   "custom",
		Algorithm:  "token_bucket",
		Registerer: reg,
		Namespace:  "myapp",
		Subsystem:  "ratelimiter",
	})

	ctx := context.Background()
	il.Allow(ctx, "key1")

	expected := `
# HELP myapp_ratelimiter_requests_total Total number of rate limit checks partitioned by decision.
# TYPE myapp_ratelimiter_requests_total counter
myapp_ratelimiter_requests_total{algorithm="token_bucket",decision="allowed",rule="custom"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "myapp_ratelimiter_requests_total"); err != nil {
		t.Error(err)
	}
}

// failingLimiter always returns an error.
type failingLimiter struct{}

func (f *failingLimiter) Allow(_ context.Context, _ string) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, context.DeadlineExceeded
}

func (f *failingLimiter) AllowN(_ context.Context, _ string, _ int64) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, context.DeadlineExceeded
}

func (f *failingLimiter) Reset(_ context.Context, _ string) error {
	return context.DeadlineExceeded
}

func (f *failingLimiter) Close() error { return nil }
