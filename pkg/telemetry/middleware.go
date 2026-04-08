package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ServerMiddleware wraps an HTTP handler with OTel server instrumentation.
// Emits http.server.request.duration and http.server.active_requests per SPEC.md §16.
func ServerMiddleware(handler http.Handler, serverName string) http.Handler {
	return otelhttp.NewHandler(handler, serverName)
}

// ClientTransport wraps an http.RoundTripper with OTel client instrumentation.
// Emits http.client.request.duration per SPEC.md §16.
func ClientTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}
