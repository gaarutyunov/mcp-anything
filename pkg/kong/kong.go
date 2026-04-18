// Package kong implements an mcp-anything Kong Go PDK plugin.
// When started as a plugin server alongside Kong, it intercepts requests
// whose URL paths match a configured MCP endpoint and serves them directly,
// bypassing the upstream service configured in the Kong route.
//
// Kong plugin configuration (kong.yaml, DB-less mode):
//
//	plugins:
//	  - name: mcp-anything
//	    config:
//	      config_path: /etc/mcp-anything/config.yaml
//
// Kong environment variables required to enable the plugin server:
//
//	KONG_PLUGINS=bundled,mcp-anything
//	KONG_PLUGINSERVER_NAMES=mcp-anything
//	KONG_PLUGINSERVER_MCP_ANYTHING_START_CMD=/usr/local/bin/mcp-anything-kong
//	KONG_PLUGINSERVER_MCP_ANYTHING_QUERY_CMD=/usr/local/bin/mcp-anything-kong --dump
package kong

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	pdk "github.com/Kong/go-pdk"

	pkgconfig "github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/mcpanything"

	// Pull in all built-in inbound auth strategies.
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/introspection"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/jwt"

	// Pull in all built-in outbound auth strategies.
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/bearer"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/none"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2usersession"

	// Pull in scripting runtimes (JS and Lua for both inbound and outbound).
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/js"
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/lua"

	// Pull in all built-in upstream builders.
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/command"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http/withui"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/script"
)

// Version is the plugin version reported to Kong via the --dump introspection call.
const Version = "1.0.0"

// Priority is the plugin execution priority. Higher values run earlier in the Access phase.
const Priority = 1000

// Config holds the Kong plugin configuration. Kong unmarshals plugin config JSON
// into this struct for each new plugin configuration object.
type Config struct {
	// ConfigPath is the path to the mcp-anything YAML configuration file.
	// When omitted, the CONFIG_PATH environment variable is consulted,
	// falling back to /etc/mcp-anything/config.yaml.
	ConfigPath string `json:"config_path,omitempty"`

	initOnce sync.Once
	initErr  error
	proxy    *mcpanything.Proxy
	handlers map[string]http.Handler
}

// New returns a new empty Config. Kong calls this factory for each new plugin config object.
func New() interface{} {
	return &Config{}
}

// Access handles the Kong proxy-access phase. If the request path matches an
// MCP endpoint, the plugin serves the response directly via kong.Response.Exit
// and Kong does not proxy to the upstream service.
//
// Note: streaming responses (SSE) are buffered in full before being sent, which
// means long-lived SSE streams are not supported in this mode.
func (conf *Config) Access(kong *pdk.PDK) {
	conf.initOnce.Do(func() {
		conf.initErr = conf.provision(context.Background())
	})
	if conf.initErr != nil {
		kong.Response.Exit(http.StatusInternalServerError,
			[]byte(fmt.Sprintf(`{"error":%q}`, conf.initErr.Error())),
			map[string][]string{"Content-Type": {"application/json"}})
		return
	}

	pathWithQuery, err := kong.Request.GetPathWithQuery()
	if err != nil {
		return
	}

	path := pathWithQuery
	if i := strings.IndexByte(pathWithQuery, '?'); i >= 0 {
		path = pathWithQuery[:i]
	}

	handler := conf.findHandler(path)
	if handler == nil {
		return
	}

	method, _ := kong.Request.GetMethod()
	rawBody, _ := kong.Request.GetRawBody()
	reqHeaders, _ := kong.Request.GetHeaders(-1)

	req, err := http.NewRequestWithContext(
		context.Background(), method, "http://kong-plugin"+pathWithQuery,
		strings.NewReader(string(rawBody)),
	)
	if err != nil {
		kong.Response.Exit(http.StatusInternalServerError,
			[]byte(`{"error":"failed to build internal request"}`),
			map[string][]string{"Content-Type": {"application/json"}})
		return
	}
	for k, vals := range reqHeaders {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	rw := newResponseCapture()
	handler.ServeHTTP(rw, req)

	kong.Response.Exit(rw.code, rw.buf, map[string][]string(rw.hdr))
}

// findHandler returns the http.Handler matching path, or nil if no handler matches.
func (conf *Config) findHandler(path string) http.Handler {
	if h, ok := conf.handlers[path]; ok {
		return h
	}
	for endpoint, h := range conf.handlers {
		if strings.HasPrefix(path, endpoint+"/") {
			return h
		}
	}
	return nil
}

// provision initialises the mcp-anything proxy. Called at most once per Config via sync.Once.
func (conf *Config) provision(ctx context.Context) error {
	var (
		path string
		cfg  *pkgconfig.ProxyConfig
		err  error
	)
	if conf.ConfigPath != "" {
		path = conf.ConfigPath
		cfg, err = pkgconfig.Load(path)
		if err != nil {
			return fmt.Errorf("mcpanything kong: loading config from %q: %w", path, err)
		}
	} else {
		path, cfg, err = mcpanything.LoadConfig()
		if err != nil {
			return fmt.Errorf("mcpanything kong: loading config: %w", err)
		}
	}

	proxy, err := mcpanything.New(ctx, cfg, mcpanything.WithConfigPath(path))
	if err != nil {
		return fmt.Errorf("mcpanything kong: creating proxy: %w", err)
	}
	proxy.StartBackground(ctx)

	conf.proxy = proxy
	conf.handlers = proxy.Handlers()
	return nil
}

// responseCapture buffers the HTTP response for forwarding to kong.Response.Exit.
type responseCapture struct {
	code int
	buf  []byte
	hdr  http.Header
}

func newResponseCapture() *responseCapture {
	return &responseCapture{
		code: http.StatusOK,
		hdr:  make(http.Header),
	}
}

func (r *responseCapture) Header() http.Header  { return r.hdr }
func (r *responseCapture) WriteHeader(code int) { r.code = code }
func (r *responseCapture) Write(b []byte) (int, error) {
	r.buf = append(r.buf, b...)
	return len(b), nil
}
