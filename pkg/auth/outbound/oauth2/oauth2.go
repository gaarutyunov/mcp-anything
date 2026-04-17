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
	pkgmiddleware.Register("outbound/oauth2_client_credentials", func(ctx context.Context, cfg any) (pkgmiddleware.Builder, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/oauth2_client_credentials: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		return NewProvider(ctx, oc.OAuth2ClientCredentials)
	})
}

// Provider obtains tokens via the OAuth2 client credentials flow.
// It caches the token and refreshes it automatically before expiry.
type Provider struct {
	src  gooauth2.TokenSource
	Next http.Handler
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
	return &Provider{src: src}, nil
}

// Build implements middleware.Builder. It returns a Provider wired to next, sharing the token source.
func (p *Provider) Build(next http.Handler) http.Handler {
	return &Provider{src: p.src, Next: next}
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

// ServeHTTP implements http.Handler. It injects an OAuth2 access token into the request context.
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
