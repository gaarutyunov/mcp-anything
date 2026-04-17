// Package jwt registers the "inbound/jwt" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package jwt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("inbound/jwt", func(ctx context.Context, cfg any) (pkgmiddleware.Builder, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/jwt: expected *config.InboundAuthConfig, got %T", cfg)
		}
		return NewValidator(ctx, ic.JWT)
	})
}

// Validator validates JWT Bearer tokens using OIDC/JWKS.
type Validator struct {
	verifier *oidc.IDTokenVerifier
	Next     http.Handler
}

// NewValidator creates a Validator from the given config.
// If cfg.JWKSURL is set, it uses that directly; otherwise it performs OIDC discovery.
func NewValidator(ctx context.Context, cfg config.JWTAuthConfig) (*Validator, error) {
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

	return &Validator{verifier: verifier}, nil
}

// Build implements middleware.Builder. It returns a Validator wired to next.
func (v *Validator) Build(next http.Handler) http.Handler {
	return &Validator{verifier: v.verifier, Next: next}
}

// ValidateToken verifies the JWT signature, expiry, and audience, then returns TokenInfo.
func (v *Validator) ValidateToken(ctx context.Context, raw string) (*inbound.TokenInfo, error) {
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

// ServeHTTP implements http.Handler. It extracts a Bearer token and validates it.
func (v *Validator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := inbound.ExtractBearerToken(r)
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
