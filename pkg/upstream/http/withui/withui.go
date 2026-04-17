// Package withui registers a Sobek-backed UI handler builder with the HTTP upstream.
//
// Import this package with a blank identifier to enable JavaScript render-script
// support for tool UIs (x-mcp-ui extensions with a script: path):
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http/withui"
//
// When this package is not imported, tools with a UIConfig are still built but
// their UIHandler is left nil (no UI resource is registered for those tools).
// Static HTML UIs (cfg.Static) are also handled through this factory.
package withui

import (
	nethttp "net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgui "github.com/gaarutyunov/mcp-anything/pkg/ui"
	pkghttp "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
)

func init() {
	pkghttp.RegisterUIHandlerBuilder(func(
		cfg *config.ToolUISpec,
		fetchClient *nethttp.Client,
		pool config.PoolAcquirer,
		toolName string,
		description string,
		schema any,
		resourceURI string,
	) (sdkmcp.ResourceHandler, error) {
		loader, err := pkgui.New(cfg, nil, fetchClient, pool)
		if err != nil {
			return nil, err
		}
		return loader.ResourceHandler(toolName, description, schema, resourceURI), nil
	})
}
