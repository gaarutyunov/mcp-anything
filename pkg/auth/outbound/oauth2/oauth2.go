// Package oauth2 registers the "outbound/oauth2_client_credentials" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package oauth2

import (
	"context"
	"fmt"
	"net/http"
	"os"

	gooauth2 "golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("outbound/oauth2_client_credentials", func(ctx context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/oauth2_client_credentials: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		p, err := NewProvider(ctx, oc.OAuth2ClientCredentials)
		if err != nil {
			return nil, err
		}
		return p.Middleware(), nil
	})
}

// Provider obtains tokens via the OAuth2 client credentials flow.
// It caches the token and refreshes it automatically before expiry.
type Provider struct {
	outbound.ProviderBase
	src gooauth2.TokenSource
}

// NewProvider creates a Provider configured for the client credentials flow.
// The token source handles caching and automatic refresh.
func NewProvider(ctx context.Context, cfg config.OAuth2CCConfig) (*Provider, error) {
	ccCfg := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: os.ExpandEnv(cfg.ClientSecret),
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	src := gooauth2.ReuseTokenSource(nil, ccCfg.TokenSource(ctx))
	p := &Provider{src: src}
	p.ProviderBase = outbound.NewProviderBase(p)
	return p, nil
}

// Token returns a valid access token, refreshing if the cached token has expired.
func (p *Provider) Token(_ context.Context) (string, error) {
	tok, err := p.src.Token()
	if err != nil {
		return "", fmt.Errorf("fetching oauth2 client credentials token: %w", err)
	}
	return tok.AccessToken, nil
}

// RawHeaders returns nil because OAuth2 auth uses Token().
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) {
	return nil, nil
}
