package algorithm

import (
	"context"
	"fmt"
	"testing"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/store"
)

func benchmarkAlgorithm(b *testing.B, algType Type, cfg ratelimiter.Config) {
	clk := clock.NewMock(time.Now())
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := New(algType, cfg, mem, WithClock(clk))
	if err != nil {
		b.Fatalf("create limiter: %v", err)
	}
	defer limiter.Close()

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow(ctx, "key1")
		// Advance time slightly to avoid all-denied after burst.
		if i%int(cfg.Rate+1) == 0 {
			clk.Advance(time.Second)
		}
	}
}

func BenchmarkTokenBucket_SingleKey(b *testing.B) {
	benchmarkAlgorithm(b, TypeTokenBucket, ratelimiter.Config{Rate: 1000, Burst: 1000})
}

func BenchmarkLeakyBucket_SingleKey(b *testing.B) {
	benchmarkAlgorithm(b, TypeLeakyBucket, ratelimiter.Config{Rate: 1000, Burst: 1000})
}

func BenchmarkFixedWindow_SingleKey(b *testing.B) {
	benchmarkAlgorithm(b, TypeFixedWindow, ratelimiter.Config{Rate: 1000, Window: time.Second})
}

func BenchmarkSlidingWindowLog_SingleKey(b *testing.B) {
	benchmarkAlgorithm(b, TypeSlidingWindowLog, ratelimiter.Config{Rate: 100, Window: time.Second})
}

func BenchmarkSlidingWindowCounter_SingleKey(b *testing.B) {
	benchmarkAlgorithm(b, TypeSlidingWindowCounter, ratelimiter.Config{Rate: 1000, Window: time.Second})
}

func BenchmarkParallelKeys(b *testing.B) {
	types := []struct {
		name string
		typ  Type
		cfg  ratelimiter.Config
	}{
		{"TokenBucket", TypeTokenBucket, ratelimiter.Config{Rate: 100, Burst: 100}},
		{"LeakyBucket", TypeLeakyBucket, ratelimiter.Config{Rate: 100, Burst: 100}},
		{"FixedWindow", TypeFixedWindow, ratelimiter.Config{Rate: 100, Window: time.Second}},
		{"SlidingWindowCounter", TypeSlidingWindowCounter, ratelimiter.Config{Rate: 100, Window: time.Second}},
	}

	for _, tt := range types {
		b.Run(tt.name, func(b *testing.B) {
			clk := clock.NewMock(time.Now())
			mem := store.NewMemory(time.Minute)
			defer mem.Close()

			limiter, _ := New(tt.typ, tt.cfg, mem, WithClock(clk))
			defer limiter.Close()

			ctx := context.Background()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := fmt.Sprintf("key-%d", i%1000)
					limiter.Allow(ctx, key)
					i++
				}
			})
		})
	}
}
