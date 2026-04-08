// Package transform implements the jq-based request and response transformation
// engine using gojq. It compiles jq expressions once at config load time and
// applies them per tool call to construct upstream HTTP requests and transform
// upstream responses (including error responses) into MCP content.
package transform
