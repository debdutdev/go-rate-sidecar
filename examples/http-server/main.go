package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	"github.com/12345debdut/rate-limiter/key"
	"github.com/12345debdut/rate-limiter/middleware"
	"github.com/12345debdut/rate-limiter/store"
)

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  2,  // 2 tokens per second
		Burst: 10, // max 10 requests burst
	}, mem)
	if err != nil {
		log.Fatal(err)
	}
	defer limiter.Close()

	// Create the middleware with custom options.
	rateLimitMiddleware := middleware.HTTP(middleware.HTTPConfig{
		Limiter:      limiter,
		KeyExtractor: key.IPExtractor(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, result ratelimiter.Result) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error":     "rate_limit_exceeded",
				"retry_at":  result.RetryAt.Format(time.RFC3339),
				"limit":     result.Limit,
				"remaining": result.Remaining,
			})
		},
		SkipFunc: func(r *http.Request) bool {
			return r.URL.Path == "/health"
		},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello, World!"})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	handler := rateLimitMiddleware(mux)

	addr := ":8080"
	fmt.Printf("Server listening on %s\n", addr)
	fmt.Println("Try: curl -i http://localhost:8080/api/data")
	fmt.Println("      (repeat to see rate limiting kick in)")
	log.Fatal(http.ListenAndServe(addr, handler))
}
