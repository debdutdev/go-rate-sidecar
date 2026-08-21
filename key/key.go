package key

import (
	"net"
	"net/http"
	"strings"
)

// Extractor produces a rate-limiting key from an HTTP request.
type Extractor func(r *http.Request) string

// IPExtractor returns the client IP from X-Forwarded-For, X-Real-IP,
// or RemoteAddr (in that order of precedence).
func IPExtractor() Extractor {
	return func(r *http.Request) string {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}

// HeaderExtractor returns the value of a specific header as the key.
// If the header is absent, falls back to the client IP so that
// requests without the header are still rate-limited individually
// rather than sharing one empty-string bucket.
func HeaderExtractor(header string) Extractor {
	ipFallback := IPExtractor()
	return func(r *http.Request) string {
		if v := r.Header.Get(header); v != "" {
			return v
		}
		return ipFallback(r)
	}
}

// PathExtractor returns the request URL path as the key.
func PathExtractor() Extractor {
	return func(r *http.Request) string {
		return r.URL.Path
	}
}

// CompositeExtractor concatenates multiple extractors with a separator.
func CompositeExtractor(sep string, extractors ...Extractor) Extractor {
	return func(r *http.Request) string {
		parts := make([]string, len(extractors))
		for i, e := range extractors {
			parts[i] = e(r)
		}
		return strings.Join(parts, sep)
	}
}
