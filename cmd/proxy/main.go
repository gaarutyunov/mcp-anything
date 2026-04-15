// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gaarutyunov/mcp-anything/pkg/mcpanything"

	// Register all built-in components (cache, upstream, auth, ratelimit, embedding, session).
	_ "github.com/gaarutyunov/mcp-anything/cmd/proxy/deps"
)

// Set by goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.Info("starting mcp-anything", "version", version, "commit", commit, "date", date)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath, cfg, err := mcpanything.LoadConfig()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	proxy, err := mcpanything.New(ctx, cfg, mcpanything.WithConfigPath(cfgPath))
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if shutErr := proxy.Shutdown(context.Background()); shutErr != nil {
			slog.Error("shutdown", "error", shutErr)
		}
	}()

	if err := proxy.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
