// AllowN: consuming multiple tokens per request.
//
// Not every request costs the same. An upload endpoint might cost
// tokens proportional to file size. A batch API might cost one token
// per item in the batch. AllowN lets you consume N tokens in a single
// atomic call.
//
// This example runs a simulated upload service where small files
// cost 1 token and large files cost 5 tokens.
//
// Run:
//
//	go run .
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

type upload struct {
	name string
	size int64 // MB
}

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	// 10 tokens/sec, burst of 15.
	// Small files (< 10MB) cost 1 token, large files cost 5 tokens.
	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10,
		Burst: 15,
	}, mem)
	if err != nil {
		log.Fatal(err)
	}
	defer limiter.Close()

	ctx := context.Background()
	userKey := "user:upload-service"

	uploads := []upload{
		{"photo.jpg", 2},
		{"document.pdf", 5},
		{"video.mp4", 50},
		{"avatar.png", 1},
		{"backup.zip", 100},
		{"icon.svg", 0},
		{"dataset.csv", 25},
		{"readme.txt", 0},
		{"presentation.pptx", 15},
	}

	fmt.Println("Upload Rate Limiter — Token Bucket (15 burst, 10/sec refill)")
	fmt.Println("Small files (< 10MB) cost 1 token, large files cost 5 tokens")
	fmt.Println()

	for _, u := range uploads {
		cost := tokenCost(u.size)
		result, err := limiter.AllowN(ctx, userKey, cost)
		if err != nil {
			log.Fatal(err)
		}

		if result.Allowed {
			fmt.Printf("  %-25s %3dMB  cost=%d  ALLOWED  (remaining: %d)\n",
				u.name, u.size, cost, result.Remaining)
		} else {
			fmt.Printf("  %-25s %3dMB  cost=%d  DENIED   (retry at: %s)\n",
				u.name, u.size, cost, result.RetryAt.Format("15:04:05"))
		}
	}

	fmt.Println()
	fmt.Println("Notice how the large file (video.mp4, 50MB) consumed 5 tokens at once,")
	fmt.Println("draining the bucket faster than multiple small files would.")
}

func tokenCost(sizeMB int64) int64 {
	if sizeMB < 10 {
		return 1
	}
	return 5
}
