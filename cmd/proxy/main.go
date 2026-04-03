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
		tools, valErr := openapi.ValidateUpstream(valCtx, upCfg, &cfg.Naming)
		valCancel()
		if valErr != nil {
			slog.Error("upstream validation failed", "upstream", upCfg.Name, "error", valErr)
			os.Exit(1)
		}
		slog.Info("validated tools", "upstream", upCfg.Name, "count", len(tools))

		validatedUpstreams = append(validatedUpstreams, &upstreampkg.ValidatedUpstream{
			Config: upCfg,
			Tools:  tools,
		})
	}

	registry, err := upstreampkg.New(validatedUpstreams, &cfg.Naming)
	if err != nil {
		slog.Error("build tool registry", "error", err)
		os.Exit(1)
	}

	mcpSrv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "mcp-anything", Version: cfg.Telemetry.ServiceVersion}, nil)
	for _, tool := range registry.Tools() {
		t := tool // capture for closure
		mcpSrv.AddTool(t, func(callCtx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			args, parseErr := parseArguments(req.Params.Arguments)
			if parseErr != nil {
				return nil, fmt.Errorf("parsing tool arguments: %w", parseErr)
			}
			return registry.Dispatch(callCtx, req.Params.Name, args)
		})
	}

	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server { return mcpSrv }, nil)
	srv := server.New(cfg, map[string]http.Handler{"/mcp": handler})

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
