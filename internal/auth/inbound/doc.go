// Package inbound implements pluggable inbound authentication middleware for
// mcp-anything. Supported strategies are JWT (via go-oidc), token introspection
// (via zitadel/oidc), API key, and Lua scripting. The middleware validates
// incoming MCP client credentials and enforces per-operation auth bypass via
// the x-mcp-auth-required OpenAPI extension.
package inbound
