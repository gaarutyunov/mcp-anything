package upstream

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/speakeasy-api/jsonpath/pkg/jsonpath"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// Upstream holds the per-upstream HTTP routing state.
type Upstream struct {
	Name       string
	ToolPrefix string
	BaseURL    string
	Client     *http.Client
}

// RegistryEntry associates a prefixed tool name with its upstream and runtime state.
// Transforms and Validator are typed as any to avoid coupling this package to the
// transform and openapi packages; HTTP builders set them to *transform.CompiledTransforms
// and *openapi.Validator respectively.
type RegistryEntry struct {
	PrefixedName   string
	OriginalName   string
	Upstream       *Upstream
	MCPTool        *sdkmcp.Tool
	Transforms     any    // set by builders; *transform.CompiledTransforms for HTTP tools
	ResponseFormat string // x-mcp-response-format value; default "json"
	AuthRequired   bool   // x-mcp-auth-required; default true
	Method         string // HTTP method (empty for command/script tools)
	PathTemplate   string // e.g. /pets/{petId} (empty for command/script tools)
	Validator      any    // set by builders; *openapi.Validator for HTTP tools
	ValidationCfg  config.ValidationConfig
	OperationNode  *yaml.Node   // YAML node for JSONPath group filter evaluation (nil for command/script tools)
	Executor       ToolExecutor // set by builders; dispatches tool execution
	// RateLimit is the name of a top-level rate_limits entry applied to this tool.
	// Empty string means no rate limiting. Set by builders from the x-mcp-rate-limit
	// overlay extension (per-tool override) or upstream-level rate_limit config (default).
	RateLimit string
	// UIHandler is the MCP resource handler that serves the tool's interactive HTML UI.
	// Nil when no UI is configured. Set by HTTP builders when a ToolUIConfig is resolved.
	UIHandler sdkmcp.ResourceHandler
}

// ValidatedUpstream is the result of validating a single upstream configuration.
// Entries holds the pre-built RegistryEntry objects for all tools in this upstream.
// SpecYAMLRoot is the post-overlay YAML root used for JSONPath group filter evaluation
// (nil for non-HTTP upstreams such as type: command or type: script).
type ValidatedUpstream struct {
	Config       *config.UpstreamConfig
	Entries      []*RegistryEntry
	SpecYAMLRoot *yaml.Node
}

// groupData holds pre-computed membership for a single tool group.
type groupData struct {
	toolList []*sdkmcp.Tool
	toolSet  map[string]bool // set of prefixed tool names in this group
}

// Registry maps prefixed tool names to their upstream and compiled tool state.
// It is immutable after construction and safe for concurrent reads.
type Registry struct {
	byPrefixedName map[string]*RegistryEntry
	byPrefix       map[string]*Upstream
	byUpstreamName map[string][]*RegistryEntry // upstream name → ordered entries
	specRootByName map[string]*yaml.Node       // upstream name → post-overlay YAML root
	toolList       []*sdkmcp.Tool
	separator      string
	groups         map[string]*groupData // group name → pre-computed membership
}

