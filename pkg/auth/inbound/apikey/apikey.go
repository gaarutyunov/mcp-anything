// Package apikey registers the "inbound/apikey" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package apikey

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("inbound/apikey", func(_ context.Context, cfg any) (pkgmiddleware.Builder, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/apikey: expected *config.InboundAuthConfig, got %T", cfg)
		}
		return NewValidator(ic.APIKey)
	})
}

// Validator validates tokens by checking them against a set of allowed API keys.
// The token is read from the header configured at construction time.
type Validator struct {
	header string
	keys   map[string]struct{}
	Next   http.Handler
}

// NewValidator creates a Validator by reading keys from the environment variable
// named cfg.KeysEnv (comma-separated list of valid keys).
func NewValidator(cfg config.APIKeyAuthConfig) (*Validator, error) {
	raw := os.Getenv(cfg.KeysEnv)
	keys := make(map[string]struct{})
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keys[k] = struct{}{}
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no API keys found in environment variable %q", cfg.KeysEnv)
	}
	return &Validator{header: cfg.Header, keys: keys}, nil
}

// Build implements middleware.Builder. It returns a Validator wired to next.
func (v *Validator) Build(next http.Handler) http.Handler {
	return &Validator{header: v.header, keys: v.keys, Next: next}
}

// ValidateToken checks whether token (the API key value) is in the allowed key set.
func (v *Validator) ValidateToken(_ context.Context, token string) (*inbound.TokenInfo, error) {
	if _, ok := v.keys[token]; !ok {
		return nil, errors.New("invalid API key")
	}
	return &inbound.TokenInfo{}, nil
}

// ServeHTTP implements http.Handler. It reads the API key from the configured header.
func (v *Validator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get(v.header)
	if token == "" {
		inbound.WriteUnauthorized(w, r, "missing_token")
		return
	}
	info, err := v.ValidateToken(r.Context(), token)
	if err != nil {
		var denied *inbound.DeniedError
		if errors.As(err, &denied) {
			inbound.WriteDenied(w, r, denied)
		} else {
			inbound.WriteUnauthorized(w, r, "invalid_token")
		}
		return
	}
	v.Next.ServeHTTP(w, r.WithContext(inbound.WithTokenInfo(r.Context(), info)))
}
