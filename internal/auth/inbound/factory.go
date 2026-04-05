package inbound

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// ValidatorFactory creates a TokenValidator from InboundAuthConfig.
// The second return value is the API key header name (non-empty only for the apikey strategy).
// pools provides shared runtime pools for strategies that need bounded concurrency (lua, js).
type ValidatorFactory func(ctx context.Context, cfg *config.InboundAuthConfig, pools *runtime.Registry) (TokenValidator, string, error)

var (
	validatorFactoriesMu sync.RWMutex
	validatorFactories   = make(map[string]ValidatorFactory)
)

// RegisterValidator registers a ValidatorFactory for the given strategy name.
// It is typically called from init() in implementation sub-packages
// (e.g., internal/auth/inbound/jwt).
func RegisterValidator(strategy string, factory ValidatorFactory) {
	validatorFactoriesMu.Lock()
	defer validatorFactoriesMu.Unlock()
	validatorFactories[strategy] = factory
}

// ValidatorRegistry maps strategy names to ValidatorFactory functions.
type ValidatorRegistry struct {
	factories map[string]ValidatorFactory
	pools     *runtime.Registry
}

// NewValidatorRegistry returns a ValidatorRegistry populated from all globally registered factories.
// pools controls the bounded runtime pools for JS and Lua script validators.
func NewValidatorRegistry(pools *runtime.Registry) *ValidatorRegistry {
	validatorFactoriesMu.RLock()
	defer validatorFactoriesMu.RUnlock()

	r := &ValidatorRegistry{
		factories: make(map[string]ValidatorFactory),
		pools:     pools,
	}
	for name, f := range validatorFactories {
		r.factories[name] = f
	}
	return r
}

// Register adds a factory for the given strategy name.
func (r *ValidatorRegistry) Register(strategy string, factory ValidatorFactory) {
	r.factories[strategy] = factory
}

// New builds the appropriate TokenValidator from config.
// Returns an error for unknown strategies.
func (r *ValidatorRegistry) New(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
	f, ok := r.factories[cfg.Strategy]
	if !ok {
		return nil, "", fmt.Errorf("unknown inbound auth strategy %q: no validator registered; "+
			"import the appropriate validator package for side effects "+
			"(e.g., _ \"github.com/gaarutyunov/mcp-anything/internal/auth/inbound/jwt\")", cfg.Strategy)
	}
	return f(ctx, cfg, r.pools)
}
