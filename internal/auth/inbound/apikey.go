package inbound

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// APIKeyValidator validates tokens by checking them against a set of allowed API keys.
// The "token" passed to ValidateToken is the value of the configured header.
type APIKeyValidator struct {
	keys map[string]struct{}
}

// NewAPIKeyValidator creates an APIKeyValidator by reading keys from the environment variable
// named cfg.KeysEnv (comma-separated list of valid keys).
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
func (v *APIKeyValidator) ValidateToken(_ context.Context, token string) (*TokenInfo, error) {
	if _, ok := v.keys[token]; !ok {
		return nil, errors.New("invalid API key")
	}
	return &TokenInfo{}, nil
}
