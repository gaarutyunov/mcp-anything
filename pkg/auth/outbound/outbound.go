// Package outbound provides the types and middleware logic for outbound authentication.
// Strategy sub-packages (bearer, apikey, oauth2, none, lua, js) register themselves
// with pkg/middleware via init() when imported. Use the all sub-package to import
// all built-in strategies.
package outbound

import (
	"context"
	"net/http"
)

// TokenProvider supplies credentials for upstream API calls.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	// Token returns the current Bearer token, refreshing if necessary.
	// Returns empty string if the strategy does not use a Bearer token.
	Token(ctx context.Context) (string, error)

	// RawHeaders returns headers to inject in addition to (or instead of) Token.
	// If non-empty, these headers are injected verbatim.
	// If both Token() and RawHeaders() return values, RawHeaders take precedence for Authorization.
	RawHeaders(ctx context.Context) (map[string]string, error)
}

// AuthRequiredError is returned by TokenProvider.Token when the calling user must
// complete an OAuth2 authorization flow before the request can proceed.
// The HTTP executor converts this error to a CallToolResult{IsError: true} containing
// the authorization URL.
type AuthRequiredError struct {
	// AuthURL is the URL the user must visit to authorize access.
	AuthURL string
}

func (e *AuthRequiredError) Error() string {
	return "authorization required: visit " + e.AuthURL
}

// Middleware is implemented by all outbound auth providers.
// Each concrete provider type implements Wrap directly on the struct.
type Middleware interface {
	Wrap(next http.Handler) http.Handler
}

// MiddlewareFunc adapts a func(http.Handler) http.Handler to implement Middleware.
// This mirrors the http.HandlerFunc / http.Handler pattern.
type MiddlewareFunc func(http.Handler) http.Handler

// Wrap implements Middleware.
func (f MiddlewareFunc) Wrap(next http.Handler) http.Handler { return f(next) }
