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

	// For this task: single upstream only.
	upstream := cfg.Upstreams[0]
	doc, router, err := openapi.Load(ctx, upstream.OpenAPI)
	if err != nil {
		slog.Error("load openapi spec", "upstream", upstream.Name, "error", err)
		os.Exit(1)
	}
	_ = router // used in later tasks for validation

	tools, err := openapi.GenerateTools(doc, &upstream, cfg.Naming.Separator)
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
