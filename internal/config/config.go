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
	Runtime     RuntimeConfig     `koanf:"runtime"`
}

// RuntimeConfig controls the bounded pools for concurrent script runtime instances.
// Limiting runtime concurrency prevents OOM conditions and denial-of-service attacks
// caused by excessive memory growth under high load.
type RuntimeConfig struct {
	JS  JSRuntimeConfig  `koanf:"js"`
	Lua LuaRuntimeConfig `koanf:"lua"`
}

// JSRuntimeConfig configures Sobek JavaScript runtime pool sizes.
type JSRuntimeConfig struct {
	// MaxAuthVMs is the maximum number of concurrent JS runtimes used for auth scripts
	// (inbound + outbound combined). Default: 10.
	MaxAuthVMs int `koanf:"max_auth_vms"`
	// MaxScriptVMs is the maximum number of concurrent JS runtimes used for tool scripts.
	// Default: 20.
	MaxScriptVMs int `koanf:"max_script_vms"`
}

// LuaRuntimeConfig configures gopher-lua runtime pool sizes.
type LuaRuntimeConfig struct {
	// MaxAuthVMs is the maximum number of concurrent Lua runtimes used for auth scripts
	// (inbound + outbound combined). Default: 10.
	MaxAuthVMs int `koanf:"max_auth_vms"`
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
	Strategy      string              `koanf:"strategy"` // jwt|introspection|apikey|lua|js_script|none
	JWT           JWTAuthConfig       `koanf:"jwt"`
	Introspection IntrospectionConfig `koanf:"introspection"`
	APIKey        APIKeyAuthConfig    `koanf:"apikey"`
	Lua           LuaAuthConfig       `koanf:"lua"`
	JS            JSAuthConfig        `koanf:"js"`
}

// LuaAuthConfig configures inbound token validation via a Lua script.
// The script receives the token as its first argument and must return:
// allowed (bool), status (int), extra_headers (table), error_msg (string).
type LuaAuthConfig struct {
	ScriptPath string        `koanf:"script_path"`
	Timeout    time.Duration `koanf:"timeout"`
}

