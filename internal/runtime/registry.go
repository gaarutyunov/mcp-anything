package runtime

import (
	"fmt"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

const (
	// DefaultMaxAuthVMs is the default maximum concurrent runtimes for auth scripts.
	DefaultMaxAuthVMs = 10
	// DefaultMaxScriptVMs is the default maximum concurrent runtimes for tool scripts.
	DefaultMaxScriptVMs = 20
)

// Registry holds the shared bounded runtime pools for JS and Lua script execution.
// A single Registry is created at startup from config and shared across all
// validators, providers, and script tool executors. Sharing ensures that the
// configured limits are enforced globally rather than per-instance.
type Registry struct {
	// JSAuth bounds concurrent Sobek JS runtimes used for authentication
	// (inbound + outbound combined).
	JSAuth *Pool
	// JSScript bounds concurrent Sobek JS runtimes used for tool script execution.
	JSScript *Pool
	// LuaAuth bounds concurrent gopher-lua runtimes used for authentication
	// (inbound + outbound combined).
	LuaAuth *Pool
}

// NewRegistry creates a Registry with pools sized according to cfg.
// Returns an error if any configured limit is negative.
func NewRegistry(cfg config.RuntimeConfig) (*Registry, error) {
	jsAuthMax := cfg.JS.MaxAuthVMs
	if jsAuthMax == 0 {
		jsAuthMax = DefaultMaxAuthVMs
	}
	if jsAuthMax < 0 {
		return nil, fmt.Errorf("runtime.js.max_auth_vms must be > 0, got %d", jsAuthMax)
	}

	jsScriptMax := cfg.JS.MaxScriptVMs
	if jsScriptMax == 0 {
		jsScriptMax = DefaultMaxScriptVMs
	}
	if jsScriptMax < 0 {
		return nil, fmt.Errorf("runtime.js.max_script_vms must be > 0, got %d", jsScriptMax)
	}

	luaAuthMax := cfg.Lua.MaxAuthVMs
	if luaAuthMax == 0 {
		luaAuthMax = DefaultMaxAuthVMs
	}
	if luaAuthMax < 0 {
		return nil, fmt.Errorf("runtime.lua.max_auth_vms must be > 0, got %d", luaAuthMax)
	}

	return &Registry{
		JSAuth:   NewPool(jsAuthMax),
		JSScript: NewPool(jsScriptMax),
		LuaAuth:  NewPool(luaAuthMax),
	}, nil
}
