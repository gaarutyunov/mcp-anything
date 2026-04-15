// Package middleware defines the canonical Middleware type for mcp-anything and
// the unified middleware registry. All pluggable processing stages — inbound auth,
// outbound auth, transforms, and rate limiting — are expressed as Middleware so
// they can be composed into a linear chain with standard net/http tooling.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Factory creates a middleware from a generic config value.
// The cfg parameter is type-asserted by each factory to its own concrete type.
// Factories are registered from init() in strategy sub-packages.
type Factory func(ctx context.Context, cfg any) (func(http.Handler) http.Handler, error)

var (
	regMu sync.RWMutex
	reg   = make(map[string]Factory)
)

// Register adds a factory for the given name.
// Typically called from init() in strategy sub-packages.
// Names should be namespaced (e.g. "inbound/jwt", "outbound/bearer", "ratelimit/client_ip").
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[name] = f
}

// New builds the appropriate middleware from config.
// Returns an error for unknown names; strategy sub-packages must be imported
// (blank import) before calling New.
func New(ctx context.Context, name string, cfg any) (func(http.Handler) http.Handler, error) {
	regMu.RLock()
	f, ok := reg[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown middleware %q — did you forget to import the strategy sub-package?", name)
	}
	return f(ctx, cfg)
}
