package engine

import (
	"net/http"
	"strings"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/key"
)

// compiledRule is a rule ready for request-time matching.
type compiledRule struct {
	config    RuleConfig
	limiter   ratelimiter.Limiter // nil when skip is true
	extractor key.Extractor       // nil when skip is true
}

// routeMatcher evaluates rules in declaration order (first match wins).
type routeMatcher struct {
	rules []compiledRule
}

// match returns the first rule whose path pattern and method match the
// request. Returns nil if no rule matches.
func (rm *routeMatcher) match(r *http.Request) *compiledRule {
	for i := range rm.rules {
		rule := &rm.rules[i]
		if rule.config.Method != "" && !strings.EqualFold(rule.config.Method, r.Method) {
			continue
		}
		if matchPath(rule.config.Path, r.URL.Path) {
			return rule
		}
	}
	return nil
}

// matchPath checks if a URL path matches a glob pattern.
//
// Rules:
//   - "*" matches a single path segment (no slashes)
//   - "**" matches zero or more path segments (any depth)
//   - Exact segments must match literally
//   - Trailing slashes are ignored on both pattern and path
func matchPath(pattern, path string) bool {
	pattern = strings.TrimRight(pattern, "/")
	path = strings.TrimRight(path, "/")

	patternParts := splitPath(pattern)
	pathParts := splitPath(path)

	return matchParts(patternParts, pathParts)
}

func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchParts(pattern, path []string) bool {
	pi, pp := 0, 0

	for pi < len(pattern) && pp < len(path) {
		seg := pattern[pi]

		if seg == "**" {
			// If ** is the last pattern segment, it matches everything remaining.
			if pi == len(pattern)-1 {
				return true
			}
			// Try matching the rest of the pattern at each position.
			for tryPP := pp; tryPP <= len(path); tryPP++ {
				if matchParts(pattern[pi+1:], path[tryPP:]) {
					return true
				}
			}
			return false
		}

		if seg == "*" {
			// Matches exactly one segment.
			pi++
			pp++
			continue
		}

		// Literal match.
		if seg != path[pp] {
			return false
		}
		pi++
		pp++
	}

	// Handle trailing ** in pattern.
	for pi < len(pattern) && pattern[pi] == "**" {
		pi++
	}

	return pi == len(pattern) && pp == len(path)
}