// New builds a Registry from all validated upstreams and group configurations.
// groups is the list of tool groups to build; if empty, a default group at /mcp with all
// upstreams is assumed to have been created by the caller.
// Returns an error if any tool prefix is shared (AC-07.5) or if a group filter is invalid.
func New(upstreams []*ValidatedUpstream, naming *config.NamingConfig, groups []config.GroupConfig) (*Registry, error) {
	r := &Registry{
		byPrefixedName: make(map[string]*RegistryEntry),
		byPrefix:       make(map[string]*Upstream),
		byUpstreamName: make(map[string][]*RegistryEntry),
		specRootByName: make(map[string]*yaml.Node),
		separator:      naming.Separator,
		groups:         make(map[string]*groupData),
	}

	// Check for shared prefixes — AC-07.5: fatal error.
	prefixToName := make(map[string]string, len(upstreams))
	for _, vu := range upstreams {
		prefix := vu.Config.ToolPrefix
		if prev, ok := prefixToName[prefix]; ok {
			return nil, fmt.Errorf("upstreams %q and %q share the same tool_prefix %q", prev, vu.Config.Name, prefix)
		}
		prefixToName[prefix] = vu.Config.Name
	}

	for _, vu := range upstreams {
		r.specRootByName[vu.Config.Name] = vu.SpecYAMLRoot

		for _, entry := range vu.Entries {
			if existing, ok := r.byPrefixedName[entry.PrefixedName]; ok {
				return nil, fmt.Errorf("tool name conflict %q between upstreams %q and %q",
					entry.PrefixedName, existing.Upstream.Name, vu.Config.Name)
			}
			r.byPrefixedName[entry.PrefixedName] = entry
			r.byUpstreamName[vu.Config.Name] = append(r.byUpstreamName[vu.Config.Name], entry)
			if entry.Upstream != nil {
				r.byPrefix[entry.Upstream.ToolPrefix] = entry.Upstream
			}
			r.toolList = append(r.toolList, entry.MCPTool)
		}
	}

	// Validate all group filter expressions upfront — invalid filter is a fatal startup error.
	compiledFilters := make(map[string]*jsonpath.JSONPath, len(groups))
	for _, g := range groups {
		if g.Filter == "" {
			continue
		}
		jp, err := jsonpath.NewPath(g.Filter)
		if err != nil {
			return nil, fmt.Errorf("group %q has invalid JSONPath filter %q: %w", g.Name, g.Filter, err)
		}
		compiledFilters[g.Name] = jp
	}

	// Build group membership by evaluating filters at build time.
	for _, g := range groups {
		gd := &groupData{
			toolSet: make(map[string]bool),
		}
		upstreamSet := make(map[string]bool, len(g.Upstreams))
		for _, name := range g.Upstreams {
			upstreamSet[name] = true
		}

		jp := compiledFilters[g.Name] // nil if no filter

		for _, vu := range upstreams {
			if !upstreamSet[vu.Config.Name] {
				continue
			}
			// Build a set of nodes selected by the filter for this upstream's spec.
			var matchedNodes map[*yaml.Node]bool
			if jp != nil && vu.SpecYAMLRoot != nil {
				results := jp.Query(vu.SpecYAMLRoot)
				matchedNodes = make(map[*yaml.Node]bool, len(results))
				for _, n := range results {
					matchedNodes[n] = true
				}
			}

			for _, entry := range vu.Entries {
				// HTTP tools have OperationNode set; command/script tools do not and are always included.
				if jp != nil && entry.OperationNode != nil && !matchedNodes[entry.OperationNode] {
					continue
				}
				gd.toolSet[entry.PrefixedName] = true
			}
		}

		// Build ordered tool list for this group, preserving registry insertion order.
		for _, t := range r.toolList {
			if gd.toolSet[t.Name] {
				gd.toolList = append(gd.toolList, t)
			}
		}

		r.groups[g.Name] = gd
	}

	return r, nil
}

// NewFromEntries builds a Registry from pre-compiled RegistryEntry objects.
// This is used by background refresh (UpdateUpstream / RemoveUpstream) to rebuild
// the registry without re-running the full validation pipeline.
// entriesByUpstream maps upstream name → ordered slice of entries (each entry must
// have MCPTool set). specRootByUpstream maps upstream name → post-overlay YAML root
// used for JSONPath group filter evaluation (may be nil to skip filtering).
func NewFromEntries(
	entriesByUpstream map[string][]*RegistryEntry,
	specRootByUpstream map[string]*yaml.Node,
	naming *config.NamingConfig,
	groups []config.GroupConfig,
) (*Registry, error) {
	r := &Registry{
		byPrefixedName: make(map[string]*RegistryEntry),
		byPrefix:       make(map[string]*Upstream),
		byUpstreamName: make(map[string][]*RegistryEntry),
		specRootByName: make(map[string]*yaml.Node),
		separator:      naming.Separator,
		groups:         make(map[string]*groupData),
	}

	for upName, root := range specRootByUpstream {
		r.specRootByName[upName] = root
	}

	// Register all entries.
	for upName, entries := range entriesByUpstream {
		r.byUpstreamName[upName] = entries
		for _, entry := range entries {
			if existing, ok := r.byPrefixedName[entry.PrefixedName]; ok {
				upA := ""
				upB := ""
				if existing.Upstream != nil {
					upA = existing.Upstream.Name
				}
				if entry.Upstream != nil {
					upB = entry.Upstream.Name
				}
				return nil, fmt.Errorf("tool name conflict %q between upstreams %q and %q", entry.PrefixedName, upA, upB)
			}
			r.byPrefixedName[entry.PrefixedName] = entry
			if entry.Upstream != nil {
				r.byPrefix[entry.Upstream.ToolPrefix] = entry.Upstream
			}
			if entry.MCPTool != nil {
				r.toolList = append(r.toolList, entry.MCPTool)
			}
		}
	}

	// Compile group filters.
	compiledFilters := make(map[string]*jsonpath.JSONPath, len(groups))
	for _, g := range groups {
		if g.Filter == "" {
			continue
		}
		jp, err := jsonpath.NewPath(g.Filter)
		if err != nil {
			return nil, fmt.Errorf("group %q has invalid JSONPath filter %q: %w", g.Name, g.Filter, err)
		}
		compiledFilters[g.Name] = jp
	}

	// Build group membership.
	for _, g := range groups {
		gd := &groupData{
			toolSet: make(map[string]bool),
		}
		upstreamSet := make(map[string]bool, len(g.Upstreams))
		for _, name := range g.Upstreams {
			upstreamSet[name] = true
		}

		jp := compiledFilters[g.Name]

		for upName, entries := range entriesByUpstream {
			if !upstreamSet[upName] {
				continue
			}
			specRoot := specRootByUpstream[upName]

			var matchedNodes map[*yaml.Node]bool
			if jp != nil && specRoot != nil {
				results := jp.Query(specRoot)
				matchedNodes = make(map[*yaml.Node]bool, len(results))
				for _, n := range results {
					matchedNodes[n] = true
				}
			}

			for _, entry := range entries {
				// HTTP tools have OperationNode set; command/script tools do not and are always included.
				if jp != nil && entry.OperationNode != nil && !matchedNodes[entry.OperationNode] {
					continue
				}
				gd.toolSet[entry.PrefixedName] = true
			}
		}

		// Build ordered tool list for this group.
		for _, t := range r.toolList {
			if gd.toolSet[t.Name] {
				gd.toolList = append(gd.toolList, t)
			}
		}

		r.groups[g.Name] = gd
	}

	return r, nil
}

