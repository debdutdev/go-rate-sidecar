// Package ratelimiter provides a production-grade rate limiting library for Go.
//
// It supports five algorithms (Token Bucket, Leaky Bucket, Fixed Window,
// Sliding Window Log, Sliding Window Counter), two storage backends
// (in-memory and Redis), and integrates as HTTP middleware, gRPC interceptor,
// or a standalone reverse-proxy sidecar.
package ratelimiter
