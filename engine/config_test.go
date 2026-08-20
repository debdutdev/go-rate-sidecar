package engine

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfig_Basic(t *testing.T) {
	yaml := `
store:
  type: memory

default:
  algorithm: token_bucket
  rate: 100
  burst: 200
  key_by: ip

rules:
  - name: auth
    path: "/api/v1/login"
    method: POST
    algorithm: sliding_window_counter
    rate: 5
    window: 1m
    key_by: ip

  - name: health
    path: "/health"
    skip: true
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.Store.Type != "memory" {
		t.Errorf("store.type=%q, want memory", cfg.Store.Type)
	}

	if cfg.Default == nil {
		t.Fatal("default should be set")
	}
	if cfg.Default.Algorithm != "token_bucket" {
		t.Errorf("default.algorithm=%q, want token_bucket", cfg.Default.Algorithm)
	}
	if cfg.Default.Rate != 100 {
		t.Errorf("default.rate=%d, want 100", cfg.Default.Rate)
	}

	if len(cfg.Rules) != 2 {
		t.Fatalf("rules count=%d, want 2", len(cfg.Rules))
	}

	auth := cfg.Rules[0]
	if auth.Name != "auth" {
		t.Errorf("rules[0].name=%q, want auth", auth.Name)
	}
	if auth.Method != "POST" {
		t.Errorf("rules[0].method=%q, want POST", auth.Method)
	}
	if auth.Window != time.Minute {
		t.Errorf("rules[0].window=%v, want 1m", auth.Window)
	}

	health := cfg.Rules[1]
	if !health.Skip {
		t.Error("rules[1].skip should be true")
	}
}

func TestParseConfig_Redis(t *testing.T) {
	yaml := `
store:
  type: redis
  redis:
    addr: "localhost:6379"
    password: "secret"
    db: 2
    prefix: "rl:"
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.Store.Type != "redis" {
		t.Errorf("store.type=%q, want redis", cfg.Store.Type)
	}
	if cfg.Store.Redis.Addr != "localhost:6379" {
		t.Errorf("redis.addr=%q, want localhost:6379", cfg.Store.Redis.Addr)
	}
	if cfg.Store.Redis.Password != "secret" {
		t.Errorf("redis.password=%q, want secret", cfg.Store.Redis.Password)
	}
	if cfg.Store.Redis.DB != 2 {
		t.Errorf("redis.db=%d, want 2", cfg.Store.Redis.DB)
	}
	if cfg.Store.Redis.Prefix != "rl:" {
		t.Errorf("redis.prefix=%q, want rl:", cfg.Store.Redis.Prefix)
	}
}

func TestValidate_InvalidAlgorithm(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "not_real", Rate: 10, Burst: 5},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid algorithm")
	}
	if !strings.Contains(err.Error(), "not_real") {
		t.Errorf("error should mention algorithm name: %v", err)
	}
}

func TestValidate_MissingRate(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "token_bucket", Burst: 5},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing rate")
	}
	if !strings.Contains(err.Error(), "rate must be > 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingBurst(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "token_bucket", Rate: 10},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing burst on bucket algorithm")
	}
	if !strings.Contains(err.Error(), "burst must be > 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingWindow(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "fixed_window", Rate: 10},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing window")
	}
	if !strings.Contains(err.Error(), "window must be > 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingPath(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Algorithm: "token_bucket", Rate: 10, Burst: 5},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_DuplicateNames(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "dup", Path: "/a", Algorithm: "token_bucket", Rate: 10, Burst: 5},
			{Name: "dup", Path: "/b", Algorithm: "token_bucket", Rate: 10, Burst: 5},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}
	if !strings.Contains(err.Error(), "duplicate rule name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_InvalidKeyBy(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "bad", Path: "/api", Algorithm: "token_bucket", Rate: 10, Burst: 5, KeyBy: "bogus"},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid key_by")
	}
	if !strings.Contains(err.Error(), "unknown key_by format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_InvalidRedisNoAddr(t *testing.T) {
	cfg := EngineConfig{
		Store: StoreConfig{Type: "redis"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for redis without addr")
	}
	if !strings.Contains(err.Error(), "redis.addr is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_InvalidStoreType(t *testing.T) {
	cfg := EngineConfig{
		Store: StoreConfig{Type: "mongodb"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid store type")
	}
	if !strings.Contains(err.Error(), "store.type must be") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_SkipRuleRequiresNoAlgorithm(t *testing.T) {
	cfg := EngineConfig{
		Rules: []RuleConfig{
			{Name: "skip-me", Path: "/health", Skip: true},
		},
	}
	err := cfg.Validate()
	if err != nil {
		t.Fatalf("skip rule should not require algorithm: %v", err)
	}
}

func TestValidate_ValidKeyByFormats(t *testing.T) {
	formats := []string{
		"ip",
		"path",
		"header:X-API-Key",
		"composite:ip,header:X-User-ID",
	}
	for _, keyBy := range formats {
		t.Run(keyBy, func(t *testing.T) {
			cfg := EngineConfig{
				Rules: []RuleConfig{
					{Name: "test", Path: "/api", Algorithm: "token_bucket", Rate: 10, Burst: 5, KeyBy: keyBy},
				},
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("key_by=%q should be valid: %v", keyBy, err)
			}
		})
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := EngineConfig{
		Store: StoreConfig{Type: "memory"},
		Default: &RuleConfig{
			Algorithm: "token_bucket",
			Rate:      100,
			Burst:     200,
			KeyBy:     "ip",
		},
		Rules: []RuleConfig{
			{Name: "auth", Path: "/api/v1/login", Method: "POST", Algorithm: "sliding_window_counter", Rate: 5, Window: time.Minute, KeyBy: "ip"},
			{Name: "users", Path: "/api/v1/users/*", Algorithm: "token_bucket", Rate: 50, Burst: 100, KeyBy: "header:X-API-Key"},
			{Name: "health", Path: "/health", Skip: true},
			{Name: "catchall", Path: "/api/**", Algorithm: "token_bucket", Rate: 500, Burst: 1000, KeyBy: "ip"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}
}
