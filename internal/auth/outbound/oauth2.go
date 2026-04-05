package outbound

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// OAuth2CCProvider obtains tokens via the OAuth2 client credentials flow.
// It caches the token and refreshes it automatically before expiry.
type OAuth2CCProvider struct {
	src oauth2.TokenSource
}

// NewOAuth2CCProvider creates an OAuth2CCProvider configured for the client credentials flow.
// The token source handles caching and automatic refresh.
func NewOAuth2CCProvider(ctx context.Context, cfg config.OAuth2CCConfig) (*OAuth2CCProvider, error) {
	ccCfg := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: os.ExpandEnv(cfg.ClientSecret),
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	src := oauth2.ReuseTokenSource(nil, ccCfg.TokenSource(ctx))
	return &OAuth2CCProvider{src: src}, nil
}

// Token returns a valid access token, refreshing if the cached token has expired.
func (p *OAuth2CCProvider) Token(_ context.Context) (string, error) {
	tok, err := p.src.Token()
	if err != nil {
		return "", fmt.Errorf("fetching oauth2 client credentials token: %w", err)
	}
	return tok.AccessToken, nil
}

// RawHeaders returns nil because OAuth2 auth uses Token().
func (p *OAuth2CCProvider) RawHeaders(_ context.Context) (map[string]string, error) {
	return nil, nil
}
