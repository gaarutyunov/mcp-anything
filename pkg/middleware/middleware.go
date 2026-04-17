// Package middleware defines the unified middleware registry for mcp-anything.
// All pluggable processing stages — inbound auth, outbound auth, transforms,
// and rate limiting — register factories here so they can be composed into
// handler chains with standard net/http tooling.
package middleware
