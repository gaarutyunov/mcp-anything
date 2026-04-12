// Package outbound provides the IoC registry for outbound authentication strategies.
// Strategy sub-packages (bearer, apikey, oauth2, none, lua, js) register themselves
// via init() when imported. Use the all sub-package to import all built-in strategies.
package outbound

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// TokenProvider supplies credentials for upstream API calls.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	// Token returns the current Bearer token, refreshing if necessary.
	// Returns empty string if the strategy does not use a Bearer token.
	Token(ctx context.Context) (string, error)

	// RawHeaders returns headers to inject in addition to (or instead of) Token.
	// If non-empty, these headers are injected verbatim.
	// If both Token() and RawHeaders() return values, RawHeaders take precedence for Authorization.
	RawHeaders(ctx context.Context) (map[string]string, error)
}

// ProviderFactory creates a TokenProvider from OutboundAuthConfig.
type ProviderFactory func(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]ProviderFactory)
)

// AuthRequiredError is returned by TokenProvider.Token when the calling user must
// complete an OAuth2 authorization flow before the request can proceed.
// The HTTP executor converts this error to a CallToolResult{IsError: true} containing
// the authorization URL.
type AuthRequiredError struct {
	// AuthURL is the URL the user must visit to authorize access.
	AuthURL string
}

func (e *AuthRequiredError) Error() string {
	return "authorization required: visit " + e.AuthURL
}

// Register adds a factory for the given strategy name.
// Typically called from init() in strategy sub-packages.
func Register(strategy string, factory ProviderFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[strategy] = factory
}

// New builds the appropriate TokenProvider from config.
// An empty strategy is treated as "none". Returns an error for unknown strategies.
// Strategy sub-packages must be imported (blank import) before calling New.
func New(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
	strategy := cfg.Strategy
	if strategy == "" {
		strategy = "none"
	}
	mu.RLock()
	f, ok := registry[strategy]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown outbound auth strategy %q — did you forget to import _ %q?",
			strategy,
			"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/"+strategy)
	}
	return f(ctx, cfg)
}
