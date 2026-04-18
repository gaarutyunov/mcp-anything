// Package configgen translates MCPProxy and MCPUpstream CRDs into proxy config YAML.
package configgen

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

const (
	defaultImage = "ghcr.io/gaarutyunov/mcp-anything:latest"
	defaultPort  = int32(8080)
	specsDir     = "/etc/mcp-anything/specs"
	overlaysDir  = "/etc/mcp-anything/overlays"
)

// The following unexported types mirror a subset of config.ProxyConfig using yaml struct tags
// so that gopkg.in/yaml.v3 produces snake_case keys that koanf can correctly parse.
// (config.ProxyConfig uses koanf tags only; marshaling it directly produces PascalCase keys.)

type generatedProxyConfig struct {
	Server      generatedServerConfig               `yaml:"server"`
	Naming      generatedNamingConfig               `yaml:"naming"`
	RateLimits  map[string]generatedRateLimitConfig `yaml:"rate_limits,omitempty"`
	Telemetry   *generatedTelemetryConfig           `yaml:"telemetry,omitempty"`
	InboundAuth *generatedInboundAuthConfig         `yaml:"inbound_auth,omitempty"`
	Upstreams   []generatedUpstreamConfig           `yaml:"upstreams"`
}

type generatedRateLimitConfig struct {
	Average int64  `yaml:"average"`
	Period  string `yaml:"period"`
	Burst   int64  `yaml:"burst,omitempty"`
	Source  string `yaml:"source,omitempty"`
}

type generatedServerConfig struct {
	Port int `yaml:"port"`
}

type generatedNamingConfig struct {
	Separator          string `yaml:"separator,omitempty"`
	MaxLength          int    `yaml:"max_length,omitempty"`
	ConflictResolution string `yaml:"conflict_resolution,omitempty"`
}

type generatedTelemetryConfig struct {
	OTLPEndpoint string `yaml:"otlp_endpoint"`
}

type generatedInboundAuthConfig struct {
	Strategy string                  `yaml:"strategy"`
	JWT      *generatedJWTAuthConfig `yaml:"jwt,omitempty"`
}

type generatedJWTAuthConfig struct {
	JWKSURL string `yaml:"jwks_url"`
}

type generatedUpstreamConfig struct {
	Name         string                     `yaml:"name"`
	Enabled      bool                       `yaml:"enabled"`
	ToolPrefix   string                     `yaml:"tool_prefix,omitempty"`
	Type         string                     `yaml:"type,omitempty"`
	BaseURL      string                     `yaml:"base_url,omitempty"`
	OpenAPI      *generatedOpenAPIConfig    `yaml:"openapi,omitempty"`
	Overlay      *generatedOverlayConfig    `yaml:"overlay,omitempty"`
	OutboundAuth *generatedOutboundAuth     `yaml:"outbound_auth,omitempty"`
	Transport    *generatedTransportConfig  `yaml:"transport,omitempty"`
	Validation   *generatedValidationConfig `yaml:"validation,omitempty"`
	Commands     []generatedCommandConfig   `yaml:"commands,omitempty"`
	RateLimit    string                     `yaml:"rate_limit,omitempty"`
}

type generatedOpenAPIConfig struct {
	Source string `yaml:"source"`
}

type generatedCommandConfig struct {
	ToolName    string                       `yaml:"tool_name"`
	Description string                       `yaml:"description,omitempty"`
	Command     string                       `yaml:"command"`
	Shell       bool                         `yaml:"shell,omitempty"`
	WorkingDir  string                       `yaml:"working_dir,omitempty"`
	Timeout     string                       `yaml:"timeout,omitempty"`
	MaxOutput   int64                        `yaml:"max_output,omitempty"`
	Env         map[string]string            `yaml:"env,omitempty"`
	InputSchema *generatedCommandInputSchema `yaml:"input_schema,omitempty"`
}

type generatedCommandInputSchema struct {
	Type       string                                    `yaml:"type,omitempty"`
	Properties map[string]generatedCommandSchemaProperty `yaml:"properties,omitempty"`
	Required   []string                                  `yaml:"required,omitempty"`
}

