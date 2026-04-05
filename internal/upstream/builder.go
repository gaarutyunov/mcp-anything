package upstream

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// Builder validates a single upstream configuration and returns a ValidatedUpstream
// ready for registration in the tool registry.
// Each upstream type (http, command, etc.) provides its own Builder implementation.
type Builder interface {
	Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error)
}

// BuilderRegistry maps upstream type names to Builder implementations.
// An empty type string is treated as equivalent to "http".
type BuilderRegistry struct {
	builders map[string]Builder
}

// NewBuilderRegistry returns a BuilderRegistry pre-populated with built-in upstream types.
// pools is shared with the auth factories so that the configured runtime limits apply
// globally across all script executions.
func NewBuilderRegistry(pools *runtime.Registry) *BuilderRegistry {
	r := &BuilderRegistry{builders: make(map[string]Builder)}
	r.Register("", &HTTPBuilder{pools: pools})
	r.Register("http", &HTTPBuilder{pools: pools})
	r.Register("command", &CommandBuilder{})
	r.Register("script", &ScriptBuilder{jsPool: pools.JSScript})
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
		return nil, fmt.Errorf("unknown upstream type %q", cfg.Type)
	}
	return b.Build(ctx, cfg, naming)
}
