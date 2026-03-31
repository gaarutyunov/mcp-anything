// Package mcp bootstraps the MCP server using the official go-sdk, registers
// all generated tools from the upstream registry, and implements the tool call
// pipeline: prefix-based routing, transform, OpenAPI validation, outbound auth,
// upstream HTTP dispatch, and response conversion.
package mcp
