// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	mcppkg "github.com/gaarutyunov/mcp-anything/internal/mcp"
	"github.com/gaarutyunov/mcp-anything/internal/server"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/etc/mcp-anything/config.yaml"
	}

	// Create the Manager — MCP servers and registry are populated by the first Rebuild.
	manager := mcppkg.NewManager()

	// NewLoader performs the initial config load and calls manager.Rebuild.
	// Fatal on initial load or validation failure.
	loader, err := config.NewLoader(cfgPath, func(cfg *config.ProxyConfig) error {
		return manager.Rebuild(ctx, cfg)
	})
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}

	cfg := loader.Current()

	// Build inbound auth middleware if configured.
	var authMiddleware func(http.Handler) http.Handler
	if cfg.InboundAuth.Strategy != "" && cfg.InboundAuth.Strategy != "none" {
		inboundRegistry := inbound.NewValidatorRegistry()
		globalValidator, globalHeader, buildErr := inboundRegistry.New(ctx, &cfg.InboundAuth)
		if buildErr != nil {
			slog.Error("build inbound auth validator", "error", buildErr)
			os.Exit(1)
		}
		slog.Info("inbound auth enabled", "strategy", cfg.InboundAuth.Strategy)

		// Build per-upstream override validators keyed by upstream name.
		type overrideEntry struct {
			validator    inbound.TokenValidator
			apiKeyHeader string
		}
		overrides := make(map[string]overrideEntry)
		for i := range cfg.Upstreams {
			up := &cfg.Upstreams[i]
			if !up.Enabled || up.InboundAuthOverride == nil {
				continue
			}
			ov, oh, ovErr := inboundRegistry.New(ctx, up.InboundAuthOverride)
			if ovErr != nil {
				slog.Error("build inbound auth override", "upstream", up.Name, "error", ovErr)
				os.Exit(1)
			}
			overrides[up.Name] = overrideEntry{ov, oh}
			slog.Info("per-upstream auth override", "upstream", up.Name, "strategy", up.InboundAuthOverride.Strategy)
		}

		selectValidator := func(toolName string) (inbound.TokenValidator, string) {
			if toolName != "" {
				upstreamName := manager.ToolUpstreamName(toolName)
				if entry, ok := overrides[upstreamName]; ok {
					return entry.validator, entry.apiKeyHeader
				}
			}
			return globalValidator, globalHeader
		}
		// manager implements inbound.RegistryReader via AuthRequired.
		authMiddleware = inbound.MiddlewareWithSelector(selectValidator, manager)
	}

	// Wrap MCP handlers with auth middleware if configured.
	rawHandlers := manager.HTTPHandlers()
	mcpHandlers := make(map[string]http.Handler, len(rawHandlers))
	for endpoint, handler := range rawHandlers {
		h := handler
		if authMiddleware != nil {
			h = authMiddleware(h)
		}
		mcpHandlers[endpoint] = h
		slog.Info("mounted group", "endpoint", endpoint)
	}

	var wellKnown http.HandlerFunc
	if cfg.InboundAuth.Strategy == "jwt" || cfg.InboundAuth.Strategy == "introspection" {
		wellKnown = inbound.WellKnownHandler(cfg)
	}

	srv := server.New(cfg, mcpHandlers, wellKnown, telemetry.ReloadMetricsHandler())

	// Start config watcher in background.
	go loader.Watch(ctx)

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
