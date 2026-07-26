package middleware

import (
	"net/http"
	"strconv"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/key"
)

// HTTPConfig configures the HTTP rate-limiting middleware.
type HTTPConfig struct {
	// Limiter is the rate limiter to use. Required.
	Limiter ratelimiter.Limiter

	// KeyExtractor derives the rate-limiting key from the request.
	// Defaults to IPExtractor if nil.
	KeyExtractor key.Extractor

	// ErrorHandler is called when a request is denied.
	// Defaults to a plain-text 429 response if nil.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, result ratelimiter.Result)

	// SkipFunc returns true for requests that should bypass rate limiting.
	// Defaults to nil (no requests skipped).
	SkipFunc func(r *http.Request) bool

	// StoreErrorHandler is called when the store returns an error.
	// If it writes a response, the request stops. If it does not write
	// a response (or is nil), the request proceeds (fail-open).
	StoreErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

// HTTP returns standard net/http middleware that rate-limits requests.
// Compatible with net/http, chi, and any mux using func(http.Handler) http.Handler.
func HTTP(cfg HTTPConfig) func(http.Handler) http.Handler {
	if cfg.KeyExtractor == nil {
		cfg.KeyExtractor = key.IPExtractor()
	}
	if cfg.ErrorHandler == nil {
		cfg.ErrorHandler = defaultErrorHandler
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.SkipFunc != nil && cfg.SkipFunc(r) {
				next.ServeHTTP(w, r)
				return
			}

			k := cfg.KeyExtractor(r)
			result, err := cfg.Limiter.Allow(r.Context(), k)
			if err != nil {
				if cfg.StoreErrorHandler != nil {
					cfg.StoreErrorHandler(w, r, err)
					return
				}
				// Fail-open: proceed to next handler.
				next.ServeHTTP(w, r)
				return
			}

			setRateLimitHeaders(w, result)

			if !result.Allowed {
				cfg.ErrorHandler(w, r, result)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func setRateLimitHeaders(w http.ResponseWriter, result ratelimiter.Result) {
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	if !result.ResetAt.IsZero() {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	}
	if !result.Allowed && !result.RetryAt.IsZero() {
		delta := time.Until(result.RetryAt).Seconds()
		if delta < 1 {
			delta = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(delta)))
	}
}

func defaultErrorHandler(w http.ResponseWriter, _ *http.Request, _ ratelimiter.Result) {
	http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
}
