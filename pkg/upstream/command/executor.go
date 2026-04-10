package command

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// Executor executes a command-backed tool.
type Executor struct {
	toolName   string
	commandDef *Def
}

// Execute runs the command and returns the MCP result.
// Latency is recorded by the outer DispatchForGroup instrumentation in manager.go;
// recording it here would double-count command tools.
func (e *Executor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.toolName))

	stdout, stderr, err := e.commandDef.Execute(ctx, args)
	if err != nil {
		if pkgtelemetry.ToolCallErrors != nil {
			pkgtelemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return ToErrorResult(stderr, err), nil
	}

	return ToTextResult(stdout), nil
}
