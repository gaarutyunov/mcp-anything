// Package main is the entry point for the mcp-anything proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	mcppkg "github.com/gaarutyunov/mcp-anything/internal/mcp"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/server"
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

	// Validate that no two upstreams share the same tool_prefix (always fatal).
	if err := validateUpstreamPrefixes(cfg.Upstreams); err != nil {
		slog.Error("invalid upstream configuration", "error", err)
		os.Exit(1)
	}

	// For this task: single upstream only.
	upstream := cfg.Upstreams[0]
	doc, router, err := openapi.LoadPipeline(ctx, upstream.OpenAPI, upstream.Overlay)
	if err != nil {
		slog.Error("load openapi spec", "upstream", upstream.Name, "error", err)
		os.Exit(1)
	}
	_ = router // used in later tasks for validation

	tools, err := openapi.GenerateTools(doc, &upstream, &cfg.Naming)
	if err != nil {
		slog.Error("generate tools", "upstream", upstream.Name, "error", err)
		os.Exit(1)
	}
	slog.Info("generated tools", "upstream", upstream.Name, "count", len(tools))

	client := &http.Client{Timeout: upstream.Timeout}
	mcpSrv := mcppkg.New(
		&sdkmcp.Implementation{Name: "mcp-anything", Version: cfg.Telemetry.ServiceVersion},
		tools, &upstream, client,
	)

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
