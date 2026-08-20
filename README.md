# rate-limiter

A production-grade rate limiting library for Go with five algorithms, in-memory and Redis backends, HTTP/gRPC middleware, Prometheus observability, config-driven setup, and sidecar proxy mode.

## Features

- **5 algorithms** — Token Bucket, Leaky Bucket, Fixed Window, Sliding Window Log, Sliding Window Counter
- **2 backends** — In-memory (sync.Map + per-key mutex) and Redis (atomic Lua scripts)
- **HTTP middleware** — Standard `func(http.Handler) http.Handler`, works with net/http, chi, gorilla/mux
- **gRPC interceptors** — Unary + Stream, returns `codes.ResourceExhausted`
- **Prometheus metrics** — Request counter, usage gauge, latency histogram, error counter
- **Config-driven engine** — YAML file controls all rate limiting rules per route
- **Sidecar/proxy mode** — Standalone binary that sits in front of any application
- **133 tests**, all passing with `-race`

## Algorithm Comparison

| Algorithm | Burst | Memory/Key | Precision | Best For |
|---|---|---|---|---|
| Token Bucket | Yes (up to capacity) | O(1) | Good | General-purpose API limiting |
| Leaky Bucket | No (smooths output) | O(1) | Good | Steady-rate processing |
| Fixed Window | Boundary burst (2×rate) | O(1) | Moderate | Simple counting |
| Sliding Window Log | None | O(rate) | Exact | Low-rate, high-precision |
| Sliding Window Counter | Minimal | O(1) | Very good | Production APIs (Cloudflare) |

## Quick Start

### Install

```bash
go get github.com/12345debdut/rate-limiter
```

### Mode A: Programmatic (full control)

```go
package main

import (
    "log"
    "net/http"
    "time"

    ratelimiter "github.com/12345debdut/rate-limiter"
    "github.com/12345debdut/rate-limiter/algorithm"
    "github.com/12345debdut/rate-limiter/middleware"
    "github.com/12345debdut/rate-limiter/store"
)

func main() {
    mem := store.NewMemory(time.Minute)
    defer mem.Close()

    limiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
        Rate:  10, // tokens per second
        Burst: 20, // max burst
    }, mem)
    defer limiter.Close()

    mux := http.NewServeMux()
    mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("OK"))
    })

    handler := middleware.HTTP(middleware.HTTPConfig{Limiter: limiter})(mux)
    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### Mode B: Config-driven engine (recommended)

```go
eng, _ := engine.New("ratelimiter.yaml")
defer eng.Close()
http.ListenAndServe(":8080", eng.Middleware()(mux))
```

### Mode C: Sidecar proxy (any language, zero code changes)

```bash
./ratelimiter --config ratelimiter.yaml
# Proxy listens on :8080, forwards allowed requests to upstream
```

## Config File Reference

```yaml
# ratelimiter.yaml

# --- Sidecar/proxy mode only ---
server:
  listen: ":8080"

upstream:
  url: "http://localhost:8081"
  timeout: 30s
  health_path: "/health"         # bypasses rate limiting

# --- Common config ---
store:
  type: memory                   # "memory" or "redis"
  redis:
    addr: "localhost:6379"
    password: ""
    db: 0
    prefix: "rl:"

metrics:
  enabled: true

default:                         # fallback for unmatched routes
  algorithm: token_bucket
  rate: 100
  burst: 200
  key_by: ip

rules:
  - name: "auth-strict"
    path: "/api/v1/login"
    method: POST
    algorithm: sliding_window_counter
    rate: 5
    window: 1m
    key_by: ip

  - name: "user-api"
    path: "/api/v1/users/*"
    algorithm: token_bucket
    rate: 50
    burst: 100
    key_by: "header:X-API-Key"

  - name: "upload"
    path: "/api/v1/upload/*"
    algorithm: leaky_bucket
    rate: 2
    burst: 5
    key_by: "header:Authorization"

  - name: "health"
    path: "/health"
    skip: true

  - name: "catch-all"
    path: "/api/**"
    algorithm: token_bucket
    rate: 500
    burst: 1000
    key_by: ip
