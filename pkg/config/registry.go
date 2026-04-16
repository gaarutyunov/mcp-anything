// Package config dynamic registry: allows packages to register their own
// config sections under specific keys, following the same init()-based
// registration pattern used by auth strategies, cache providers, etc.
package config

import (
	"fmt"
	"sync"

	"github.com/knadh/koanf/v2"
)

// SectionFactory creates a typed config value from a koanf subtree.
// The koanf instance passed to the factory is scoped to the registered key,
// so the factory can call k.Unmarshal("", &myStruct) directly.
//
// For proxy sections the subtree is k.Cut(registeredKey).
// For upstream sections the subtree is built from the raw upstream map value.
//
// Factories are called during every Load (including hot-reloads).
type SectionFactory func(k *koanf.Koanf) (any, error)

var (
	sectionMu        sync.RWMutex
	proxyRegistry    = map[string]SectionFactory{}
	upstreamRegistry = map[string]SectionFactory{}
)

// RegisterProxySection registers a factory for a top-level proxy config section.
// key is the koanf path in the proxy config (e.g. "tool_search", "session_store").
// The factory is called with a koanf subtree scoped to that key on every Load.
// Typically called from init() in packages that define their own proxy-level config.
// Panics if a factory is already registered under the same key.
func RegisterProxySection(key string, f SectionFactory) {
	sectionMu.Lock()
	defer sectionMu.Unlock()
	if _, exists := proxyRegistry[key]; exists {
		panic(fmt.Sprintf("config: proxy section %q is already registered — import conflict", key))
	}
	proxyRegistry[key] = f
}

// RegisterUpstreamSection registers a factory for a per-upstream config section.
// key is the sub-key within each upstream config object in the YAML.
// The factory is called once per upstream per Load for upstreams that contain the key.
// Typically called from init() in packages that define upstream-level config.
// Panics if a factory is already registered under the same key.
func RegisterUpstreamSection(key string, f SectionFactory) {
	sectionMu.Lock()
	defer sectionMu.Unlock()
	if _, exists := upstreamRegistry[key]; exists {
		panic(fmt.Sprintf("config: upstream section %q is already registered — import conflict", key))
	}
	upstreamRegistry[key] = f
}

// DynamicConfig wraps ProxyConfig and adds dynamically registered extension sections.
// The embedded ProxyConfig provides all standard fields; extension sections are accessed
// via GetProxySection and GetUpstreamSection.
type DynamicConfig struct {
	ProxyConfig

	// proxySections holds extension config values keyed by their registered key.
	proxySections map[string]any

	// upstreamSections holds per-upstream extension config values.
	// upstreamSections[i][key] gives the typed value for upstream at index i.
	upstreamSections []map[string]any
}

// GetProxySection returns the typed value for the given registered proxy section key.
// Returns (zero, false) when the key is not registered or was absent from the config file.
func GetProxySection[T any](d *DynamicConfig, key string) (T, bool) {
	if d == nil || d.proxySections == nil {
		var zero T
		return zero, false
	}
	v, ok := d.proxySections[key]
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// GetUpstreamSection returns the typed value for the given registered upstream section key
// for the upstream at the specified index in DynamicConfig.Upstreams.
// Returns (zero, false) when the key is not registered, the index is out of range,
// or the section was absent from that upstream's config.
func GetUpstreamSection[T any](d *DynamicConfig, upstreamIndex int, key string) (T, bool) {
	if d == nil || upstreamIndex < 0 || upstreamIndex >= len(d.upstreamSections) {
		var zero T
		return zero, false
	}
	m := d.upstreamSections[upstreamIndex]
	if m == nil {
		var zero T
		return zero, false
	}
	v, ok := m[key]
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// loadExtensions populates the extension sections of d from the koanf instance and
// raw upstream slice. Called from Load after the core ProxyConfig is unmarshalled.
func loadExtensions(d *DynamicConfig, k *koanf.Koanf, rawUpstreams []interface{}) error {
	// Snapshot both registries under a single read lock.
	sectionMu.RLock()
	proxyCopy := make(map[string]SectionFactory, len(proxyRegistry))
	for key, f := range proxyRegistry {
		proxyCopy[key] = f
	}
	upstreamCopy := make(map[string]SectionFactory, len(upstreamRegistry))
	for key, f := range upstreamRegistry {
		upstreamCopy[key] = f
	}
	sectionMu.RUnlock()

	// ── Proxy-level sections ──────────────────────────────────────────────────────
	d.proxySections = make(map[string]any, len(proxyCopy))
	for key, f := range proxyCopy {
		if !k.Exists(key) {
			continue
		}
		sub := k.Cut(key)
		v, err := f(sub)
		if err != nil {
			return fmt.Errorf("proxy section %q: %w", key, err)
		}
		d.proxySections[key] = v
	}

	// ── Upstream-level sections ───────────────────────────────────────────────────
	if len(upstreamCopy) == 0 {
		return nil
	}

	d.upstreamSections = make([]map[string]any, len(rawUpstreams))
	for i, rawItem := range rawUpstreams {
		rawUp, _ := rawItem.(map[string]interface{})
		if rawUp == nil {
			continue
		}
		secs := make(map[string]any, len(upstreamCopy))
		for key, f := range upstreamCopy {
			rawVal, ok := rawUp[key]
			if !ok {
				continue
			}
			rawMap, _ := rawVal.(map[string]interface{})
			if rawMap == nil {
				continue
			}
			// Build a scoped koanf instance from the raw map so the factory can
			// use the standard k.Unmarshal("", &target) pattern.
			sub := koanf.New(".")
			if err := sub.Load(&mapProvider{m: rawMap}, nil); err != nil {
				return fmt.Errorf("upstream[%d] section %q: load: %w", i, key, err)
			}
			v, err := f(sub)
			if err != nil {
				return fmt.Errorf("upstream[%d] section %q: %w", i, key, err)
			}
			secs[key] = v
		}
		d.upstreamSections[i] = secs
	}
	return nil
}

// mapProvider is a minimal koanf.Provider that serves an existing
// map[string]interface{}. Used to create scoped koanf instances for
// upstream extension sections without adding external dependencies.
type mapProvider struct {
	m map[string]interface{}
}

// Read implements koanf.Provider by returning the underlying map.
func (p *mapProvider) Read() (map[string]interface{}, error) {
	return p.m, nil
}

// ReadBytes implements koanf.Provider. For map-based providers the raw
// bytes representation is not available; koanf uses Read() instead.
func (p *mapProvider) ReadBytes() ([]byte, error) {
	return nil, nil
}
