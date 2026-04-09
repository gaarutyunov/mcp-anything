package inbound

import (
	"context"

	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/all"
	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgruntime "github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

// ValidatorRegistry is a backward-compatible wrapper around the global IoC registry.
// Deprecated: Use pkg/auth/inbound.New() directly with JSAuthPool and LuaAuthPool
// set on InboundAuthConfig.
type ValidatorRegistry struct {
	pools *pkgruntime.Registry
}

// NewValidatorRegistry returns a ValidatorRegistry that injects runtime pools into
// InboundAuthConfig before delegating to the global pkg/auth/inbound registry.
func NewValidatorRegistry(pools *pkgruntime.Registry) *ValidatorRegistry {
	return &ValidatorRegistry{pools: pools}
}

// New builds the appropriate TokenValidator from config, injecting runtime pools
// for script-based strategies (lua, js_script).
func (r *ValidatorRegistry) New(ctx context.Context, cfg *pkgconfig.InboundAuthConfig) (TokenValidator, string, error) {
	c := *cfg
	c.JSAuthPool = r.pools.JSAuth
	c.LuaAuthPool = r.pools.LuaAuth
	return pkginbound.New(ctx, &c)
}
