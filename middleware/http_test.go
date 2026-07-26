package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	"github.com/12345debdut/rate-limiter/internal/clock"
	"github.com/12345debdut/rate-limiter/key"
	"github.com/12345debdut/rate-limiter/store"
)

// failingLimiter always returns an error, used to test fail-open behavior.
type failingLimiter struct{}

func (f *failingLimiter) Allow(_ context.Context, _ string) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, fmt.Errorf("store connection failed")
}

func (f *failingLimiter) AllowN(_ context.Context, _ string, _ int64) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, fmt.Errorf("store connection failed")
}

func (f *failingLimiter) Reset(_ context.Context, _ string) error {
	return fmt.Errorf("store connection failed")
}

func (f *failingLimiter) Close() error { return nil }

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}

func newTestLimiter(t *testing.T, rate int64, burst int64) ratelimiter.Limiter {
	t.Helper()
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })
	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{Rate: rate, Burst: burst}, mem)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}
	return limiter
}

func TestHTTP_AllowedRequest(t *testing.T) {
	limiter := newTestLimiter(t, 10, 5)
	mw := HTTP(HTTPConfig{Limiter: limiter})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200", rec.Code)
	}

	// Check rate limit headers.
	limit := rec.Header().Get("X-RateLimit-Limit")
	if limit != "5" {
		t.Errorf("X-RateLimit-Limit=%q, want %q", limit, "5")
	}

	remaining := rec.Header().Get("X-RateLimit-Remaining")
	if remaining != "4" {
		t.Errorf("X-RateLimit-Remaining=%q, want %q", remaining, "4")
	}

	reset := rec.Header().Get("X-RateLimit-Reset")
	if reset == "" {
		t.Error("X-RateLimit-Reset should be set")
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter != "" {
		t.Errorf("Retry-After should not be set on allowed requests, got %q", retryAfter)
	}
}

func TestHTTP_DeniedRequest(t *testing.T) {
	limiter := newTestLimiter(t, 10, 2)
	mw := HTTP(HTTPConfig{Limiter: limiter})

	// Exhaust the bucket.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		rec := httptest.NewRecorder()
		mw(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200", i, rec.Code)
		}
	}

	// This one should be denied.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status=%d, want 429", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After should be set on denied requests")
	}

	remaining := rec.Header().Get("X-RateLimit-Remaining")
	if remaining != "0" {
		t.Errorf("X-RateLimit-Remaining=%q, want %q", remaining, "0")
	}
}

func TestHTTP_CustomKeyExtractor(t *testing.T) {
	limiter := newTestLimiter(t, 10, 1)
	mw := HTTP(HTTPConfig{
		Limiter:      limiter,
		KeyExtractor: key.HeaderExtractor("X-API-Key"),
	})

	// Request with API key "abc" — should be allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "abc")
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: status=%d, want 200", rec.Code)
	}

	// Same API key again — should be denied (burst=1).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "abc")
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request same key: status=%d, want 429", rec.Code)
	}

	// Different API key "xyz" — should be allowed (independent).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "xyz")
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("different key: status=%d, want 200", rec.Code)
	}
}

func TestHTTP_SkipFunc(t *testing.T) {
	limiter := newTestLimiter(t, 10, 1)
	mw := HTTP(HTTPConfig{
		Limiter: limiter,
		SkipFunc: func(r *http.Request) bool {
			return r.URL.Path == "/health"
		},
	})

	// Exhaust rate limit.
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	// /health should always pass through, even after exhaustion.
	for i := 0; i < 5; i++ {
		req = httptest.NewRequest(http.MethodGet, "/health", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec = httptest.NewRecorder()
		mw(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("health check %d: status=%d, want 200", i, rec.Code)
		}
		// Skipped requests should not have rate limit headers.
		if rec.Header().Get("X-RateLimit-Limit") != "" {
			t.Error("skipped requests should not have rate limit headers")
		}
	}
}

func TestHTTP_CustomErrorHandler(t *testing.T) {
	limiter := newTestLimiter(t, 10, 1)

	type errorBody struct {
		Error     string `json:"error"`
		RetryAt   string `json:"retry_at"`
		Limit     int64  `json:"limit"`
		Remaining int64  `json:"remaining"`
	}

	mw := HTTP(HTTPConfig{
		Limiter: limiter,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, result ratelimiter.Result) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(errorBody{
				Error:     "rate_limit_exceeded",
				RetryAt:   result.RetryAt.Format(time.RFC3339),
				Limit:     result.Limit,
				Remaining: result.Remaining,
			})
		},
	})

	// First request: allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.5.5.5:1111"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	// Second request: denied with custom JSON body.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.5.5.5:1111"
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}

	var body errorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Error != "rate_limit_exceeded" {
		t.Errorf("error=%q, want rate_limit_exceeded", body.Error)
	}
}

