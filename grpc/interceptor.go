package grpc

import (
	"context"
	"strconv"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// KeyExtractor derives a rate-limiting key from gRPC context and method info.
type KeyExtractor func(ctx context.Context, fullMethod string) string

// InterceptorConfig configures the gRPC rate-limiting interceptors.
type InterceptorConfig struct {
	// Limiter is the rate limiter to use. Required.
	Limiter ratelimiter.Limiter

	// KeyExtractor derives the rate-limiting key from the gRPC context.
	// Defaults to peer address extraction if nil.
	KeyExtractor KeyExtractor

	// SkipFunc returns true for methods that should bypass rate limiting.
	// Defaults to nil (no methods skipped).
	SkipFunc func(fullMethod string) bool
}

// PeerAddrExtractor returns the peer's address as the rate-limiting key.
func PeerAddrExtractor() KeyExtractor {
	return func(ctx context.Context, _ string) string {
		if p, ok := peer.FromContext(ctx); ok {
			return p.Addr.String()
		}
		return "unknown"
	}
}

// MetadataExtractor returns the value of a metadata key as the rate-limiting key.
func MetadataExtractor(key string) KeyExtractor {
	return func(ctx context.Context, _ string) string {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			vals := md.Get(key)
			if len(vals) > 0 {
				return vals[0]
			}
		}
		return ""
	}
}

// MethodExtractor returns the full gRPC method name as the rate-limiting key.
func MethodExtractor() KeyExtractor {
	return func(_ context.Context, fullMethod string) string {
		return fullMethod
	}
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces rate limiting. Denied requests receive codes.ResourceExhausted
// with rate-limit details in trailing metadata.
func UnaryServerInterceptor(cfg InterceptorConfig) grpc.UnaryServerInterceptor {
	if cfg.KeyExtractor == nil {
		cfg.KeyExtractor = PeerAddrExtractor()
	}

	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if cfg.SkipFunc != nil && cfg.SkipFunc(info.FullMethod) {
			return handler(ctx, req)
		}

		key := cfg.KeyExtractor(ctx, info.FullMethod)
		result, err := cfg.Limiter.Allow(ctx, key)
		if err != nil {
			// Fail-open on store errors.
			return handler(ctx, req)
		}

		setRateLimitMetadata(ctx, result)

		if !result.Allowed {
			return nil, rateLimitStatus(result).Err()
		}

		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that
// enforces rate limiting. Denied streams receive codes.ResourceExhausted
// before the stream handler is invoked.
func StreamServerInterceptor(cfg InterceptorConfig) grpc.StreamServerInterceptor {
	if cfg.KeyExtractor == nil {
		cfg.KeyExtractor = PeerAddrExtractor()
	}

	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if cfg.SkipFunc != nil && cfg.SkipFunc(info.FullMethod) {
			return handler(srv, ss)
		}

		ctx := ss.Context()
		key := cfg.KeyExtractor(ctx, info.FullMethod)
		result, err := cfg.Limiter.Allow(ctx, key)
		if err != nil {
			return handler(srv, ss)
		}

		setRateLimitMetadata(ctx, result)

		if !result.Allowed {
			return rateLimitStatus(result).Err()
		}

		return handler(srv, ss)
	}
}

func setRateLimitMetadata(ctx context.Context, result ratelimiter.Result) {
	md := metadata.Pairs(
		"x-ratelimit-limit", strconv.FormatInt(result.Limit, 10),
		"x-ratelimit-remaining", strconv.FormatInt(result.Remaining, 10),
	)
	if !result.ResetAt.IsZero() {
		md.Append("x-ratelimit-reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	}
	if !result.Allowed && !result.RetryAt.IsZero() {
		md.Append("retry-after", strconv.FormatInt(result.RetryAt.Unix(), 10))
	}
	grpc.SetTrailer(ctx, md)
}

func rateLimitStatus(result ratelimiter.Result) *status.Status {
	st := status.New(codes.ResourceExhausted, "rate limit exceeded")
	return st
}
