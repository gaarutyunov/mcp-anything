// Package configgen translates MCPProxy and MCPUpstream CRDs into proxy config YAML.
package configgen

import (
	"context"
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

const (
	defaultImage = "ghcr.io/gaarutyunov/mcp-anything:latest"
	defaultPort  = int32(8080)
	specsDir     = "/etc/mcp-anything/specs"
	overlaysDir  = "/etc/mcp-anything/overlays"
)

// Generate translates MCPProxy and its selected MCPUpstream resources into a
// config.ProxyConfig and returns it serialised as YAML bytes.
func Generate(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) ([]byte, error) {
	_ = ctx // reserved for future async lookups

	cfg := config.ProxyConfig{}

	// Server configuration.
	port := proxy.Spec.Server.Port
	if port == 0 {
		port = defaultPort
	}
	cfg.Server = config.ServerConfig{
		Port: int(port),
	}

	// Naming configuration.
	cfg.Naming = config.NamingConfig{
		Separator:          proxy.Spec.Naming.Separator,
		MaxLength:          proxy.Spec.Naming.MaxLength,
		ConflictResolution: proxy.Spec.Naming.ConflictResolution,
	}

	// Telemetry configuration.
	if proxy.Spec.Telemetry != nil && proxy.Spec.Telemetry.Enabled {
		cfg.Telemetry = config.TelemetryConfig{
			OTLPEndpoint: proxy.Spec.Telemetry.OTLPEndpoint,
		}
	}

	// Inbound auth configuration.
	if proxy.Spec.InboundAuth != nil {
		cfg.InboundAuth = config.InboundAuthConfig{
			Strategy: proxy.Spec.InboundAuth.Strategy,
		}
		if proxy.Spec.InboundAuth.JWT != nil {
			cfg.InboundAuth.JWT = config.JWTAuthConfig{
				JWKSURL: proxy.Spec.InboundAuth.JWT.JWKSUrl,
			}
		}
	}

	// Build upstream configs.
	upstreamCfgs := make([]config.UpstreamConfig, 0, len(upstreams))
	for i := range upstreams {
		up := &upstreams[i]
		upCfg, err := buildUpstreamConfig(up)
		if err != nil {
			return nil, fmt.Errorf("building upstream config for %s/%s: %w", up.Namespace, up.Name, err)
		}
		upstreamCfgs = append(upstreamCfgs, upCfg)
	}
	cfg.Upstreams = upstreamCfgs

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling proxy config: %w", err)
	}
	return data, nil
}

// buildUpstreamConfig converts a single MCPUpstream into a config.UpstreamConfig.
func buildUpstreamConfig(up *v1alpha1.MCPUpstream) (config.UpstreamConfig, error) {
	uc := config.UpstreamConfig{
		Name:       fmt.Sprintf("%s-%s", up.Namespace, up.Name),
		Enabled:    true,
		ToolPrefix: up.Spec.ToolPrefix,
	}

	// Base URL resolution.
	switch {
	case up.Spec.ServiceRef != nil:
		uc.BaseURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
			up.Spec.ServiceRef.Name,
			up.Namespace,
			up.Spec.ServiceRef.Port,
		)
	case up.Spec.BaseURL != "":
		uc.BaseURL = up.Spec.BaseURL
	default:
		return config.UpstreamConfig{}, fmt.Errorf("upstream %s/%s: neither serviceRef nor baseURL is set", up.Namespace, up.Name)
	}

	// OpenAPI source.
	openAPI := up.Spec.OpenAPI
	switch {
	case openAPI.ConfigMapRef != nil:
		uc.OpenAPI = config.OpenAPISourceConfig{
			Source: fmt.Sprintf("%s/%s_%s.yaml", specsDir, up.Namespace, up.Name),
		}
	case openAPI.URL != "":
		uc.OpenAPI = config.OpenAPISourceConfig{
			Source: openAPI.URL,
		}
	case openAPI.AutoDiscover != nil:
		path := openAPI.AutoDiscover.Path
		if path == "" {
			path = "/openapi.json"
		}
		uc.OpenAPI = config.OpenAPISourceConfig{
			Source: uc.BaseURL + path,
		}
	default:
		return config.UpstreamConfig{}, fmt.Errorf("upstream %s/%s: no openapi source configured", up.Namespace, up.Name)
	}

	// Overlay source.
	if up.Spec.Overlay != nil && up.Spec.Overlay.ConfigMapRef != nil {
		uc.Overlay = &config.OverlayConfig{
			Source: fmt.Sprintf("%s/%s_%s.yaml", overlaysDir, up.Namespace, up.Name),
		}
	}

	// Outbound auth.
	if up.Spec.OutboundAuth != nil {
		uc.OutboundAuth = config.OutboundAuthConfig{
			Strategy: up.Spec.OutboundAuth.Strategy,
		}
		if up.Spec.OutboundAuth.OAuth2 != nil {
			uc.OutboundAuth.OAuth2ClientCredentials = config.OAuth2CCConfig{
				TokenURL: up.Spec.OutboundAuth.OAuth2.TokenURL,
			}
			// Secret values are injected via environment variables at runtime;
			// the operator creates a corresponding env secret mount.
			if up.Spec.OutboundAuth.OAuth2.SecretRef != nil {
				uc.OutboundAuth.OAuth2ClientCredentials.ClientSecret =
					fmt.Sprintf("${UPSTREAM_%s_%s_OAUTH2_CLIENT_SECRET}", up.Namespace, up.Name)
				uc.OutboundAuth.OAuth2ClientCredentials.ClientID =
					fmt.Sprintf("${UPSTREAM_%s_%s_OAUTH2_CLIENT_ID}", up.Namespace, up.Name)
			}
		}
	}

	// Transport.
	if up.Spec.Transport != nil {
		uc.Transport = config.TransportConfig{
			MaxIdleConns: up.Spec.Transport.MaxIdleConns,
		}
		if up.Spec.Transport.TLS != nil {
			// TLS credentials are mounted from the referenced secret.
			uc.Transport.TLS = config.TLSConfig{
				RootCAPath: fmt.Sprintf("/etc/mcp-anything/tls/%s-%s/ca.crt", up.Namespace, up.Name),
			}
		}
	}

	// Validation.
	if up.Spec.Validation != nil {
		uc.Validation = config.ValidationConfig{
			ValidateRequest:  up.Spec.Validation.ValidateRequest,
			ValidateResponse: up.Spec.Validation.ValidateResponse,
		}
	}

	return uc, nil
}
