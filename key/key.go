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
func HeaderExtractor(header string) Extractor {
	return func(r *http.Request) string {
		return r.Header.Get(header)
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
