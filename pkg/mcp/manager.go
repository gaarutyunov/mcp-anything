// Package mcp bootstraps the MCP server using the official go-sdk, registers
// all generated tools from the upstream registry, and implements the tool call
// pipeline: prefix-based routing, transform, OpenAPI validation, outbound auth,
// upstream HTTP dispatch, and response conversion.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	"github.com/gaarutyunov/mcp-anything/pkg/tokencounter"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

// upstreamState holds the current per-upstream entries and spec YAML root,
// used for incremental registry rebuilds triggered by background refresh.
type upstreamState struct {
	entries      []*pkgupstream.RegistryEntry
	specYAMLRoot *yaml.Node
}

// Manager owns the MCP servers and live tool registry.
// It rebuilds and updates them on config reload.
type Manager struct {
	servers  map[string]*sdkmcp.Server // keyed by group endpoint
	registry *pkgupstream.Registry
	impl     *sdkmcp.Implementation // set on first Rebuild
	mu       sync.RWMutex

	pools   *runtime.Registry     // for injecting runtime pools into upstream configs
	counter *tokencounter.Counter // nil when token counting is disabled

	// State needed for per-upstream incremental updates (background refresh).
	groups         []config.GroupConfig
	namingCfg      *config.NamingConfig
	upstreamByName map[string]*upstreamState // latest state per upstream name
}

// NewManager creates a Manager with no active servers.
// pools is used to bound the number of concurrent script runtime instances
// across all upstream builders and auth factories.
func NewManager(pools *runtime.Registry) *Manager {
	return &Manager{
		servers:        make(map[string]*sdkmcp.Server),
		upstreamByName: make(map[string]*upstreamState),
		pools:          pools,
	}
}

// HTTPHandlers returns HTTP handlers for each group endpoint.
// Must be called after the initial Rebuild so servers are populated.
func (m *Manager) HTTPHandlers() map[string]http.Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()

	handlers := make(map[string]http.Handler, len(m.servers))
	for endpoint, srv := range m.servers {
		s := srv // capture for closure
		handlers[endpoint] = sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server { return s }, nil)
	}
	return handlers
}

// AuthRequired implements inbound.RegistryReader. Returns true (conservative
// default) when no registry is loaded or the tool is unknown.
func (m *Manager) AuthRequired(toolName string) bool {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	if reg == nil {
		return true
	}
	return reg.AuthRequired(toolName)
}

// ToolUpstreamName returns the upstream name for the given tool, or an empty
// string if unknown. Used by inbound auth middleware to select per-upstream validators.
func (m *Manager) ToolUpstreamName(toolName string) string {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.ToolUpstreamName(toolName)
}

// DispatchForGroup dispatches a tool call for the named group using the current registry.
func (m *Manager) DispatchForGroup(ctx context.Context, groupName, toolName string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	if reg == nil {
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "no config loaded"}},
		}, nil
	}
	return reg.DispatchForGroup(ctx, groupName, toolName, args)
}

