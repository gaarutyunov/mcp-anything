package inbound

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// WellKnownHandler returns an http.HandlerFunc for the OAuth 2.0 Protected Resource Metadata
// endpoint defined in RFC 9728. It is always public (no auth middleware applied).
func WellKnownHandler(cfg *config.ProxyConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issuer := cfg.InboundAuth.JWT.Issuer
		if issuer == "" {
			issuer = cfg.InboundAuth.Introspection.Issuer
		}

		scheme := "https"
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else if r.TLS == nil {
			scheme = "http"
		}
		host := r.Host
		if host == "" {
			host = r.URL.Host
		}
		resource := fmt.Sprintf("%s://%s", scheme, host)

		var authServers []string
		if issuer != "" {
			authServers = []string{issuer}
		} else {
			authServers = []string{}
		}

		payload := map[string]any{
			"resource":                 resource,
			"authorization_servers":    authServers,
			"scopes_supported":         []string{},
			"bearer_methods_supported": []string{"header"},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			slog.Warn("encoding well-known response", "error", err)
		}
	}
}
