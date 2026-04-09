package outbound

import (
	"context"

	pkgoutbound "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/all"
	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgruntime "github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

// ProviderFactory aliases pkg/auth/outbound.ProviderFactory.
type ProviderFactory = pkgoutbound.ProviderFactory

// Register aliases pkg/auth/outbound.Register.
var Register = pkgoutbound.Register

// New aliases pkg/auth/outbound.New.
func New(ctx context.Context, cfg *pkgconfig.OutboundAuthConfig) (TokenProvider, error) {
	return pkgoutbound.New(ctx, cfg)
}

// Registry is a backward-compatible wrapper around the global IoC registry.
// Deprecated: Use pkg/auth/outbound.New() directly with JSAuthPool and LuaAuthPool
// set on OutboundAuthConfig.
type Registry struct {
	pools *pkgruntime.Registry
}

// NewRegistry returns a Registry that injects runtime pools into OutboundAuthConfig
// before delegating to the global pkg/auth/outbound registry.
func NewRegistry(pools *pkgruntime.Registry) *Registry {
	return &Registry{pools: pools}
}

// New builds the appropriate TokenProvider from config, injecting runtime pools
// for script-based strategies (lua, js_script).
func (r *Registry) New(ctx context.Context, cfg *pkgconfig.OutboundAuthConfig) (TokenProvider, error) {
	c := *cfg
	c.JSAuthPool = r.pools.JSAuth
	c.LuaAuthPool = r.pools.LuaAuth
	return pkgoutbound.New(ctx, &c)
}
