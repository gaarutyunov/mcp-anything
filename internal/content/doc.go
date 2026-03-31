// Package content handles mapping non-JSON upstream responses (images, audio,
// binary) to the appropriate MCP content types: ImageContent, AudioContent,
// and ResourceContent. Content-Type detection drives the mapping, with
// x-mcp-response-format as a per-operation override.
package content
