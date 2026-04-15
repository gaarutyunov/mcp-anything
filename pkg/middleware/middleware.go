// Package middleware defines the canonical Middleware type for mcp-anything.
// All pluggable processing stages — inbound auth, outbound auth, transforms,
// and rate limiting — are expressed as Middleware so they can be composed
// into a linear chain with standard net/http tooling.
package middleware

import "net/http"

// Middleware is the standard HTTP middleware type: it wraps an http.Handler and
// returns a new one. Middleware is applied in a linear chain; the innermost
// handler is the tool executor.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple Middleware into a single Middleware, applying them
// left-to-right (the first Middleware is the outermost wrapper).
func Chain(ms ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(ms) - 1; i >= 0; i-- {
			next = ms[i](next)
		}
		return next
	}
}
