// Package oauth2usersession registers the "outbound/oauth2_user_session" middleware strategy.
// It stores per-user OAuth tokens in a session store and automatically refreshes them.
// Import this package with a blank import to make the strategy available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2usersession"
package oauth2usersession

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("outbound/oauth2_user_session", func(ctx context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/oauth2_user_session: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		p, err := newProvider(ctx, oc)
		if err != nil {
			return nil, err
		}
		return outbound.Middleware(p), nil
	})
}

// Provider implements the oauth2_user_session outbound auth strategy.
type Provider struct {
	store       config.OAuthTokenStore
	callbackReg config.OAuthCallbackRegistrar
	upstream    string
	oauth2Cfg   *oauth2.Config
	httpClient  *http.Client
}

func newProvider(ctx context.Context, cfg *config.OutboundAuthConfig) (*Provider, error) {
	if cfg.OAuthTokenStore == nil {
		return nil, fmt.Errorf("oauth2_user_session strategy requires session_store to be configured in the top-level proxy config")
	}
	if cfg.OAuthCallbackReg == nil {
		return nil, fmt.Errorf("oauth2_user_session: OAuthCallbackReg not injected (internal error)")
	}
	if cfg.Upstream == "" {
		return nil, fmt.Errorf("oauth2_user_session: upstream name not set")
	}

	ocfg := cfg.OAuth2UserSession
	if ocfg.ClientID == "" {
		return nil, fmt.Errorf("oauth2_user_session: client_id is required")
	}
	if ocfg.CallbackURL == "" {
		return nil, fmt.Errorf("oauth2_user_session: callback_url is required")
	}

	clientSecret := os.ExpandEnv(ocfg.ClientSecret)

	authURL, tokenURL, err := resolveEndpoints(ctx, ocfg)
	if err != nil {
		return nil, fmt.Errorf("oauth2_user_session: %w", err)
	}

	oauth2Cfg := &oauth2.Config{
		ClientID:     ocfg.ClientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
		RedirectURL: ocfg.CallbackURL,
		Scopes:      ocfg.Scopes,
	}

	// Register with the callback mux so /oauth/callback/{upstream} can exchange codes.
	cfg.OAuthCallbackReg.RegisterProvider(
		cfg.Upstream, authURL, tokenURL, ocfg.ClientID, clientSecret, ocfg.Scopes, ocfg.CallbackURL,
	)

	return &Provider{
		store:       cfg.OAuthTokenStore,
		callbackReg: cfg.OAuthCallbackReg,
		upstream:    cfg.Upstream,
		oauth2Cfg:   oauth2Cfg,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Token returns the current access token for the user, refreshing or prompting OAuth flow as needed.
func (p *Provider) Token(ctx context.Context) (string, error) {
	subject := subjectFromContext(ctx)

	tok, err := p.store.Load(ctx, subject, p.upstream)
	if err != nil {
		return "", fmt.Errorf("loading session token for upstream %q: %w", p.upstream, err)
	}

	// No token stored — user must authorize.
	if tok == nil {
		authURL, urlErr := p.callbackReg.AuthURL(p.upstream, subject)
		if urlErr != nil {
			return "", fmt.Errorf("generating auth URL for upstream %q: %w", p.upstream, urlErr)
		}
		return "", &outbound.AuthRequiredError{AuthURL: authURL}
	}

	now := time.Now()

	// Token is valid and has more than 5 minutes remaining — return immediately.
	if tok.Expiry.IsZero() || tok.Expiry.After(now.Add(5*time.Minute)) {
		return tok.AccessToken, nil
	}

	// Token still valid but near expiry — refresh in background, return current token.
	if tok.Expiry.After(now) {
		if tok.RefreshToken != "" {
			go func() {
				if _, err := p.doRefresh(context.WithoutCancel(ctx), subject, tok); err != nil { //nolint:contextcheck
					slog.Warn("background token refresh failed", "upstream", p.upstream, "error", err)
				}
			}()
		}
		return tok.AccessToken, nil
	}

	// Token expired — refresh synchronously.
	if tok.RefreshToken == "" {
		// No refresh token; user must re-authorize.
		authURL, urlErr := p.callbackReg.AuthURL(p.upstream, subject)
		if urlErr != nil {
			return "", fmt.Errorf("generating re-auth URL for upstream %q: %w", p.upstream, urlErr)
		}
		return "", &outbound.AuthRequiredError{AuthURL: authURL}
	}

	newTok, refreshErr := p.doRefresh(ctx, subject, tok)
	if refreshErr != nil {
		return "", fmt.Errorf("refreshing token for upstream %q: %w", p.upstream, refreshErr)
	}
	return newTok.AccessToken, nil
}

// RawHeaders returns nil — this strategy uses Bearer token injection via Token().
func (p *Provider) RawHeaders(_ context.Context) (map[string]string, error) {
	return nil, nil
}

// doRefresh exchanges the refresh token for a new access token and saves it.
func (p *Provider) doRefresh(ctx context.Context, subject string, tok *config.OAuthToken) (*config.OAuthToken, error) {
	existing := &oauth2.Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		TokenType:    tok.TokenType,
	}
	httpCtx := context.WithValue(ctx, oauth2.HTTPClient, p.httpClient)
	src := p.oauth2Cfg.TokenSource(httpCtx, existing)
	newOAuth2Tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh: %w", err)
	}

	newTok := &config.OAuthToken{
		AccessToken:  newOAuth2Tok.AccessToken,
		RefreshToken: newOAuth2Tok.RefreshToken,
		Expiry:       newOAuth2Tok.Expiry,
		TokenType:    newOAuth2Tok.TokenType,
	}
	// Keep old refresh token when the provider does not rotate it.
	if newTok.RefreshToken == "" {
		newTok.RefreshToken = tok.RefreshToken
	}

	if saveErr := p.store.Save(ctx, subject, p.upstream, newTok); saveErr != nil {
		slog.Warn("saving refreshed oauth token", "upstream", p.upstream, "error", saveErr)
	}
	return newTok, nil
}

