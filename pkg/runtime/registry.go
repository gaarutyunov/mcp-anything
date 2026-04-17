package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/registry"
)

const (
	// DefaultMaxAuthVMs is the default maximum concurrent runtimes for auth scripts.
	DefaultMaxAuthVMs = int64(10)
	// DefaultMaxScriptVMs is the default maximum concurrent runtimes for tool scripts.
	DefaultMaxScriptVMs = int64(20)
)

// Factory is a function that constructs a Runtime from the global RuntimeConfig.
// Each scripting sub-package (js, lua, wasm, …) registers one or more factories
// via Register in its init() function.
type Factory func(ctx context.Context, cfg config.RuntimeConfig) (Runtime, error)

var factories registry.Registry[Factory]

// Register registers a Factory under the given name. Typically called from init()
// in a scripting sub-package. Logs and skips if name is empty or already registered.
func Register(name string, f Factory) {
	if name == "" {
		slog.Error("runtime.Register: name must not be empty")
		return
	}
	if !factories.RegisterIfAbsent(name, f) {
		slog.Error("runtime.Register: duplicate runtime name", "name", name)
	}
}

// Registry holds a bounded Runtime pool for every registered scripting runtime.
// A single Registry is created at startup from config and shared across all
// validators, providers, and script tool executors. Sharing ensures that the
// configured limits are enforced globally rather than per-instance.
type Registry struct {
	pools map[string]Runtime
}

// NewRegistry creates a Registry by calling every registered Factory.
// Returns an error if any factory returns an error.
func NewRegistry(ctx context.Context, cfg config.RuntimeConfig) (*Registry, error) {
	snap := factories.Snapshot()

	pools := make(map[string]Runtime, len(snap))
	for name, f := range snap {
		rt, err := f(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("building runtime pool %q: %w", name, err)
		}
		pools[name] = rt
	}
	return &Registry{pools: pools}, nil
}

// Get returns the Runtime registered under name, or nil if not found.
func (r *Registry) Get(name string) Runtime {
	if r == nil {
		return nil
	}
	return r.pools[name]
}

// All returns a copy of the name→Runtime map for iteration (e.g. logging).
func (r *Registry) All() map[string]Runtime {
	if r == nil {
		return nil
	}
	out := make(map[string]Runtime, len(r.pools))
	for k, v := range r.pools {
		out[k] = v
	}
	return out
}
