package ratelimiter

import "errors"

var (
	ErrRateLimited   = errors.New("rate limited")
	ErrStoreFailed   = errors.New("store operation failed")
	ErrInvalidConfig = errors.New("invalid configuration")
	ErrLimiterClosed = errors.New("limiter has been closed")
)
