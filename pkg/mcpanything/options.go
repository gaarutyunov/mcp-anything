package mcpanything

import "log/slog"

// Option configures a Proxy.
type Option func(*Proxy) error

// WithConfigPath sets the path to the configuration file.
func WithConfigPath(path string) Option {
	return func(p *Proxy) error {
		p.configPath = path
		return nil
	}
}

// WithLogger sets a custom logger for the proxy.
func WithLogger(logger *slog.Logger) Option {
	return func(p *Proxy) error {
		p.logger = logger
		return nil
	}
}
