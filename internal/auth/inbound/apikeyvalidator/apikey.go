// Package apikeyvalidator registers the "apikey" inbound auth strategy.
package apikeyvalidator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func init() {
	inbound.RegisterValidator("apikey", func(_ context.Context, cfg *config.InboundAuthConfig, _ *runtime.Registry) (inbound.TokenValidator, string, error) {
		v, err := NewAPIKeyValidator(cfg.APIKey)
		return v, cfg.APIKey.Header, err
	})
}

// APIKeyValidator validates tokens by checking them against a set of allowed API keys.
type APIKeyValidator struct {
	keys map[string]struct{}
}

// NewAPIKeyValidator creates an APIKeyValidator by reading keys from the environment variable.
func NewAPIKeyValidator(cfg config.APIKeyAuthConfig) (*APIKeyValidator, error) {
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
	return &APIKeyValidator{keys: keys}, nil
}

// ValidateToken checks whether token (the API key value) is in the allowed key set.
func (v *APIKeyValidator) ValidateToken(_ context.Context, token string) (*inbound.TokenInfo, error) {
	if _, ok := v.keys[token]; !ok {
		return nil, errors.New("invalid API key")
	}
	return &inbound.TokenInfo{}, nil
}
