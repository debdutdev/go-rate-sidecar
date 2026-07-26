package ratelimiter

import "time"

// Config holds the parameters for constructing a limiter.
// Not all fields apply to every algorithm — see per-algorithm docs.
type Config struct {
	// Rate is the sustained request rate: requests per Window (fixed/sliding)
	// or tokens per second (token bucket) or drain rate (leaky bucket).
	Rate int64

	// Burst is the maximum instantaneous burst size.
	// Applies to Token Bucket (bucket capacity) and Leaky Bucket (queue depth).
	// Ignored by window-based algorithms.
	Burst int64

	// Window is the time window for Fixed Window, Sliding Window Log,
	// and Sliding Window Counter algorithms.
	Window time.Duration
}
