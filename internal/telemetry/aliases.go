// Package telemetry re-exports from pkg/telemetry. See pkg/telemetry for documentation.
package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// Config holds the telemetry initialisation parameters.
// See pkg/telemetry.Config.
type Config = pkgtelemetry.Config

// Metric instruments — mirrored from pkg/telemetry after Init is called.
// These are kept in sync by Init and InitMetrics so that existing callers
// of internal/telemetry do not need to change their import paths.
var (
	// ToolCallDuration tracks MCP tool call duration.
	ToolCallDuration metric.Float64Histogram
	// ToolCallErrors counts failed MCP tool calls (IsError: true).
	ToolCallErrors metric.Int64Counter
	// TransformDuration tracks jq transform stage duration.
	TransformDuration metric.Float64Histogram
	// ConfigReloadCounter counts config reloads by status (success/failure).
	ConfigReloadCounter metric.Int64Counter
	// SpecRefreshCounter counts spec refresh attempts by upstream and status.
	SpecRefreshCounter metric.Int64Counter
)

// syncMetrics copies metric instrument values from pkg/telemetry into this package's
// vars so that callers using internal/telemetry see the initialised instruments.
func syncMetrics() {
	ToolCallDuration = pkgtelemetry.ToolCallDuration
	ToolCallErrors = pkgtelemetry.ToolCallErrors
	TransformDuration = pkgtelemetry.TransformDuration
	ConfigReloadCounter = pkgtelemetry.ConfigReloadCounter
	SpecRefreshCounter = pkgtelemetry.SpecRefreshCounter
}

// Init initialises the OTel SDK and syncs metric vars into this package.
// See pkg/telemetry.Init.
func Init(ctx context.Context, cfg *Config) (func(context.Context) error, error) {
	shutdown, err := pkgtelemetry.Init(ctx, cfg)
	if err != nil {
		return nil, err
	}
	syncMetrics()
	return shutdown, nil
}

// InitMetrics creates all MCP-specific metric instruments and syncs vars.
// See pkg/telemetry.InitMetrics.
func InitMetrics(mp metric.MeterProvider) error {
	if err := pkgtelemetry.InitMetrics(mp); err != nil {
		return err
	}
	syncMetrics()
	return nil
}

// ServerMiddleware wraps an HTTP handler with OTel server instrumentation.
// See pkg/telemetry.ServerMiddleware.
func ServerMiddleware(handler http.Handler, serverName string) http.Handler {
	return pkgtelemetry.ServerMiddleware(handler, serverName)
}

// ClientTransport wraps an http.RoundTripper with OTel client instrumentation.
// See pkg/telemetry.ClientTransport.
func ClientTransport(base http.RoundTripper) http.RoundTripper {
	return pkgtelemetry.ClientTransport(base)
}

// ToolCallAttributes returns OTel attributes for an MCP tool call span.
// See pkg/telemetry.ToolCallAttributes.
func ToolCallAttributes(toolName, method, sessionID string) []attribute.KeyValue {
	return pkgtelemetry.ToolCallAttributes(toolName, method, sessionID)
}

// UpstreamAttributes returns OTel attributes for an upstream HTTP call span.
// See pkg/telemetry.UpstreamAttributes.
func UpstreamAttributes(upstreamName string) []attribute.KeyValue {
	return pkgtelemetry.UpstreamAttributes(upstreamName)
}

// RecordConfigReload records a config reload outcome to the OTel counter.
// See pkg/telemetry.RecordConfigReload.
func RecordConfigReload(ctx context.Context, success bool) {
	pkgtelemetry.RecordConfigReload(ctx, success)
}

// RecordSpecRefresh records a spec refresh outcome to the OTel counter.
// See pkg/telemetry.RecordSpecRefresh.
func RecordSpecRefresh(ctx context.Context, upstreamName string, success bool) {
	pkgtelemetry.RecordSpecRefresh(ctx, upstreamName, success)
}

// IncrConfigReloadTotal increments the total reload attempt counter.
// See pkg/telemetry.IncrConfigReloadTotal.
func IncrConfigReloadTotal() {
	pkgtelemetry.IncrConfigReloadTotal()
}

// IncrConfigReloadErrors increments the reload error counter and emits an OTel failure event.
// See pkg/telemetry.IncrConfigReloadErrors.
func IncrConfigReloadErrors(ctx context.Context) {
	pkgtelemetry.IncrConfigReloadErrors(ctx)
}

// IncrConfigReloadSuccess emits an OTel success event for a completed reload.
// See pkg/telemetry.IncrConfigReloadSuccess.
func IncrConfigReloadSuccess(ctx context.Context) {
	pkgtelemetry.IncrConfigReloadSuccess(ctx)
}

// ReloadTotal returns the total number of reload attempts.
// See pkg/telemetry.ReloadTotal.
func ReloadTotal() int64 {
	return pkgtelemetry.ReloadTotal()
}

// ReloadErrors returns the number of failed reload attempts.
// See pkg/telemetry.ReloadErrors.
func ReloadErrors() int64 {
	return pkgtelemetry.ReloadErrors()
}

// ReloadMetricsHandler returns an HTTP handler that exposes reload counters as plain text.
// See pkg/telemetry.ReloadMetricsHandler.
func ReloadMetricsHandler() http.HandlerFunc {
	return pkgtelemetry.ReloadMetricsHandler()
}
