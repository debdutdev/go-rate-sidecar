package ratelimiter

import "errors"

var (
	// ErrStoreFailed is returned when a store operation (Get, Set, CAS) fails.
	ErrStoreFailed = errors.New("store operation failed")

	// ErrInvalidConfig is returned when a configuration value is out of range or missing.
	ErrInvalidConfig = errors.New("invalid configuration")
)
