// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/server"
	upstreampkg "github.com/gaarutyunov/mcp-anything/internal/upstream"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "/etc/mcp-anything/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// Validate that no two enabled upstreams share the same tool_prefix (AC-07.5 — fast fail).
	if err := validateUpstreamPrefixes(cfg.Upstreams); err != nil {
		slog.Error("invalid upstream configuration", "error", err)
		os.Exit(1)
	}

	// Validate each enabled upstream and collect results.
	var validatedUpstreams []*upstreampkg.ValidatedUpstream
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled {
			continue
		}

		valCtx, valCancel := context.WithTimeout(ctx, upCfg.StartupValidationTimeout)
		tools, specYAMLRoot, valErr := openapi.ValidateUpstream(valCtx, upCfg, &cfg.Naming)
		valCancel()
		if valErr != nil {
			slog.Error("upstream validation failed", "upstream", upCfg.Name, "error", valErr)
			os.Exit(1)
		}
		slog.Info("validated tools", "upstream", upCfg.Name, "count", len(tools))

		provider, provErr := outbound.NewRegistry().New(ctx, &upCfg.OutboundAuth)
		if provErr != nil {
			slog.Error("build outbound auth provider", "upstream", upCfg.Name, "error", provErr)
			os.Exit(1)
		}

		validatedUpstreams = append(validatedUpstreams, &upstreampkg.ValidatedUpstream{
			Config:       upCfg,
			Tools:        tools,
			Provider:     provider,
			SpecYAMLRoot: specYAMLRoot,
		})
	}

	// Build groups config — create a default group if none configured.
	groups := cfg.Groups
	if len(groups) == 0 {
		allNames := make([]string, 0, len(validatedUpstreams))
		for _, vu := range validatedUpstreams {
			allNames = append(allNames, vu.Config.Name)
		}
		groups = []config.GroupConfig{{
			Name:      "default",
			Endpoint:  "/mcp",
			Upstreams: allNames,
		}}
	}

	registry, err := upstreampkg.New(validatedUpstreams, &cfg.Naming, groups)
	if err != nil {
		slog.Error("build tool registry", "error", err)
		os.Exit(1)
	}

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
				upstreamName := registry.ToolUpstreamName(toolName)
				if entry, ok := overrides[upstreamName]; ok {
					return entry.validator, entry.apiKeyHeader
				}
			}
			return globalValidator, globalHeader
		}
		authMiddleware = inbound.MiddlewareWithSelector(selectValidator, registry)
	}

	// Build one MCP handler per group, each mounted at the group's endpoint.
	mcpHandlers := make(map[string]http.Handler, len(groups))
	for _, g := range groups {
		groupName := g.Name
		groupToolList := registry.ToolsForGroup(groupName)

		groupSrv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "mcp-anything", Version: cfg.Telemetry.ServiceVersion}, nil)
		for _, tool := range groupToolList {
			t := tool // capture for closure
			groupSrv.AddTool(t, func(callCtx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				args, parseErr := parseArguments(req.Params.Arguments)
				if parseErr != nil {
					return nil, fmt.Errorf("parsing tool arguments: %w", parseErr)
				}
				return registry.DispatchForGroup(callCtx, groupName, req.Params.Name, args)
			})
		}

		var groupHandler http.Handler = sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server { return groupSrv }, nil)
		if authMiddleware != nil {
			groupHandler = authMiddleware(groupHandler)
		}
		mcpHandlers[g.Endpoint] = groupHandler
		slog.Info("mounted group", "name", groupName, "endpoint", g.Endpoint, "tools", len(groupToolList))
	}

	var wellKnown http.HandlerFunc
	if cfg.InboundAuth.Strategy == "jwt" || cfg.InboundAuth.Strategy == "introspection" {
		wellKnown = inbound.WellKnownHandler(cfg)
	}
	srv := server.New(cfg, mcpHandlers, wellKnown)

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}

// validateUpstreamPrefixes returns an error if any two enabled upstreams share the same tool_prefix.
func validateUpstreamPrefixes(upstreams []config.UpstreamConfig) error {
	seen := make(map[string]string) // prefix → upstream name
	for _, up := range upstreams {
		if !up.Enabled {
			continue
		}
		if prev, ok := seen[up.ToolPrefix]; ok {
			return fmt.Errorf("upstreams %q and %q share the same tool_prefix %q", prev, up.Name, up.ToolPrefix)
		}
		seen[up.ToolPrefix] = up.Name
	}
	return nil
}

// parseArguments unmarshals the tool call arguments into a map.
func parseArguments(raw any) (map[string]any, error) {
	if raw == nil {
		return make(map[string]any), nil
	}

	b, ok := raw.(json.RawMessage)
	if !ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-marshalling arguments: %w", err)
		}
		b = data
	}

	if len(b) == 0 || string(b) == "null" {
		return make(map[string]any), nil
	}

	var args map[string]any
	if err := json.Unmarshal(b, &args); err != nil {
		return nil, fmt.Errorf("unmarshalling arguments: %w", err)
	}
	return args, nil
}
