// Package introspection registers the "introspection" inbound auth strategy.
// Import this package (blank import) to make the strategy available via inbound.New().
package introspection

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/zitadel/oidc/v3/pkg/client/rs"
	zoidc "github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	inbound.Register("introspection", func(ctx context.Context, cfg *config.InboundAuthConfig) (inbound.TokenValidator, string, error) {
		v, err := NewValidator(ctx, cfg.Introspection)
		return v, "", err
	})
}

// Validator validates tokens by calling a token introspection endpoint.
type Validator struct {
	server rs.ResourceServer
	aud    string
}

// NewValidator creates a Validator using client credentials.
// cfg.ClientSecret supports ${ENV_VAR} expansion.
func NewValidator(ctx context.Context, cfg config.IntrospectionConfig) (*Validator, error) {
	secret := os.ExpandEnv(cfg.ClientSecret)
	server, err := rs.NewResourceServerClientCredentials(ctx, cfg.Issuer, cfg.ClientID, secret)
	if err != nil {
		return nil, fmt.Errorf("creating introspection resource server: %w", err)
	}
	return &Validator{server: server, aud: cfg.Audience}, nil
}

// ValidateToken introspects the token and checks it is active and has the expected audience.
func (v *Validator) ValidateToken(ctx context.Context, raw string) (*inbound.TokenInfo, error) {
	resp, err := rs.Introspect[*zoidc.IntrospectionResponse](ctx, v.server, raw)
	if err != nil {
		return nil, fmt.Errorf("introspecting token: %w", err)
	}
	if !resp.Active {
		return nil, errors.New("token inactive")
	}
	if v.aud != "" && !slices.Contains(resp.Audience, v.aud) {
		return nil, errors.New("invalid audience")
	}
	return &inbound.TokenInfo{
		Subject:  resp.Subject,
		Audience: resp.Audience,
	}, nil
}
