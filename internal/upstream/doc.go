// Package upstream manages the per-upstream tool registry, HTTP clients with
// OpenTelemetry instrumentation, and background refresh of OpenAPI specs and
// overlays. Each upstream snapshot is stored as an atomic pointer to allow
// lock-free hot-reload without request interruption.
package upstream
