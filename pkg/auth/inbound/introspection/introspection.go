// Package introspection registers the "inbound/introspection" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package introspection

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/zitadel/oidc/v3/pkg/client/rs"
	zoidc "github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

func init() {
	pkgmiddleware.Register("inbound/introspection", func(ctx context.Context, cfg any) (pkgmiddleware.Builder, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/introspection: expected *config.InboundAuthConfig, got %T", cfg)
		}
		return NewValidator(ctx, ic.Introspection)
	})
}

// Validator validates tokens by calling a token introspection endpoint.
type Validator struct {
	server rs.ResourceServer
	aud    string
	Next   http.Handler
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

// Build implements middleware.Builder. It returns a Validator wired to next.
func (v *Validator) Build(next http.Handler) http.Handler {
	return &Validator{server: v.server, aud: v.aud, Next: next}
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
