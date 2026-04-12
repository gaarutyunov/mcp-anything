// Package main is the entry point for a Caddy binary that embeds
// the mcp-anything Caddy module (http.handlers.mcpanything).
// Build with: go build ./cmd/caddy
package main

import (
	_ "time/tzdata"

	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Standard Caddy modules (HTTP, TLS, etc.).
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	// mcp-anything Caddy middleware.
	_ "github.com/gaarutyunov/mcp-anything/pkg/caddy"
)

func main() {
	caddycmd.Main()
}
