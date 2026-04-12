// Package mcpanything is the top-level SDK entry point for mcp-anything.
// It assembles the MCP manager, HTTP server, telemetry, inbound auth, and config
// hot-reload into a single Proxy type.
//
// Consumers compose exactly what they need via blank imports:
//
//	import (
//	    "github.com/gaarutyunov/mcp-anything/pkg/mcpanything"
//	    _ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
//	    _ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/bearer"
//	)
package mcpanything

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmcp "github.com/gaarutyunov/mcp-anything/pkg/mcp"
	pkgoauthcallback "github.com/gaarutyunov/mcp-anything/pkg/oauth/callbackmux"
	pkgratelimit "github.com/gaarutyunov/mcp-anything/pkg/ratelimit"
	pkgruntime "github.com/gaarutyunov/mcp-anything/pkg/runtime"
	pkgserver "github.com/gaarutyunov/mcp-anything/pkg/server"
	pkgsession "github.com/gaarutyunov/mcp-anything/pkg/session"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

// Proxy is the assembled mcp-anything proxy. It holds the MCP manager, HTTP
// server, telemetry shutdown function, runtime pools, and background refreshers.
type Proxy struct {
	cfg         *pkgconfig.ProxyConfig
	cfgPath     string
	manager     *pkgmcp.Manager
	srv         *pkgserver.Server
	loader      *pkgconfig.Loader
	telShutdown func(context.Context) error
	pools       *pkgruntime.Registry
	refreshers  []pkgupstream.Refresher
	mcpHandlers map[string]http.Handler
	oauthMux    *pkgoauthcallback.Mux
}

// Option is a functional option for configuring a Proxy.
type Option func(*Proxy)

// WithConfigPath sets the configuration file path for hot-reload watching.
// When set, New will re-read the file, apply the initial Rebuild via NewLoader,
// and Start will watch for changes.
func WithConfigPath(path string) Option {
	return func(p *Proxy) { p.cfgPath = path }
}

// LoadConfig reads the config file path from the CONFIG_PATH environment variable
// (defaulting to /etc/mcp-anything/config.yaml), loads and parses the file, and
// returns both the path and the parsed ProxyConfig.
func LoadConfig() (string, *pkgconfig.ProxyConfig, error) {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "/etc/mcp-anything/config.yaml"
	}
	cfg, err := pkgconfig.Load(path)
	if err != nil {
		return "", nil, fmt.Errorf("loading config from %q: %w", path, err)
	}
	return path, cfg, nil
}

