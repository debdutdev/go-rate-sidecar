// Prometheus metrics integration.
//
// This example shows how to wrap limiters with InstrumentedLimiter to
// get Prometheus metrics, and expose them on a /metrics endpoint for
// scraping. It demonstrates per-rule metrics with multiple limiters
// sharing one registry.
//
// Run:
//
//	go run .
//	curl http://localhost:8080/api/users        # generate some traffic
//	curl http://localhost:8080/api/admin         # generate some traffic
//	curl http://localhost:9090/metrics           # view Prometheus metrics
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/key"
	"github.com/debdutdev/rate-limiter/metrics"
	"github.com/debdutdev/rate-limiter/middleware"
	"github.com/debdutdev/rate-limiter/store"
)

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	// Use a dedicated registry instead of the global default.
	// This keeps rate-limiter metrics separate from Go runtime metrics
	// and makes testing easier.
	reg := prometheus.NewRegistry()

	// --- Users API: moderate limits ---
	usersLimiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10,
		Burst: 20,
	}, mem)
	defer usersLimiter.Close()

	// Wrap with InstrumentedLimiter to get metrics.
	// RuleName and Algorithm become Prometheus labels.
	instrumentedUsers := metrics.NewInstrumentedLimiter(usersLimiter, metrics.InstrumentedConfig{
		RuleName:   "users-api",
		Algorithm:  "token_bucket",
		Registerer: reg,
	})

	// --- Admin API: strict limits ---
	adminLimiter, _ := algorithm.NewSlidingWindowCounter(ratelimiter.Config{
		Rate:   3,
		Window: time.Minute,
	}, mem)
	defer adminLimiter.Close()

	// Same registry — metrics collectors are shared, labels differentiate rules.
	instrumentedAdmin := metrics.NewInstrumentedLimiter(adminLimiter, metrics.InstrumentedConfig{
		RuleName:   "admin-api",
		Algorithm:  "sliding_window_counter",
		Registerer: reg,
	})

	// Wire up HTTP handlers with the instrumented limiters.
	mux := http.NewServeMux()

	mux.Handle("/api/users", middleware.HTTP(middleware.HTTPConfig{
		Limiter:      instrumentedUsers,
		KeyExtractor: key.IPExtractor(),
		ErrorHandler: jsonError,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"users": []string{"alice", "bob", "charlie"},
		})
	})))

	mux.Handle("/api/admin", middleware.HTTP(middleware.HTTPConfig{
		Limiter:      instrumentedAdmin,
		KeyExtractor: key.IPExtractor(),
		ErrorHandler: jsonError,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": "admin panel",
		})
	})))

	// Expose metrics on a separate port so it's not rate-limited.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	go func() {
		fmt.Println("Metrics server on :9090/metrics")
		log.Fatal(http.ListenAndServe(":9090", metricsMux))
	}()

	fmt.Println("API server on :8080")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  /api/users  — 10 req/sec burst 20 (token_bucket)")
	fmt.Println("  /api/admin  — 3 req/min (sliding_window_counter)")
	fmt.Println()
	fmt.Println("Metrics exposed at http://localhost:9090/metrics")
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println("  # Generate traffic:")
	fmt.Println("  for i in $(seq 1 25); do curl -s -o /dev/null http://localhost:8080/api/users; done")
	fmt.Println("  for i in $(seq 1 5); do curl -s -o /dev/null http://localhost:8080/api/admin; done")
	fmt.Println()
	fmt.Println("  # View metrics:")
	fmt.Println("  curl -s http://localhost:9090/metrics | grep ratelimiter")
	fmt.Println()
	fmt.Println("You'll see metrics like:")
	fmt.Println("  ratelimiter_requests_total{rule=\"users-api\",algorithm=\"token_bucket\",decision=\"allowed\"} 20")
	fmt.Println("  ratelimiter_requests_total{rule=\"users-api\",algorithm=\"token_bucket\",decision=\"denied\"} 5")
	fmt.Println("  ratelimiter_current_usage_ratio{rule=\"users-api\",algorithm=\"token_bucket\"} 0.75")
	fmt.Println("  ratelimiter_check_duration_seconds_bucket{...}")
	log.Fatal(http.ListenAndServe(":8080", mux))
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
