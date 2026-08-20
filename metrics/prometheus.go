package metrics

import (
	"context"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/prometheus/client_golang/prometheus"
)

// InstrumentedLimiter wraps any Limiter and records Prometheus metrics
// on every Allow/AllowN call. It implements the Limiter interface so
// it can be used as a drop-in replacement anywhere a Limiter is expected.
type InstrumentedLimiter struct {
	limiter ratelimiter.Limiter

	requestsTotal  *prometheus.CounterVec
	usageRatio     *prometheus.GaugeVec
	checkDuration  *prometheus.HistogramVec
	storeErrors    *prometheus.CounterVec

	labels prometheus.Labels // static labels applied to every observation
}

// InstrumentedConfig configures the InstrumentedLimiter.
type InstrumentedConfig struct {
	// RuleName identifies this limiter in metrics (e.g. "auth-strict").
	RuleName string

	// Algorithm is the algorithm name (e.g. "token_bucket").
	Algorithm string

	// Registerer is the Prometheus registerer. Defaults to prometheus.DefaultRegisterer.
	Registerer prometheus.Registerer

	// Namespace prefixes all metric names (e.g. "myapp" → "myapp_ratelimiter_requests_total").
	Namespace string

	// Subsystem is inserted between namespace and metric name.
	Subsystem string
}

// NewInstrumentedLimiter wraps a Limiter with Prometheus instrumentation.
// Metrics are registered on the provided (or default) registerer.
// If the same metric names are already registered (e.g. multiple rules
// sharing a registerer), the existing collectors are reused.
func NewInstrumentedLimiter(limiter ratelimiter.Limiter, cfg InstrumentedConfig) *InstrumentedLimiter {
	if cfg.Registerer == nil {
		cfg.Registerer = prometheus.DefaultRegisterer
	}

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "ratelimiter"
	}

	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: cfg.Subsystem,
		Name:      "requests_total",
		Help:      "Total number of rate limit checks partitioned by decision.",
	}, []string{"rule", "algorithm", "decision"})

	usageRatio := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: cfg.Subsystem,
		Name:      "current_usage_ratio",
		Help:      "Current usage as a ratio of limit (0.0 to 1.0+).",
	}, []string{"rule", "algorithm"})

	checkDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: cfg.Subsystem,
		Name:      "check_duration_seconds",
		Help:      "Latency of rate limit checks.",
		Buckets:   []float64{.00001, .00005, .0001, .0005, .001, .005, .01, .05, .1},
	}, []string{"rule", "algorithm"})

	storeErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: cfg.Subsystem,
		Name:      "store_errors_total",
		Help:      "Total number of store errors during rate limit checks.",
	}, []string{"rule", "algorithm"})

	// Use MustRegister-or-reuse pattern to handle shared registries.
	requestsTotal = registerOrReuse(cfg.Registerer, requestsTotal).(*prometheus.CounterVec)
	usageRatio = registerOrReuse(cfg.Registerer, usageRatio).(*prometheus.GaugeVec)
	checkDuration = registerOrReuse(cfg.Registerer, checkDuration).(*prometheus.HistogramVec)
	storeErrors = registerOrReuse(cfg.Registerer, storeErrors).(*prometheus.CounterVec)

	return &InstrumentedLimiter{
		limiter:       limiter,
		requestsTotal: requestsTotal,
		usageRatio:    usageRatio,
		checkDuration: checkDuration,
		storeErrors:   storeErrors,
		labels: prometheus.Labels{
			"rule":      cfg.RuleName,
			"algorithm": cfg.Algorithm,
		},
	}
}

func registerOrReuse(reg prometheus.Registerer, c prometheus.Collector) prometheus.Collector {
	err := reg.Register(c)
	if err == nil {
		return c
	}
	if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
		return are.ExistingCollector
	}
	// Shouldn't happen with valid collectors. Panic matches prometheus.MustRegister behavior.
	panic(err)
}

func (il *InstrumentedLimiter) Allow(ctx context.Context, key string) (ratelimiter.Result, error) {
	return il.AllowN(ctx, key, 1)
}

func (il *InstrumentedLimiter) AllowN(ctx context.Context, key string, n int64) (ratelimiter.Result, error) {
	start := time.Now()
	result, err := il.limiter.AllowN(ctx, key, n)
	elapsed := time.Since(start).Seconds()

	il.checkDuration.With(il.labels).Observe(elapsed)

	if err != nil {
		il.storeErrors.With(il.labels).Inc()
		return result, err
	}

	decision := "allowed"
	if !result.Allowed {
		decision = "denied"
	}

	il.requestsTotal.With(prometheus.Labels{
		"rule":      il.labels["rule"],
		"algorithm": il.labels["algorithm"],
		"decision":  decision,
	}).Inc()

	if result.Limit > 0 {
		used := float64(result.Limit-result.Remaining) / float64(result.Limit)
		il.usageRatio.With(il.labels).Set(used)
	}

	return result, nil
}

func (il *InstrumentedLimiter) Reset(ctx context.Context, key string) error {
	return il.limiter.Reset(ctx, key)
}

func (il *InstrumentedLimiter) Close() error {
	return il.limiter.Close()
}
