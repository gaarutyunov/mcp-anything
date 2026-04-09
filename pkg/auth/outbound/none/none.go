// Package none registers the "none" outbound auth strategy (no authentication).
// Import this package (blank import) to make the strategy available via outbound.New().
package none

import (
	"context"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	outbound.Register("none", func(_ context.Context, _ *config.OutboundAuthConfig) (outbound.TokenProvider, error) {
		return &Provider{}, nil
	})
}

// Provider is a no-op provider that adds no authentication headers.
type Provider struct{}

// Token returns an empty token; no authentication is injected.
func (p *Provider) Token(_ context.Context) (string, error) { return "", nil }

// RawHeaders returns nil; no authentication headers are injected.
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) { return nil, nil }
