// Package caddy implements an mcp-anything Caddy HTTP middleware module.
// Import this package and register it with caddy.RegisterModule to expose
// mcp-anything as a native Caddy handler directive ("mcpanything").
//
// Caddyfile example:
//
//	{
//	    order mcpanything before respond
//	}
//
//	:8080 {
//	    handle /mcp* {
//	        mcpanything {
//	            config_path /etc/mcp-anything/config.yaml
//	        }
//	    }
//	}
package caddy

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/mcpanything"

	// Pull in all built-in auth and upstream strategies so that the Caddy
	// binary supports every feature without requiring explicit blank imports
	// from the operator.
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/all"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/all"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/all"
)

func init() {
	caddy.RegisterModule(MCPAnything{})
	httpcaddyfile.RegisterHandlerDirective("mcpanything", parseCaddyfile)
}

// MCPAnything is a Caddy HTTP handler module that embeds a full mcp-anything
// proxy as middleware. It dispatches requests whose paths match a configured
// MCP group endpoint to the corresponding MCP handler, and forwards all other
// requests to the next handler in the chain.
type MCPAnything struct {
	// ConfigPath is the path to the mcp-anything YAML configuration file.
	// When omitted, the CONFIG_PATH environment variable is consulted,
	// falling back to /etc/mcp-anything/config.yaml.
	ConfigPath string `json:"config_path,omitempty"`

	proxy    *mcpanything.Proxy
	handlers map[string]http.Handler
}

// CaddyModule returns the Caddy module information.
func (MCPAnything) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.mcpanything",
		New: func() caddy.Module { return new(MCPAnything) },
	}
}

// Provision initialises the mcp-anything proxy. Called by Caddy before the
// first request is served, and again on every config reload.
func (m *MCPAnything) Provision(ctx caddy.Context) error {
	var (
		path string
		cfg  *pkgconfig.ProxyConfig
		err  error
	)
	if m.ConfigPath != "" {
		path = m.ConfigPath
		cfg, err = pkgconfig.Load(path)
		if err != nil {
			return fmt.Errorf("mcpanything: loading config from %q: %w", path, err)
		}
	} else {
		path, cfg, err = mcpanything.LoadConfig()
		if err != nil {
			return fmt.Errorf("mcpanything: loading config: %w", err)
		}
	}

	proxy, err := mcpanything.New(ctx, cfg, mcpanything.WithConfigPath(path))
	if err != nil {
		return fmt.Errorf("mcpanything: creating proxy: %w", err)
	}
	proxy.StartBackground(ctx)

	m.proxy = proxy
	m.handlers = proxy.Handlers()
	return nil
}

// Validate checks the module configuration after Provision.
func (m *MCPAnything) Validate() error {
	if m.proxy == nil {
		return fmt.Errorf("mcpanything: proxy not provisioned")
	}
	return nil
}

// ServeHTTP dispatches the incoming request to the MCP group handler whose
// endpoint path is a prefix of r.URL.Path. Requests that do not match any
// endpoint are forwarded to next.
func (m *MCPAnything) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	path := r.URL.Path
	// Exact-match check first.
	if h, ok := m.handlers[path]; ok {
		h.ServeHTTP(w, r)
		return nil
	}
	// Prefix-match: forward any sub-path under the endpoint to its handler.
	for endpoint, h := range m.handlers {
		if strings.HasPrefix(path, endpoint+"/") {
			h.ServeHTTP(w, r)
			return nil
		}
	}
	return next.ServeHTTP(w, r)
}

// Cleanup shuts down the proxy when Caddy unloads this module (config reload
// or server shutdown).
func (m *MCPAnything) Cleanup() error {
	if m.proxy != nil {
		return m.proxy.Shutdown(context.Background())
	}
	return nil
}

// UnmarshalCaddyfile parses the Caddyfile block:
//
//	mcpanything {
//	    config_path /etc/mcp-anything/config.yaml
//	}
func (m *MCPAnything) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "config_path":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.ConfigPath = d.Val()
			default:
				return d.Errf("unrecognised mcpanything option: %s", d.Val())
			}
		}
	}
	return nil
}

// parseCaddyfile wires UnmarshalCaddyfile into the httpcaddyfile handler
// directive registry.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	m := new(MCPAnything)
	if err := m.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return m, nil
}

// Interface guards — compile-time verification that MCPAnything implements
// the required Caddy interfaces.
var (
	_ caddy.Module                = (*MCPAnything)(nil)
	_ caddy.Provisioner           = (*MCPAnything)(nil)
	_ caddy.Validator             = (*MCPAnything)(nil)
	_ caddy.CleanerUpper          = (*MCPAnything)(nil)
	_ caddyhttp.MiddlewareHandler = (*MCPAnything)(nil)
	_ caddyfile.Unmarshaler       = (*MCPAnything)(nil)
)
