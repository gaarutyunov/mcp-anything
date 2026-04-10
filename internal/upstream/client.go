package upstream

import (
	nethttp "net/http"

	pkgoutbound "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkghttp "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
)

// NewHTTPClient builds an *http.Client for an upstream.
// See pkg/upstream/http.NewHTTPClient.
func NewHTTPClient(cfg *config.UpstreamConfig, provider pkgoutbound.TokenProvider) (*nethttp.Client, error) {
	return pkghttp.NewHTTPClient(cfg, provider)
}
