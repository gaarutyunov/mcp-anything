package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	upstreampkg "github.com/gaarutyunov/mcp-anything/internal/upstream"
)

// Manager owns the MCP servers and live tool registry.
// It rebuilds and updates them on config reload.
type Manager struct {
	servers  map[string]*sdkmcp.Server // keyed by group endpoint
	registry *upstreampkg.Registry
	impl     *sdkmcp.Implementation // set on first Rebuild
	mu       sync.RWMutex
}

// NewManager creates a Manager with no active servers.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*sdkmcp.Server),
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
	var validatedUpstreams []*upstreampkg.ValidatedUpstream
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled {
			continue
		}

		valCtx, valCancel := context.WithTimeout(ctx, upCfg.StartupValidationTimeout)
		tools, specYAMLRoot, valErr := openapi.ValidateUpstream(valCtx, upCfg, &cfg.Naming)
		valCancel()
		if valErr != nil {
			return fmt.Errorf("upstream %q validation failed: %w", upCfg.Name, valErr)
		}
		slog.Info("validated tools", "upstream", upCfg.Name, "count", len(tools))

		provider, provErr := outbound.NewRegistry().New(ctx, &upCfg.OutboundAuth)
		if provErr != nil {
			return fmt.Errorf("build outbound auth for upstream %q: %w", upCfg.Name, provErr)
		}

		validatedUpstreams = append(validatedUpstreams, &upstreampkg.ValidatedUpstream{
			Config:       upCfg,
			Tools:        tools,
			Provider:     provider,
			SpecYAMLRoot: specYAMLRoot,
		})
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
	newRegistry, err := upstreampkg.New(validatedUpstreams, &cfg.Naming, groups)
	if err != nil {
		return fmt.Errorf("building tool registry: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

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

		// Add or replace tools (AddTool replaces existing tools with the same name).
		var addedNames []string
		for _, tool := range newTools {
			if !oldToolSet[tool.Name] {
				addedNames = append(addedNames, tool.Name)
			}
			t := tool       // capture
			gn := groupName // capture
			srv.AddTool(t, func(callCtx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				args, parseErr := managerParseArguments(req.Params.Arguments)
				if parseErr != nil {
					return nil, fmt.Errorf("parsing tool arguments: %w", parseErr)
				}
				return m.DispatchForGroup(callCtx, gn, req.Params.Name, args)
			})
		}

		if len(addedNames) > 0 || len(removedNames) > 0 {
			slog.Info("config reloaded tools", "group", g.Name, "added", addedNames, "removed", removedNames)
		}
	}

	m.registry = newRegistry
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
