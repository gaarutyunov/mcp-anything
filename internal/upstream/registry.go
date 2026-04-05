package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/speakeasy-api/jsonpath/pkg/jsonpath"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gopkg.in/yaml.v3"

	outboundauth "github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/command"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/content"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// Upstream holds the per-upstream HTTP routing state.
type Upstream struct {
	Name       string
	ToolPrefix string
	BaseURL    string
	Client     *http.Client
}

// RegistryEntry associates a prefixed tool name with its upstream and runtime state.
type RegistryEntry struct {
	PrefixedName   string
	OriginalName   string // without prefix — used for upstream HTTP path
	Upstream       *Upstream
	MCPTool        *sdkmcp.Tool // MCP tool definition for tools/list
	Transforms     *transform.CompiledTransforms
	ResponseFormat string // x-mcp-response-format value; default "json"
	AuthRequired   bool   // x-mcp-auth-required; default true
	Method         string // HTTP method (empty for command tools)
	PathTemplate   string // e.g. /pets/{petId} (empty for command tools)
	Validator      *openapi.Validator
	ValidationCfg  config.ValidationConfig
	OperationNode  *yaml.Node   // YAML node for JSONPath group filter evaluation (nil for command tools)
	CommandDef     *command.Def // non-nil for command-backed tools; nil for HTTP tools
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

// ValidatedUpstream is the result of validating a single upstream configuration.
type ValidatedUpstream struct {
	Config       *config.UpstreamConfig
	Tools        []*openapi.ValidatedTool // populated for type: http upstreams
	CommandTools []*command.Tool          // populated for type: command upstreams
	Provider     outboundauth.TokenProvider
	SpecYAMLRoot *yaml.Node // post-overlay YAML root for JSONPath group filter evaluation
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
		var up *Upstream
		if vu.Config.Type == "command" {
			// Command upstreams have no HTTP client.
			up = &Upstream{
				Name:       vu.Config.Name,
				ToolPrefix: vu.Config.ToolPrefix,
			}
		} else {
			up = &Upstream{
				Name:       vu.Config.Name,
				ToolPrefix: vu.Config.ToolPrefix,
				BaseURL:    vu.Config.BaseURL,
				Client:     NewHTTPClient(vu.Config, vu.Provider),
			}
		}
		r.byPrefix[vu.Config.ToolPrefix] = up
		r.specRootByName[vu.Config.Name] = vu.SpecYAMLRoot

		for _, vt := range vu.Tools {
			if existing, ok := r.byPrefixedName[vt.PrefixedName]; ok {
				return nil, fmt.Errorf("tool name conflict %q between upstreams %q and %q",
					vt.PrefixedName, existing.Upstream.Name, vu.Config.Name)
			}
			authRequired := extractAuthRequired(vt.Operation)
			if !authRequired {
				slog.Info("public operation (auth not required)", "tool", vt.PrefixedName)
			}
			entry := &RegistryEntry{
				PrefixedName:   vt.PrefixedName,
				OriginalName:   vt.OriginalName,
				Upstream:       up,
				MCPTool:        vt.MCPTool,
				Transforms:     vt.Transforms,
				ResponseFormat: extractResponseFormat(vt.Operation),
				AuthRequired:   authRequired,
				Method:         vt.Method,
				PathTemplate:   vt.PathTemplate,
				Validator:      vt.Validator,
				ValidationCfg:  vu.Config.Validation,
				OperationNode:  vt.OperationNode,
			}
			r.byPrefixedName[vt.PrefixedName] = entry
			r.byUpstreamName[vu.Config.Name] = append(r.byUpstreamName[vu.Config.Name], entry)
			r.toolList = append(r.toolList, vt.MCPTool)
		}

		for _, ct := range vu.CommandTools {
			if existing, ok := r.byPrefixedName[ct.PrefixedName]; ok {
				return nil, fmt.Errorf("tool name conflict %q between upstreams %q and %q",
					ct.PrefixedName, existing.Upstream.Name, vu.Config.Name)
			}
			entry := &RegistryEntry{
				PrefixedName: ct.PrefixedName,
				OriginalName: ct.OriginalName,
				Upstream:     up,
				MCPTool:      ct.MCPTool,
				Transforms:   ct.Transforms,
				AuthRequired: true, // default: command tools require auth
				CommandDef:   ct.Def,
			}
			r.byPrefixedName[ct.PrefixedName] = entry
			r.byUpstreamName[vu.Config.Name] = append(r.byUpstreamName[vu.Config.Name], entry)
			r.toolList = append(r.toolList, ct.MCPTool)
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

			for _, vt := range vu.Tools {
				entry := r.byPrefixedName[vt.PrefixedName]
				if entry == nil {
					continue
				}
				if jp != nil {
					// Include only if the operation's YAML node was selected by the filter.
					if entry.OperationNode == nil || !matchedNodes[entry.OperationNode] {
						continue
					}
				}
				gd.toolSet[vt.PrefixedName] = true
			}

			// Command tools have no OperationNode and are always included
			// in their upstream's groups regardless of any JSONPath filter.
			for _, ct := range vu.CommandTools {
				gd.toolSet[ct.PrefixedName] = true
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

// handleToolCall executes the full request pipeline for a single tool call.
// It dispatches to handleCommandCall for command-backed tools and to the HTTP
// pipeline for OpenAPI-backed tools.
func (r *Registry) handleToolCall(ctx context.Context, entry *RegistryEntry, args map[string]any) (*sdkmcp.CallToolResult, error) {
	if entry.CommandDef != nil {
		return r.handleCommandCall(ctx, entry, args)
	}
	return r.handleHTTPCall(ctx, entry, args)
}

// handleCommandCall executes a command-backed tool.
func (r *Registry) handleCommandCall(ctx context.Context, entry *RegistryEntry, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", entry.PrefixedName))

	start := time.Now()
	stdout, stderr, err := entry.CommandDef.Execute(ctx, args)
	if telemetry.ToolCallDuration != nil {
		telemetry.ToolCallDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", entry.PrefixedName),
				attribute.String("mcp.method", "tools/call"),
			),
		)
	}

	if err != nil {
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return command.ToErrorResult(stderr, err), nil
	}

	return command.ToTextResult(stdout), nil
}

// handleHTTPCall executes the full HTTP request pipeline from SPEC.md §17 for a single tool call.
func (r *Registry) handleHTTPCall(ctx context.Context, entry *RegistryEntry, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", entry.PrefixedName))

	// Apply request transform jq → RequestEnvelope.
	reqStart := time.Now()
	envelope, err := entry.Transforms.RunRequest(ctx, args)
	if telemetry.TransformDuration != nil {
		telemetry.TransformDuration.Record(ctx, time.Since(reqStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", entry.PrefixedName),
				attribute.String("transform.stage", "request"),
			),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("request transform: %w", err)
	}

	// Build upstream URL from the envelope.
	upstreamURL, err := buildUpstreamURL(entry.Upstream.BaseURL, entry.PathTemplate, envelope)
	if err != nil {
		return nil, fmt.Errorf("building upstream URL: %w", err)
	}

	// Build request body if present.
	var bodyReader io.Reader
	if envelope.Body != nil {
		bodyBytes, marshalErr := json.Marshal(envelope.Body)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshalling request body: %w", marshalErr)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, entry.Method, upstreamURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Add envelope headers; static upstream headers are injected by the RoundTripper.
	for k, v := range envelope.Headers {
		httpReq.Header.Set(k, v)
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Validate the outbound request against the OpenAPI spec (if configured).
	// When only response validation is enabled, we still build the route metadata
	// (BuildRequestInput) so that ValidateResponse has the required context.
	var reqInput *openapi3filter.RequestValidationInput
	if entry.Validator != nil {
		if entry.ValidationCfg.ValidateRequest {
			ri, valErr := entry.Validator.ValidateRequest(ctx, httpReq)
			if valErr != nil {
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("request validation failed: %v", valErr)},
					},
				}, nil
			}
			reqInput = ri
		} else if entry.ValidationCfg.ValidateResponse {
			ri, routeErr := entry.Validator.BuildRequestInput(httpReq)
			if routeErr != nil {
				slog.Warn("could not resolve route for response validation", "tool", entry.PrefixedName, "error", routeErr)
			} else {
				reqInput = ri
			}
		}
	}

	// Inject outbound auth — no-op for now; TASK-10 fills this in.

	// Execute the upstream HTTP call.
	resp, err := entry.Upstream.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("closing response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	inSuccess := statusIn(entry.ValidationCfg.SuccessStatus, resp.StatusCode)
	inError := statusIn(entry.ValidationCfg.ErrorStatus, resp.StatusCode)

	if !inSuccess && !inError {
		result := &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprintf("unexpected HTTP %d", resp.StatusCode)},
			},
		}
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	contentType := resp.Header.Get("Content-Type")

	if inError {
		errStart := time.Now()
		result := buildErrorResult(ctx, entry.Transforms, resp.StatusCode, contentType, body)
		if telemetry.TransformDuration != nil {
			telemetry.TransformDuration.Record(ctx, time.Since(errStart).Seconds(),
				metric.WithAttributes(
					attribute.String("mcp.tool.name", entry.PrefixedName),
					attribute.String("transform.stage", "error"),
				),
			)
		}
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	// Success path: validate response if configured.
	if entry.ValidationCfg.ValidateResponse && reqInput != nil && entry.Validator != nil {
		if valErr := entry.Validator.ValidateResponse(ctx, reqInput, resp, body); valErr != nil {
			if entry.ValidationCfg.ResponseValidationFailure == "fail" {
				result := &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("response validation failed: %v", valErr)},
					},
				}
				if telemetry.ToolCallErrors != nil {
					telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
				}
				return result, nil
			}
			slog.Warn("response validation failed", "tool", entry.PrefixedName, "error", valErr)
		}
	}

	respStart := time.Now()
	result := buildSuccessResult(ctx, entry.Transforms, entry.ResponseFormat, contentType, body)
	if telemetry.TransformDuration != nil {
		telemetry.TransformDuration.Record(ctx, time.Since(respStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", entry.PrefixedName),
				attribute.String("transform.stage", "response"),
			),
		)
	}
	return result, nil
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

