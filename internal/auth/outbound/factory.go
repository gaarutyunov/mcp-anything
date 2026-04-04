package outbound

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// New builds the appropriate TokenProvider from config.
// Returns an error for unknown strategies.
func New(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
	switch cfg.Strategy {
	case "bearer":
		return NewBearerProvider(cfg.Bearer), nil
	case "api_key":
		return NewAPIKeyProvider(cfg.APIKey), nil
	case "oauth2_client_credentials":
		return NewOAuth2CCProvider(ctx, cfg.OAuth2ClientCredentials)
	case "none", "":
		return &NoneProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown outbound auth strategy: %q", cfg.Strategy)
	}
}
