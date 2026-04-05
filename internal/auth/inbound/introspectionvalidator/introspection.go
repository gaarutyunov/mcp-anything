// Package introspectionvalidator registers the "introspection" inbound auth strategy.
package introspectionvalidator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/zitadel/oidc/v3/pkg/client/rs"
	zoidc "github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func init() {
	inbound.RegisterValidator("introspection", func(ctx context.Context, cfg *config.InboundAuthConfig, _ *runtime.Registry) (inbound.TokenValidator, string, error) {
		v, err := NewIntrospectionValidator(ctx, cfg.Introspection)
		return v, "", err
	})
}

// IntrospectionValidator validates tokens by calling a token introspection endpoint.
type IntrospectionValidator struct {
	server rs.ResourceServer
	aud    string
}

// NewIntrospectionValidator creates an IntrospectionValidator using client credentials.
func NewIntrospectionValidator(ctx context.Context, cfg config.IntrospectionConfig) (*IntrospectionValidator, error) {
	secret := os.ExpandEnv(cfg.ClientSecret)
	server, err := rs.NewResourceServerClientCredentials(ctx, cfg.Issuer, cfg.ClientID, secret)
	if err != nil {
		return nil, fmt.Errorf("creating introspection resource server: %w", err)
	}
	return &IntrospectionValidator{server: server, aud: cfg.Audience}, nil
}

// ValidateToken introspects the token and checks it is active and has the expected audience.
func (v *IntrospectionValidator) ValidateToken(ctx context.Context, raw string) (*inbound.TokenInfo, error) {
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
