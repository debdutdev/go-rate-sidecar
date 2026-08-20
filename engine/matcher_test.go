package engine

import "testing"

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Exact matches.
		{"/api/v1/login", "/api/v1/login", true},
		{"/api/v1/login", "/api/v1/signup", false},
		{"/health", "/health", true},
		{"/health", "/healthz", false},

		// Single wildcard (*) — matches one segment.
		{"/api/v1/users/*", "/api/v1/users/123", true},
		{"/api/v1/users/*", "/api/v1/users/123/orders", false},
		{"/api/*/users", "/api/v1/users", true},
		{"/api/*/users", "/api/v2/users", true},
		{"/api/*/users", "/api/v1/admin", false},

		// Double wildcard (**) — matches any depth.
		{"/api/**", "/api/v1/users", true},
		{"/api/**", "/api/v1/users/123/orders", true},
		{"/api/**", "/api", true},
		{"/api/**", "/other", false},
		{"/api/**/orders", "/api/v1/users/123/orders", true},
		{"/api/**/orders", "/api/orders", true},

		// Root path.
		{"/", "/", true},
		{"/", "/api", false},

		// Trailing slashes ignored.
		{"/api/v1/", "/api/v1", true},
		{"/api/v1", "/api/v1/", true},

		// Complex patterns.
		{"/api/v1/*/orders/*", "/api/v1/users/orders/456", true},
		{"/api/v1/*/orders/*", "/api/v1/users/orders/456/items", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"→"+tt.path, func(t *testing.T) {
			got := matchPath(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchPath_FirstMatchWins(t *testing.T) {
	rules := []RuleConfig{
		{Name: "exact", Path: "/api/v1/login"},
		{Name: "wildcard", Path: "/api/v1/*"},
		{Name: "catchall", Path: "/api/**"},
	}

	rm := &routeMatcher{}
	for _, r := range rules {
		rm.rules = append(rm.rules, compiledRule{config: r})
	}

	tests := []struct {
		path     string
		wantRule string
	}{
		{"/api/v1/login", "exact"},
		{"/api/v1/users", "wildcard"},
		{"/api/v2/anything", "catchall"},
		{"/api/v1/users/123", "catchall"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := newRequest(t, "GET", tt.path)
			rule := rm.match(r)
			if rule == nil {
				t.Fatalf("no match for %s", tt.path)
			}
			if rule.config.Name != tt.wantRule {
				t.Errorf("matched %q, want %q", rule.config.Name, tt.wantRule)
			}
		})
	}
}

func TestMatcher_MethodFilter(t *testing.T) {
	rm := &routeMatcher{
		rules: []compiledRule{
			{config: RuleConfig{Name: "post-only", Path: "/api/v1/login", Method: "POST"}},
			{config: RuleConfig{Name: "all-methods", Path: "/api/v1/login"}},
		},
	}

	// POST should match the first rule.
	r := newRequest(t, "POST", "/api/v1/login")
	rule := rm.match(r)
	if rule == nil || rule.config.Name != "post-only" {
		t.Errorf("POST should match post-only, got %v", rule)
	}

	// GET should skip the first rule (method mismatch) and match the second.
	r = newRequest(t, "GET", "/api/v1/login")
	rule = rm.match(r)
	if rule == nil || rule.config.Name != "all-methods" {
		t.Errorf("GET should match all-methods, got %v", rule)
	}
}

func TestMatcher_NoMatch(t *testing.T) {
	rm := &routeMatcher{
		rules: []compiledRule{
			{config: RuleConfig{Name: "api", Path: "/api/**"}},
		},
	}

	r := newRequest(t, "GET", "/other/path")
	rule := rm.match(r)
	if rule != nil {
		t.Errorf("should not match, got %q", rule.config.Name)
	}
}
