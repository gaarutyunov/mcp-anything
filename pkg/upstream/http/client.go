package http

import (
	"fmt"
	nethttp "net/http"
	"strings"

	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	pkgtransport "github.com/gaarutyunov/mcp-anything/pkg/transport"
)

// headerRoundTripper injects static headers into every outbound request.
type headerRoundTripper struct {
	headers map[string]string
	wrapped nethttp.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
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
	wrapped nethttp.RoundTripper
}

func (t *tokenInfoHeaderTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	if info := pkginbound.TokenInfoFromContext(req.Context()); info != nil {
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
// Outbound authentication is handled by the per-tool middleware chain (outbound.Middleware)
// rather than a RoundTripper, so no auth transport is added here.
func NewHTTPClient(cfg *config.UpstreamSpec) (*nethttp.Client, error) {
	transportCfg := cfg.Transport
	// Legacy tls_skip_verify field: propagate to transport TLS config for backward compat.
	if cfg.TLSSkipVerify {
		transportCfg.TLS.InsecureSkipVerify = true
	}

	t, err := pkgtransport.NewBuilder().Build(transportCfg)
	if err != nil {
		return nil, fmt.Errorf("building transport: %w", err)
	}

	var rt nethttp.RoundTripper = t
	if len(cfg.Headers) > 0 {
		rt = &headerRoundTripper{headers: cfg.Headers, wrapped: t}
	}

	// Inject extra headers from the inbound TokenInfo (e.g. from Lua check_auth extra_headers).
	rt = &tokenInfoHeaderTransport{wrapped: rt}

	// Wrap with OTel client instrumentation to emit http.client.request.duration.
	rt = pkgtelemetry.ClientTransport(rt)

	return &nethttp.Client{
		Timeout:   cfg.Timeout,
		Transport: rt,
	}, nil
}
