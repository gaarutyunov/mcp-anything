package outbound

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// ProviderFactory creates a TokenProvider from OutboundAuthConfig.
// pools provides shared runtime pools for strategies that need bounded concurrency (lua, js).
type ProviderFactory func(ctx context.Context, cfg *config.OutboundAuthConfig, pools *runtime.Registry) (TokenProvider, error)

var (
	providerFactoriesMu sync.RWMutex
	providerFactories   = make(map[string]ProviderFactory)
)

// RegisterProvider registers a ProviderFactory for the given strategy name.
// It is typically called from init() in implementation sub-packages
// (e.g., internal/auth/outbound/bearer).
func RegisterProvider(strategy string, factory ProviderFactory) {
	providerFactoriesMu.Lock()
	defer providerFactoriesMu.Unlock()
	providerFactories[strategy] = factory
}

// Registry maps strategy names to ProviderFactory functions.
type Registry struct {
	factories map[string]ProviderFactory
	pools     *runtime.Registry
}

// NewRegistry returns a Registry populated from all globally registered factories.
// pools controls the bounded runtime pools for JS and Lua script providers.
func NewRegistry(pools *runtime.Registry) *Registry {
	providerFactoriesMu.RLock()
	defer providerFactoriesMu.RUnlock()

	r := &Registry{
		factories: make(map[string]ProviderFactory),
		pools:     pools,
	}
	for name, f := range providerFactories {
		r.factories[name] = f
	}
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
		return nil, fmt.Errorf("unknown outbound auth strategy %q: no provider registered; "+
			"import the appropriate provider package for side effects "+
			"(e.g., _ \"github.com/gaarutyunov/mcp-anything/internal/auth/outbound/bearer\")", cfg.Strategy)
	}
	return f(ctx, cfg, r.pools)
}
