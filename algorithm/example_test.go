package algorithm_test

import (
	"context"
	"fmt"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/store"
)

func ExampleNewTokenBucket() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10,
		Burst: 20,
	}, mem)
	if err != nil {
		panic(err)
	}
	defer limiter.Close()

	result, err := limiter.Allow(context.Background(), "user-123")
	if err != nil {
		panic(err)
	}
	fmt.Printf("allowed=%v remaining=%d\n", result.Allowed, result.Remaining)
	// Output: allowed=true remaining=19
}

func ExampleNewSlidingWindowCounter() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := algorithm.NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   5,
		Window: time.Minute,
	}, mem)
	if err != nil {
		panic(err)
	}
	defer limiter.Close()

	for i := 0; i < 6; i++ {
		result, _ := limiter.Allow(context.Background(), "user-456")
		fmt.Printf("request %d: allowed=%v\n", i+1, result.Allowed)
	}
	// Output:
	// request 1: allowed=true
	// request 2: allowed=true
	// request 3: allowed=true
	// request 4: allowed=true
	// request 5: allowed=true
	// request 6: allowed=false
}
