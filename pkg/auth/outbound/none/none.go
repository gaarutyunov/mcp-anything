// Package none registers the "outbound/none" middleware strategy (no authentication).
// Import this package (blank import) to make the strategy available via middleware.New().
package none

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("outbound/none", func(_ context.Context, cfg any) (pkgmiddleware.Builder, error) {
		if _, ok := cfg.(*config.OutboundAuthConfig); !ok {
			return nil, fmt.Errorf("outbound/none: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		return NewProvider(), nil
	})
}

// Provider is a no-op provider that adds no authentication headers.
type Provider struct {
	Next http.Handler
}

// NewProvider creates a no-op Provider.
func NewProvider() *Provider { return &Provider{} }

// Build implements middleware.Builder. It returns a Provider wired to next.
func (p *Provider) Build(next http.Handler) http.Handler {
	return &Provider{Next: next}
}

// Token returns an empty token; no authentication is injected.
func (p *Provider) Token(_ context.Context) (string, error) { return "", nil }

// RawHeaders returns nil; no authentication headers are injected.
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) { return nil, nil }

// ServeHTTP implements http.Handler. It passes through to Next with no credential injection.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.Next.ServeHTTP(w, r)
}
