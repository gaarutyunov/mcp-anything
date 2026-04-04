package upstream

import (
	"crypto/tls"
	"net/http"

	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// headerRoundTripper injects static headers into every outbound request.
type headerRoundTripper struct {
	headers map[string]string
	wrapped http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		if r.Header.Get(k) == "" {
			r.Header.Set(k, v)
		}
	}
	return h.wrapped.RoundTrip(r)
}

// NewHTTPClient builds an *http.Client for an upstream.
// It honours cfg.Timeout, cfg.TLSSkipVerify, and injects cfg.Headers via a
// custom RoundTripper so every request carries the configured static headers.
// The provider wraps the transport to inject outbound authentication on every request.
func NewHTTPClient(cfg *config.UpstreamConfig, provider outbound.TokenProvider) *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	}
	transport = transport.Clone()

	if cfg.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	var rt http.RoundTripper = transport
	if len(cfg.Headers) > 0 {
		rt = &headerRoundTripper{headers: cfg.Headers, wrapped: transport}
	}

	rt = &outbound.AuthTransport{Base: rt, Provider: provider}

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: rt,
	}
}
