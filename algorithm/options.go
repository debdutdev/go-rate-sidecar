package algorithm

import "github.com/debdutdev/rate-limiter/internal/clock"

type options struct {
	clock clock.Clock
}

// Option configures algorithm constructors.
type Option func(*options)

// WithClock overrides the clock used by the algorithm (for testing).
func WithClock(c clock.Clock) Option {
	return func(o *options) {
		o.clock = c
	}
}

func applyOptions(opts []Option) options {
	o := options{
		clock: clock.RealClock{},
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
