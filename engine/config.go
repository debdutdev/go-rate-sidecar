package engine

import (
	"fmt"
	"os"
	"strings"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"gopkg.in/yaml.v3"
)

// EngineConfig is the top-level configuration parsed from YAML.
type EngineConfig struct {
	Server   ServerConfig   `yaml:"server"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Store    StoreConfig    `yaml:"store"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	Default  *RuleConfig    `yaml:"default"`
	Rules    []RuleConfig   `yaml:"rules"`
}

// ServerConfig controls the proxy listener (sidecar mode only).
type ServerConfig struct {
	Listen string `yaml:"listen"`
}

// UpstreamConfig controls the proxy target (sidecar mode only).
type UpstreamConfig struct {
	URL        string        `yaml:"url"`
	Timeout    time.Duration `yaml:"timeout"`
	HealthPath string        `yaml:"health_path"`
}

// StoreConfig controls the shared backing store.
type StoreConfig struct {
	Type  string      `yaml:"type"`
	Redis RedisConfig `yaml:"redis"`
}

// RedisConfig holds Redis connection options.
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	Prefix   string `yaml:"prefix"`
}

// MetricsConfig controls Prometheus instrumentation.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// RuleConfig defines a single rate-limiting rule.
type RuleConfig struct {
	Name      string        `yaml:"name"`
	Path      string        `yaml:"path"`
	Method    string        `yaml:"method"`
	Algorithm string        `yaml:"algorithm"`
	Rate      int64         `yaml:"rate"`
	Burst     int64         `yaml:"burst"`
	Window    time.Duration `yaml:"window"`
	KeyBy     string        `yaml:"key_by"`
	Skip      bool          `yaml:"skip"`
}

// LoadConfig reads and parses a YAML config file.
func LoadConfig(path string) (EngineConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EngineConfig{}, fmt.Errorf("reading config: %w", err)
	}
	return ParseConfig(data)
}

// ParseConfig parses YAML bytes into an EngineConfig.
func ParseConfig(data []byte) (EngineConfig, error) {
	var cfg EngineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return EngineConfig{}, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// Validate checks the config for errors that should fail at startup.
func (cfg *EngineConfig) Validate() error {
	if cfg.Store.Type == "" {
		cfg.Store.Type = "memory"
	}
	if cfg.Store.Type != "memory" && cfg.Store.Type != "redis" {
		return fmt.Errorf("%w: store.type must be 'memory' or 'redis', got %q", ratelimiter.ErrInvalidConfig, cfg.Store.Type)
	}
	if cfg.Store.Type == "redis" && cfg.Store.Redis.Addr == "" {
		return fmt.Errorf("%w: store.redis.addr is required when store.type is 'redis'", ratelimiter.ErrInvalidConfig)
	}

	if cfg.Default != nil {
		if err := validateRule(*cfg.Default, "default"); err != nil {
			return err
		}
	}

	names := make(map[string]bool)
	for i, rule := range cfg.Rules {
		label := rule.Name
		if label == "" {
			label = fmt.Sprintf("rules[%d]", i)
		}

		if rule.Name != "" {
			if names[rule.Name] {
				return fmt.Errorf("%w: duplicate rule name %q", ratelimiter.ErrInvalidConfig, rule.Name)
			}
			names[rule.Name] = true
		}

		if rule.Path == "" {
			return fmt.Errorf("%w: %s: path is required", ratelimiter.ErrInvalidConfig, label)
		}

		if rule.Skip {
			continue
		}

		if err := validateRule(rule, label); err != nil {
			return err
		}
	}

	return nil
}

func validateRule(rule RuleConfig, label string) error {
	if rule.Skip {
		return nil
	}

	if rule.Algorithm == "" {
		return fmt.Errorf("%w: %s: algorithm is required", ratelimiter.ErrInvalidConfig, label)
	}

	if _, err := algorithm.ParseType(rule.Algorithm); err != nil {
		return fmt.Errorf("%w: %s: %v", ratelimiter.ErrInvalidConfig, label, err)
	}

	if rule.Rate <= 0 {
		return fmt.Errorf("%w: %s: rate must be > 0", ratelimiter.ErrInvalidConfig, label)
	}

	algType, _ := algorithm.ParseType(rule.Algorithm)
	switch algType {
	case algorithm.TypeTokenBucket, algorithm.TypeLeakyBucket:
		if rule.Burst <= 0 {
			return fmt.Errorf("%w: %s: burst must be > 0 for %s", ratelimiter.ErrInvalidConfig, label, rule.Algorithm)
		}
	case algorithm.TypeFixedWindow, algorithm.TypeSlidingWindowLog, algorithm.TypeSlidingWindowCounter:
		if rule.Window <= 0 {
			return fmt.Errorf("%w: %s: window must be > 0 for %s", ratelimiter.ErrInvalidConfig, label, rule.Algorithm)
		}
	}

	if rule.KeyBy != "" {
		if err := validateKeyBy(rule.KeyBy); err != nil {
			return fmt.Errorf("%w: %s: %v", ratelimiter.ErrInvalidConfig, label, err)
		}
	}

	return nil
}

func validateKeyBy(keyBy string) error {
	switch {
	case keyBy == "ip":
		return nil
	case strings.HasPrefix(keyBy, "header:"):
		header := strings.TrimPrefix(keyBy, "header:")
		if header == "" {
			return fmt.Errorf("key_by 'header:' requires a header name")
		}
		return nil
	case keyBy == "path":
		return nil
	case strings.HasPrefix(keyBy, "composite:"):
		parts := strings.Split(strings.TrimPrefix(keyBy, "composite:"), ",")
		if len(parts) < 2 {
			return fmt.Errorf("key_by 'composite:' requires at least 2 extractors")
		}
		for _, p := range parts {
			if err := validateKeyBy(strings.TrimSpace(p)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown key_by format: %q", keyBy)
	}
}
