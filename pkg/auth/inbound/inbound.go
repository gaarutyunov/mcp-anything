// Package inbound provides the types and middleware logic for inbound authentication.
// Strategy sub-packages (jwt, introspection, apikey, lua, js) register themselves
// with pkg/middleware via init() when imported. Use the all sub-package to import
// all built-in strategies.
package inbound

import (
	"context"
	"fmt"
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

// ValidatorBase can be embedded in any concrete TokenValidator struct to provide
// the Middleware() method directly on the struct.
// Call NewValidatorBase(v) in the constructor to wire the self-reference.
type ValidatorBase struct {
	self TokenValidator
}

// NewValidatorBase creates a ValidatorBase wired to v.
// Assign the result to the embedded ValidatorBase field of your concrete type:
//
//	v.ValidatorBase = inbound.NewValidatorBase(v)
func NewValidatorBase(v TokenValidator) ValidatorBase {
	return ValidatorBase{self: v}
}
