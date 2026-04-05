// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"log/slog"
	"os"

	// Register all upstream builder types (http, command, script).
	_ "github.com/gaarutyunov/mcp-anything/internal/upstream/all"

	// Register all inbound auth strategies (jwt, introspection, apikey, lua, js).
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/inbound/all"

	// Register all outbound auth strategies (bearer, api_key, oauth2, lua, js, none).
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/all"

	"github.com/gaarutyunov/mcp-anything/pkg/mcpanything"
)

// Set by goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.Info("starting mcp-anything", "version", version, "commit", commit, "date", date)

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/etc/mcp-anything/config.yaml"
	}

	proxy, err := mcpanything.New(
		mcpanything.WithConfigPath(cfgPath),
	)
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}

	if err := proxy.RunWithSignals(); err != nil {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
