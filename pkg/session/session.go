// Package session provides the IoC registry for session store backends used by
// the oauth2_user_session outbound auth strategy. Backend sub-packages (memory,
// postgres, redis) register themselves via init() when imported.
// Use the all sub-package to import all built-in backends.
package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// Token is an alias for config.OAuthToken for convenience in session sub-packages.
type Token = config.OAuthToken

// Store is an alias for config.OAuthTokenStore for convenience in session sub-packages.
type Store = config.OAuthTokenStore

// StoreFactory creates a Store from a SessionStoreConfig.
type StoreFactory func(ctx context.Context, cfg *config.SessionStoreConfig) (Store, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]StoreFactory)
)

// Register adds a factory for the given provider name.
// Typically called from init() in session sub-packages.
func Register(provider string, f StoreFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[provider] = f
}

// New creates a Store from the given config.
// The provider sub-package must be imported (blank import) before calling New.
func New(ctx context.Context, cfg *config.SessionStoreConfig) (Store, error) {
	if cfg.Provider == "" {
		return nil, fmt.Errorf("session_store.provider is required")
	}
	mu.RLock()
	f, ok := registry[cfg.Provider]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown session store provider %q — import _ %q",
			cfg.Provider,
			"github.com/gaarutyunov/mcp-anything/pkg/session/"+cfg.Provider)
	}
	return f(ctx, cfg)
}
