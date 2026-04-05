package upstream

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// Builder validates a single upstream configuration and returns a ValidatedUpstream
// ready for registration in the tool registry.
// Each upstream type (http, command, etc.) provides its own Builder implementation.
type Builder interface {
	Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error)
}

// BuilderFactory creates a Builder given shared runtime dependencies.
// This function signature is used by sub-packages to register their builder
// implementations via init().
type BuilderFactory func(pools *runtime.Registry) Builder

var (
	builderFactoriesMu sync.RWMutex
	builderFactories   = make(map[string]BuilderFactory)
)

// RegisterBuilder registers a BuilderFactory for the given upstream type name.
// It is typically called from init() in implementation packages
// (e.g., internal/upstream/http, internal/upstream/command).
func RegisterBuilder(name string, factory BuilderFactory) {
	builderFactoriesMu.Lock()
	defer builderFactoriesMu.Unlock()
	builderFactories[name] = factory
}

// BuilderRegistry maps upstream type names to Builder implementations.
// An empty type string is treated as equivalent to "http".
type BuilderRegistry struct {
	builders map[string]Builder
}

// NewBuilderRegistry creates a BuilderRegistry from all globally registered factories.
// pools is shared with the auth factories so that the configured runtime limits apply
// globally across all script executions.
func NewBuilderRegistry(pools *runtime.Registry) *BuilderRegistry {
	builderFactoriesMu.RLock()
	defer builderFactoriesMu.RUnlock()

	r := &BuilderRegistry{builders: make(map[string]Builder)}
	for name, factory := range builderFactories {
		r.builders[name] = factory(pools)
	}
	return r
}

// Register adds a Builder for the given upstream type name.
func (r *BuilderRegistry) Register(upstreamType string, b Builder) {
	r.builders[upstreamType] = b
}

// Build dispatches to the registered Builder for cfg.Type and returns
// a ValidatedUpstream ready for use in the tool registry.
// Returns an error if the upstream type is not registered.
func (r *BuilderRegistry) Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error) {
	b, ok := r.builders[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown upstream type %q: no builder registered; "+
			"import the appropriate builder package for side effects "+
			"(e.g., _ \"github.com/gaarutyunov/mcp-anything/internal/upstream/http\")", cfg.Type)
	}
	return b.Build(ctx, cfg, naming)
}
