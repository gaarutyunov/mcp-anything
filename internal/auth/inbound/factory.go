package inbound

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// ValidatorFactory creates a TokenValidator from InboundAuthConfig.
// The second return value is the API key header name (non-empty only for the apikey strategy).
type ValidatorFactory func(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error)

// ValidatorRegistry maps strategy names to ValidatorFactory functions.
type ValidatorRegistry struct {
	factories map[string]ValidatorFactory
}

// NewValidatorRegistry returns a ValidatorRegistry pre-populated with all built-in strategies.
func NewValidatorRegistry() *ValidatorRegistry {
	r := &ValidatorRegistry{factories: make(map[string]ValidatorFactory)}
	r.Register("jwt", func(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
		v, err := NewJWTValidator(ctx, cfg.JWT)
		return v, "", err
	})
	r.Register("introspection", func(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
		v, err := NewIntrospectionValidator(ctx, cfg.Introspection)
		return v, "", err
	})
	r.Register("apikey", func(_ context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
		v, err := NewAPIKeyValidator(cfg.APIKey)
		return v, cfg.APIKey.Header, err
	})
	r.Register("lua", func(_ context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
		v, err := NewLuaValidator(cfg.Lua)
		return v, "", err
	})
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
		return nil, "", fmt.Errorf("unknown inbound auth strategy: %q", cfg.Strategy)
	}
	return f(ctx, cfg)
}
