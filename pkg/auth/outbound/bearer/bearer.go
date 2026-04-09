// Package bearer registers the "bearer" outbound auth strategy.
// Import this package (blank import) to make the strategy available via outbound.New().
package bearer

import (
	"context"
	"fmt"
	"os"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	outbound.Register("bearer", func(_ context.Context, cfg *config.OutboundAuthConfig) (outbound.TokenProvider, error) {
		return NewProvider(cfg.Bearer), nil
	})
}

// Provider injects a static Bearer token read from an environment variable.
type Provider struct {
	tokenEnv string
}

// NewProvider creates a Provider from config.
func NewProvider(cfg config.BearerOutboundConfig) *Provider {
	return &Provider{tokenEnv: cfg.TokenEnv}
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
