// Package config defines the configuration structs and loading logic for mcp-anything.
package config

import "time"

// ProxyConfig is the top-level configuration struct.
type ProxyConfig struct {
	Server      ServerConfig      `koanf:"server"`
	Telemetry   TelemetryConfig   `koanf:"telemetry"`
	Naming      NamingConfig      `koanf:"naming"`
	Upstreams   []UpstreamConfig  `koanf:"upstreams"`
	InboundAuth InboundAuthConfig `koanf:"inbound_auth"`
	Groups      []GroupConfig     `koanf:"groups"`
}

// GroupConfig configures a named group of upstreams exposed at a single MCP endpoint.
// If no groups are configured, a synthetic default group is created at /mcp with all upstreams.
type GroupConfig struct {
	Name      string   `koanf:"name"`
	Endpoint  string   `koanf:"endpoint"`  // e.g. /mcp or /mcp/readonly
	Upstreams []string `koanf:"upstreams"` // upstream names to include
	Filter    string   `koanf:"filter"`    // RFC 9535 JSONPath expression (optional)
}

// InboundAuthConfig controls how inbound MCP clients are authenticated.
type InboundAuthConfig struct {
	Strategy      string              `koanf:"strategy"` // jwt|introspection|apikey|lua|none
	JWT           JWTAuthConfig       `koanf:"jwt"`
	Introspection IntrospectionConfig `koanf:"introspection"`
	APIKey        APIKeyAuthConfig    `koanf:"apikey"`
	Lua           LuaAuthConfig       `koanf:"lua"`
}

// LuaAuthConfig configures inbound token validation via a Lua script.
// The script receives the token as its first argument and must return:
// allowed (bool), status (int), extra_headers (table), error_msg (string).
type LuaAuthConfig struct {
	ScriptPath string        `koanf:"script_path"`
	Timeout    time.Duration `koanf:"timeout"`
}

// JWTAuthConfig configures JWT Bearer token validation via OIDC/JWKS.
type JWTAuthConfig struct {
	Issuer   string `koanf:"issuer"`
	Audience string `koanf:"audience"`
	JWKSURL  string `koanf:"jwks_url"` // optional; uses OIDC discovery if empty
}

// IntrospectionConfig configures token introspection via an OIDC server.
type IntrospectionConfig struct {
	Issuer       string `koanf:"issuer"`
	ClientID     string `koanf:"client_id"`
	ClientSecret string `koanf:"client_secret"` // supports ${ENV_VAR} expansion
	Audience     string `koanf:"audience"`
}

// APIKeyAuthConfig configures API key validation from a request header.
type APIKeyAuthConfig struct {
	Header  string `koanf:"header"`   // header name to read the key from
	KeysEnv string `koanf:"keys_env"` // env var containing comma-separated valid keys
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port                     int           `koanf:"port"`
	ReadTimeout              time.Duration `koanf:"read_timeout"`
	WriteTimeout             time.Duration `koanf:"write_timeout"`
	ShutdownTimeout          time.Duration `koanf:"shutdown_timeout"`
	MaxRequestBody           string        `koanf:"max_request_body"`
	StartupValidationTimeout time.Duration `koanf:"startup_validation_timeout"`
}

// TelemetryConfig holds observability settings.
type TelemetryConfig struct {
	ServiceName    string `koanf:"service_name"`
	ServiceVersion string `koanf:"service_version"`
	OTLPEndpoint   string `koanf:"otlp_endpoint"` // e.g. "localhost:4317"; empty = no trace exporter
	Insecure       bool   `koanf:"insecure"`      // skip TLS for OTLP gRPC (useful in tests)
}

// SlugRulesConfig controls which slug transformations are applied.
type SlugRulesConfig struct {
	ReplaceSlashes     bool `koanf:"replace_slashes"`
	ReplaceBraces      bool `koanf:"replace_braces"`
	Lowercase          bool `koanf:"lowercase"`
	CollapseSeparators bool `koanf:"collapse_separators"`
}

// NamingConfig controls how tool names are generated.
type NamingConfig struct {
	Separator                   string          `koanf:"separator"`
	MaxLength                   int             `koanf:"max_length"`
	ConflictResolution          string          `koanf:"conflict_resolution"`
	DescriptionMaxLength        int             `koanf:"description_max_length"`
	DescriptionTruncationSuffix string          `koanf:"description_truncation_suffix"`
	DefaultSlugRules            SlugRulesConfig `koanf:"default_slug_rules"`
}

