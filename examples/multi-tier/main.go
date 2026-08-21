// Multi-tier rate limiting based on API key.
//
// This example shows how to implement tiered rate limiting where
// free-tier users get lower limits than premium users. The tier
// is determined by looking up the API key, and each tier gets
// its own limiter with different configuration.
//
// Run:
//
//	go run .
//	curl -i -H "X-API-Key: free-key-1" http://localhost:8080/api/data
//	curl -i -H "X-API-Key: pro-key-1" http://localhost:8080/api/data
//	curl -i http://localhost:8080/api/data   # no key → free tier
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/store"
)

// Simulated API key → tier mapping. In production this would come from
// a database, cache, or auth middleware.
var apiKeyTiers = map[string]string{
	"free-key-1": "free",
	"free-key-2": "free",
	"pro-key-1":  "pro",
	"pro-key-2":  "pro",
}

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	// Free tier: 5 requests per minute.
	freeLimiter, _ := algorithm.NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   5,
		Window: time.Minute,
	}, mem)
	defer freeLimiter.Close()

	// Pro tier: 60 requests per minute (1/sec sustained, with burst).
	proLimiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  60,
		Burst: 120,
	}, mem)
	defer proLimiter.Close()

	tierLimiters := map[string]ratelimiter.Limiter{
		"free": freeLimiter,
		"pro":  proLimiter,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from the API!",
			"tier":    tierFromKey(r.Header.Get("X-API-Key")),
		})
	})

	// The rate-limiting handler selects the right limiter per request
	// based on the API key's tier.
	handler := tierRateLimiter(tierLimiters, mux)

	fmt.Println("Multi-tier rate limiting server on :8080")
	fmt.Println()
	fmt.Println("Tiers:")
	fmt.Println("  free — 5 req/min  (sliding window counter)")
	fmt.Println("  pro  — 60 req/min burst 120 (token bucket)")
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println("  # Free tier (hits limit after 5 requests):")
	fmt.Println("  for i in $(seq 1 7); do curl -s -w '\\n' -H 'X-API-Key: free-key-1' http://localhost:8080/api/data; done")
	fmt.Println()
	fmt.Println("  # Pro tier (much higher limit):")
	fmt.Println("  for i in $(seq 1 7); do curl -s -w '\\n' -H 'X-API-Key: pro-key-1' http://localhost:8080/api/data; done")
	log.Fatal(http.ListenAndServe(":8080", handler))
}

func tierRateLimiter(limiters map[string]ratelimiter.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		tier := tierFromKey(apiKey)
		limiter := limiters[tier]

		// Use the API key as the rate-limit key so each key gets its own quota.
		// If no key is provided, fall back to client IP.
		rateLimitKey := apiKey
		if rateLimitKey == "" {
			rateLimitKey = r.RemoteAddr
		}

		// Prefix with tier to keep free/pro counters separate.
		rateLimitKey = tier + ":" + rateLimitKey

		result, err := limiter.Allow(r.Context(), rateLimitKey)
		if err != nil {
			// Fail-open: if the store has an error, let the request through.
			next.ServeHTTP(w, r)
			return
		}

		// Set standard rate-limit headers.
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", result.Limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))
		w.Header().Set("X-RateLimit-Tier", tier)

		if !result.Allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error":    "rate_limit_exceeded",
				"tier":     tier,
				"limit":    result.Limit,
				"retry_at": result.RetryAt.Format(time.RFC3339),
				"upgrade":  "Contact sales@example.com for higher limits",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func tierFromKey(apiKey string) string {
	if tier, ok := apiKeyTiers[apiKey]; ok {
		return tier
	}
	return "free"
}
