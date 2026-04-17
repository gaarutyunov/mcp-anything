// Package upstream provides the public API for upstream tool execution,
// builder registration, and the immutable tool registry.
// Builder sub-packages (http, command, script) register themselves via
// RegisterBuilder in their init() functions. Import the all sub-package
// to make all built-in builders available.
package upstream

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/registry"
)

// ToolExecutor executes a single tool call and returns the MCP result.
type ToolExecutor interface {
	Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error)
}

// Builder creates validated upstreams from config.
// Each upstream type (http, command, script) provides its own Builder implementation
// that is registered via RegisterBuilder from its init() function.
type Builder interface {
	Build(ctx context.Context, cfg *config.UpstreamSpec, naming *config.NamingSpec) (*ValidatedUpstream, error)
}

var builders registry.Registry[Builder]

// RegisterBuilder registers a Builder for the given upstream type name.
// Typically called from init() in upstream type sub-packages.
func RegisterBuilder(upstreamType string, b Builder) {
	builders.Register(upstreamType, b)
}

// Build dispatches to the registered Builder for cfg.Type and returns
// a ValidatedUpstream ready for use in the tool registry.
// Returns an error if no builder is registered for the upstream type.
func Build(ctx context.Context, cfg *config.UpstreamSpec, naming *config.NamingSpec) (*ValidatedUpstream, error) {
	b, ok := builders.Get(cfg.Type)
	if !ok {
		return nil, fmt.Errorf("unknown upstream type %q — did you forget to import _ %q?",
			cfg.Type,
			"github.com/gaarutyunov/mcp-anything/pkg/upstream/"+cfg.Type)
	}
	return b.Build(ctx, cfg, naming)
}
