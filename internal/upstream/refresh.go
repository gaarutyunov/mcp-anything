package upstream

import (
	"context"

	pkghttp "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

// Snapshot is the compiled state for one upstream at a point in time.
// See pkg/upstream/http.Snapshot.
type Snapshot = pkghttp.Snapshot

// RegistryManager is implemented by the MCP Manager to receive upstream updates.
// See pkg/upstream/http.RegistryManager.
type RegistryManager = pkghttp.RegistryManager

// Refresher manages the lifecycle of background spec refresh for one upstream.
// See pkg/upstream/http.Refresher.
type Refresher = pkghttp.Refresher

// NewRefresher creates a Refresher with an initial snapshot loaded synchronously.
// See pkg/upstream/http.NewRefresher.
func NewRefresher(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig, manager RegistryManager, pools *runtime.Registry) (*Refresher, error) {
	return pkghttp.NewRefresher(ctx, cfg, naming, manager, pools)
}
