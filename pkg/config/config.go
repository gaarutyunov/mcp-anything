// Package config defines the public configuration types for mcp-anything.
// These types form the SDK contract and can be used by SDK consumers to
// define configurations programmatically.
package config

import (
	"context"
	"time"
)

// PoolAcquirer is the minimal interface for a bounded script runtime pool.
// Implemented by *runtime.Pool. Used by script-based outbound auth strategies
// to bound the number of concurrent runtime instances.
type PoolAcquirer interface {
	Acquire(ctx context.Context) (release func(), err error)
}

// ProxyConfig is the top-level configuration struct.
type ProxyConfig struct {
	Server         ServerConfig               `koanf:"server"`
	Telemetry      TelemetryConfig            `koanf:"telemetry"`
	Naming         NamingConfig               `koanf:"naming"`
	Upstreams      []UpstreamConfig           `koanf:"upstreams"`
	InboundAuth    InboundAuthConfig          `koanf:"inbound_auth"`
	Groups         []GroupConfig              `koanf:"groups"`
	Runtime        RuntimeConfig              `koanf:"runtime"`
	TokenCounting  TokenCountingConfig        `koanf:"token_counting"`
	RateLimits     map[string]RateLimitConfig `koanf:"rate_limits"`
	RateLimitStore RateLimitStoreConfig       `koanf:"rate_limit_store"`
	SessionStore   SessionStoreConfig         `koanf:"session_store"`
}

// RateLimitConfig defines a named rate limit policy.
// Named policies are referenced by upstreams or per-tool overlays.
type RateLimitConfig struct {
	// Average is the number of requests allowed per Period.
	Average int64 `koanf:"average"`
	// Period is the time window for the rate limit.
	Period time.Duration `koanf:"period"`
	// Burst is the number of additional requests allowed above Average in a window.
	// Total capacity = Average + Burst.
	Burst int64 `koanf:"burst"`
	// Source determines the counter key: "user" (authenticated subject),
	// "ip" (remote address), or "session" (MCP session ID).
	Source string `koanf:"source"` // "user" | "ip" | "session"
}

// RateLimitStoreConfig configures the backend store for rate limit counters.
// When Redis is nil, an in-memory store is used.
type RateLimitStoreConfig struct {
	Redis *RedisStoreConfig `koanf:"redis"`
}

// RedisStoreConfig configures a Redis-backed rate limit store.
type RedisStoreConfig struct {
	Addr     string `koanf:"addr"`
	Password string `koanf:"password"` // supports ${ENV_VAR} expansion
}

