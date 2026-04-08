package config

import pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"

// Type aliases pointing to pkg/config — transparent to all callers; no behavior change.
// See pkg/config for full documentation of each type.

// ProxyConfig is the top-level configuration struct. See pkg/config.ProxyConfig.
type ProxyConfig = pkgconfig.ProxyConfig

// RuntimeConfig controls bounded pools for concurrent script runtime instances. See pkg/config.RuntimeConfig.
type RuntimeConfig = pkgconfig.RuntimeConfig

// JSRuntimeConfig configures Sobek JavaScript runtime pool sizes. See pkg/config.JSRuntimeConfig.
type JSRuntimeConfig = pkgconfig.JSRuntimeConfig

// LuaRuntimeConfig configures gopher-lua runtime pool sizes. See pkg/config.LuaRuntimeConfig.
type LuaRuntimeConfig = pkgconfig.LuaRuntimeConfig

// GroupConfig configures a named group of upstreams exposed at a single MCP endpoint. See pkg/config.GroupConfig.
type GroupConfig = pkgconfig.GroupConfig

// InboundAuthConfig controls how inbound MCP clients are authenticated. See pkg/config.InboundAuthConfig.
type InboundAuthConfig = pkgconfig.InboundAuthConfig

// LuaAuthConfig configures inbound token validation via a Lua script. See pkg/config.LuaAuthConfig.
type LuaAuthConfig = pkgconfig.LuaAuthConfig

// JSAuthConfig configures inbound token validation via a JavaScript script. See pkg/config.JSAuthConfig.
type JSAuthConfig = pkgconfig.JSAuthConfig

// JWTAuthConfig configures JWT Bearer token validation via OIDC/JWKS. See pkg/config.JWTAuthConfig.
type JWTAuthConfig = pkgconfig.JWTAuthConfig

// IntrospectionConfig configures token introspection via an OIDC server. See pkg/config.IntrospectionConfig.
type IntrospectionConfig = pkgconfig.IntrospectionConfig

// APIKeyAuthConfig configures API key validation from a request header. See pkg/config.APIKeyAuthConfig.
type APIKeyAuthConfig = pkgconfig.APIKeyAuthConfig

// ServerTLSConfig configures inbound TLS termination for the MCP server. See pkg/config.ServerTLSConfig.
type ServerTLSConfig = pkgconfig.ServerTLSConfig

// TLSConfig configures TLS for an outbound upstream connection. See pkg/config.TLSConfig.
type TLSConfig = pkgconfig.TLSConfig

// TransportConfig configures the HTTP transport per upstream. See pkg/config.TransportConfig.
type TransportConfig = pkgconfig.TransportConfig

// ServerConfig holds HTTP server settings. See pkg/config.ServerConfig.
type ServerConfig = pkgconfig.ServerConfig

// TelemetryConfig holds observability settings. See pkg/config.TelemetryConfig.
type TelemetryConfig = pkgconfig.TelemetryConfig

// SlugRulesConfig controls which slug transformations are applied. See pkg/config.SlugRulesConfig.
type SlugRulesConfig = pkgconfig.SlugRulesConfig

// NamingConfig controls how tool names are generated. See pkg/config.NamingConfig.
type NamingConfig = pkgconfig.NamingConfig

// ValidationConfig controls runtime request and response validation. See pkg/config.ValidationConfig.
type ValidationConfig = pkgconfig.ValidationConfig

// UpstreamConfig describes a single upstream. See pkg/config.UpstreamConfig.
type UpstreamConfig = pkgconfig.UpstreamConfig

// CommandConfig defines a single command-backed MCP tool. See pkg/config.CommandConfig.
type CommandConfig = pkgconfig.CommandConfig

// ScriptConfig defines a single JavaScript-backed MCP tool. See pkg/config.ScriptConfig.
type ScriptConfig = pkgconfig.ScriptConfig

// CommandInputSchema is the JSON Schema definition for a command tool's input parameters. See pkg/config.CommandInputSchema.
type CommandInputSchema = pkgconfig.CommandInputSchema

// CommandSchemaProperty describes a single property in a command tool's input schema. See pkg/config.CommandSchemaProperty.
type CommandSchemaProperty = pkgconfig.CommandSchemaProperty

// OutboundAuthConfig controls how the proxy authenticates outbound requests. See pkg/config.OutboundAuthConfig.
type OutboundAuthConfig = pkgconfig.OutboundAuthConfig

// LuaOutboundConfig configures outbound credential acquisition via a Lua script. See pkg/config.LuaOutboundConfig.
type LuaOutboundConfig = pkgconfig.LuaOutboundConfig

// JSOutboundConfig configures outbound credential acquisition via a JavaScript script. See pkg/config.JSOutboundConfig.
type JSOutboundConfig = pkgconfig.JSOutboundConfig

// BearerOutboundConfig configures static Bearer token injection. See pkg/config.BearerOutboundConfig.
type BearerOutboundConfig = pkgconfig.BearerOutboundConfig

// APIKeyOutboundConfig configures API key header injection. See pkg/config.APIKeyOutboundConfig.
type APIKeyOutboundConfig = pkgconfig.APIKeyOutboundConfig

// OAuth2CCConfig configures OAuth2 client credentials flow. See pkg/config.OAuth2CCConfig.
type OAuth2CCConfig = pkgconfig.OAuth2CCConfig

// OpenAPISourceConfig points to an OpenAPI spec file or URL. See pkg/config.OpenAPISourceConfig.
type OpenAPISourceConfig = pkgconfig.OpenAPISourceConfig

// OverlayConfig points to an OpenAPI Overlay document. See pkg/config.OverlayConfig.
type OverlayConfig = pkgconfig.OverlayConfig
