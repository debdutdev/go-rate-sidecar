// Per-route rate limiting without the engine.
//
// This example shows how to apply different rate limits to different
// endpoints using the programmatic API. Each route gets its own limiter
// with its own algorithm and configuration, all sharing one store.
//
// Run:
//
//	go run .
//	curl -i http://localhost:8080/api/login     # strict: 3 req/min
//	curl -i http://localhost:8080/api/search     # moderate: 10 req/sec burst 20
//	curl -i http://localhost:8080/api/feed       # generous: 100 req/sec burst 200
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/key"
	"github.com/debdutdev/rate-limiter/middleware"
	"github.com/debdutdev/rate-limiter/store"
)

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	// Strict limiter for auth endpoints — sliding window, 3 requests per minute.
	loginLimiter, _ := algorithm.NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   3,
		Window: time.Minute,
	}, mem)
	defer loginLimiter.Close()

	// Moderate limiter for search — token bucket, 10/sec with burst of 20.
	searchLimiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10,
		Burst: 20,
	}, mem)
	defer searchLimiter.Close()

	// Generous limiter for read-heavy feed — token bucket, 100/sec with burst of 200.
	feedLimiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  100,
		Burst: 200,
	}, mem)
	defer feedLimiter.Close()

	// Build the mux with per-route middleware.
	mux := http.NewServeMux()

	// Each endpoint is wrapped with its own rate limiter.
	// key.CompositeExtractor(":", ...) prefixes the key with the path so that
	// "/api/login" and "/api/search" track limits independently.
	loginHandler := middleware.HTTP(middleware.HTTPConfig{
		Limiter:      loginLimiter,
		KeyExtractor: key.CompositeExtractor(":", key.PathExtractor(), key.IPExtractor()),
		ErrorHandler: jsonError,
	})(http.HandlerFunc(handleLogin))

	searchHandler := middleware.HTTP(middleware.HTTPConfig{
		Limiter:      searchLimiter,
		KeyExtractor: key.CompositeExtractor(":", key.PathExtractor(), key.IPExtractor()),
		ErrorHandler: jsonError,
	})(http.HandlerFunc(handleSearch))

	feedHandler := middleware.HTTP(middleware.HTTPConfig{
		Limiter:      feedLimiter,
		KeyExtractor: key.CompositeExtractor(":", key.PathExtractor(), key.IPExtractor()),
		ErrorHandler: jsonError,
	})(http.HandlerFunc(handleFeed))

	mux.Handle("/api/login", loginHandler)
	mux.Handle("/api/search", searchHandler)
	mux.Handle("/api/feed", feedHandler)

	fmt.Println("Per-route rate limiting server on :8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  /api/login   — 3 req/min   (sliding window counter)")
	fmt.Println("  /api/search  — 10 req/sec  burst 20 (token bucket)")
	fmt.Println("  /api/feed    — 100 req/sec burst 200 (token bucket)")
	fmt.Println()
	fmt.Println("Try: for i in $(seq 1 5); do curl -s -o /dev/null -w '%{http_code}\\n' http://localhost:8080/api/login; done")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"token": "eyJhbGciOiJIUzI1NiJ9..."})
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{"results": []string{"item1", "item2"}, "total": 42})
}

func handleFeed(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{"posts": []string{"post1", "post2", "post3"}, "page": 1})
}

func jsonError(w http.ResponseWriter, r *http.Request, result ratelimiter.Result) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(map[string]any{
		"error":     "rate_limit_exceeded",
		"limit":     result.Limit,
		"remaining": result.Remaining,
		"retry_at":  result.RetryAt.Format(time.RFC3339),
	})
}
