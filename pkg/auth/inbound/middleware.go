package inbound

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Middleware returns an HTTP middleware that validates inbound Bearer tokens
// (or API keys when apiKeyHeader is non-empty).
// It extracts the token, calls ValidateToken on the embedded validator, and stores
// the resulting TokenInfo in the request context.
// It does NOT implement per-tool auth bypass — use DispatchMiddleware for that.
func (vb *ValidatorBase) Middleware(apiKeyHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var token string
			if apiKeyHeader != "" {
				token = r.Header.Get(apiKeyHeader)
			} else {
				authHeader := r.Header.Get("Authorization")
				if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
					token = strings.TrimSpace(after)
				}
			}

			if token == "" {
				writeUnauthorized(w, r, "missing_token")
				return
			}

			info, err := vb.self.ValidateToken(r.Context(), token)
			if err != nil {
				var denied *DeniedError
				if errors.As(err, &denied) {
					writeDenied(w, r, denied)
				} else {
					writeUnauthorized(w, r, "invalid_token")
				}
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, info)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// DispatchMiddleware builds a middleware that:
//  1. Peeks at the request body to detect tools/call with per-tool auth bypass.
//  2. Routes to the per-upstream override middleware when one is configured, falling
//     back to globalMW for all other tools.
//
// globalMW is the middleware used when no per-upstream override applies.
// overrides maps upstream names to their specific validator middlewares.
// registry is used to check whether auth is required for a given tool.
// upstreamLookup maps a tool name to its upstream name; may be nil when overrides is empty.
func DispatchMiddleware(
	globalMW func(http.Handler) http.Handler,
	overrides map[string]func(http.Handler) http.Handler,
	registry RegistryReader,
	upstreamLookup func(string) string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Pre-compute handlers to avoid allocating a new wrapper per request.
		globalH := globalMW(next)
		overrideHandlers := make(map[string]http.Handler, len(overrides))
		for upstreamName, mw := range overrides {
			overrideHandlers[upstreamName] = mw(next)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			toolName, isToolCall, body := peekToolCallName(r)
			if body != nil {
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			// Per-operation bypass: tool explicitly marked as public.
			if isToolCall && !registry.AuthRequired(toolName) {
				next.ServeHTTP(w, r)
				return
			}

			// Route to per-upstream override when configured.
			if upstreamLookup != nil && toolName != "" && len(overrideHandlers) > 0 {
				upstreamName := upstreamLookup(toolName)
				if h, ok := overrideHandlers[upstreamName]; ok {
					h.ServeHTTP(w, r)
					return
				}
			}

			globalH.ServeHTTP(w, r)
		})
	}
}

// peekToolCallName reads the request body, attempts to parse a JSON-RPC tools/call message,
// and returns the tool name and whether it is indeed a tools/call request.
// It always returns the body bytes so the caller can restore r.Body.
func peekToolCallName(r *http.Request) (toolName string, isToolCall bool, body []byte) {
	if r.Body == nil {
		return "", false, nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil || len(body) == 0 {
		return "", false, body
	}

	var msg struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", false, body
	}
	if msg.Method != "tools/call" {
		return "", false, body
	}
	return msg.Params.Name, true, body
}

// writeUnauthorized writes an HTTP 401 response with the appropriate WWW-Authenticate header.
func writeUnauthorized(w http.ResponseWriter, r *http.Request, errCode string) {
	metadataURL := resourceMetadataURL(r)
	wwwAuth := fmt.Sprintf(
		`Bearer realm="mcp-anything", error=%q, resource_metadata=%q`,
		errCode, metadataURL,
	)
	w.Header().Set("WWW-Authenticate", wwwAuth)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	resp, err := json.Marshal(map[string]string{"error": errCode})
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"internal_error"}`))
		return
	}
	_, _ = w.Write(resp)
}

// writeDenied writes an HTTP response for an explicit denial with a specific status code.
// If the status is 401, it delegates to writeUnauthorized to preserve WWW-Authenticate semantics.
func writeDenied(w http.ResponseWriter, r *http.Request, denied *DeniedError) {
	if denied.Status == 0 || denied.Status == http.StatusUnauthorized {
		errCode := denied.Message
		if errCode == "" {
			errCode = "access_denied"
		}
		writeUnauthorized(w, r, errCode)
		return
	}
	errCode := denied.Message
	if errCode == "" {
		errCode = "access_denied"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(denied.Status)
	resp, err := json.Marshal(map[string]string{"error": errCode})
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"internal_error"}`))
		return
	}
	_, _ = w.Write(resp)
}

// resourceMetadataURL constructs the well-known metadata URL from the request.
func resourceMetadataURL(r *http.Request) string {
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
	return fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", scheme, host)
}
