package engine

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/key"
	"github.com/debdutdev/rate-limiter/metrics"
	"github.com/debdutdev/rate-limiter/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// Engine is the top-level composition layer. It reads a config, creates
// a shared store, builds per-rule limiters, and provides middleware that
// routes each request to the correct limiter.
type Engine struct {
	config    EngineConfig
	store     store.Store
	matcher   *routeMatcher
	defaultRL *compiledRule
}

// Option configures the engine.
type Option func(*engineOptions)

type engineOptions struct {
	registerer prometheus.Registerer
	store      store.Store // override store creation (for testing)
}

// WithRegisterer sets the Prometheus registerer for metrics.
func WithRegisterer(r prometheus.Registerer) Option {
	return func(o *engineOptions) { o.registerer = r }
}

// WithStore overrides the store created from config (useful for testing).
func WithStore(s store.Store) Option {
	return func(o *engineOptions) { o.store = s }
}

// New creates an Engine from a YAML config file.
func New(configPath string, opts ...Option) (*Engine, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return NewFromConfig(cfg, opts...)
}

// NewFromConfig creates an Engine from an already-parsed config.
func NewFromConfig(cfg EngineConfig, opts ...Option) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	o := &engineOptions{}
	for _, opt := range opts {
		opt(o)
	}

	// Create or use provided store.
	var s store.Store
	if o.store != nil {
		s = o.store
	} else {
		var err error
		s, err = createStore(cfg.Store)
		if err != nil {
			return nil, fmt.Errorf("creating store: %w", err)
		}
	}

	eng := &Engine{
		config:  cfg,
		store:   s,
		matcher: &routeMatcher{},
	}

	// Build per-rule limiters.
	for _, rule := range cfg.Rules {
		compiled, err := eng.compileRule(rule, o)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		eng.matcher.rules = append(eng.matcher.rules, compiled)
	}

	// Build default rule limiter.
	if cfg.Default != nil {
		compiled, err := eng.compileRule(*cfg.Default, o)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("default rule: %w", err)
		}
		eng.defaultRL = &compiled
	}

	return eng, nil
}

// Config returns the engine's configuration.
func (e *Engine) Config() EngineConfig {
	return e.config
}

// Middleware returns HTTP middleware that rate-limits requests based on
// the engine's config. First matching rule wins; unmatched requests
// fall through to the default rule (or pass through if no default).
func (e *Engine) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := e.matcher.match(r)
			if rule == nil {
				rule = e.defaultRL
			}
			if rule == nil {
				next.ServeHTTP(w, r)
				return
			}

			if rule.config.Skip {
				next.ServeHTTP(w, r)
				return
			}

			k := rule.config.Name + ":" + rule.extractor(r)
			result, err := rule.limiter.Allow(r.Context(), k)
			if err != nil {
				// Fail-open.
				next.ServeHTTP(w, r)
				return
			}

			setRateLimitHeaders(w, result)

			if !result.Allowed {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Close releases all resources held by the engine.
func (e *Engine) Close() error {
	for _, rule := range e.matcher.rules {
		if rule.limiter != nil {
			rule.limiter.Close()
		}
	}
	if e.defaultRL != nil && e.defaultRL.limiter != nil {
		e.defaultRL.limiter.Close()
	}
	return e.store.Close()
}

func (e *Engine) compileRule(rule RuleConfig, opts *engineOptions) (compiledRule, error) {
	if rule.Skip {
		return compiledRule{config: rule}, nil
	}

	algType, err := algorithm.ParseType(rule.Algorithm)
	if err != nil {
		return compiledRule{}, err
	}
	cfg := ratelimiter.Config{
		Rate:   rule.Rate,
		Burst:  rule.Burst,
		Window: rule.Window,
	}

	limiter, err := algorithm.New(algType, cfg, e.store)
	if err != nil {
		return compiledRule{}, err
	}

	if e.config.Metrics.Enabled {
		reg := opts.registerer
		if reg == nil {
			reg = prometheus.DefaultRegisterer
		}
		limiter = metrics.NewInstrumentedLimiter(limiter, metrics.InstrumentedConfig{
			RuleName:   rule.Name,
			Algorithm:  rule.Algorithm,
			Registerer: reg,
		})
	}

	extractor := buildExtractor(rule.KeyBy)

	return compiledRule{
		config:    rule,
		limiter:   limiter,
		extractor: extractor,
	}, nil
}

func buildExtractor(keyBy string) key.Extractor {
	switch {
	case keyBy == "" || keyBy == "ip":
		return key.IPExtractor()
	case strings.HasPrefix(keyBy, "header:"):
		return key.HeaderExtractor(strings.TrimPrefix(keyBy, "header:"))
	case keyBy == "path":
		return key.PathExtractor()
	case strings.HasPrefix(keyBy, "composite:"):
		parts := strings.Split(strings.TrimPrefix(keyBy, "composite:"), ",")
		extractors := make([]key.Extractor, len(parts))
		for i, p := range parts {
			extractors[i] = buildExtractor(strings.TrimSpace(p))
		}
		return key.CompositeExtractor(":", extractors...)
	default:
		return key.IPExtractor()
	}
}

func createStore(cfg StoreConfig) (store.Store, error) {
	switch cfg.Type {
	case "memory", "":
		return store.NewMemory(time.Minute), nil
	case "redis":
		client := redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		return store.NewRedisStore(context.Background(), store.RedisConfig{
			Client: client,
			Prefix: cfg.Redis.Prefix,
		})
	default:
		return nil, fmt.Errorf("%w: unknown store type: %q", ratelimiter.ErrInvalidConfig, cfg.Type)
	}
}

func setRateLimitHeaders(w http.ResponseWriter, result ratelimiter.Result) {
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	if !result.ResetAt.IsZero() {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	}
	if !result.Allowed && !result.RetryAt.IsZero() {
		delta := time.Until(result.RetryAt).Seconds()
		if delta < 1 {
			delta = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(int(delta)))
	}
}
