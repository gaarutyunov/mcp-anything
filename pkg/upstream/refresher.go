package upstream

import (
	"context"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

// RegistryManager is implemented by the MCP Manager to receive upstream updates
// from background refresh goroutines.
type RegistryManager interface {
	// UpdateUpstream atomically replaces the tools for one upstream in the registry.
	UpdateUpstream(upstreamName string, entries []*RegistryEntry, specYAMLRoot *yaml.Node) error
	// RemoveUpstream removes all tools for one upstream from the registry.
	RemoveUpstream(upstreamName string)
}

// Refresher manages the lifecycle of background spec refresh for one upstream.
// It extends HealthChecker with a lifecycle Start method.
type Refresher interface {
	HealthChecker
	// Start launches the background refresh goroutine. It exits when ctx is cancelled.
	Start(ctx context.Context)
}

// RefresherFactory creates a Refresher for an upstream with a URL-based spec and
// a positive refresh interval. Typically registered from init() in pkg/upstream/http.
type RefresherFactory func(
	ctx context.Context,
	cfg *config.UpstreamConfig,
	naming *config.NamingConfig,
	manager RegistryManager,
	pools *runtime.Registry,
) (Refresher, error)

var (
	refresherFactoryMu sync.RWMutex
	refresherFactory   RefresherFactory
)

// RegisterRefresherFactory registers the factory used to create spec Refreshers.
// Typically called from init() in pkg/upstream/http.
func RegisterRefresherFactory(f RefresherFactory) {
	refresherFactoryMu.Lock()
	defer refresherFactoryMu.Unlock()
	refresherFactory = f
}

// NewRefresher creates a Refresher using the registered factory.
// Returns an error if no factory has been registered — import pkg/upstream/http to register one.
func NewRefresher(
	ctx context.Context,
	cfg *config.UpstreamConfig,
	naming *config.NamingConfig,
	manager RegistryManager,
	pools *runtime.Registry,
) (Refresher, error) {
	refresherFactoryMu.RLock()
	f := refresherFactory
	refresherFactoryMu.RUnlock()
	if f == nil {
		return nil, fmt.Errorf(
			"no refresher factory registered for upstream %q — import pkg/upstream/http to enable background spec refresh",
			cfg.Name,
		)
	}
	return f(ctx, cfg, naming, manager, pools)
}
