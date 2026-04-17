// Package none registers the "outbound/none" middleware strategy (no authentication).
// Import this package (blank import) to make the strategy available via middleware.New().
package none

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("outbound/none", func(_ context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		if _, ok := cfg.(*config.OutboundAuthConfig); !ok {
			return nil, fmt.Errorf("outbound/none: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		return NewProvider().Wrap, nil
	})
}

// Provider is a no-op provider that adds no authentication headers.
type Provider struct{}

// NewProvider creates a no-op Provider.
func NewProvider() *Provider { return &Provider{} }

// Token returns an empty token; no authentication is injected.
func (p *Provider) Token(_ context.Context) (string, error) { return "", nil }

// RawHeaders returns nil; no authentication headers are injected.
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) { return nil, nil }

// Wrap implements outbound.Middleware. It passes through to next with no credential injection.
func (p *Provider) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outbound.ServeWithProvider(w, r, next, p)
	})
}