// ValidationConfig controls runtime request and response validation against the OpenAPI schema.
type ValidationConfig struct {
	ValidateRequest           bool   `koanf:"validate_request"`
	ValidateResponse          bool   `koanf:"validate_response"`
	ResponseValidationFailure string `koanf:"response_validation_failure"` // "warn" | "fail"
	SuccessStatus             []int  `koanf:"success_status"`
	ErrorStatus               []int  `koanf:"error_status"`
}

// UpstreamConfig describes a single upstream HTTP API.
type UpstreamConfig struct {
	Name                     string              `koanf:"name"`
	Enabled                  bool                `koanf:"enabled"`
	ToolPrefix               string              `koanf:"tool_prefix"`
	BaseURL                  string              `koanf:"base_url"`
	Timeout                  time.Duration       `koanf:"timeout"`
	TLSSkipVerify            bool                `koanf:"tls_skip_verify"`
	Headers                  map[string]string   `koanf:"headers"`
	OpenAPI                  OpenAPISourceConfig `koanf:"openapi"`
	Overlay                  *OverlayConfig      `koanf:"overlay"`
	StartupValidationTimeout time.Duration       `koanf:"startup_validation_timeout"`
	Validation               ValidationConfig    `koanf:"validation"`
	InboundAuthOverride      *InboundAuthConfig  `koanf:"inbound_auth_override"`
	OutboundAuth             OutboundAuthConfig  `koanf:"outbound_auth"`
}

// OutboundAuthConfig controls how the proxy authenticates outbound requests to an upstream API.
type OutboundAuthConfig struct {
	Strategy                string               `koanf:"strategy"` // bearer|api_key|oauth2_client_credentials|lua|none
	Bearer                  BearerOutboundConfig `koanf:"bearer"`
	APIKey                  APIKeyOutboundConfig `koanf:"api_key"`
	OAuth2ClientCredentials OAuth2CCConfig       `koanf:"oauth2_client_credentials"`
	Lua                     LuaOutboundConfig    `koanf:"lua"`
	// Upstream is set programmatically (not from config file) to the owning upstream's name.
	// Used by the lua strategy to pass the upstream name to scripts.
	Upstream string `koanf:"-"`
}

// LuaOutboundConfig configures outbound credential acquisition via a Lua script.
// The script receives (upstream, cached_token, cached_expiry) as arguments and must return:
// token (string), expiry_unix (int), raw_headers (table), error_msg (string).
type LuaOutboundConfig struct {
	ScriptPath string        `koanf:"script_path"`
	Timeout    time.Duration `koanf:"timeout"`
}

// BearerOutboundConfig configures static Bearer token injection.
type BearerOutboundConfig struct {
	TokenEnv string `koanf:"token_env"` // env var name containing the token
}

// APIKeyOutboundConfig configures API key header injection.
type APIKeyOutboundConfig struct {
	Header   string `koanf:"header"`    // header name to inject
	ValueEnv string `koanf:"value_env"` // env var name containing the value
	Prefix   string `koanf:"prefix"`    // prepended to value, e.g. "ApiKey "
}

// OAuth2CCConfig configures OAuth2 client credentials flow.
type OAuth2CCConfig struct {
	TokenURL     string   `koanf:"token_url"`
	ClientID     string   `koanf:"client_id"`
	ClientSecret string   `koanf:"client_secret"` // supports ${ENV_VAR} expansion
	Scopes       []string `koanf:"scopes"`
}

// OpenAPISourceConfig points to an OpenAPI spec file or URL.
type OpenAPISourceConfig struct {
	Source             string        `koanf:"source"`
	AuthHeader         string        `koanf:"auth_header"`
	RefreshInterval    time.Duration `koanf:"refresh_interval"`
	MaxRefreshFailures int           `koanf:"max_refresh_failures"`
	AllowExternalRefs  bool          `koanf:"allow_external_refs"`
	Version            string        `koanf:"version"`
}

// OverlayConfig points to an OpenAPI Overlay document.
type OverlayConfig struct {
	Source          string        `koanf:"source"`
	AuthHeader      string        `koanf:"auth_header"`
	RefreshInterval time.Duration `koanf:"refresh_interval"`
	Inline          string        `koanf:"inline"`
}
