// Package mcpanything provides the main SDK entry point for embedding
// mcp-anything as a library in Go applications.
package mcpanything

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/gaarutyunov/mcp-anything/internal/upstream"
	upstreamhttp "github.com/gaarutyunov/mcp-anything/internal/upstream/http"
)

// Proxy is the central MCP proxy server.
type Proxy struct {
	configPath string
	logger     *slog.Logger
}

// New creates a Proxy with functional options.
func New(opts ...Option) (*Proxy, error) {
	p := &Proxy{}
	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, fmt.Errorf("applying option: %w", err)
		}
	}
	if p.configPath == "" {
		p.configPath = os.Getenv("CONFIG_PATH")
		if p.configPath == "" {
			p.configPath = "/etc/mcp-anything/config.yaml"
		}
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p, nil
}

// Run starts the proxy and blocks until ctx is cancelled.
func (p *Proxy) Run(ctx context.Context) error {
	cfgPath := p.configPath

	// Load initial config to extract telemetry and runtime pool settings.
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

	// Create the global runtime pool registry.
	runtimePools, poolErr := runtime.NewRegistry(runtimeCfg)
	if poolErr != nil {
		return fmt.Errorf("invalid runtime pool config: %w", poolErr)
	}
	p.logger.Info("runtime pools configured",
		"js_auth_vms", runtimePools.JSAuth.Cap(),
		"js_script_vms", runtimePools.JSScript.Cap(),
		"lua_auth_vms", runtimePools.LuaAuth.Cap(),
	)

	// Create the Manager.
	manager := mcppkg.NewManager(runtimePools)

	// Initialise OpenTelemetry SDK.
	shutdown, telErr := telemetry.Init(ctx, earlyTelemetryCfg)
	if telErr != nil {
		return fmt.Errorf("telemetry init: %w", telErr)
	}
	//nolint:contextcheck // parent ctx is cancelled; shutdown needs a fresh context with its own timeout
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			p.logger.Error("telemetry shutdown", "error", err)
		}
	}()

	// NewLoader performs the initial config load and calls manager.Rebuild.
	loader, err := config.NewLoader(cfgPath, func(cfg *config.ProxyConfig) error {
		return manager.Rebuild(ctx, cfg)
	})
	if err != nil {
		return fmt.Errorf("startup failed: %w", err)
	}

	cfg := loader.Current()

	// Build inbound auth middleware if configured.
	var authMiddleware func(http.Handler) http.Handler
	if cfg.InboundAuth.Strategy != "" && cfg.InboundAuth.Strategy != "none" {
		inboundRegistry := inbound.NewValidatorRegistry(runtimePools)
		globalValidator, globalHeader, buildErr := inboundRegistry.New(ctx, &cfg.InboundAuth)
		if buildErr != nil {
			return fmt.Errorf("build inbound auth validator: %w", buildErr)
		}
		p.logger.Info("inbound auth enabled", "strategy", cfg.InboundAuth.Strategy)

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
				return fmt.Errorf("build inbound auth override for upstream %q: %w", up.Name, ovErr)
			}
			overrides[up.Name] = overrideEntry{ov, oh}
			p.logger.Info("per-upstream auth override", "upstream", up.Name, "strategy", up.InboundAuthOverride.Strategy)
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
		p.logger.Info("mounted group", "endpoint", endpoint)
	}

	var wellKnown http.HandlerFunc
	if cfg.InboundAuth.Strategy == "jwt" || cfg.InboundAuth.Strategy == "introspection" {
		wellKnown = inbound.WellKnownHandler(cfg)
	}

	// Create background refreshers for URL-based upstreams.
	var refreshers []upstream.RefreshableUpstream
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled || upCfg.OpenAPI.RefreshInterval <= 0 {
			continue
		}
		if !isURLSource(upCfg.OpenAPI.Source) {
			continue
		}
		refresher, refErr := upstreamhttp.NewRefresher(ctx, upCfg, &cfg.Naming, manager, runtimePools)
		if refErr != nil {
			return fmt.Errorf("creating refresher for upstream %q: %w", upCfg.Name, refErr)
		}
		refreshers = append(refreshers, refresher)
	}
	for _, r := range refreshers {
		r.(interface{ Start(context.Context) }).Start(ctx)
	}

	var readiness server.ReadinessChecker
	if len(refreshers) > 0 {
		readiness = upstream.NewRefresherSet(refreshers)
	}

	srv := server.New(cfg, mcpHandlers, wellKnown, telemetry.ReloadMetricsHandler(), promhttp.Handler(), readiness)

	// Start config watcher in background.
	go loader.Watch(ctx)

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// RunWithSignals creates a context that cancels on SIGINT/SIGTERM and runs the proxy.
func (p *Proxy) RunWithSignals() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return p.Run(ctx)
}

// isURLSource reports whether the given source string is an HTTP/HTTPS URL.
func isURLSource(source string) bool {
	return strings.HasPrefix(source, "http")
}
