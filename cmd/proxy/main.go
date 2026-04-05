// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	mcppkg "github.com/gaarutyunov/mcp-anything/internal/mcp"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/server"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	upstreampkg "github.com/gaarutyunov/mcp-anything/internal/upstream"
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

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/etc/mcp-anything/config.yaml"
	}

	// Load initial config to extract telemetry and runtime pool settings.
	// The full load and validation happens below via NewLoader.
	earlyTelemetryCfg := &telemetry.Config{
		ServiceName:    "mcp-anything",
		ServiceVersion: "unknown",
	}
	var runtimeCfg config.RuntimeConfig
	if earlyCfg, loadErr := config.Load(cfgPath); loadErr == nil {
		earlyTelemetryCfg = &telemetry.Config{
			ServiceName:    earlyCfg.Telemetry.ServiceName,
			ServiceVersion: earlyCfg.Telemetry.ServiceVersion,
			OTLPEndpoint:   earlyCfg.Telemetry.OTLPEndpoint,
			Insecure:       earlyCfg.Telemetry.Insecure,
		}
		runtimeCfg = earlyCfg.Runtime
	}

	// Create the global runtime pool registry. This bounds the number of concurrent
	// JS and Lua script runtimes to prevent OOM under high concurrency.
	runtimePools, poolErr := runtime.NewRegistry(runtimeCfg)
	if poolErr != nil {
		slog.Error("invalid runtime pool config", "error", poolErr)
		os.Exit(1)
	}
	slog.Info("runtime pools configured",
		"js_auth_vms", runtimePools.JSAuth.Cap(),
		"js_script_vms", runtimePools.JSScript.Cap(),
		"lua_auth_vms", runtimePools.LuaAuth.Cap(),
	)

	// Create the Manager — MCP servers and registry are populated by the first Rebuild.
	manager := mcppkg.NewManager(runtimePools)

	// Initialise OpenTelemetry SDK.
	shutdown, telErr := telemetry.Init(ctx, earlyTelemetryCfg)
	if telErr != nil {
		slog.Error("telemetry init failed", "error", telErr)
		os.Exit(1)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			slog.Error("telemetry shutdown", "error", err)
		}
	}()

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
		inboundRegistry := inbound.NewValidatorRegistry(runtimePools)
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

	// Create background refreshers for URL-based upstreams.
	var refreshers []*upstreampkg.Refresher
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled || upCfg.OpenAPI.RefreshInterval <= 0 {
			continue
		}
		if !isURLSource(upCfg.OpenAPI.Source) {
			continue
		}
		refresher, refErr := upstreampkg.NewRefresher(ctx, upCfg, &cfg.Naming, manager, runtimePools)
		if refErr != nil {
			slog.Error("creating refresher", "upstream", upCfg.Name, "error", refErr)
			os.Exit(1)
		}
		refreshers = append(refreshers, refresher)
	}
	for _, r := range refreshers {
		r.Start(ctx)
	}

	var readiness server.ReadinessChecker
	if len(refreshers) > 0 {
		readiness = upstreampkg.NewRefresherSet(refreshers)
	}

	srv := server.New(cfg, mcpHandlers, wellKnown, telemetry.ReloadMetricsHandler(), promhttp.Handler(), readiness)

	// Start config watcher in background.
	go loader.Watch(ctx)

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}

// isURLSource reports whether the given source string is an HTTP/HTTPS URL.
func isURLSource(source string) bool {
	return strings.HasPrefix(source, "http")
}