// EntriesForUpstream returns all RegistryEntry objects for the named upstream.
// Returns nil if the upstream is unknown.
func (r *Registry) EntriesForUpstream(upstreamName string) []*RegistryEntry {
	return r.byUpstreamName[upstreamName]
}

// SpecRootForUpstream returns the post-overlay YAML root for the named upstream.
// Returns nil if the upstream is unknown or has no YAML root.
func (r *Registry) SpecRootForUpstream(upstreamName string) *yaml.Node {
	return r.specRootByName[upstreamName]
}

// UpstreamNames returns the names of all registered upstreams.
func (r *Registry) UpstreamNames() []string {
	names := make([]string, 0, len(r.byUpstreamName))
	for name := range r.byUpstreamName {
		names = append(names, name)
	}
	return names
}

// ToolsForGroup returns the MCP tool list for the given group.
// Only tools whose upstream is in the group's upstream list AND whose operation
// satisfies the JSONPath filter (if set) are returned.
// Returns nil if the group is unknown.
func (r *Registry) ToolsForGroup(groupName string) []*sdkmcp.Tool {
	gd, ok := r.groups[groupName]
	if !ok {
		return nil
	}
	return gd.toolList
}

// DispatchForGroup routes a tool call, but only if the tool belongs to the group.
// Returns IsError: true if the tool exists globally but not in this group.
func (r *Registry) DispatchForGroup(ctx context.Context, groupName, name string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	gd, ok := r.groups[groupName]
	if !ok {
		return newErrorResult("unknown group: " + groupName), nil
	}
	if !gd.toolSet[name] {
		return newErrorResult("tool not available in this group: " + name), nil
	}
	entry, ok := r.byPrefixedName[name]
	if !ok {
		return newErrorResult("unknown tool: " + name), nil
	}
	return r.handleToolCall(ctx, entry, args)
}

// Tools returns all tools for use in MCP tools/list.
func (r *Registry) Tools() []*sdkmcp.Tool {
	return r.toolList
}

// AuthRequired reports whether authentication is required for the named tool.
// Returns true (conservative default) if the tool is not found in the registry.
func (r *Registry) AuthRequired(toolName string) bool {
	entry, ok := r.byPrefixedName[toolName]
	if !ok {
		return true
	}
	return entry.AuthRequired
}

// ToolUpstreamName returns the upstream name for the given tool, or an empty string if unknown.
func (r *Registry) ToolUpstreamName(toolName string) string {
	entry, ok := r.byPrefixedName[toolName]
	if !ok {
		return ""
	}
	return entry.Upstream.Name
}

// EntryForTool returns the RegistryEntry for the given prefixed tool name.
// Returns nil if the tool is unknown.
func (r *Registry) EntryForTool(toolName string) *RegistryEntry {
	return r.byPrefixedName[toolName]
}

// UIHandlerForTool returns the ResourceHandler for the tool's interactive HTML UI,
// or nil if the tool has no UI configured.
func (r *Registry) UIHandlerForTool(toolName string) sdkmcp.ResourceHandler {
	entry, ok := r.byPrefixedName[toolName]
	if !ok {
		return nil
	}
	return entry.UIHandler
}

// Dispatch routes a tool call to the correct upstream entry.
// Returns an error result if the tool name is unknown or malformed.
func (r *Registry) Dispatch(ctx context.Context, name string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	// Find the first occurrence of the separator (AC-10.2).
	if !strings.Contains(name, r.separator) {
		return newErrorResult("tool name missing prefix separator: " + name), nil
	}

	entry, ok := r.byPrefixedName[name]
	if !ok {
		return newErrorResult("unknown tool: " + name), nil
	}

	return r.handleToolCall(ctx, entry, args)
}

// handleToolCall executes the tool call by delegating to the entry's Executor.
func (r *Registry) handleToolCall(ctx context.Context, entry *RegistryEntry, args map[string]any) (*sdkmcp.CallToolResult, error) {
	return entry.Executor.Execute(ctx, args)
}

// newErrorResult creates an IsError CallToolResult with the given message.
func newErrorResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: msg},
		},
	}
}