func TestHTTP_DefaultIPExtraction(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		wantKey    string
	}{
		{
			name:       "X-Forwarded-For",
			xff:        "203.0.113.50, 70.41.3.18",
			remoteAddr: "127.0.0.1:9999",
		},
		{
			name:       "X-Real-IP",
			xri:        "198.51.100.1",
			remoteAddr: "127.0.0.1:9999",
		},
		{
			name:       "RemoteAddr",
			remoteAddr: "192.0.2.1:54321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a fresh limiter per sub-test so keys don't conflict.
			lim := newTestLimiter(t, 10, 1)
			m := HTTP(HTTPConfig{Limiter: lim})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			rec := httptest.NewRecorder()
			m(okHandler()).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status=%d, want 200", rec.Code)
			}

			// Second request with same identity should be denied (burst=1).
			req2 := httptest.NewRequest(http.MethodGet, "/", nil)
			req2.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req2.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req2.Header.Set("X-Real-IP", tt.xri)
			}
			rec2 := httptest.NewRecorder()
			m(okHandler()).ServeHTTP(rec2, req2)

			if rec2.Code != http.StatusTooManyRequests {
				t.Errorf("second request: status=%d, want 429", rec2.Code)
			}
		})
	}
}

func TestHTTP_HeaderValues(t *testing.T) {
	clk := clock.NewMock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	limiter, err := algorithm.NewTokenBucket(
		ratelimiter.Config{Rate: 10, Burst: 5},
		mem,
		algorithm.WithClock(clk),
	)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}

	mw := HTTP(HTTPConfig{Limiter: limiter})

	// Make 3 requests — remaining should go 4, 3, 2.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		mw(okHandler()).ServeHTTP(rec, req)

		expected := strconv.Itoa(4 - i)
		got := rec.Header().Get("X-RateLimit-Remaining")
		if got != expected {
			t.Errorf("request %d: X-RateLimit-Remaining=%q, want %q", i, got, expected)
		}
	}
}

func TestHTTP_StoreError_FailOpen(t *testing.T) {
	mw := HTTP(HTTPConfig{Limiter: &failingLimiter{}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.1.1.1:1111"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	// No StoreErrorHandler set → fail-open: request passes through.
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200 (fail-open)", rec.Code)
	}
}

func TestHTTP_StoreError_CustomHandler(t *testing.T) {
	var capturedErr error
	mw := HTTP(HTTPConfig{
		Limiter: &failingLimiter{},
		StoreErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			capturedErr = err
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("service unavailable"))
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "2.2.2.2:2222"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	if capturedErr == nil {
		t.Error("StoreErrorHandler should have received an error")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rec.Code)
	}
}

func TestHTTP_MultipleClients(t *testing.T) {
	limiter := newTestLimiter(t, 10, 2)
	mw := HTTP(HTTPConfig{Limiter: limiter})

	clients := []string{"10.0.0.1:1234", "10.0.0.2:1234", "10.0.0.3:1234"}

	for _, addr := range clients {
		// Each client gets its own bucket.
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = addr
			rec := httptest.NewRecorder()
			mw(okHandler()).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("client %s request %d: status=%d, want 200", addr, i, rec.Code)
			}
		}

		// Third request from same client should be denied.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		mw(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("client %s 3rd request: status=%d, want 429", addr, rec.Code)
		}
	}
}

func TestHTTP_CompositeExtractor(t *testing.T) {
	limiter := newTestLimiter(t, 10, 1)
	mw := HTTP(HTTPConfig{
		Limiter: limiter,
		KeyExtractor: key.CompositeExtractor(":",
			key.IPExtractor(),
			key.PathExtractor(),
		),
	})

	// IP+path "1.2.3.4:/api" — first request allowed.
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: status=%d, want 200", rec.Code)
	}

	// Same IP but different path — should be allowed (different key).
	req = httptest.NewRequest(http.MethodGet, "/other", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("different path: status=%d, want 200", rec.Code)
	}

	// Same IP+path again — should be denied (burst=1).
	req = httptest.NewRequest(http.MethodGet, "/api", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("same key again: status=%d, want 429", rec.Code)
	}
}

func TestHTTP_UpstreamHandlerNotCalledOnDeny(t *testing.T) {
	limiter := newTestLimiter(t, 10, 1)
	mw := HTTP(HTTPConfig{Limiter: limiter})

	called := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	// First request — upstream called.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1111"
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if called != 1 {
		t.Fatalf("upstream called %d times, want 1", called)
	}

	// Second request — denied, upstream should NOT be called.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1111"
	rec = httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if called != 1 {
		t.Errorf("upstream called %d times after denial, want 1", called)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status=%d, want 429", rec.Code)
	}
}

func TestHTTP_RetryAfterHeader(t *testing.T) {
	clk := clock.NewMock(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	limiter, _ := algorithm.NewTokenBucket(
		ratelimiter.Config{Rate: 1, Burst: 1},
		mem,
		algorithm.WithClock(clk),
	)

	mw := HTTP(HTTPConfig{Limiter: limiter})

	// First request: allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	rec := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	// Second request: denied.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	rec = httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("Retry-After header should be set")
	}

	val, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After=%q is not an integer: %v", retryAfter, err)
	}
	if val < 1 {
		t.Errorf("Retry-After=%d, want >= 1", val)
	}
}

func BenchmarkHTTP_Middleware(b *testing.B) {
	mem := store.NewMemory(0)
	defer mem.Close()
	limiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 1e9, Burst: 1e9}, mem)
	mw := HTTP(HTTPConfig{Limiter: limiter})
	handler := mw(okHandler())

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = fmt.Sprintf("10.0.0.%d:%d", i%256, 1000+i%60000)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			i++
		}
	})
}
