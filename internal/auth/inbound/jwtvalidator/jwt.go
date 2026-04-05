// Package jwtvalidator registers the "jwt" inbound auth strategy.
package jwtvalidator

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func init() {
	inbound.RegisterValidator("jwt", func(ctx context.Context, cfg *config.InboundAuthConfig, _ *runtime.Registry) (inbound.TokenValidator, string, error) {
		v, err := NewJWTValidator(ctx, cfg.JWT)
		return v, "", err
	})
}

// JWTValidator validates JWT Bearer tokens using OIDC/JWKS.
type JWTValidator struct {
	verifier *oidc.IDTokenVerifier
}

// NewJWTValidator creates a JWTValidator from the given config.
func NewJWTValidator(ctx context.Context, cfg config.JWTAuthConfig) (*JWTValidator, error) {
	oidcConfig := &oidc.Config{ClientID: cfg.Audience}

	var verifier *oidc.IDTokenVerifier
	if cfg.JWKSURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
		verifier = oidc.NewVerifier(cfg.Issuer, keySet, oidcConfig)
	} else {
		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("creating OIDC provider: %w", err)
		}
		verifier = provider.Verifier(oidcConfig)
	}

	return &JWTValidator{verifier: verifier}, nil
}

// ValidateToken verifies the JWT signature, expiry, and audience, then returns TokenInfo.
func (v *JWTValidator) ValidateToken(ctx context.Context, raw string) (*inbound.TokenInfo, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verifying JWT: %w", err)
	}

	var claims struct {
		Scope string `json:"scope"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("extracting JWT claims: %w", err)
	}

	return &inbound.TokenInfo{
		Subject:  token.Subject,
		Scopes:   strings.Fields(claims.Scope),
		Audience: token.Audience,
	}, nil
}
