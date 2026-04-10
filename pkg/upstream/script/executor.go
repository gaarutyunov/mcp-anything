package script

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// Executor executes a JavaScript-backed tool.
type Executor struct {
	toolName  string
	scriptDef *Def
}

// Execute runs the script and returns the MCP result.
func (e *Executor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.toolName))

	out, err := e.scriptDef.Execute(ctx, args)
	if err != nil {
		if pkgtelemetry.ToolCallErrors != nil {
			pkgtelemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return ToErrorResult(err), nil
	}
	return ToTextResult(out), nil
}
