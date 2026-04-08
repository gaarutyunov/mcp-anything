// Package runtime re-exports from pkg/runtime. See pkg/runtime for documentation.
package runtime

import (
	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgruntime "github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

const (
	// DefaultMaxAuthVMs is the default maximum concurrent runtimes for auth scripts.
	DefaultMaxAuthVMs = pkgruntime.DefaultMaxAuthVMs
	// DefaultMaxScriptVMs is the default maximum concurrent runtimes for tool scripts.
	DefaultMaxScriptVMs = pkgruntime.DefaultMaxScriptVMs
)

// Pool is a bounded semaphore that limits the number of concurrently active script runtimes.
// See pkg/runtime.Pool.
type Pool = pkgruntime.Pool

// Registry holds the shared bounded runtime pools for JS and Lua script execution.
// See pkg/runtime.Registry.
type Registry = pkgruntime.Registry

// NewPool creates a Pool that permits at most max concurrent runtime instances.
// See pkg/runtime.NewPool.
func NewPool(max int) *Pool {
	return pkgruntime.NewPool(max)
}

// NewRegistry creates a Registry with pools sized according to cfg.
// See pkg/runtime.NewRegistry.
func NewRegistry(cfg pkgconfig.RuntimeConfig) (*Registry, error) {
	return pkgruntime.NewRegistry(cfg)
}