type generatedCommandSchemaProperty struct {
	Type        string `yaml:"type,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type generatedOverlayConfig struct {
	Source string `yaml:"source"`
}

type generatedOutboundAuth struct {
	Strategy                string                   `yaml:"strategy"`
	OAuth2ClientCredentials *generatedOAuth2CCConfig `yaml:"oauth2_client_credentials,omitempty"`
}

type generatedOAuth2CCConfig struct {
	TokenURL     string   `yaml:"token_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes,omitempty"`
}

type generatedTransportConfig struct {
	MaxIdleConns int                 `yaml:"max_idle_conns,omitempty"`
	TLS          *generatedTLSConfig `yaml:"tls,omitempty"`
}

type generatedTLSConfig struct {
	RootCAPath string `yaml:"root_ca_path"`
}

type generatedValidationConfig struct {
	ValidateRequest  bool `yaml:"validate_request"`
	ValidateResponse bool `yaml:"validate_response"`
}

// Generate translates MCPProxy and its selected MCPUpstream resources into a
// config.ProxyConfig and returns it serialised as YAML bytes.
func Generate(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) ([]byte, error) {
	_ = ctx // reserved for future async lookups

	cfg := generatedProxyConfig{}

	// Server configuration.
	port := proxy.Spec.Server.Port
	if port == 0 {
		port = defaultPort
	}
	cfg.Server = generatedServerConfig{
		Port: int(port),
	}

	// Naming configuration.
	cfg.Naming = generatedNamingConfig{
		Separator:          proxy.Spec.Naming.Separator,
		MaxLength:          proxy.Spec.Naming.MaxLength,
		ConflictResolution: proxy.Spec.Naming.ConflictResolution,
	}

	// Rate limit policies.
	if proxy.Spec.RateLimits != nil && len(proxy.Spec.RateLimits.Policies) > 0 {
		rl := make(map[string]generatedRateLimitConfig, len(proxy.Spec.RateLimits.Policies))
		for name, policy := range proxy.Spec.RateLimits.Policies {
			rl[name] = generatedRateLimitConfig{
				Average: policy.Average,
				Period:  policy.Period,
				Burst:   policy.Burst,
				Source:  policy.Source,
			}
		}
		cfg.RateLimits = rl
	}

	// Telemetry configuration.
	if proxy.Spec.Telemetry != nil && proxy.Spec.Telemetry.OTLPEndpoint != "" {
		cfg.Telemetry = &generatedTelemetryConfig{
			OTLPEndpoint: proxy.Spec.Telemetry.OTLPEndpoint,
		}
	}

	// Inbound auth configuration.
	if proxy.Spec.InboundAuth != nil {
		inboundAuth := &generatedInboundAuthConfig{
			Strategy: proxy.Spec.InboundAuth.Strategy,
		}
		if proxy.Spec.InboundAuth.JWT != nil {
			inboundAuth.JWT = &generatedJWTAuthConfig{
				JWKSURL: proxy.Spec.InboundAuth.JWT.JWKSURL,
			}
		}
		cfg.InboundAuth = inboundAuth
	}

	// Build upstream configs.
	upstreamCfgs := make([]generatedUpstreamConfig, 0, len(upstreams))
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

// buildUpstreamConfig converts a single MCPUpstream into a generatedUpstreamConfig.
func buildUpstreamConfig(up *v1alpha1.MCPUpstream) (generatedUpstreamConfig, error) {
	uc := generatedUpstreamConfig{
		Name:       fmt.Sprintf("%s-%s", up.Namespace, up.Name),
		Enabled:    true,
		ToolPrefix: up.Spec.ToolPrefix,
	}

	// Dispatch by upstream type.
	upstreamType := up.Spec.Type
	if upstreamType == "" {
		upstreamType = "http"
	}

	switch upstreamType {
	case "command":
		return buildCommandUpstreamConfig(up, uc)
	default:
		return buildHTTPUpstreamConfig(up, uc)
	}
}

// buildCommandUpstreamConfig fills in a generatedUpstreamConfig for a command upstream.
func buildCommandUpstreamConfig(up *v1alpha1.MCPUpstream, uc generatedUpstreamConfig) (generatedUpstreamConfig, error) {
	if len(up.Spec.Commands) == 0 {
		return generatedUpstreamConfig{}, fmt.Errorf("upstream %s/%s: type command requires at least one command entry", up.Namespace, up.Name)
	}

	uc.Type = "command"
	cmds := make([]generatedCommandConfig, 0, len(up.Spec.Commands))
	for _, cmd := range up.Spec.Commands {
		gc := generatedCommandConfig{
			ToolName:    cmd.ToolName,
			Description: cmd.Description,
			Command:     cmd.Command,
			Shell:       cmd.Shell,
			WorkingDir:  cmd.WorkingDir,
			Timeout:     cmd.Timeout,
			MaxOutput:   cmd.MaxOutput,
			Env:         cmd.Env,
		}
		if cmd.InputSchema.Type != "" || len(cmd.InputSchema.Properties) > 0 || len(cmd.InputSchema.Required) > 0 {
			gc.InputSchema = buildGeneratedInputSchema(&cmd.InputSchema)
		}
		cmds = append(cmds, gc)
	}
	uc.Commands = cmds
	return uc, nil
}

// buildGeneratedInputSchema converts a CommandInputSchema to its generated form.
func buildGeneratedInputSchema(s *v1alpha1.CommandInputSchema) *generatedCommandInputSchema {
	gs := &generatedCommandInputSchema{
		Type:     s.Type,
		Required: s.Required,
	}
	if len(s.Properties) > 0 {
		gs.Properties = make(map[string]generatedCommandSchemaProperty, len(s.Properties))
		for name, prop := range s.Properties {
			gs.Properties[name] = generatedCommandSchemaProperty{
				Type:        prop.Type,
				Description: prop.Description,
			}
		}
	}
	return gs
}

// buildHTTPUpstreamConfig fills in a generatedUpstreamConfig for an HTTP upstream.
func buildHTTPUpstreamConfig(up *v1alpha1.MCPUpstream, uc generatedUpstreamConfig) (generatedUpstreamConfig, error) {
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
		return generatedUpstreamConfig{}, fmt.Errorf("upstream %s/%s: neither serviceRef nor baseURL is set", up.Namespace, up.Name)
	}

	// OpenAPI source.
	openAPI := up.Spec.OpenAPI
	switch {
	case openAPI.ConfigMapRef != nil:
		uc.OpenAPI = &generatedOpenAPIConfig{
			Source: fmt.Sprintf("%s/%s_%s.yaml", specsDir, up.Namespace, up.Name),
		}
	case openAPI.URL != "":
		uc.OpenAPI = &generatedOpenAPIConfig{
			Source: openAPI.URL,
		}
	case openAPI.AutoDiscover != nil:
		path := openAPI.AutoDiscover.Path
		if path == "" {
			path = "/openapi.json"
		}
		uc.OpenAPI = &generatedOpenAPIConfig{
			Source: uc.BaseURL + path,
		}
	default:
		return generatedUpstreamConfig{}, fmt.Errorf("upstream %s/%s: no openapi source configured", up.Namespace, up.Name)
	}

	// Overlay source.
	if up.Spec.Overlay != nil && up.Spec.Overlay.ConfigMapRef != nil {
		uc.Overlay = &generatedOverlayConfig{
			Source: fmt.Sprintf("%s/%s_%s.yaml", overlaysDir, up.Namespace, up.Name),
		}
	}

	// Outbound auth.
	if up.Spec.OutboundAuth != nil {
		outboundAuth := &generatedOutboundAuth{
			Strategy: up.Spec.OutboundAuth.Strategy,
		}
		if up.Spec.OutboundAuth.OAuth2 != nil {
			oauth2 := &generatedOAuth2CCConfig{
				TokenURL: up.Spec.OutboundAuth.OAuth2.TokenURL,
			}
			// Secret values are injected via environment variables at runtime;
			// the operator creates a corresponding env secret mount.
			if up.Spec.OutboundAuth.OAuth2.SecretRef != nil {
				oauth2.ClientSecret = fmt.Sprintf("${UPSTREAM_%s_%s_OAUTH2_CLIENT_SECRET}", up.Namespace, up.Name)
				oauth2.ClientID = fmt.Sprintf("${UPSTREAM_%s_%s_OAUTH2_CLIENT_ID}", up.Namespace, up.Name)
			}
			outboundAuth.OAuth2ClientCredentials = oauth2
		}
		uc.OutboundAuth = outboundAuth
	}

	// Transport.
	if up.Spec.Transport != nil {
		transport := &generatedTransportConfig{
			MaxIdleConns: up.Spec.Transport.MaxIdleConns,
		}
		if up.Spec.Transport.TLS != nil {
			// TLS credentials are mounted from the referenced secret.
			transport.TLS = &generatedTLSConfig{
				RootCAPath: fmt.Sprintf("/etc/mcp-anything/tls/%s-%s/ca.crt", up.Namespace, up.Name),
			}
		}
		uc.Transport = transport
	}

	// Validation.
	if up.Spec.Validation != nil {
		uc.Validation = &generatedValidationConfig{
			ValidateRequest:  up.Spec.Validation.ValidateRequest,
			ValidateResponse: up.Spec.Validation.ValidateResponse,
		}
	}

	// Rate limit policy reference.
	uc.RateLimit = up.Spec.RateLimit

	return uc, nil
}
