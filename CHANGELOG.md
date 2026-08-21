# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-21

### Added

- Five rate limiting algorithms: Token Bucket, Leaky Bucket, Fixed Window, Sliding Window Log, Sliding Window Counter
- In-memory store with background eviction
- Redis store with atomic Lua scripts (single round-trip per check)
- HTTP middleware (`func(http.Handler) http.Handler`) with X-RateLimit headers and Retry-After
- gRPC unary and stream server interceptors with `codes.ResourceExhausted`
- Prometheus metrics decorator (requests_total, usage_ratio, check_duration, store_errors)
- Config-driven engine with YAML parsing and glob-based route matching
- Sidecar/reverse proxy mode with Dockerfile and Kubernetes manifest
- Key extractors: IP, header, path, composite
- Fail-open behavior on store errors
- Input validation on all constructors (nil store, invalid config, negative AllowN)
- CI pipeline with GitHub Actions
- Examples for all integration modes