// New assembles a Proxy from the given ProxyConfig and options.
// It initialises runtime pools, telemetry, the MCP manager, inbound auth, spec
// refreshers, and the HTTP server. Call Start to begin serving requests.
func New(ctx context.Context, cfg *pkgconfig.ProxyConfig, opts ...Option) (*Proxy, error) {
	p := &Proxy{cfg: cfg}
	for _, opt := range opts {
		opt(p)
	}

	// Create bounded runtime pools for JS/Lua script execution.
	pools, err := pkgruntime.NewRegistry(p.cfg.Runtime)
	if err != nil {
		return nil, fmt.Errorf("runtime pools: %w", err)
	}
	p.pools = pools
	slog.Info("runtime pools configured",
		"js_auth_vms", pools.JSAuth.Cap(),
		"js_vms", pools.JSScript.Cap(),
		"lua_auth_vms", pools.LuaAuth.Cap(),
	)

	// Create the MCP manager.
	p.manager = pkgmcp.NewManager(pools)

	// Set up per-user OAuth session store when configured.
	if err := p.buildSessionStore(ctx); err != nil {
		return nil, err
	}

	// Initialise OpenTelemetry.
	telCfg := &pkgtelemetry.Config{
		ServiceName:    p.cfg.Telemetry.ServiceName,
		ServiceVersion: p.cfg.Telemetry.ServiceVersion,
		OTLPEndpoint:   p.cfg.Telemetry.OTLPEndpoint,
		Insecure:       p.cfg.Telemetry.Insecure,
	}
	shutdown, err := pkgtelemetry.Init(ctx, telCfg)
	if err != nil {
		return nil, fmt.Errorf("telemetry init: %w", err)
	}
	p.telShutdown = shutdown

	// Set up config loading. If a path is given, use NewLoader (which also does
	// the initial Rebuild and sets up fsnotify for hot-reload via Watch).
	// Otherwise perform the initial Rebuild directly from the provided config.
	if p.cfgPath != "" {
		loader, loaderErr := pkgconfig.NewLoader(p.cfgPath, func(newCfg *pkgconfig.ProxyConfig) error {
			return p.manager.Rebuild(ctx, newCfg)
		})
		if loaderErr != nil {
			return nil, fmt.Errorf("config loader: %w", loaderErr)
		}
		p.loader = loader
		p.cfg = loader.Current()
	} else {
		if err := p.manager.Rebuild(ctx, p.cfg); err != nil {
			return nil, fmt.Errorf("initial rebuild: %w", err)
		}
	}

	// Build inbound auth middleware (global + per-upstream overrides).
	authMiddleware, wellKnown, err := p.buildAuth(ctx)
	if err != nil {
		return nil, err
	}

	// Wrap MCP handlers with optional auth middleware and IP extraction for rate limiting.
	rawHandlers := p.manager.HTTPHandlers()
	mcpHandlers := make(map[string]http.Handler, len(rawHandlers))
	for endpoint, handler := range rawHandlers {
		h := handler
		if authMiddleware != nil {
			h = authMiddleware(h)
		}
		// Always wrap with ClientIPMiddleware so that source: ip rate limits work
		// even when no auth is configured. Middleware is lightweight (single header read).
		h = pkgratelimit.ClientIPMiddleware(h)
		mcpHandlers[endpoint] = h
		slog.Info("mounted group", "endpoint", endpoint)
	}

	// Create background refreshers for URL-based upstreams.
	if err := p.buildRefreshers(ctx); err != nil {
		return nil, err
	}

	// Build readiness checker combining refresher health and circuit breaker state.
	var readinessCheckers []pkgupstream.ReadinessChecker
	if len(p.refreshers) > 0 {
		hcs := make([]pkgupstream.HealthChecker, len(p.refreshers))
		for i, r := range p.refreshers {
			hcs[i] = r
		}
		readinessCheckers = append(readinessCheckers, pkgupstream.NewRefresherSet(hcs))
	}
	// Circuit breaker readiness is always added; it returns (true, "") when no
	// circuit breakers are configured, so it is a no-op for unconfigured upstreams.
	readinessCheckers = append(readinessCheckers,
		pkgupstream.ReadinessCheckerFunc(p.manager.IsCircuitBreakerReady),
	)
	var readiness pkgserver.ReadinessChecker = pkgupstream.NewCompositeReadiness(readinessCheckers...)

	// Store handlers for external consumers (e.g. pkg/caddy Caddy module).
	p.mcpHandlers = mcpHandlers

	// Assemble the HTTP server.
	p.srv = pkgserver.New(
		p.cfg,
		mcpHandlers,
		wellKnown,
		pkgtelemetry.ReloadMetricsHandler(),
		promhttp.Handler(),
		readiness,
		p.oauthMux,
	)

	return p, nil
}

// Handlers returns the MCP group HTTP handlers assembled by New().
// Each key is a group endpoint path (e.g. "/mcp", "/mcp/readonly").
// The returned map is safe to read concurrently but must not be mutated.
func (p *Proxy) Handlers() map[string]http.Handler { return p.mcpHandlers }

// StartBackground starts background tasks (config hot-reload watcher and spec
// refreshers) without starting the HTTP server. Use this when embedding the proxy
// into an external server (e.g. Caddy) that manages its own server lifecycle.
func (p *Proxy) StartBackground(ctx context.Context) {
	for _, r := range p.refreshers {
		r.Start(ctx)
	}
	if p.loader != nil {
		go p.loader.Watch(ctx)
	}
}

// Start begins serving MCP requests. It starts background refreshers and the
// optional config watcher, then blocks until ctx is cancelled or the server
// encounters a fatal error.
func (p *Proxy) Start(ctx context.Context) error {
	for _, r := range p.refreshers {
		r.Start(ctx)
	}
	if p.loader != nil {
		go p.loader.Watch(ctx)
	}
	return p.srv.Start(ctx)
}

