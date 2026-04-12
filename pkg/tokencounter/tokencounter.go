// Package tokencounter tokenizes MCP tool result content and records the token
// count to an OTel histogram. It is used by the MCP manager to provide
// per-tool token count observability, helping operators identify which tools
// return large responses and need a x-mcp-response-transform to slim them down.
package tokencounter

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tiktoken "github.com/pkoukk/tiktoken-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// defaultEncoding is used when no encoding is specified in config.
const defaultEncoding = "cl100k_base"

// Counter tokenizes tool result content and records the token count to the
// mcp_tool_result_tokens OTel histogram. A nil Counter is safe to use —
// Record is a no-op on nil.
type Counter struct {
	enc *tiktoken.Tiktoken
}

// New creates a Counter using the given tiktoken BPE encoding name.
// If encoding is empty, "cl100k_base" is used.
// Returns an error if the encoding data cannot be loaded.
func New(encoding string) (*Counter, error) {
	if encoding == "" {
		encoding = defaultEncoding
	}
	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("loading tiktoken encoding %q: %w", encoding, err)
	}
	return &Counter{enc: enc}, nil
}

// Record tokenizes the content items in result and records the total token count
// to the mcp_tool_result_tokens histogram with tool_name and upstream_name labels.
//
// It is a no-op when:
//   - c is nil
//   - result is nil
//   - result.IsError is true (error results are not counted)
//   - pkgtelemetry.ToolResultTokens is nil (metrics not initialised)
func (c *Counter) Record(ctx context.Context, toolName, upstreamName string, result *sdkmcp.CallToolResult) {
	if c == nil || result == nil || result.IsError {
		return
	}
	if pkgtelemetry.ToolResultTokens == nil {
		return
	}

	total := c.countContent(result.Content)

	pkgtelemetry.ToolResultTokens.Record(ctx, int64(total), metric.WithAttributes(
		attribute.String("tool_name", toolName),
		attribute.String("upstream_name", upstreamName),
	))
}

// countContent sums the token counts of all content items.
func (c *Counter) countContent(content []sdkmcp.Content) int {
	total := 0
	for _, item := range content {
		total += c.countItem(item)
	}
	return total
}

// countItem returns the token count for a single content item.
func (c *Counter) countItem(item sdkmcp.Content) int {
	switch v := item.(type) {
	case *sdkmcp.TextContent:
		return c.countString(v.Text)
	case *sdkmcp.ImageContent:
		return c.countString(base64.StdEncoding.EncodeToString(v.Data))
	case *sdkmcp.AudioContent:
		return c.countString(base64.StdEncoding.EncodeToString(v.Data))
	default:
		slog.Debug("tokencounter: skipping unsupported content type", "type", fmt.Sprintf("%T", item))
		return 0
	}
}

// countString tokenizes text and returns the token count.
func (c *Counter) countString(text string) int {
	return len(c.enc.Encode(text, nil, nil))
}
