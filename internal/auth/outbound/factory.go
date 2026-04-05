package outbound

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// ProviderFactory creates a TokenProvider from OutboundAuthConfig.
type ProviderFactory func(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error)

// Registry maps strategy names to ProviderFactory functions.
type Registry struct {
	factories map[string]ProviderFactory
}

// NewRegistry returns a Registry pre-populated with all built-in strategies.
// pools controls the bounded runtime pools for JS and Lua script providers; both
// outbound strategies share the same pool as their inbound counterparts so that
// the configured limit applies globally across all auth script executions.
func NewRegistry(pools *runtime.Registry) *Registry {
	r := &Registry{factories: make(map[string]ProviderFactory)}
	r.Register("bearer", func(_ context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
		return NewBearerProvider(cfg.Bearer), nil
	})
	r.Register("api_key", func(_ context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
		return NewAPIKeyProvider(cfg.APIKey), nil
	})
	r.Register("oauth2_client_credentials", func(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
		return NewOAuth2CCProvider(ctx, cfg.OAuth2ClientCredentials)
	})
	r.Register("none", func(_ context.Context, _ *config.OutboundAuthConfig) (TokenProvider, error) {
		return &NoneProvider{}, nil
	})
	r.Register("lua", func(_ context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
		return NewLuaProvider(cfg.Upstream, cfg.Lua, pools.LuaAuth)
	})
	r.Register("js_script", func(_ context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
		return NewJSProvider(cfg.Upstream, cfg.JS, pools.JSAuth)
	})
	return r
}

// Register adds a factory for the given strategy name.
func (r *Registry) Register(strategy string, factory ProviderFactory) {
	r.factories[strategy] = factory
}

// New builds the appropriate TokenProvider from config.
// An empty strategy is treated as "none". Returns an error for unknown strategies.
func (r *Registry) New(ctx context.Context, cfg *config.OutboundAuthConfig) (TokenProvider, error) {
	strategy := cfg.Strategy
	if strategy == "" {
		strategy = "none"
	}
	f, ok := r.factories[strategy]
	if !ok {
		return nil, fmt.Errorf("unknown outbound auth strategy: %q", cfg.Strategy)
	}
	return f(ctx, cfg)
}
