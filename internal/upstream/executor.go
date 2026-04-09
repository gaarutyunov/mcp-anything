package upstream

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolExecutor executes a single tool call and returns the MCP result.
type ToolExecutor interface {
	Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error)
}
