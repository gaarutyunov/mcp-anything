// Package bearer registers the "outbound/bearer" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package bearer

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
	pkgmiddleware.Register("outbound/bearer", func(_ context.Context, cfg any) (pkgmiddleware.Builder, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/bearer: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		return NewProvider(oc.Bearer), nil
	})
}

// Provider injects a static Bearer token read from an environment variable.
type Provider struct {
	tokenEnv string
	Next     http.Handler
}

// NewProvider creates a Provider from config.
func NewProvider(cfg config.BearerOutboundConfig) *Provider {
	return &Provider{tokenEnv: cfg.TokenEnv}
}

// Build implements middleware.Builder. It returns a Provider wired to next.
func (p *Provider) Build(next http.Handler) http.Handler {
	return &Provider{tokenEnv: p.tokenEnv, Next: next}
}

// Token returns the Bearer token value from the configured environment variable.
func (p *Provider) Token(_ context.Context) (string, error) {
	val := os.Getenv(p.tokenEnv)
	if val == "" {
		return "", fmt.Errorf("outbound bearer token env var %q is empty or unset", p.tokenEnv)
	}
	return val, nil
}

// RawHeaders returns nil because bearer auth uses Token().
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) {
	return nil, nil
}

// ServeHTTP implements http.Handler. It injects a Bearer token into the request context.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token, err := p.Token(ctx)
	if err != nil {
		p.Next.ServeHTTP(w, r.WithContext(outbound.WithAuthResult(ctx, outbound.AuthErrResult(err))))
		return
	}
	if token != "" {
		ctx = outbound.WithHeaders(ctx, map[string]string{"Authorization": "Bearer " + token})
	}
	p.Next.ServeHTTP(w, r.WithContext(ctx))
}
