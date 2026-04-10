package upstream

import (
	"context"
	"fmt"

	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// Builder validates a single upstream configuration and returns a ValidatedUpstream
// ready for registration in the tool registry.
// See pkg/upstream.Builder.
type Builder = pkgupstream.Builder

// BuilderRegistry maps upstream type names to Builder implementations.
// An empty type string is treated as equivalent to "http".
type BuilderRegistry struct {
	builders map[string]Builder
	pools    *runtime.Registry
}

// NewBuilderRegistry returns a BuilderRegistry pre-populated with built-in upstream types.
// pools is shared with the auth factories so that the configured runtime limits apply
// globally across all script executions.
func NewBuilderRegistry(pools *runtime.Registry) *BuilderRegistry {
	r := &BuilderRegistry{
		builders: make(map[string]Builder),
		pools:    pools,
	}
	r.Register("", &HTTPBuilder{})
	r.Register("http", &HTTPBuilder{})
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
// For HTTP upstreams, runtime pools are injected into the outbound auth config
// so that the pkg-level HTTPBuilder does not need to hold a reference to pools.
func (r *BuilderRegistry) Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error) {
	b, ok := r.builders[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown upstream type %q", cfg.Type)
	}

	// Inject runtime pools into a copy of the outbound auth config so that
	// pkg/upstream/http.HTTPBuilder can call outbound.New() without needing
	// a direct reference to the runtime registry.
	cfgCopy := *cfg
	cfgCopy.OutboundAuth = cfg.OutboundAuth
	if r.pools != nil {
		cfgCopy.OutboundAuth.JSAuthPool = r.pools.JSAuth
		cfgCopy.OutboundAuth.LuaAuthPool = r.pools.LuaAuth
	}

	return b.Build(ctx, &cfgCopy, naming)
}
