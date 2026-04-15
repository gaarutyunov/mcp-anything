// Package configgen registry: allows packages to register their own configgen
// section factories under specific keys, following the same init()-based
// registration pattern used by pkg/config.RegisterProxySection and friends.
package configgen

import (
	"context"
	"fmt"
	"sync"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// ProxySectionFactory generates an additional top-level proxy config section
// from a MCPProxy CRD and its selected upstreams. The returned value is merged
// into the generated proxy config YAML at cfg.Extensions[key].
//
// Typically called from init() in packages that define their own proxy-level
// operator config generation.
type ProxySectionFactory func(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) (interface{}, error)

// UpstreamSectionFactory generates an additional upstream config section from a
// MCPUpstream CRD. The returned value is merged into the generated upstream
// config YAML at upstreamCfg.Extensions[key].
//
// Typically called from init() in packages that define their own upstream-level
// operator config generation.
type UpstreamSectionFactory func(ctx context.Context, upstream *v1alpha1.MCPUpstream) (interface{}, error)

var (
	genMu               sync.RWMutex
	proxyGenRegistry    = map[string]ProxySectionFactory{}
	upstreamGenRegistry = map[string]UpstreamSectionFactory{}
)

// RegisterProxySectionFactory registers a factory for a top-level proxy config
// section that is injected by the operator when generating proxy config YAML.
// key is the top-level YAML key the returned value will be set at.
// Panics if a factory is already registered under the same key.
func RegisterProxySectionFactory(key string, f ProxySectionFactory) {
	genMu.Lock()
	defer genMu.Unlock()
	if _, exists := proxyGenRegistry[key]; exists {
		panic(fmt.Sprintf("configgen: proxy section factory %q is already registered — import conflict", key))
	}
	proxyGenRegistry[key] = f
}

// RegisterUpstreamSectionFactory registers a factory for a per-upstream config
// section that is injected by the operator when generating upstream config YAML.
// key is the YAML key within the upstream object the returned value will be set at.
// Panics if a factory is already registered under the same key.
func RegisterUpstreamSectionFactory(key string, f UpstreamSectionFactory) {
	genMu.Lock()
	defer genMu.Unlock()
	if _, exists := upstreamGenRegistry[key]; exists {
		panic(fmt.Sprintf("configgen: upstream section factory %q is already registered — import conflict", key))
	}
	upstreamGenRegistry[key] = f
}

// applyProxyExtensions calls all registered proxy section factories and merges
// their output into the provided extensions map.
func applyProxyExtensions(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream, exts map[string]interface{}) error {
	genMu.RLock()
	snapshot := make(map[string]ProxySectionFactory, len(proxyGenRegistry))
	for k, f := range proxyGenRegistry {
		snapshot[k] = f
	}
	genMu.RUnlock()

	for key, f := range snapshot {
		v, err := f(ctx, proxy, upstreams)
		if err != nil {
			return fmt.Errorf("proxy section factory %q: %w", key, err)
		}
		exts[key] = v
	}
	return nil
}

// applyUpstreamExtensions calls all registered upstream section factories and
// merges their output into the provided extensions map for a single upstream.
func applyUpstreamExtensions(ctx context.Context, upstream *v1alpha1.MCPUpstream, exts map[string]interface{}) error {
	genMu.RLock()
	snapshot := make(map[string]UpstreamSectionFactory, len(upstreamGenRegistry))
	for k, f := range upstreamGenRegistry {
		snapshot[k] = f
	}
	genMu.RUnlock()

	for key, f := range snapshot {
		v, err := f(ctx, upstream)
		if err != nil {
			return fmt.Errorf("upstream section factory %q: %w", key, err)
		}
		exts[key] = v
	}
	return nil
}
