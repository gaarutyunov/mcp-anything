// Package config defines the configuration structs and loading logic for mcp-anything.
package config

import "time"

// ProxyConfig is the top-level configuration struct.
type ProxyConfig struct {
	Server    ServerConfig     `koanf:"server"`
	Telemetry TelemetryConfig  `koanf:"telemetry"`
	Naming    NamingConfig     `koanf:"naming"`
	Upstreams []UpstreamConfig `koanf:"upstreams"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port            int           `koanf:"port"`
	ReadTimeout     time.Duration `koanf:"read_timeout"`
	WriteTimeout    time.Duration `koanf:"write_timeout"`
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	MaxRequestBody  string        `koanf:"max_request_body"`
}

// TelemetryConfig holds observability settings.
type TelemetryConfig struct {
	ServiceName    string `koanf:"service_name"`
	ServiceVersion string `koanf:"service_version"`
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

// UpstreamConfig describes a single upstream HTTP API.
type UpstreamConfig struct {
	Name       string              `koanf:"name"`
	Enabled    bool                `koanf:"enabled"`
	ToolPrefix string              `koanf:"tool_prefix"`
	BaseURL    string              `koanf:"base_url"`
	Timeout    time.Duration       `koanf:"timeout"`
	Headers    map[string]string   `koanf:"headers"`
	OpenAPI    OpenAPISourceConfig `koanf:"openapi"`
	Overlay    *OverlayConfig      `koanf:"overlay"`
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