```

### Config Fields

**Algorithms:** `token_bucket`, `leaky_bucket`, `fixed_window`, `sliding_window_log`, `sliding_window_counter`

**Key extractors (`key_by`):**

| Format | Description |
|---|---|
| `ip` | Client IP (X-Forwarded-For > X-Real-IP > RemoteAddr) |
| `header:X-Name` | Value of the named header |
| `path` | Request URL path |
| `composite:ip,header:X-ID` | Multiple extractors joined with `:` |

**Path patterns:**

| Pattern | Matches |
|---|---|
| `/api/v1/users` | Exact path |
| `/api/v1/users/*` | One segment: `/api/v1/users/123` |
| `/api/**` | Any depth: `/api/v1/users/123/orders` |

Rules are evaluated in declaration order (first match wins).

## HTTP Middleware

```go
mw := middleware.HTTP(middleware.HTTPConfig{
    Limiter:      limiter,
    KeyExtractor: key.IPExtractor(),         // default
    ErrorHandler: customJSONErrorHandler,     // optional
    SkipFunc:     func(r *http.Request) bool { return r.URL.Path == "/health" },
    StoreErrorHandler: customStoreErrHandler, // optional (fail-open by default)
})
handler := mw(yourMux)
```

**Response headers set on every request:**

| Header | Description |
|---|---|
| `X-RateLimit-Limit` | Configured maximum |
| `X-RateLimit-Remaining` | Remaining in current window/bucket |
| `X-RateLimit-Reset` | Unix timestamp when the limit resets |
| `Retry-After` | Seconds until retry (429 responses only) |

## gRPC Interceptors

```go
srv := grpc.NewServer(
    grpc.UnaryInterceptor(rlgrpc.UnaryServerInterceptor(rlgrpc.InterceptorConfig{
        Limiter:      limiter,
        KeyExtractor: rlgrpc.MetadataExtractor("x-api-key"),
    })),
    grpc.StreamInterceptor(rlgrpc.StreamServerInterceptor(rlgrpc.InterceptorConfig{
        Limiter: limiter,
    })),
)
```

Denied requests return `codes.ResourceExhausted` with rate-limit info in trailing metadata.

**Key extractors:** `PeerAddrExtractor()`, `MetadataExtractor(key)`, `MethodExtractor()`

## Prometheus Metrics

Wrap any limiter with the `InstrumentedLimiter` decorator:

```go
instrumented := metrics.NewInstrumentedLimiter(limiter, metrics.InstrumentedConfig{
    RuleName:  "auth-api",
    Algorithm: "token_bucket",
})
```

| Metric | Type | Labels |
|---|---|---|
| `ratelimiter_requests_total` | Counter | rule, algorithm, decision |
| `ratelimiter_current_usage_ratio` | Gauge | rule, algorithm |
| `ratelimiter_check_duration_seconds` | Histogram | rule, algorithm |
| `ratelimiter_store_errors_total` | Counter | rule, algorithm |

The engine enables metrics automatically when `metrics.enabled: true` in the config.

## Redis Backend

```go
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
rs, _ := store.NewRedisStore(ctx, store.RedisConfig{
    Client: client,
    Prefix: "rl:",
})
limiter, _ := algorithm.NewTokenBucket(config, rs)
```

Each algorithm has a dedicated Lua script that runs atomically in a single Redis round-trip. Scripts use `redis.call("TIME")` for server-side timestamps to avoid clock drift between application servers.

## Sidecar Deployment

### Docker

```bash
docker build -t ratelimiter .
docker run -p 8080:8080 -v ./config.yaml:/etc/ratelimiter/config.yaml ratelimiter
```

### Docker Compose

```yaml
services:
  rate-limiter:
    build: .
    ports: ["8080:8080"]
    volumes: ["./config.yaml:/etc/ratelimiter/config.yaml:ro"]
    depends_on: [app]
  app:
    image: your-app:latest
    expose: ["8081"]
```

### Kubernetes Sidecar

```yaml
spec:
  containers:
    - name: app
      image: your-app:latest
      ports: [{containerPort: 8081}]
    - name: rate-limiter
      image: ratelimiter:latest
      ports: [{containerPort: 8080}]
      args: ["--config", "/etc/ratelimiter/config.yaml"]
      volumeMounts:
        - {name: config, mountPath: /etc/ratelimiter}
      resources:
        requests: {cpu: 50m, memory: 32Mi}
        limits: {cpu: 200m, memory: 64Mi}
  volumes:
    - name: config
      configMap: {name: ratelimiter-config}
```

The `/__metrics` endpoint serves Prometheus metrics for scraping.

## Benchmarks

In-memory store, Apple M4, Go 1.25.3:

| Algorithm | Single Key | Parallel Keys | Allocs/op |
|---|---|---|---|
| Token Bucket | 171 ns/op | 87 ns/op | 2-3 |
| Leaky Bucket | 167 ns/op | 89 ns/op | 2-3 |
| Fixed Window | 157 ns/op | 87 ns/op | 2-3 |
| Sliding Window Log | 323 ns/op | — | 8 |
| Sliding Window Counter | 162 ns/op | 89 ns/op | 2-3 |

Sliding Window Log is ~2x slower with ~7x more allocations due to per-request timestamp storage. All other algorithms are O(1) memory per key.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Mode A: Programmatic    Mode B: Engine    Mode C: Sidecar  │
│  limiter.Allow(key)      eng.Middleware()  ./ratelimiter     │
│  middleware.HTTP(cfg)    (from YAML)       --config x.yaml  │
│                                            ↓ reverse proxy  │
├─────────────────────────────────────────────────────────────┤
│                         Engine                               │
│  Config Parser → Route Matcher → Per-rule Limiters           │
├─────────────────────────────────────────────────────────────┤
│  HTTP Middleware │ gRPC Interceptors │ Reverse Proxy         │
├─────────────────────────────────────────────────────────────┤
│              InstrumentedLimiter (metrics)                    │
├─────────────────────────────────────────────────────────────┤
│                    Limiter interface                          │
├──────────┬──────────┬────────┬──────────┬───────────────────┤
│  Token   │  Leaky   │ Fixed  │ Sliding  │ Sliding Window    │
│  Bucket  │  Bucket  │ Window │ Win. Log │ Counter           │
├──────────┴──────────┴────────┴──────────┴───────────────────┤
│                    Store interface                            │
├─────────────────────────┬───────────────────────────────────┤
│   Memory Store          │   Redis Store (Lua scripts)        │
└─────────────────────────┴───────────────────────────────────┘
```

## Project Structure

```
rate-limiter/
├── limiter.go              # Limiter interface, Result struct
├── config.go               # Config struct
├── errors.go               # Sentinel errors
├── algorithm/
│   ├── algorithm.go        # Type enum + factory
│   ├── token_bucket.go
│   ├── leaky_bucket.go
│   ├── fixed_window.go
│   ├── sliding_window_log.go
│   ├── sliding_window_counter.go
│   └── bench_test.go
├── store/
│   ├── store.go            # Store interface
│   ├── memory.go           # In-memory store
│   ├── redis.go            # Redis store
│   └── redis_scripts.go    # Lua scripts
├── key/
│   └── key.go              # Key extractors (IP, header, path, composite)
├── middleware/
│   └── http.go             # net/http middleware
├── grpc/
│   └── interceptor.go      # Unary + Stream interceptors
├── metrics/
│   └── prometheus.go       # InstrumentedLimiter decorator
├── engine/
│   ├── engine.go           # Engine composition layer
│   ├── config.go           # YAML config parsing
│   ├── matcher.go          # Glob-based route matcher
│   └── proxy.go            # Reverse proxy handler
├── cmd/ratelimiter/
│   └── main.go             # Sidecar binary
├── internal/clock/
│   └── clock.go            # Clock interface (real + mock)
├── Dockerfile
└── examples/
    ├── basic/              # Direct API usage
    ├── redis/              # Redis-backed limiting
    ├── http-server/        # Programmatic middleware
    ├── engine-server/      # Config-driven engine
    ├── grpc-server/        # gRPC interceptors
    ├── multi-algorithm/    # Algorithm comparison
    └── sidecar/            # Docker + Kubernetes deployment
```

## Integration Modes

| | Mode A: Programmatic | Mode B: Engine | Mode C: Sidecar |
|---|---|---|---|
| **Setup** | Go code | Go code + YAML | YAML + binary |
| **Language** | Go only | Go only | Any |
| **Latency** | ~0 (in-process) | ~0 (in-process) | ~1-2ms (proxy hop) |
| **CPU** | Shared with app | Shared with app | Separate container |
| **Code changes** | Yes | Minimal | None |
| **Best for** | Custom logic | Go services | Polyglot, legacy apps |

## License

MIT
