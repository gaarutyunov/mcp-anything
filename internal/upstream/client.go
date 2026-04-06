package upstream

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	"github.com/gaarutyunov/mcp-anything/internal/transport"
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
// It uses cfg.Transport for connection pooling, TLS, and dialing settings.
// The legacy cfg.TLSSkipVerify field is honoured for backward compatibility.
// Static headers from cfg.Headers are injected via a custom RoundTripper.
// The provider wraps the transport to inject outbound authentication on every request.
func NewHTTPClient(cfg *config.UpstreamConfig, provider outbound.TokenProvider) (*http.Client, error) {
	transportCfg := cfg.Transport
	// Legacy tls_skip_verify field: propagate to transport TLS config for backward compat.
	if cfg.TLSSkipVerify {
		transportCfg.TLS.InsecureSkipVerify = true
	}

	t, err := transport.NewBuilder().Build(transportCfg)
	if err != nil {
		return nil, fmt.Errorf("building transport: %w", err)
	}

	var rt http.RoundTripper = t
	if len(cfg.Headers) > 0 {
		rt = &headerRoundTripper{headers: cfg.Headers, wrapped: t}
	}

	rt = &outbound.AuthTransport{Base: rt, Provider: provider}

	// Inject extra headers from the inbound TokenInfo (e.g. from Lua check_auth extra_headers).
	rt = &tokenInfoHeaderTransport{wrapped: rt}

	// Wrap with OTel client instrumentation to emit http.client.request.duration.
	rt = telemetry.ClientTransport(rt)

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: rt,
	}, nil
}
