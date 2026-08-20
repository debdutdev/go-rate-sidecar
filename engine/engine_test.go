package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/debdutdev/rate-limiter/store"
	"github.com/prometheus/client_golang/prometheus"
)

func newRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	return httptest.NewRequest(method, path, nil)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}

func newTestEngine(t *testing.T, yaml string, opts ...Option) *Engine {
	t.Helper()
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	allOpts := append([]Option{WithStore(mem)}, opts...)
	eng, err := NewFromConfig(cfg, allOpts...)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

func TestEngine_BasicRateLimiting(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 3
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// 3 requests allowed.
	for i := 0; i < 3; i++ {
		req := newRequest(t, "GET", "/api/v1/users")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200", i, rec.Code)
		}
	}

	// 4th request denied.
	req := newRequest(t, "GET", "/api/v1/users")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status=%d, want 429", rec.Code)
	}
}

func TestEngine_PerRouteRateLimiting(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: strict
    path: "/api/v1/login"
    method: POST
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: ip

  - name: relaxed
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// POST /api/v1/login — strict (burst=1).
	req := newRequest(t, "POST", "/api/v1/login")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login 1st: status=%d, want 200", rec.Code)
	}

	req = newRequest(t, "POST", "/api/v1/login")
	req.RemoteAddr = "10.0.0.1:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("login 2nd: status=%d, want 429", rec.Code)
	}

	// GET /api/v1/users — relaxed (burst=5), should still work.
	req = newRequest(t, "GET", "/api/v1/users")
	req.RemoteAddr = "10.0.0.1:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("users GET: status=%d, want 200", rec.Code)
	}
}

func TestEngine_SkipRule(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: health
    path: "/health"
    skip: true

  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// /health always passes — no rate limit headers.
	for i := 0; i < 10; i++ {
		req := newRequest(t, "GET", "/health")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("health check %d: status=%d, want 200", i, rec.Code)
		}
		if rec.Header().Get("X-RateLimit-Limit") != "" {
			t.Error("skip rule should not set rate limit headers")
		}
	}
}

func TestEngine_DefaultRule(t *testing.T) {
	eng := newTestEngine(t, `
default:
  algorithm: token_bucket
  rate: 10
  burst: 2
  key_by: ip

rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// /other doesn't match any rule — falls to default (burst=2).
	for i := 0; i < 2; i++ {
		req := newRequest(t, "GET", "/other")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("default %d: status=%d, want 200", i, rec.Code)
		}
	}

	req := newRequest(t, "GET", "/other")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("default 3rd: status=%d, want 429", rec.Code)
	}
}

func TestEngine_NoDefaultNoMatch_PassesThrough(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// /other doesn't match and no default — passes through unlimited.
	for i := 0; i < 10; i++ {
		req := newRequest(t, "GET", "/other")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("no-match %d: status=%d, want 200", i, rec.Code)
		}
	}
}

func TestEngine_HeaderKeyExtractor(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: "header:X-API-Key"
`)

	handler := eng.Middleware()(okHandler())

	// Key "abc" — allowed.
	req := newRequest(t, "GET", "/api/data")
	req.Header.Set("X-API-Key", "abc")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("abc first: status=%d, want 200", rec.Code)
	}

	// Key "abc" again — denied.
	req = newRequest(t, "GET", "/api/data")
	req.Header.Set("X-API-Key", "abc")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("abc second: status=%d, want 429", rec.Code)
	}

	// Key "xyz" — independent, allowed.
	req = newRequest(t, "GET", "/api/data")
	req.Header.Set("X-API-Key", "xyz")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("xyz first: status=%d, want 200", rec.Code)
	}
}

func TestEngine_WindowAlgorithm(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: fixed
    path: "/api/**"
    algorithm: fixed_window
    rate: 3
    window: 1m
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	for i := 0; i < 3; i++ {
		req := newRequest(t, "GET", "/api/test")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200", i, rec.Code)
		}
	}

	req := newRequest(t, "GET", "/api/test")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("4th request: status=%d, want 429", rec.Code)
	}
}

func TestEngine_RateLimitHeaders(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	req := newRequest(t, "GET", "/api/test")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit=%q, want 5", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Errorf("X-RateLimit-Remaining=%q, want 4", rec.Header().Get("X-RateLimit-Remaining"))
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset should be set")
	}
}

func TestEngine_WithMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	eng := newTestEngine(t, `
metrics:
  enabled: true

rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`, WithRegisterer(reg))

	handler := eng.Middleware()(okHandler())

	req := newRequest(t, "GET", "/api/test")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	// Verify metrics were registered.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	metricNames := make(map[string]bool)
	for _, f := range families {
		metricNames[f.GetName()] = true
	}

	expected := []string{
		"ratelimiter_requests_total",
		"ratelimiter_current_usage_ratio",
		"ratelimiter_check_duration_seconds",
	}
	for _, name := range expected {
		if !metricNames[name] {
			t.Errorf("metric %q not found", name)
		}
	}
}

func TestEngine_MultipleAlgorithms(t *testing.T) {
	eng := newTestEngine(t, `
rules:
  - name: bucket
    path: "/api/bucket"
    algorithm: token_bucket
    rate: 10
    burst: 2
    key_by: ip

  - name: window
    path: "/api/window"
    algorithm: sliding_window_counter
    rate: 2
    window: 1m
    key_by: ip
`)

	handler := eng.Middleware()(okHandler())

	// Token bucket route.
	for i := 0; i < 2; i++ {
		req := newRequest(t, "GET", "/api/bucket")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("bucket %d: status=%d, want 200", i, rec.Code)
		}
	}

	// Sliding window route.
	for i := 0; i < 2; i++ {
		req := newRequest(t, "GET", "/api/window")
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("window %d: status=%d, want 200", i, rec.Code)
		}
	}

	// Both should be denied now.
	for _, path := range []string{"/api/bucket", "/api/window"} {
		req := newRequest(t, "GET", path)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s: status=%d, want 429", path, rec.Code)
		}
	}
}

func TestEngine_ConfigAccessor(t *testing.T) {
	eng := newTestEngine(t, `
server:
  listen: ":9090"
upstream:
  url: "http://localhost:8081"
  timeout: 30s
rules:
  - name: api
    path: "/api/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`)

	cfg := eng.Config()
	if cfg.Server.Listen != ":9090" {
		t.Errorf("server.listen=%q, want :9090", cfg.Server.Listen)
	}
	if cfg.Upstream.URL != "http://localhost:8081" {
		t.Errorf("upstream.url=%q, want http://localhost:8081", cfg.Upstream.URL)
	}
	if cfg.Upstream.Timeout != 30*time.Second {
		t.Errorf("upstream.timeout=%v, want 30s", cfg.Upstream.Timeout)
	}
}

func TestEngine_InvalidConfig_Errors(t *testing.T) {
	_, err := NewFromConfig(EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "nonexistent", Rate: 10, Burst: 5},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}