// subjectFromContext returns the authenticated user subject from inbound auth context.
// Returns empty string when no inbound auth is configured.
func subjectFromContext(ctx context.Context) string {
	info := pkginbound.TokenInfoFromContext(ctx)
	if info == nil {
		return ""
	}
	return info.Subject
}

// resolveEndpoints determines the OAuth2 auth and token endpoint URLs.
func resolveEndpoints(ctx context.Context, cfg config.OAuth2UserSessionConfig) (authURL, tokenURL string, err error) {
	switch cfg.Provider {
	case "github":
		return "https://github.com/login/oauth/authorize",
			"https://github.com/login/oauth/access_token", nil
	case "google":
		return "https://accounts.google.com/o/oauth2/auth",
			"https://oauth2.googleapis.com/token", nil
	case "gitlab":
		return "https://gitlab.com/oauth/authorize",
			"https://gitlab.com/oauth/token", nil
	case "slack":
		return "https://slack.com/oauth/v2/authorize",
			"https://slack.com/api/oauth.v2.access", nil
	case "oidc":
		if cfg.IssuerURL == "" {
			return "", "", fmt.Errorf("provider %q requires issuer_url", cfg.Provider)
		}
		return discoverOIDCEndpoints(ctx, cfg.IssuerURL)
	case "oauth2":
		if cfg.AuthURL == "" {
			return "", "", fmt.Errorf("provider %q requires auth_url", cfg.Provider)
		}
		if cfg.TokenURL == "" {
			return "", "", fmt.Errorf("provider %q requires token_url", cfg.Provider)
		}
		return cfg.AuthURL, cfg.TokenURL, nil
	case "":
		return "", "", fmt.Errorf("oauth2_user_session.provider is required (github|google|gitlab|slack|oidc|oauth2)")
	default:
		return "", "", fmt.Errorf("unknown oauth2_user_session.provider %q", cfg.Provider)
	}
}

// oidcDiscovery is a minimal OIDC discovery document.
type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// discoverOIDCEndpoints fetches and parses the OIDC discovery document.
func discoverOIDCEndpoints(ctx context.Context, issuerURL string) (authURL, tokenURL string, err error) {
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("building OIDC discovery request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching OIDC discovery doc from %q: %w", discoveryURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("OIDC discovery from %q returned HTTP %d", discoveryURL, resp.StatusCode)
	}
	var doc oidcDiscovery
	if decErr := json.NewDecoder(resp.Body).Decode(&doc); decErr != nil {
		return "", "", fmt.Errorf("parsing OIDC discovery doc: %w", decErr)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return "", "", fmt.Errorf("OIDC discovery doc missing authorization_endpoint or token_endpoint")
	}
	return doc.AuthorizationEndpoint, doc.TokenEndpoint, nil
}
