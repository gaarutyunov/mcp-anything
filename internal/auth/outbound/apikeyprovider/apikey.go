// Package apikeyprovider registers the "api_key" outbound auth strategy.
package apikeyprovider

import (
	"context"
	"fmt"
	"os"

	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func init() {
	outbound.RegisterProvider("api_key", func(_ context.Context, cfg *config.OutboundAuthConfig, _ *runtime.Registry) (outbound.TokenProvider, error) {
		return NewAPIKeyProvider(cfg.APIKey), nil
	})
}

// APIKeyProvider injects an API key into a configured request header.
type APIKeyProvider struct {
	header   string
	valueEnv string
	prefix   string
}

// NewAPIKeyProvider creates an APIKeyProvider from config.
func NewAPIKeyProvider(cfg config.APIKeyOutboundConfig) *APIKeyProvider {
	return &APIKeyProvider{
		header:   cfg.Header,
		valueEnv: cfg.ValueEnv,
		prefix:   cfg.Prefix,
	}
}

// Token returns empty string because API key auth uses RawHeaders().
func (p *APIKeyProvider) Token(_ context.Context) (string, error) {
	return "", nil
}

// RawHeaders returns the API key header to inject.
func (p *APIKeyProvider) RawHeaders(_ context.Context) (map[string]string, error) {
	val := os.Getenv(p.valueEnv)
	if val == "" {
		return nil, fmt.Errorf("outbound API key env var %q is empty or unset", p.valueEnv)
	}
	return map[string]string{p.header: p.prefix + val}, nil
}
