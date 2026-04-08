package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// histogramBoundaries are the histogram bucket boundaries per SPEC.md §16.
var histogramBoundaries = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// Metric instruments — initialised once at startup by InitMetrics.
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

// InitMetrics creates all MCP-specific metric instruments using the given provider.
// Must be called once at startup before any tool calls.
func InitMetrics(mp metric.MeterProvider) error {
	m := mp.Meter("mcp-anything")

	var err error

	ToolCallDuration, err = m.Float64Histogram("mcp.tool.call.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of MCP tool calls in seconds"),
		metric.WithExplicitBucketBoundaries(histogramBoundaries...),
	)
	if err != nil {
		return fmt.Errorf("creating mcp.tool.call.duration: %w", err)
	}

	ToolCallErrors, err = m.Int64Counter("mcp.tool.call.errors.total",
		metric.WithUnit("{call}"),
		metric.WithDescription("Number of failed MCP tool calls"),
	)
	if err != nil {
		return fmt.Errorf("creating mcp.tool.call.errors.total: %w", err)
	}

	TransformDuration, err = m.Float64Histogram("mcp_anything.transform.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of jq transform stages in seconds"),
		metric.WithExplicitBucketBoundaries(histogramBoundaries...),
	)
	if err != nil {
		return fmt.Errorf("creating mcp_anything.transform.duration: %w", err)
	}

	ConfigReloadCounter, err = m.Int64Counter("mcp_anything.config_reload",
		metric.WithUnit("{reload}"),
		metric.WithDescription("Total number of config reload attempts by status"),
	)
	if err != nil {
		return fmt.Errorf("creating mcp_anything.config_reload: %w", err)
	}

	SpecRefreshCounter, err = m.Int64Counter("mcp_anything.spec_refresh",
		metric.WithUnit("{refresh}"),
		metric.WithDescription("Total number of spec refresh attempts by upstream and status"),
	)
	if err != nil {
		return fmt.Errorf("creating mcp_anything.spec_refresh: %w", err)
	}

	return nil
}

// RecordConfigReload records a config reload outcome to the OTel counter.
func RecordConfigReload(ctx context.Context, success bool) {
	if ConfigReloadCounter == nil {
		return
	}
	status := "success"
	if !success {
		status = "failure"
	}
	ConfigReloadCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordSpecRefresh records a spec refresh outcome to the OTel counter.
func RecordSpecRefresh(ctx context.Context, upstreamName string, success bool) {
	if SpecRefreshCounter == nil {
		return
	}
	status := "success"
	if !success {
		status = "failure"
	}
	SpecRefreshCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("upstream", upstreamName),
		attribute.String("status", status),
	))
}