// TokenCountingConfig configures per-tool token counting on tool results.
// When enabled, successful tool call results are tokenized and the count is
// recorded as a Prometheus histogram (mcp_tool_result_tokens).
// When absent or enabled: false, no tokenization occurs.
type TokenCountingConfig struct {
	// Enabled activates token counting. Default: false.
	Enabled bool `koanf:"enabled"`
	// Encoding selects the tiktoken BPE encoding used for tokenization.
	// Supported values: "cl100k_base" (default), "o200k_base".
	Encoding string `koanf:"encoding"`
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
	Strategy      string              `koanf:"strategy"` // jwt|introspection|apikey|lua|js|none
	JWT           JWTAuthConfig       `koanf:"jwt"`
	Introspection IntrospectionConfig `koanf:"introspection"`
	APIKey        APIKeyAuthConfig    `koanf:"apikey"`
	Lua           LuaAuthConfig       `koanf:"lua"`
	JS            JSAuthConfig        `koanf:"js"`
	// JSAuthPool and LuaAuthPool are set programmatically for script-based strategies.
	// Not loaded from the config file. Nil is valid when no script strategy is configured.
	JSAuthPool  PoolAcquirer `koanf:"-"`
	LuaAuthPool PoolAcquirer `koanf:"-"`
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

// AppUIConfig configures an interactive HTML UI for all tools in an upstream.
// If both static and script are set, script takes precedence.
type AppUIConfig struct {
	// Static is the path to a static HTML file served as-is for every tool.
	Static string `koanf:"static"`
	// Script is the path to a JavaScript render script executed by Sobek at
	// resource-fetch time. The function receives a ctx object with toolName,
	// description, schema, env, fetch, and log, and must return an HTML string.
	Script string `koanf:"script"`
}

// ToolUIConfig is the resolved UI configuration for a single tool.
// It is computed by merging the per-upstream AppUIConfig with per-operation
// x-mcp-ui-static / x-mcp-ui-script OpenAPI overlay extensions.
// Script takes precedence over static when both are set at the same level.
type ToolUIConfig struct {
	Static string // path to static HTML file
	Script string // path to JS render script
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
	// RateLimit is the name of a top-level rate_limits entry to apply to every tool
	// from this upstream. Per-tool x-mcp-rate-limit overlay extension overrides this.
	// Empty string means no rate limiting.
	RateLimit string `koanf:"rate_limit"`
	// AppUI configures an optional interactive HTML UI for every tool in this upstream.
	// Per-tool overlay extensions (x-mcp-ui-static, x-mcp-ui-script) take precedence.
	AppUI *AppUIConfig `koanf:"app_ui"`
	// JSScriptPool is set programmatically (not from config file) to bound concurrent JS
	// script tool executions. Nil is valid when no script upstream is configured.
	JSScriptPool PoolAcquirer `koanf:"-"`
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
	Strategy                string                  `koanf:"strategy"` // bearer|api_key|oauth2_client_credentials|oauth2_user_session|lua|js|none
	Bearer                  BearerOutboundConfig    `koanf:"bearer"`
	APIKey                  APIKeyOutboundConfig    `koanf:"api_key"`
	OAuth2ClientCredentials OAuth2CCConfig          `koanf:"oauth2_client_credentials"`
	OAuth2UserSession       OAuth2UserSessionConfig `koanf:"oauth2_user_session"`
	Lua                     LuaOutboundConfig       `koanf:"lua"`
	JS                      JSOutboundConfig        `koanf:"js"`
	// Upstream is set programmatically (not from config file) to the owning upstream's name.
	// Used by the lua and js strategies to pass the upstream name to scripts.
	Upstream string `koanf:"-"`
	// JSAuthPool and LuaAuthPool are set programmatically for script-based strategies.
	// Not loaded from the config file. Nil is valid when no script strategy is configured.
	JSAuthPool  PoolAcquirer `koanf:"-"`
	LuaAuthPool PoolAcquirer `koanf:"-"`
	// OAuthTokenStore and OAuthCallbackReg are set programmatically for oauth2_user_session strategy.
	// Not loaded from the config file. Nil is valid when no oauth2_user_session strategy is configured.
	OAuthTokenStore  OAuthTokenStore        `koanf:"-"`
	OAuthCallbackReg OAuthCallbackRegistrar `koanf:"-"`
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

// OAuthToken holds per-user OAuth 2.0 token data returned by an authorization flow.
type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	TokenType    string
}

// OAuthTokenStore persists per-user OAuth tokens keyed by (userSubject, upstreamName).
// Implementations must be safe for concurrent use.
// Implemented by pkg/session/memory, pkg/session/postgres, and pkg/session/redis.
type OAuthTokenStore interface {
	Save(ctx context.Context, userSubject, upstreamName string, token *OAuthToken) error
	Load(ctx context.Context, userSubject, upstreamName string) (*OAuthToken, error)
	Delete(ctx context.Context, userSubject, upstreamName string) error
}

// OAuthCallbackRegistrar is implemented by pkg/oauth/callbackmux.Mux.
// Outbound auth providers call RegisterProvider to participate in the OAuth2 callback flow.
type OAuthCallbackRegistrar interface {
	// RegisterProvider registers an upstream's OAuth2 configuration.
	// After registration the proxy handles GET /oauth/callback/{upstreamName}.
	RegisterProvider(upstreamName, authURL, tokenURL, clientID, clientSecret string, scopes []string, redirectURL string)
	// AuthURL returns the full authorization URL for the given upstream and user subject,
	// including an HMAC-SHA256-signed state parameter.
	AuthURL(upstreamName, userSubject string) (string, error)
}

// SessionStoreConfig configures the session store backend for oauth2_user_session.
type SessionStoreConfig struct {
	// Provider selects the store backend: memory|postgres|redis.
	Provider string `koanf:"provider"`
	// HMACKey is used to sign OAuth state parameters (CSRF protection).
	// Supports ${ENV_VAR} expansion. If empty, a random key is generated on startup
	// (states will not survive proxy restarts).
	HMACKey  string                `koanf:"hmac_key"`
	Postgres PostgresSessionConfig `koanf:"postgres"`
	Redis    RedisSessionConfig    `koanf:"redis"`
}

// PostgresSessionConfig configures a PostgreSQL-backed session store.
type PostgresSessionConfig struct {
	// DSN is the PostgreSQL connection string. Supports ${ENV_VAR} expansion.
	DSN string `koanf:"dsn"`
	// EncryptionKey is a 32-byte AES-256 key encoded as 64 hex characters.
	// Supports ${ENV_VAR} expansion.
	EncryptionKey string `koanf:"encryption_key"`
}

// RedisSessionConfig configures a Redis-backed session store.
type RedisSessionConfig struct {
	// Addr is the Redis server address (host:port).
	Addr string `koanf:"addr"`
	// Password is the Redis AUTH password. Supports ${ENV_VAR} expansion.
	Password string `koanf:"password"`
	// EncryptionKey is a 32-byte AES-256 key encoded as 64 hex characters.
	// Supports ${ENV_VAR} expansion.
	EncryptionKey string `koanf:"encryption_key"`
}

// OAuth2UserSessionConfig configures the oauth2_user_session outbound auth strategy.
// It stores per-user OAuth tokens in a session store and handles token refresh.
type OAuth2UserSessionConfig struct {
	// Provider selects the OAuth2/OIDC provider.
	// Built-in shortcuts: "github" | "google" | "gitlab" | "slack"
	// Standard: "oidc" (auto-discovers endpoints from issuer_url) | "oauth2" (explicit endpoints)
	Provider string `koanf:"provider"`
	// IssuerURL is the OIDC issuer URL for provider "oidc".
	// Endpoints are discovered from <issuer_url>/.well-known/openid-configuration.
	IssuerURL string `koanf:"issuer_url"`
	// AuthURL is the OAuth2 authorization endpoint for provider "oauth2".
	AuthURL string `koanf:"auth_url"`
	// TokenURL is the OAuth2 token endpoint for provider "oauth2".
	TokenURL string `koanf:"token_url"`
	// ClientID is the OAuth2 application client ID.
	ClientID string `koanf:"client_id"`
	// ClientSecret is the OAuth2 application client secret. Supports ${ENV_VAR} expansion.
	ClientSecret string `koanf:"client_secret"`
	// Scopes is the list of OAuth2 scopes to request.
	Scopes []string `koanf:"scopes"`
	// CallbackURL is the full URL of the proxy's OAuth callback endpoint,
	// e.g. "https://mcp.example.com/oauth/callback/my-upstream".
	CallbackURL string `koanf:"callback_url"`
}
