package upstream

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/internal/command"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
)

// CommandExecutor executes a command-backed tool.
type CommandExecutor struct {
	toolName   string
	commandDef *command.Def
}

// Execute runs the command and returns the MCP result.
// Latency is recorded by the outer DispatchForGroup instrumentation in manager.go;
// recording it here would double-count command tools.
func (e *CommandExecutor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.toolName))

	stdout, stderr, err := e.commandDef.Execute(ctx, args)
	if err != nil {
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return command.ToErrorResult(stderr, err), nil
	}

	return command.ToTextResult(stdout), nil
}
