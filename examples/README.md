# Examples

Each directory is a self-contained, runnable Go program demonstrating a specific use case of the rate-limiter library.

## Getting Started

| Example | What It Shows | Run |
|---|---|---|
| [basic](basic/) | Direct `Allow()` calls against a token bucket — the simplest possible usage | `go run ./examples/basic/` |
| [allown](allown/) | `AllowN()` for variable-cost requests (upload endpoints where large files cost more tokens) | `go run ./examples/allown/` |
| [multi-algorithm](multi-algorithm/) | All 5 algorithms side by side with the same traffic pattern | `go run ./examples/multi-algorithm/` |

## HTTP Servers

| Example | What It Shows | Run |
|---|---|---|
| [http-server](http-server/) | Programmatic middleware with custom JSON error handler and health check skip | `go run ./examples/http-server/` |
| [per-route](per-route/) | Different limiters per endpoint (login=strict, search=moderate, feed=generous) | `go run ./examples/per-route/` |
| [multi-tier](multi-tier/) | API key tier-based rate limiting (free vs pro users with different quotas) | `go run ./examples/multi-tier/` |
| [engine-server](engine-server/) | Config-driven engine — all rules in YAML, two lines of Go | `go run ./examples/engine-server/` |

## Integrations

| Example | What It Shows | Run |
|---|---|---|
| [redis](redis/) | Redis-backed sliding window counter (requires Redis on localhost:6379) | `go run ./examples/redis/` |
| [grpc-server](grpc-server/) | gRPC unary + stream interceptors with health check skip | `go run ./examples/grpc-server/` |
| [prometheus](prometheus/) | Prometheus metrics: `InstrumentedLimiter` with `/metrics` endpoint | `go run ./examples/prometheus/` |

## Deployment

| Example | What It Shows |
|---|---|
| [sidecar](sidecar/) | Sidecar proxy configs: Docker Compose + Kubernetes pod manifest |

## Running Examples

All examples except `redis` and `sidecar` need only Go installed — no external dependencies.

```bash
# From the repository root:
go run ./examples/basic/
go run ./examples/allown/
go run ./examples/per-route/
# etc.
```

For HTTP server examples, use curl to test:

```bash
# Single request:
curl -i http://localhost:8080/api/data

# Burst test (watch for 429s):
for i in $(seq 1 20); do
  curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/data
done
```
