// Package inbound provides the IoC registry for inbound authentication strategies.
// Strategy sub-packages (jwt, introspection, apikey, lua, js) register themselves
// via init() when imported. Use the all sub-package to import all built-in strategies.
package inbound

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// DeniedError is returned by a TokenValidator when access is explicitly denied
// with a specific HTTP status code. Middleware maps it to the appropriate
// HTTP response instead of the default 401 unauthorized.
type DeniedError struct {
	Status  int
	Message string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("auth denied (status %d): %s", e.Status, e.Message)
}

// TokenInfo holds validated identity information extracted from a token.
type TokenInfo struct {
	Subject  string
	Scopes   []string
	Audience []string
	Extra    map[string]any
}

// TokenValidator validates an inbound token and returns identity information.
type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (*TokenInfo, error)
}

// RegistryReader allows the middleware to check per-tool auth requirements.
type RegistryReader interface {
	// AuthRequired returns whether authentication is required for the given tool name.
	// Returns true (conservative default) for unknown tool names.
	AuthRequired(toolName string) bool
}

// contextKey is an unexported type for context keys in this package.
type contextKey struct{}

// TokenInfoFromContext returns the TokenInfo stored in ctx, or nil if not present.
func TokenInfoFromContext(ctx context.Context) *TokenInfo {
	v, _ := ctx.Value(contextKey{}).(*TokenInfo)
	return v
}

// ValidatorFactory creates a TokenValidator from InboundAuthConfig.
// The second return value is the API key header name (non-empty only for the apikey strategy).
type ValidatorFactory func(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]ValidatorFactory)
)

// Register adds a factory for the given strategy name.
// Typically called from init() in strategy sub-packages.
func Register(strategy string, factory ValidatorFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[strategy] = factory
}

// New builds the appropriate TokenValidator from config.
// Returns an error for unknown strategies.
// Strategy sub-packages must be imported (blank import) before calling New.
func New(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
	mu.RLock()
	f, ok := registry[cfg.Strategy]
	mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("unknown inbound auth strategy %q — did you forget to import _ %q?",
			cfg.Strategy,
			"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/"+cfg.Strategy)
	}
	return f(ctx, cfg)
}
