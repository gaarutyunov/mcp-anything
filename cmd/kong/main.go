// Package main is the entry point for the mcp-anything Kong Go PDK plugin server.
// Build with: go build ./cmd/kong
//
// The binary serves dual roles:
//   - Kong plugin schema introspection: mcp-anything-kong --dump
//   - Kong plugin server:               mcp-anything-kong (started by Kong automatically)
package main

import (
	"log/slog"
	"os"
	_ "time/tzdata"

	"github.com/Kong/go-pdk/server"

	"github.com/gaarutyunov/mcp-anything/pkg/kong"
)

func main() {
	if err := server.StartServer(kong.New, kong.Version, kong.Priority); err != nil {
		slog.Error("kong plugin server stopped", "error", err)
		os.Exit(1)
	}
}
