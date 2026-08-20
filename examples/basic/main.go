package main

import (
	"context"
	"fmt"
	"log"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/store"
)

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10, // 10 tokens per second
		Burst: 5,  // bucket holds 5 tokens max
	}, mem)
	if err != nil {
		log.Fatal(err)
	}
	defer limiter.Close()

	ctx := context.Background()

	// Simulate 8 requests from the same client.
	for i := 1; i <= 8; i++ {
		result, err := limiter.Allow(ctx, "user:123")
		if err != nil {
			log.Fatal(err)
		}

		if result.Allowed {
			fmt.Printf("Request %d: ALLOWED (remaining: %d)\n", i, result.Remaining)
		} else {
			fmt.Printf("Request %d: DENIED  (retry at: %s)\n", i, result.RetryAt.Format(time.RFC3339))
		}
	}

	// Wait for token refill.
	fmt.Println("\nWaiting 500ms for token refill...")
	time.Sleep(500 * time.Millisecond)

	result, _ := limiter.Allow(ctx, "user:123")
	fmt.Printf("After wait: ALLOWED=%v (remaining: %d)\n", result.Allowed, result.Remaining)
}
