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

// NamingConfig controls how tool names are generated.
type NamingConfig struct {
	Separator string `koanf:"separator"`
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
}

// OpenAPISourceConfig points to an OpenAPI spec file.
type OpenAPISourceConfig struct {
	Source  string `koanf:"source"`
	Version string `koanf:"version"`
}
