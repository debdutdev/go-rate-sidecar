package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/store"
)

func main() {
	ctx := context.Background()

	// Connect to Redis.
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis not available: %v\nRun: docker run -p 6379:6379 redis:alpine", err)
	}

	rs, err := store.NewRedisStore(ctx, store.RedisConfig{
		Client: client,
		Prefix: "example:",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer rs.Close()

	// Create a sliding window counter — the same algorithm Cloudflare uses.
	limiter, err := algorithm.NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   5,
		Window: 10 * time.Second,
	}, rs)
	if err != nil {
		log.Fatal(err)
	}
	defer limiter.Close()

	fmt.Println("Sliding Window Counter (5 req / 10s window, Redis-backed)")
	fmt.Println("---")

	// Simulate requests from two different API keys.
	keys := []string{"api-key:abc123", "api-key:xyz789"}
	for _, key := range keys {
		fmt.Printf("\nClient: %s\n", key)
		for i := 1; i <= 7; i++ {
			result, err := limiter.Allow(ctx, key)
			if err != nil {
				log.Fatal(err)
			}
			if result.Allowed {
				fmt.Printf("  Request %d: ALLOWED (remaining: %d)\n", i, result.Remaining)
			} else {
				fmt.Printf("  Request %d: DENIED  (reset at: %s)\n", i, result.ResetAt.Format(time.RFC3339))
			}
		}
	}
}
