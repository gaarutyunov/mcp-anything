// Package telemetry initialises the OpenTelemetry SDK with OTLP gRPC trace
// export and Prometheus metric export. It provides HTTP middleware for automatic
// span and metric emission on both the inbound MCP server and the outbound
// upstream HTTP clients, following standard HTTP and MCP semantic conventions.
package telemetry
