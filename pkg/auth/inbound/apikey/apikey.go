// Package apikey registers the "apikey" inbound auth strategy.
// Import this package (blank import) to make the strategy available via inbound.New().
package apikey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	inbound.Register("apikey", func(_ context.Context, cfg *config.InboundAuthConfig) (inbound.TokenValidator, string, error) {
		v, err := NewValidator(cfg.APIKey)
		return v, cfg.APIKey.Header, err
	})
}

// Validator validates tokens by checking them against a set of allowed API keys.
// The "token" passed to ValidateToken is the value of the configured header.
type Validator struct {
	keys map[string]struct{}
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
	return &Validator{keys: keys}, nil
}

// ValidateToken checks whether token (the API key value) is in the allowed key set.
func (v *Validator) ValidateToken(_ context.Context, token string) (*inbound.TokenInfo, error) {
	if _, ok := v.keys[token]; !ok {
		return nil, errors.New("invalid API key")
	}
	return &inbound.TokenInfo{}, nil
}
