package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

var (
	configReloadTotal  atomic.Int64
	configReloadErrors atomic.Int64
)

// IncrConfigReloadTotal increments the total reload attempt counter.
func IncrConfigReloadTotal() { configReloadTotal.Add(1) }

// IncrConfigReloadErrors increments the reload error counter and emits an OTel failure event.
func IncrConfigReloadErrors(ctx context.Context) {
	configReloadErrors.Add(1)
	RecordConfigReload(ctx, false)
}

// IncrConfigReloadSuccess emits an OTel success event for a completed reload.
// It does not affect the atomic counters (which only track total/errors).
func IncrConfigReloadSuccess(ctx context.Context) {
	RecordConfigReload(ctx, true)
}

// ReloadTotal returns the total number of reload attempts.
func ReloadTotal() int64 { return configReloadTotal.Load() }

// ReloadErrors returns the number of failed reload attempts.
func ReloadErrors() int64 { return configReloadErrors.Load() }

// ReloadMetricsHandler returns an HTTP handler that exposes reload counters as plain text.
func ReloadMetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, "mcp_anything_config_reload_total %d\nmcp_anything_config_reload_errors_total %d\n",
			configReloadTotal.Load(), configReloadErrors.Load())
	}
}