// Shutdown gracefully stops the proxy, flushing telemetry and draining in-flight
// requests within the server's configured shutdown timeout.
func (p *Proxy) Shutdown(ctx context.Context) error {
	var firstErr error
	if p.telShutdown != nil {
		if err := p.telShutdown(ctx); err != nil {
			slog.Error("telemetry shutdown", "error", err)
			firstErr = err
		}
	}
	if p.srv != nil {
		if err := p.srv.Shutdown(ctx); err != nil {
			slog.Error("server shutdown", "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// buildAuth constructs the inbound auth middleware and the optional well-known
// handler. Returns nil middleware and nil handler when auth is disabled.
func (p *Proxy) buildAuth(ctx context.Context) (func(http.Handler) http.Handler, http.HandlerFunc, error) {
	strategy := p.cfg.InboundAuth.Strategy
	if strategy == "" || strategy == "none" {
		return nil, nil, nil
	}

	authCfg := p.cfg.InboundAuth
	authCfg.JSAuthPool = p.pools.JSAuth
	authCfg.LuaAuthPool = p.pools.LuaAuth
	globalValidator, globalHeader, err := pkginbound.New(ctx, &authCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build inbound auth validator: %w", err)
	}
	slog.Info("inbound auth enabled", "strategy", strategy)

	type overrideEntry struct {
		validator    pkginbound.TokenValidator
		apiKeyHeader string
	}
	overrides := make(map[string]overrideEntry)
	for i := range p.cfg.Upstreams {
		up := &p.cfg.Upstreams[i]
		if !up.Enabled || up.InboundAuthOverride == nil {
			continue
		}
		ovCfg := *up.InboundAuthOverride
		ovCfg.JSAuthPool = p.pools.JSAuth
		ovCfg.LuaAuthPool = p.pools.LuaAuth
		ov, oh, ovErr := pkginbound.New(ctx, &ovCfg)
		if ovErr != nil {
			return nil, nil, fmt.Errorf("build inbound auth override for %q: %w", up.Name, ovErr)
		}
		overrides[up.Name] = overrideEntry{ov, oh}
		slog.Info("per-upstream auth override", "upstream", up.Name, "strategy", up.InboundAuthOverride.Strategy)
	}

	selectValidator := func(toolName string) (pkginbound.TokenValidator, string) {
		if toolName != "" {
			upstreamName := p.manager.ToolUpstreamName(toolName)
			if entry, ok := overrides[upstreamName]; ok {
				return entry.validator, entry.apiKeyHeader
			}
		}
		return globalValidator, globalHeader
	}

	middleware := pkginbound.MiddlewareWithSelector(selectValidator, p.manager)

	var wellKnown http.HandlerFunc
	if strategy == "jwt" || strategy == "introspection" {
		wellKnown = pkginbound.WellKnownHandler(p.cfg)
	}

	return middleware, wellKnown, nil
}

// buildSessionStore creates the OAuth session store and callback mux when configured.
func (p *Proxy) buildSessionStore(ctx context.Context) error {
	cfg := &p.cfg.SessionStore
	if cfg.Provider == "" {
		// No session store configured; oauth2_user_session strategy will error at startup
		// if any upstream tries to use it without a store.
		return nil
	}

	store, err := pkgsession.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}

	hmacKey := resolveHMACKey(cfg.HMACKey)
	p.oauthMux = pkgoauthcallback.New(store, hmacKey)
	p.manager.SetOAuthConfig(store, p.oauthMux)
	slog.Info("session store configured", "provider", cfg.Provider)
	return nil
}

// resolveHMACKey derives the 32-byte HMAC key from the configured string.
// Uses SHA-256 of the expanded value so any length input is accepted.
// If the string is empty, a random key is generated (states will not survive restarts).
func resolveHMACKey(raw string) []byte {
	expanded := os.ExpandEnv(raw)
	if expanded == "" {
		slog.Warn("session_store.hmac_key not configured; using random key (OAuth state will not survive proxy restarts)")
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			// Fall back to SHA-256 of a fixed string if crypto/rand is unavailable.
			h := sha256.Sum256([]byte("mcp-anything-ephemeral-hmac-key"))
			return h[:]
		}
		return b
	}
	h := sha256.Sum256([]byte(expanded))
	return h[:]
}

// buildRefreshers creates background spec refreshers for URL-based upstreams.
func (p *Proxy) buildRefreshers(ctx context.Context) error {
	for i := range p.cfg.Upstreams {
		upCfg := &p.cfg.Upstreams[i]
		if !upCfg.Enabled || upCfg.OpenAPI.RefreshInterval <= 0 {
			continue
		}
		if !strings.HasPrefix(upCfg.OpenAPI.Source, "http") {
			continue
		}
		refresher, err := pkgupstream.NewRefresher(ctx, upCfg, &p.cfg.Naming, p.manager, p.pools)
		if err != nil {
			return fmt.Errorf("creating refresher for %q: %w", upCfg.Name, err)
		}
		p.refreshers = append(p.refreshers, refresher)
	}
	return nil
}
