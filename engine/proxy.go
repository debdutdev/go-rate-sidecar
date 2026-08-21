package engine

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// ProxyHandler returns an HTTP handler that rate-limits requests and
// forwards allowed ones to the upstream server configured in the engine.
// Denied requests receive 429 without touching the upstream.
//
// This is the handler used in sidecar/proxy mode. The rate-limiting
// middleware wraps a httputil.ReverseProxy so the request flow is:
//
//	client → engine middleware → reverse proxy → upstream
func (e *Engine) ProxyHandler() (http.Handler, error) {
	if e.config.Upstream.URL == "" {
		return nil, &ProxyConfigError{Msg: "upstream.url is required for proxy mode"}
	}

	upstream, err := url.Parse(e.config.Upstream.URL)
	if err != nil {
		return nil, &ProxyConfigError{Msg: "invalid upstream.url: " + err.Error()}
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = upstream.Host
		},
	}

	if e.config.Upstream.Timeout > 0 {
		proxy.Transport = &http.Transport{
			ResponseHeaderTimeout: e.config.Upstream.Timeout,
		}
	}

	var handler http.Handler = proxy

	// If a health path is configured, bypass rate limiting for it.
	if e.config.Upstream.HealthPath != "" {
		healthPath := e.config.Upstream.HealthPath
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == healthPath {
				proxy.ServeHTTP(w, r)
				return
			}
			e.Middleware()(proxy).ServeHTTP(w, r)
		})
	} else {
		handler = e.Middleware()(proxy)
	}

	return handler, nil
}

// ProxyConfigError indicates a configuration problem for proxy mode.
type ProxyConfigError struct {
	Msg string
}

// Error implements the error interface.
func (e *ProxyConfigError) Error() string {
	return e.Msg
}
