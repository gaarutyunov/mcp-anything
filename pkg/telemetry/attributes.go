package telemetry

import "go.opentelemetry.io/otel/attribute"

// ToolCallAttributes returns OTel attributes for an MCP tool call span.
func ToolCallAttributes(toolName, method, sessionID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("mcp.tool.name", toolName),
		attribute.String("mcp.method", method),
	}
	if sessionID != "" {
		attrs = append(attrs, attribute.String("mcp.session.id", sessionID))
	}
	return attrs
}

// UpstreamAttributes returns OTel attributes for an upstream HTTP call span.
func UpstreamAttributes(upstreamName string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("upstream.name", upstreamName),
	}
}
