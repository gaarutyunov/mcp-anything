// Package server wires together the net/http server with chi routing, mounts
// the MCP handler at the configured group endpoints, attaches OTel middleware,
// and handles graceful shutdown on SIGTERM/SIGINT.
package server
