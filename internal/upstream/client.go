package upstream

import (
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
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

// tokenInfoHeaderTransport reads inbound TokenInfo from the request context and
// injects any extra entries with the "header:" prefix as upstream request headers.
type tokenInfoHeaderTransport struct {
	wrapped http.RoundTripper
}

func (t *tokenInfoHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if info := inbound.TokenInfoFromContext(req.Context()); info != nil {
		r := req.Clone(req.Context())
		for k, v := range info.Extra {
			if hdr, ok := strings.CutPrefix(k, "header:"); ok {
				if s, ok := v.(string); ok {
					r.Header.Set(hdr, s)
				}
			}
		}
		req = r
	}
	return t.wrapped.RoundTrip(req)
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

	// Inject extra headers from the inbound TokenInfo (e.g. from Lua check_auth extra_headers).
	rt = &tokenInfoHeaderTransport{wrapped: rt}

	// Wrap with OTel client instrumentation to emit http.client.request.duration.
	rt = telemetry.ClientTransport(rt)

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: rt,
	}
}
