package http

import (
	"net/http"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// UIHandlerBuilder creates a resource handler for a tool's interactive HTML UI.
// toolName, description, schema, and resourceURI are the values to embed in the handler.
// Registered from init() in pkg/upstream/http/withui; nil when UI support is not imported.
type UIHandlerBuilder func(
	cfg *config.ToolUIConfig,
	fetchClient *http.Client,
	pool config.PoolAcquirer,
	toolName string,
	description string,
	schema any,
	resourceURI string,
) (sdkmcp.ResourceHandler, error)

var (
	uiHandlerMu      sync.RWMutex
	uiHandlerBuilder UIHandlerBuilder
)

// RegisterUIHandlerBuilder registers the factory for creating tool UI resource handlers.
// Typically called from init() in pkg/upstream/http/withui.
func RegisterUIHandlerBuilder(f UIHandlerBuilder) {
	uiHandlerMu.Lock()
	defer uiHandlerMu.Unlock()
	uiHandlerBuilder = f
}

// buildUIHandler creates a UI resource handler using the registered factory.
// Returns nil, nil when no factory is registered (UI support not imported).
func buildUIHandler(
	cfg *config.ToolUIConfig,
	fetchClient *http.Client,
	pool config.PoolAcquirer,
	toolName string,
	description string,
	schema any,
	resourceURI string,
) (sdkmcp.ResourceHandler, error) {
	uiHandlerMu.RLock()
	f := uiHandlerBuilder
	uiHandlerMu.RUnlock()
	if f == nil {
		return nil, nil
	}
	return f(cfg, fetchClient, pool, toolName, description, schema, resourceURI)
}
