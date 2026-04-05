// Package commandbuilder implements the upstream builder for command-backed tools.
// Import this package for side effects to register the "command" upstream type.
package commandbuilder

import (
	"context"
	"fmt"
	"log/slog"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/internal/command"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	"github.com/gaarutyunov/mcp-anything/internal/upstream"
)

func init() {
	upstream.RegisterBuilder("command", func(_ *runtime.Registry) upstream.Builder {
		return &CommandBuilder{}
	})
}

// CommandBuilder implements upstream.Builder for type: command upstreams.
type CommandBuilder struct{}

// Build compiles command tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
func (b *CommandBuilder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*upstream.ValidatedUpstream, error) {
	cmdTools, err := command.BuildTools(cfg.Commands, cfg, naming)
	if err != nil {
		return nil, fmt.Errorf("upstream %q command validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated command tools", "upstream", cfg.Name, "count", len(cmdTools))

	up := &upstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*upstream.RegistryEntry, 0, len(cmdTools))
	for _, ct := range cmdTools {
		entries = append(entries, &upstream.RegistryEntry{
			PrefixedName: ct.PrefixedName,
			OriginalName: ct.OriginalName,
			Upstream:     up,
			MCPTool:      ct.MCPTool,
			AuthRequired: true,
			Executor: &CommandExecutor{
				prefixedName: ct.PrefixedName,
				def:          ct.Def,
			},
		})
	}

	return &upstream.ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}

// CommandExecutor handles execution of command-backed tool calls.
type CommandExecutor struct {
	prefixedName string
	def          *command.Def
}

// Execute runs the command and returns the result.
func (e *CommandExecutor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.prefixedName))

	stdout, stderr, err := e.def.Execute(ctx, args)
	if err != nil {
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return command.ToErrorResult(stderr, err), nil
	}
	return command.ToTextResult(stdout), nil
}