// buildErrorResult transforms an error response body and returns an error CallToolResult.
func buildErrorResult(ctx context.Context, transforms *transform.CompiledTransforms, statusCode int, contentType string, body []byte) *sdkmcp.CallToolResult {
	return content.ToErrorResult(ctx, body, contentType, statusCode, transforms.Error)
}

// buildSuccessResult converts a success response body to MCP content and returns a CallToolResult.
func buildSuccessResult(ctx context.Context, transforms *transform.CompiledTransforms, responseFormat, contentType string, body []byte) *sdkmcp.CallToolResult {
	format := content.Detect(content.Format(responseFormat), contentType)
	contents, err := content.ToMCPContent(ctx, format, body, contentType, transforms.Response)
	if err != nil {
		slog.Warn("response content conversion failed, using raw body", "error", err)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}
	}
	return &sdkmcp.CallToolResult{Content: contents}
}

// buildUpstreamURL constructs the upstream URL using the request envelope.
func buildUpstreamURL(baseURL, pathTemplate string, envelope *transform.RequestEnvelope) (string, error) {
	path := pathTemplate
	for name, val := range envelope.Path {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(val))
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	if len(envelope.Query) > 0 {
		q := u.Query()
		for k, v := range envelope.Query {
			if v != "" {
				q.Set(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

// statusIn reports whether status is in the list.
func statusIn(list []int, status int) bool {
	for _, s := range list {
		if s == status {
			return true
		}
	}
	return false
}

// extractResponseFormat reads x-mcp-response-format from an operation extension.
func extractResponseFormat(op *openapi3.Operation) string {
	if op == nil {
		return "json"
	}
	val, ok := op.Extensions["x-mcp-response-format"]
	if !ok {
		return "json"
	}
	if s, ok := val.(string); ok && s != "" {
		return s
	}
	return "json"
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
				if jp != nil && entry.CommandDef == nil {
					// Command tools have no OperationNode; always include them.
					if entry.OperationNode == nil || !matchedNodes[entry.OperationNode] {
						continue
					}
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

// extractAuthRequired reads x-mcp-auth-required from an operation extension (default true).
func extractAuthRequired(op *openapi3.Operation) bool {
	if op == nil {
		return true
	}
	val, ok := op.Extensions["x-mcp-auth-required"]
	if !ok {
		return true
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return strings.ToLower(v) != "false"
	}
	return true
}
