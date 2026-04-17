package outbound

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// outboundHeadersKey is the unexported context key for outbound auth headers.
type outboundHeadersKey struct{}

// outboundResultKey is the unexported context key for an early-exit tool result
// produced by the outbound auth middleware (e.g. on AuthRequiredError).
type outboundResultKey struct{}

// HeadersFromContext retrieves outbound auth headers stored by the Middleware constructor.
// Returns nil when no headers have been set.
func HeadersFromContext(ctx context.Context) map[string]string {
	h, _ := ctx.Value(outboundHeadersKey{}).(map[string]string)
	return h
}

// WithHeaders stores outbound auth headers in ctx under the package-private outboundHeadersKey.
func WithHeaders(ctx context.Context, headers map[string]string) context.Context {
	return context.WithValue(ctx, outboundHeadersKey{}, headers)
}

// AuthResultFromContext retrieves an early-exit CallToolResult set by the outbound auth
// middleware (e.g. when an OAuth2 redirect is required). Returns nil when normal
// execution should continue.
func AuthResultFromContext(ctx context.Context) *sdkmcp.CallToolResult {
	r, _ := ctx.Value(outboundResultKey{}).(*sdkmcp.CallToolResult)
	return r
}

// WithAuthResult stores an early-exit result in ctx under the package-private outboundResultKey.
func WithAuthResult(ctx context.Context, result *sdkmcp.CallToolResult) context.Context {
	return context.WithValue(ctx, outboundResultKey{}, result)
}