// JSAuthConfig configures inbound token validation via a JavaScript (Sobek) script.
// The script receives (token, ctx) and must return:
// { allowed: bool, status?: number, error?: string, subject?: string, extra_headers?: object }
type JSAuthConfig struct {
	ScriptPath string            `koanf:"script_path"`
	Timeout    time.Duration     `koanf:"timeout"`
	Env        map[string]string `koanf:"env"`
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

// ServerTLSConfig configures inbound TLS termination for the MCP server.
type ServerTLSConfig struct {
	CertPath     string `koanf:"cert_path"`
	KeyPath      string `koanf:"key_path"`
	MinVersion   string `koanf:"min_version"`    // "1.0" | "1.1" | "1.2" | "1.3"; default: "1.2"
	ClientAuth   string `koanf:"client_auth"`    // "none" | "request" | "require_and_verify"
	ClientCAPath string `koanf:"client_ca_path"` // CA cert for verifying MCP client certs (mTLS)
}

// TLSConfig configures TLS for an outbound upstream connection.
type TLSConfig struct {
	InsecureSkipVerify bool   `koanf:"insecure_skip_verify"` // WARNING: disables certificate verification
	MinVersion         string `koanf:"min_version"`          // "1.0" | "1.1" | "1.2" | "1.3"; default: "1.2"
	MaxVersion         string `koanf:"max_version"`          // "1.0" | "1.1" | "1.2" | "1.3"
	RootCAPath         string `koanf:"root_ca_path"`         // PEM file with additional trusted CA certs
	ClientCertPath     string `koanf:"client_cert_path"`     // PEM file with client cert for mTLS
	ClientKeyPath      string `koanf:"client_key_path"`      // PEM file with client private key for mTLS
	ServerName         string `koanf:"server_name"`          // SNI override
	SessionCacheSize   int    `koanf:"session_cache_size"`   // LRU TLS session cache; default: 64
}

// TransportConfig configures the HTTP transport (connection pooling, dialing, TLS) per upstream.
type TransportConfig struct {
	// Connection pooling
	MaxIdleConns        int           `koanf:"max_idle_conns"`          // default: 100
	MaxIdleConnsPerHost int           `koanf:"max_idle_conns_per_host"` // default: 10
	IdleConnTimeout     time.Duration `koanf:"idle_conn_timeout"`       // default: 90s

	// Dialing
	DialTimeout   time.Duration `koanf:"dial_timeout"`   // default: 30s
	DialKeepalive time.Duration `koanf:"dial_keepalive"` // default: 30s

	// Response
	ResponseHeaderTimeout time.Duration `koanf:"response_header_timeout"` // default: 0 (no separate timeout)

	// HTTP/2
	ForceHTTP2 bool `koanf:"force_http2"` // default: false

	// Proxy
	ProxyURL string `koanf:"proxy_url"` // http://, https://, socks5://, socks5h://

	// TLS
	TLS TLSConfig `koanf:"tls"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port                     int             `koanf:"port"`
	ReadTimeout              time.Duration   `koanf:"read_timeout"`
	WriteTimeout             time.Duration   `koanf:"write_timeout"`
	ShutdownTimeout          time.Duration   `koanf:"shutdown_timeout"`
	MaxRequestBody           string          `koanf:"max_request_body"`
	StartupValidationTimeout time.Duration   `koanf:"startup_validation_timeout"`
	TLS                      ServerTLSConfig `koanf:"tls"`
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

// UpstreamConfig describes a single upstream, either HTTP API or command-backed tools.
type UpstreamConfig struct {
	Name                     string              `koanf:"name"`
	Enabled                  bool                `koanf:"enabled"`
	ToolPrefix               string              `koanf:"tool_prefix"`
	Type                     string              `koanf:"type"`     // "http" (default) | "command"
	BaseURL                  string              `koanf:"base_url"` // used by type: http only
	Timeout                  time.Duration       `koanf:"timeout"`
	TLSSkipVerify            bool                `koanf:"tls_skip_verify"` // Deprecated: use transport.tls.insecure_skip_verify
	Headers                  map[string]string   `koanf:"headers"`
	Transport                TransportConfig     `koanf:"transport"`
	OpenAPI                  OpenAPISourceConfig `koanf:"openapi"`
	Overlay                  *OverlayConfig      `koanf:"overlay"`
	StartupValidationTimeout time.Duration       `koanf:"startup_validation_timeout"`
	Validation               ValidationConfig    `koanf:"validation"`
	InboundAuthOverride      *InboundAuthConfig  `koanf:"inbound_auth_override"`
	OutboundAuth             OutboundAuthConfig  `koanf:"outbound_auth"`
	Commands                 []CommandConfig     `koanf:"commands"` // used by type: command only
	Scripts                  []ScriptConfig      `koanf:"scripts"`  // used by type: script only
}

// CommandConfig defines a single command-backed MCP tool within a command upstream.
type CommandConfig struct {
	ToolName    string             `koanf:"tool_name"`
	Description string             `koanf:"description"`
	Command     string             `koanf:"command"`
	InputSchema CommandInputSchema `koanf:"input_schema"`
	Timeout     time.Duration      `koanf:"timeout"`
	Env         map[string]string  `koanf:"env"`
	WorkingDir  string             `koanf:"working_dir"`
	Shell       bool               `koanf:"shell"`      // execute via sh -c; default false (direct exec)
	MaxOutput   int64              `koanf:"max_output"` // max bytes from stdout/stderr; 0 = 1 MiB default
}

// ScriptConfig defines a single JavaScript-backed MCP tool within a script upstream.
type ScriptConfig struct {
	ToolName    string             `koanf:"tool_name"`
	Description string             `koanf:"description"`
	ScriptPath  string             `koanf:"script_path"`
	InputSchema CommandInputSchema `koanf:"input_schema"` // reuses CommandInputSchema
	Timeout     time.Duration      `koanf:"timeout"`
	Env         map[string]string  `koanf:"env"`
}

// CommandInputSchema is the JSON Schema definition for a command tool's input parameters.
type CommandInputSchema struct {
	Type       string                           `koanf:"type"`
	Properties map[string]CommandSchemaProperty `koanf:"properties"`
	Required   []string                         `koanf:"required"`
}

// CommandSchemaProperty describes a single property in a command tool's input schema.
type CommandSchemaProperty struct {
	Type        string `koanf:"type"`
	Description string `koanf:"description"`
}

// OutboundAuthConfig controls how the proxy authenticates outbound requests to an upstream API.
type OutboundAuthConfig struct {
	Strategy                string               `koanf:"strategy"` // bearer|api_key|oauth2_client_credentials|lua|js_script|none
	Bearer                  BearerOutboundConfig `koanf:"bearer"`
	APIKey                  APIKeyOutboundConfig `koanf:"api_key"`
	OAuth2ClientCredentials OAuth2CCConfig       `koanf:"oauth2_client_credentials"`
	Lua                     LuaOutboundConfig    `koanf:"lua"`
	JS                      JSOutboundConfig     `koanf:"js"`
	// Upstream is set programmatically (not from config file) to the owning upstream's name.
	// Used by the lua and js_script strategies to pass the upstream name to scripts.
	Upstream string `koanf:"-"`
}

// LuaOutboundConfig configures outbound credential acquisition via a Lua script.
// The script receives (upstream, cached_token, cached_expiry) as arguments and must return:
// token (string), expiry_unix (int), raw_headers (table), error_msg (string).
type LuaOutboundConfig struct {
	ScriptPath string        `koanf:"script_path"`
	Timeout    time.Duration `koanf:"timeout"`
}

// JSOutboundConfig configures outbound credential acquisition via a JavaScript (Sobek) script.
// The script receives (upstream, ctx) and must return:
// { token?: string, raw_headers?: object, expiry?: number, error?: string }
type JSOutboundConfig struct {
	ScriptPath string            `koanf:"script_path"`
	Timeout    time.Duration     `koanf:"timeout"`
	Env        map[string]string `koanf:"env"`
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
