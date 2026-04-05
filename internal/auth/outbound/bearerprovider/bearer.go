// Package bearerprovider registers the "bearer" outbound auth strategy.
package bearerprovider

import (
	"context"
	"fmt"
	"os"

	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func init() {
	outbound.RegisterProvider("bearer", func(_ context.Context, cfg *config.OutboundAuthConfig, _ *runtime.Registry) (outbound.TokenProvider, error) {
		return NewBearerProvider(cfg.Bearer), nil
	})
}

// BearerProvider injects a static Bearer token read from an environment variable.
type BearerProvider struct {
	tokenEnv string
}

// NewBearerProvider creates a BearerProvider from config.
func NewBearerProvider(cfg config.BearerOutboundConfig) *BearerProvider {
	return &BearerProvider{tokenEnv: cfg.TokenEnv}
}

// Token returns the Bearer token value from the configured environment variable.
func (p *BearerProvider) Token(_ context.Context) (string, error) {
	val := os.Getenv(p.tokenEnv)
	if val == "" {
		return "", fmt.Errorf("outbound bearer token env var %q is empty or unset", p.tokenEnv)
	}
	return val, nil
}

// RawHeaders returns nil because bearer auth uses Token().
func (p *BearerProvider) RawHeaders(_ context.Context) (map[string]string, error) {
	return nil, nil
}
