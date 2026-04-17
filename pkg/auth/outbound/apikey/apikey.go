// Package apikey registers the "outbound/api_key" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package apikey

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("outbound/api_key", func(_ context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		oc, ok := cfg.(*config.OutboundAuthSpec)
		if !ok {
			return nil, fmt.Errorf("outbound/api_key: expected *config.OutboundAuthSpec, got %T", cfg)
		}
		return outbound.Middleware(NewProvider(oc.APIKey)), nil
	})
}

// Provider injects an API key into a configured request header.
type Provider struct {
	header   string
	valueEnv string
	prefix   string
}

// NewProvider creates a Provider from config.
func NewProvider(cfg config.APIKeyOutboundSpec) *Provider {
	return &Provider{
		header:   cfg.Header,
		valueEnv: cfg.ValueEnv,
		prefix:   cfg.Prefix,
	}
}

// Token returns empty string because API key auth uses RawHeaders().
func (p *Provider) Token(_ context.Context) (string, error) {
	return "", nil
}

// RawHeaders returns the API key header to inject.
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) {
	val := os.Getenv(p.valueEnv)
	if val == "" {
		return nil, fmt.Errorf("outbound API key env var %q is empty or unset", p.valueEnv)
	}
	return map[string]string{p.header: p.prefix + val}, nil
}
