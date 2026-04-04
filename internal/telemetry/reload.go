package telemetry

import (
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

// IncrConfigReloadErrors increments the reload error counter.
func IncrConfigReloadErrors() { configReloadErrors.Add(1) }

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
