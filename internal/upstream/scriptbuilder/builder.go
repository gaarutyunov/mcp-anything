// Package scriptbuilder implements the upstream builder for JavaScript-backed tools.
// Import this package for side effects to register the "script" upstream type.
package scriptbuilder

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/script"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	"github.com/gaarutyunov/mcp-anything/internal/upstream"
)

func init() {
	upstream.RegisterBuilder("script", func(pools *runtime.Registry) upstream.Builder {
		return &ScriptBuilder{jsPool: pools.JSScript}
	})
}

// ScriptBuilder implements upstream.Builder for type: script upstreams.
type ScriptBuilder struct {
	jsPool *runtime.Pool
}

// Build compiles all script tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
func (b *ScriptBuilder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*upstream.ValidatedUpstream, error) {
	fetchTimeout := cfg.Timeout
	if fetchTimeout <= 0 {
		fetchTimeout = 30 * time.Second
	}
	httpClient := &http.Client{
		Timeout: fetchTimeout,
	}

	scriptTools, err := script.BuildTools(cfg.Scripts, cfg, naming, httpClient, b.jsPool)
	if err != nil {
		return nil, fmt.Errorf("upstream %q script validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated script tools", "upstream", cfg.Name, "count", len(scriptTools))

	up := &upstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*upstream.RegistryEntry, 0, len(scriptTools))
	for _, st := range scriptTools {
		entries = append(entries, &upstream.RegistryEntry{
			PrefixedName: st.PrefixedName,
			OriginalName: st.OriginalName,
			Upstream:     up,
			MCPTool:      st.MCPTool,
			AuthRequired: true,
			Executor: &ScriptExecutor{
				prefixedName: st.PrefixedName,
				def:          st.Def,
			},
		})
	}

	return &upstream.ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}

// ScriptExecutor handles execution of JavaScript-backed tool calls.
type ScriptExecutor struct {
	prefixedName string
	def          *script.Def
}

// Execute runs the JavaScript script and returns the result.
func (e *ScriptExecutor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.prefixedName))

	out, err := e.def.Execute(ctx, args)
	if err != nil {
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return script.ToErrorResult(err), nil
	}
	return script.ToTextResult(out), nil
}