// Rebuild revalidates all upstreams from the new config, builds a new registry,
// and diffs the tool sets to call AddTool/RemoveTools on each MCP server.
// Connected clients receive notifications/tools/list_changed automatically.
// If any upstream validation fails, the existing registry and servers are unchanged.
func (m *Manager) Rebuild(ctx context.Context, cfg *config.ProxyConfig) error {
	// Set Implementation on first Rebuild.
	if m.impl == nil {
		m.impl = &sdkmcp.Implementation{
			Name:    "mcp-anything",
			Version: cfg.Telemetry.ServiceVersion,
		}
	}

	// Validate that no two enabled upstreams share the same tool_prefix.
	if err := validateUpstreamPrefixes(cfg.Upstreams); err != nil {
		return fmt.Errorf("invalid upstream configuration: %w", err)
	}

	// Validate each enabled upstream with its configured timeout.
	var validatedUpstreams []*pkgupstream.ValidatedUpstream
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled {
			continue
		}

		// Inject runtime pools into a copy of the config so that pkg-level builders
		// can use bounded pools without holding a direct reference to the runtime registry.
		cfgCopy := *upCfg
		if m.pools != nil {
			cfgCopy.OutboundAuth.JSAuthPool = m.pools.JSAuth
			cfgCopy.OutboundAuth.LuaAuthPool = m.pools.LuaAuth
			cfgCopy.JSScriptPool = m.pools.JSScript
		}

		vu, vuErr := pkgupstream.Build(ctx, &cfgCopy, &cfg.Naming)
		if vuErr != nil {
			return fmt.Errorf("upstream %q: %w", upCfg.Name, vuErr)
		}
		slog.Info("upstream ready", "upstream", upCfg.Name, "tools", len(vu.Entries))

		validatedUpstreams = append(validatedUpstreams, vu)
	}

	// Build group config — synthesise a default group if none are configured.
	groups := cfg.Groups
	if len(groups) == 0 {
		allNames := make([]string, 0, len(validatedUpstreams))
		for _, vu := range validatedUpstreams {
			allNames = append(allNames, vu.Config.Name)
		}
		groups = []config.GroupConfig{{
			Name:      "default",
			Endpoint:  "/mcp",
			Upstreams: allNames,
		}}
	}

	// Build new registry (full validation; no partial updates on error).
	newRegistry, err := pkgupstream.New(validatedUpstreams, &cfg.Naming, groups)
	if err != nil {
		return fmt.Errorf("building tool registry: %w", err)
	}

	// Build token counter when enabled in config.
	var newCounter *tokencounter.Counter
	if cfg.TokenCounting.Enabled {
		newCounter, err = tokencounter.New(cfg.TokenCounting.Encoding)
		if err != nil {
			slog.Warn("token counting disabled: could not initialise tokenizer", "error", err)
		}
	}

	// Build per-upstream state map from the new registry.
	newUpstreamByName := make(map[string]*upstreamState, len(validatedUpstreams))
	for _, vu := range validatedUpstreams {
		newUpstreamByName[vu.Config.Name] = &upstreamState{
			entries:      newRegistry.EntriesForUpstream(vu.Config.Name),
			specYAMLRoot: newRegistry.SpecRootForUpstream(vu.Config.Name),
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.counter = newCounter
	m.applyRegistryLocked(newRegistry, groups)

	m.groups = groups
	m.namingCfg = &cfg.Naming
	m.upstreamByName = newUpstreamByName
	return nil
}

// applyRegistryLocked diffs the new registry against the current one and updates
// MCP servers accordingly. Must be called with m.mu held for writing.
func (m *Manager) applyRegistryLocked(newRegistry *pkgupstream.Registry, groups []config.GroupConfig) {
	for _, g := range groups {
		groupName := g.Name // capture for closure

		srv, exists := m.servers[g.Endpoint]
		if !exists {
			srv = sdkmcp.NewServer(m.impl, nil)
			m.servers[g.Endpoint] = srv
		}

		newTools := newRegistry.ToolsForGroup(g.Name)

		// Build set of new tool names for O(1) lookups.
		newToolSet := make(map[string]bool, len(newTools))
		for _, t := range newTools {
			newToolSet[t.Name] = true
		}

		// Build set of existing tool names for diffing.
		oldToolSet := make(map[string]bool)
		if m.registry != nil {
			for _, t := range m.registry.ToolsForGroup(g.Name) {
				oldToolSet[t.Name] = true
			}
		}

		// Remove tools no longer present in the new registry.
		var removedNames []string
		for name := range oldToolSet {
			if !newToolSet[name] {
				removedNames = append(removedNames, name)
			}
		}
		if len(removedNames) > 0 {
			srv.RemoveTools(removedNames...)
		}

		// Diff and update resources (MCP Apps UI handlers).
		m.applyResourcesLocked(srv, g.Name, newRegistry)

		// Add or replace tools (AddTool replaces existing tools with the same name).
		var addedNames []string
		for _, tool := range newTools {
			if !oldToolSet[tool.Name] {
				addedNames = append(addedNames, tool.Name)
			}
			t := tool       // capture
			gn := groupName // capture
			srv.AddTool(t, func(callCtx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				// Extract W3C trace context from MCP _meta.traceparent (AC-28.4).
				if tp, ok := req.Params.Meta["traceparent"].(string); ok && tp != "" {
					carrier := propagation.MapCarrier{"traceparent": tp}
					if ts, ok2 := req.Params.Meta["tracestate"].(string); ok2 && ts != "" {
						carrier["tracestate"] = ts
					}
					callCtx = otel.GetTextMapPropagator().Extract(callCtx, carrier)
				}

				// Get session ID for span attributes.
				sessionID := ""
				if req.Session != nil {
					sessionID = req.Session.ID()
				}

				spanName := "tools/call " + t.Name
				callCtx, span := otel.Tracer("mcp-anything").Start(callCtx, spanName,
					trace.WithAttributes(pkgtelemetry.ToolCallAttributes(t.Name, "tools/call", sessionID)...),
					trace.WithSpanKind(trace.SpanKindServer),
				)
				defer span.End()

				start := time.Now()

				args, parseErr := managerParseArguments(req.Params.Arguments)
				if parseErr != nil {
					span.RecordError(parseErr)
					return nil, fmt.Errorf("parsing tool arguments: %w", parseErr)
				}

				result, dispErr := m.DispatchForGroup(callCtx, gn, req.Params.Name, args)

				elapsed := time.Since(start).Seconds()
				toolAttrs := metric.WithAttributes(
					attribute.String("mcp.tool.name", t.Name),
					attribute.String("mcp.method", "tools/call"),
				)
				if pkgtelemetry.ToolCallDuration != nil {
					pkgtelemetry.ToolCallDuration.Record(callCtx, elapsed, toolAttrs)
				}
				if dispErr != nil {
					span.RecordError(dispErr)
				}

				// Record token count for successful results (no error, non-nil counter).
				if dispErr == nil && result != nil {
					m.mu.RLock()
					ctr := m.counter
					reg := m.registry
					m.mu.RUnlock()
					upstreamName := ""
					if reg != nil {
						upstreamName = reg.ToolUpstreamName(t.Name)
					}
					ctr.Record(callCtx, t.Name, upstreamName, result)
				}

				return result, dispErr
			})
		}

		if len(addedNames) > 0 || len(removedNames) > 0 {
			slog.Info("tools updated", "group", g.Name, "added", addedNames, "removed", removedNames)
		}
	}

	m.registry = newRegistry
}

// applyResourcesLocked diffs the MCP Apps UI resources for a single group and
// calls AddResource / RemoveResources on the server to keep them in sync with the
// new registry. Must be called with m.mu held for writing.
func (m *Manager) applyResourcesLocked(srv *sdkmcp.Server, groupName string, newRegistry *pkgupstream.Registry) {
	newTools := newRegistry.ToolsForGroup(groupName)

	// Build the new resource set: tool name → handler (nil entry = no UI).
	type resourceEntry struct {
		handler sdkmcp.ResourceHandler
		name    string
	}
	newResources := make(map[string]resourceEntry, len(newTools))
	for _, tool := range newTools {
		h := newRegistry.UIHandlerForTool(tool.Name)
		if h != nil {
			uri := "ui://" + tool.Name + "/app"
			newResources[uri] = resourceEntry{handler: h, name: tool.Name}
		}
	}

	// Build the old resource set for diffing.
	oldResourceURIs := make(map[string]bool)
	if m.registry != nil {
		for _, tool := range m.registry.ToolsForGroup(groupName) {
			if m.registry.UIHandlerForTool(tool.Name) != nil {
				oldResourceURIs["ui://"+tool.Name+"/app"] = true
			}
		}
	}

	// Remove resources whose tools are no longer present or no longer have a UI.
	var removedURIs []string
	for uri := range oldResourceURIs {
		if _, ok := newResources[uri]; !ok {
			removedURIs = append(removedURIs, uri)
		}
	}
	if len(removedURIs) > 0 {
		srv.RemoveResources(removedURIs...)
	}

	// Register (add or replace) resources for all tools that have a UI handler.
	for uri, re := range newResources {
		srv.AddResource(&sdkmcp.Resource{
			URI:      uri,
			Name:     re.name,
			MIMEType: "text/html",
		}, re.handler)
	}
}

// UpdateUpstream atomically replaces the tools for one upstream in the registry.
// It is called by the background Refresher after a successful spec re-fetch.
// Implements upstream.RegistryManager.
func (m *Manager) UpdateUpstream(upstreamName string, entries []*pkgupstream.RegistryEntry, specYAMLRoot *yaml.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.namingCfg == nil {
		return fmt.Errorf("manager not yet initialised (no Rebuild called)")
	}

	// Update per-upstream state.
	m.upstreamByName[upstreamName] = &upstreamState{
		entries:      entries,
		specYAMLRoot: specYAMLRoot,
	}

	return m.rebuildFromStateLocked()
}

// RemoveUpstream removes all tools for one upstream from the registry.
// It is called by the background Refresher after max_refresh_failures is exceeded.
// Implements upstream.RegistryManager.
func (m *Manager) RemoveUpstream(upstreamName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.namingCfg == nil {
		return
	}

	oldState := m.upstreamByName[upstreamName]
	delete(m.upstreamByName, upstreamName)
	if err := m.rebuildFromStateLocked(); err != nil {
		if oldState != nil {
			m.upstreamByName[upstreamName] = oldState
		}
		slog.Error("failed to remove upstream from registry", "upstream", upstreamName, "error", err)
	}
}

// rebuildFromStateLocked builds a new Registry from m.upstreamByName and applies it.
// Must be called with m.mu held for writing.
func (m *Manager) rebuildFromStateLocked() error {
	entriesByUpstream := make(map[string][]*pkgupstream.RegistryEntry, len(m.upstreamByName))
	specRootByUpstream := make(map[string]*yaml.Node, len(m.upstreamByName))
	for name, state := range m.upstreamByName {
		entriesByUpstream[name] = state.entries
		specRootByUpstream[name] = state.specYAMLRoot
	}

	newRegistry, err := pkgupstream.NewFromEntries(entriesByUpstream, specRootByUpstream, m.namingCfg, m.groups)
	if err != nil {
		return fmt.Errorf("rebuilding registry: %w", err)
	}

	m.applyRegistryLocked(newRegistry, m.groups)
	return nil
}

// validateUpstreamPrefixes returns an error if any two enabled upstreams share the same tool_prefix.
func validateUpstreamPrefixes(upstreams []config.UpstreamConfig) error {
	seen := make(map[string]string)
	for _, up := range upstreams {
		if !up.Enabled {
			continue
		}
		if prev, ok := seen[up.ToolPrefix]; ok {
			return fmt.Errorf("upstreams %q and %q share the same tool_prefix %q", prev, up.Name, up.ToolPrefix)
		}
		seen[up.ToolPrefix] = up.Name
	}
	return nil
}

// managerParseArguments unmarshals tool call arguments into a map.
func managerParseArguments(raw any) (map[string]any, error) {
	if raw == nil {
		return make(map[string]any), nil
	}
	b, ok := raw.(json.RawMessage)
	if !ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-marshalling arguments: %w", err)
		}
		b = data
	}
	if len(b) == 0 || string(b) == "null" {
		return make(map[string]any), nil
	}
	var args map[string]any
	if err := json.Unmarshal(b, &args); err != nil {
		return nil, fmt.Errorf("unmarshalling arguments: %w", err)
	}
	return args, nil
}
