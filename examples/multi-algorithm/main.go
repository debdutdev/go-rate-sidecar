package main

import (
	"context"
	"fmt"
	"log"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	"github.com/12345debdut/rate-limiter/store"
)

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	ctx := context.Background()

	algorithms := []struct {
		name string
		typ  algorithm.Type
		cfg  ratelimiter.Config
	}{
		{
			name: "Token Bucket",
			typ:  algorithm.TypeTokenBucket,
			cfg:  ratelimiter.Config{Rate: 10, Burst: 5},
		},
		{
			name: "Leaky Bucket",
			typ:  algorithm.TypeLeakyBucket,
			cfg:  ratelimiter.Config{Rate: 10, Burst: 5},
		},
		{
			name: "Fixed Window",
			typ:  algorithm.TypeFixedWindow,
			cfg:  ratelimiter.Config{Rate: 5, Window: time.Minute},
		},
		{
			name: "Sliding Window Log",
			typ:  algorithm.TypeSlidingWindowLog,
			cfg:  ratelimiter.Config{Rate: 5, Window: time.Minute},
		},
		{
			name: "Sliding Window Counter",
			typ:  algorithm.TypeSlidingWindowCounter,
			cfg:  ratelimiter.Config{Rate: 5, Window: time.Minute},
		},
	}

	for _, alg := range algorithms {
		limiter, err := algorithm.New(alg.typ, alg.cfg, mem)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("=== %s ===\n", alg.name)
		fmt.Printf("Config: %+v\n", alg.cfg)

		key := alg.name + ":user1"
		allowed, denied := 0, 0

		for i := 0; i < 8; i++ {
			result, err := limiter.Allow(ctx, key)
			if err != nil {
				log.Fatal(err)
			}
			if result.Allowed {
				allowed++
				fmt.Printf("  Request %d: ALLOWED (remaining: %d)\n", i+1, result.Remaining)
			} else {
				denied++
				fmt.Printf("  Request %d: DENIED\n", i+1)
			}
		}

		fmt.Printf("  Summary: %d allowed, %d denied\n\n", allowed, denied)
		limiter.Close()
	}

	fmt.Println("Key differences:")
	fmt.Println("  - Token Bucket: allows burst up to capacity, then rate-limited")
	fmt.Println("  - Leaky Bucket: smooths output, queue-based (no instant burst)")
	fmt.Println("  - Fixed Window: resets at window boundary (susceptible to boundary burst)")
	fmt.Println("  - Sliding Window Log: precise, O(n) memory per key")
	fmt.Println("  - Sliding Window Counter: O(1) memory, weighted estimate (production favorite)")
}
