package upstream

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/internal/script"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
)

// ScriptExecutor executes a JavaScript-backed tool.
type ScriptExecutor struct {
	toolName  string
	scriptDef *script.Def
}

// Execute runs the script and returns the MCP result.
func (e *ScriptExecutor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.toolName))

	out, err := e.scriptDef.Execute(ctx, args)
	if err != nil {
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return script.ToErrorResult(err), nil
	}
	return script.ToTextResult(out), nil
}
