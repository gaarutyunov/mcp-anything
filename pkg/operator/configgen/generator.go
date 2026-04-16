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
	Server      generatedServerConfig       `yaml:"server"`
	Naming      generatedNamingConfig       `yaml:"naming"`
	Telemetry   *generatedTelemetryConfig   `yaml:"telemetry,omitempty"`
	InboundAuth *generatedInboundAuthConfig `yaml:"inbound_auth,omitempty"`
	Upstreams   []generatedUpstreamConfig   `yaml:"upstreams"`
	// Extensions holds additional top-level config keys contributed by registered
	// proxy section factories. The inline tag merges them into the top-level YAML
	// output so that registered proxy section factories can find them at their expected paths.
	Extensions map[string]interface{} `yaml:",inline"`
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
	// Extensions holds additional upstream-level config keys contributed by registered
	// upstream section factories. The inline tag merges them into this upstream's YAML object.
	Extensions map[string]interface{} `yaml:",inline"`
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
		upCfg, err := buildUpstreamConfig(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("building upstream config for %s/%s: %w", up.Namespace, up.Name, err)
		}
		upstreamCfgs = append(upstreamCfgs, upCfg)
	}
	cfg.Upstreams = upstreamCfgs

	// Apply registered proxy section factories.
	proxyExts := make(map[string]interface{})
	if err := applyProxyExtensions(ctx, proxy, upstreams, proxyExts); err != nil {
		return nil, fmt.Errorf("applying proxy extensions: %w", err)
	}
	if len(proxyExts) > 0 {
		cfg.Extensions = proxyExts
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling proxy config: %w", err)
	}
	return data, nil
}

// buildUpstreamConfig converts a single MCPUpstream into a generatedUpstreamConfig.
func buildUpstreamConfig(ctx context.Context, up *v1alpha1.MCPUpstream) (generatedUpstreamConfig, error) {
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

	var err error
	switch upstreamType {
	case "command":
		uc, err = buildCommandUpstreamConfig(up, uc)
	default:
		uc, err = buildHTTPUpstreamConfig(up, uc)
	}
	if err != nil {
		return generatedUpstreamConfig{}, err
	}

	// Apply registered upstream section factories.
	upstreamExts := make(map[string]interface{})
	if err := applyUpstreamExtensions(ctx, up, upstreamExts); err != nil {
		return generatedUpstreamConfig{}, fmt.Errorf("applying extensions for upstream %s/%s: %w", up.Namespace, up.Name, err)
	}
	if len(upstreamExts) > 0 {
		uc.Extensions = upstreamExts
	}

	return uc, nil
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

// buildGeneratedInputSchema converts an MCPUpstreamCommandInputSchema to its generated form.
func buildGeneratedInputSchema(s *v1alpha1.MCPUpstreamCommandInputSchema) *generatedCommandInputSchema {
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

	return uc, nil
}
