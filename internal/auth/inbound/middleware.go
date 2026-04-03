// Package inbound implements pluggable inbound authentication middleware for
// mcp-anything. Supported strategies are JWT (via go-oidc), token introspection
// (via zitadel/oidc), API key, and Lua scripting. The middleware validates
// incoming MCP client credentials and enforces per-operation auth bypass via
// the x-mcp-auth-required OpenAPI extension.
package inbound

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TokenInfo holds validated identity information extracted from a token.
type TokenInfo struct {
	Subject  string
	Scopes   []string
	Audience []string
	Extra    map[string]any
}

// TokenValidator validates an inbound token and returns identity information.
type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (*TokenInfo, error)
}

// RegistryReader allows the middleware to check per-tool auth requirements.
type RegistryReader interface {
	// AuthRequired returns whether authentication is required for the given tool name.
	// Returns true (conservative default) for unknown tool names.
	AuthRequired(toolName string) bool
}

// contextKey is an unexported type for context keys in this package.
type contextKey struct{}

// TokenInfoFromContext returns the TokenInfo stored in ctx, or nil if not present.
func TokenInfoFromContext(ctx context.Context) *TokenInfo {
	v, _ := ctx.Value(contextKey{}).(*TokenInfo)
	return v
}

// Middleware returns an HTTP middleware that validates inbound Bearer tokens (or API keys).
// apiKeyHeader: when non-empty, the token is extracted from this header instead of Authorization: Bearer.
// The middleware skips validation for tools/call requests where the tool has AuthRequired==false.
// For all other requests (tools/list, initialize, etc.), auth is always enforced.
func Middleware(validator TokenValidator, registry RegistryReader, apiKeyHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Peek at the request body to detect tools/call with auth bypass.
			toolName, isToolCall, body := peekToolCallName(r)
			if body != nil {
				// Restore the body so the downstream handler can read it.
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			if isToolCall && !registry.AuthRequired(toolName) {
				// Per-operation bypass: tool explicitly marked as public.
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from the appropriate header.
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

			info, err := validator.ValidateToken(r.Context(), token)
			if err != nil {
				writeUnauthorized(w, r, "invalid_token")
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, info)
			next.ServeHTTP(w, r.WithContext(ctx))
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
	_, _ = w.Write([]byte(`{"error":"` + errCode + `"}`))
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
