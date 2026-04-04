// Package outbound implements per-upstream outbound authentication providers.
// Supported strategies are static Bearer token, API key injection, OAuth2
// client credentials (with automatic token refresh), and Lua scripting.
// Providers are instantiated at config load time and called per upstream request.
package outbound

import "context"

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
