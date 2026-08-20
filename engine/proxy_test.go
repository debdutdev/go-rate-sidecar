package engine

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/12345debdut/rate-limiter/store"
)

func newTestProxy(t *testing.T, yaml string, upstream *httptest.Server) (http.Handler, *Engine) {
	t.Helper()
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.Upstream.URL = upstream.URL

	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })

	eng, err := NewFromConfig(cfg, WithStore(mem))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	handler, err := eng.ProxyHandler()
	if err != nil {
		t.Fatalf("ProxyHandler: %v", err)
	}
	return handler, eng
}

func TestProxy_ForwardsAllowedRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "true")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`, upstream)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Upstream") != "true" {
		t.Error("response should come from upstream")
	}
	if rec.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit=%q, want 5", rec.Header().Get("X-RateLimit-Limit"))
	}
}

func TestProxy_DeniedRequestsDoNotHitUpstream(t *testing.T) {
	var upstreamHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 2
    key_by: ip
`, upstream)

	// Exhaust bucket (2 allowed).
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200", i, rec.Code)
		}
	}

	if upstreamHits.Load() != 2 {
		t.Errorf("upstream hits=%d, want 2", upstreamHits.Load())
	}

	// Third request: denied, upstream NOT hit.
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status=%d, want 429", rec.Code)
	}
	if upstreamHits.Load() != 2 {
		t.Errorf("upstream hits=%d after denial, want 2 (upstream should not be hit)", upstreamHits.Load())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After should be set on denied request")
	}
}

func TestProxy_HealthPathBypassesRateLimiting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy"))
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
  health_path: "/health"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: ip
`, upstream)

	// Exhaust rate limit.
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first api request: status=%d, want 200", rec.Code)
	}

	// Verify rate limit is exhausted.
	req = httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second api request: status=%d, want 429", rec.Code)
	}

	// Health check should always pass through.
	for i := 0; i < 5; i++ {
		req = httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("health check %d: status=%d, want 200", i, rec.Code)
		}
	}
}

func TestProxy_UpstreamPreservesHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Received-Auth", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`, upstream)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Received-Auth") != "Bearer test-token-123" {
		t.Errorf("Authorization header not forwarded: got %q", rec.Header().Get("X-Received-Auth"))
	}
}

func TestProxy_UpstreamPreservesResponseBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42,"name":"test"}`))
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`, upstream)

	req := httptest.NewRequest("POST", "/api/users", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status=%d, want 201", rec.Code)
	}
	if rec.Body.String() != `{"id":42,"name":"test"}` {
		t.Errorf("body=%q, want JSON", rec.Body.String())
	}
}

func TestProxy_MissingUpstreamURL(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 5
    key_by: ip
`))
	mem := store.NewMemory(0)
	defer mem.Close()

	eng, err := NewFromConfig(cfg, WithStore(mem))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer eng.Close()

	_, err = eng.ProxyHandler()
	if err == nil {
		t.Fatal("expected error for missing upstream.url")
	}
	if _, ok := err.(*ProxyConfigError); !ok {
		t.Errorf("expected ProxyConfigError, got %T: %v", err, err)
	}
}

func TestProxy_MultipleClientsIndependent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, _ := newTestProxy(t, `
upstream:
  url: "PLACEHOLDER"
rules:
  - name: api
    path: "/**"
    algorithm: token_bucket
    rate: 10
    burst: 1
    key_by: ip
`, upstream)

	clients := []string{"10.0.0.1:1234", "10.0.0.2:1234", "10.0.0.3:1234"}
	for _, addr := range clients {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("client %s: status=%d, want 200", addr, rec.Code)
		}
	}

	// Each client exhausted — second request from each should be denied.
	for _, addr := range clients {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("client %s second: status=%d, want 429", addr, rec.Code)
		}
	}
}
