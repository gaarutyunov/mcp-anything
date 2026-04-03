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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// Upstream holds the per-upstream HTTP routing state.
type Upstream struct {
	Name    string
	BaseURL string
	Client  *http.Client
	// OutboundAuth will be added in TASK-10.
}

// RegistryEntry associates a prefixed tool name with its upstream and runtime state.
type RegistryEntry struct {
	PrefixedName   string
	OriginalName   string // without prefix — used for upstream HTTP path
	Upstream       *Upstream
	Transforms     *transform.CompiledTransforms
	ResponseFormat string // x-mcp-response-format value; default "json"
	AuthRequired   bool   // x-mcp-auth-required; default true
	Method         string // HTTP method
	PathTemplate   string // e.g. /pets/{petId}
	Validator      *openapi.Validator
	ValidationCfg  config.ValidationConfig
}

// Registry maps prefixed tool names to their upstream and compiled tool state.
// It is immutable after construction and safe for concurrent reads.
type Registry struct {
	byPrefixedName map[string]*RegistryEntry
	byPrefix       map[string]*Upstream
	toolList       []*sdkmcp.Tool
	separator      string
}

// ValidatedUpstream is the result of validating a single upstream configuration.
type ValidatedUpstream struct {
	Config *config.UpstreamConfig
	Tools  []*openapi.ValidatedTool
}

// New builds a Registry from all validated upstreams.
// Returns an error if any tool prefix is shared (AC-07.5).
func New(upstreams []*ValidatedUpstream, naming *config.NamingConfig) (*Registry, error) {
	r := &Registry{
		byPrefixedName: make(map[string]*RegistryEntry),
		byPrefix:       make(map[string]*Upstream),
		separator:      naming.Separator,
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
		up := &Upstream{
			Name:    vu.Config.Name,
			BaseURL: vu.Config.BaseURL,
			Client:  NewHTTPClient(vu.Config),
		}
		r.byPrefix[vu.Config.ToolPrefix] = up

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
				Transforms:     vt.Transforms,
				ResponseFormat: extractResponseFormat(vt.Operation),
				AuthRequired:   authRequired,
				Method:         vt.Method,
				PathTemplate:   vt.PathTemplate,
				Validator:      vt.Validator,
				ValidationCfg:  vu.Config.Validation,
			}
			r.byPrefixedName[vt.PrefixedName] = entry
			r.toolList = append(r.toolList, vt.MCPTool)
		}
	}

	return r, nil
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

// handleToolCall executes the full request pipeline from SPEC.md §17 for a single tool call.
func (r *Registry) handleToolCall(ctx context.Context, entry *RegistryEntry, args map[string]any) (*sdkmcp.CallToolResult, error) {
	// Apply request transform jq → RequestEnvelope.
	envelope, err := entry.Transforms.RunRequest(ctx, args)
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
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprintf("unexpected HTTP %d", resp.StatusCode)},
			},
		}, nil
	}

	if inError {
		return buildErrorResult(ctx, entry.Transforms, resp.StatusCode, body), nil
	}

	// Success path: validate response if configured.
	if entry.ValidationCfg.ValidateResponse && reqInput != nil && entry.Validator != nil {
		if valErr := entry.Validator.ValidateResponse(ctx, reqInput, resp, body); valErr != nil {
			if entry.ValidationCfg.ResponseValidationFailure == "fail" {
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("response validation failed: %v", valErr)},
					},
				}, nil
			}
			slog.Warn("response validation failed", "tool", entry.PrefixedName, "error", valErr)
		}
	}

	return buildSuccessResult(ctx, entry.Transforms, body), nil
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
func buildErrorResult(ctx context.Context, transforms *transform.CompiledTransforms, statusCode int, body []byte) *sdkmcp.CallToolResult {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		parsed = map[string]any{"status": statusCode, "body": string(body)}
	} else if m, ok := parsed.(map[string]any); ok {
		if m["status"] == nil {
			m["status"] = statusCode
		}
	} else {
		parsed = map[string]any{"status": statusCode, "body": parsed}
	}

	transformed, err := transforms.RunError(ctx, parsed)
	if err != nil {
		slog.Warn("error transform failed, using raw body", "error", err)
		transformed = map[string]any{"error": fmt.Sprintf("upstream returned %d", statusCode), "body": string(body)}
	}

	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: marshalToString(transformed)},
		},
	}
}

// buildSuccessResult transforms a success response body and returns a CallToolResult.
func buildSuccessResult(ctx context.Context, transforms *transform.CompiledTransforms, body []byte) *sdkmcp.CallToolResult {
	var parsed any = string(body)
	var jsonParsed any
	if err := json.Unmarshal(body, &jsonParsed); err == nil {
		parsed = jsonParsed
	}

	transformed, err := transforms.RunResponse(ctx, parsed)
	if err != nil {
		slog.Warn("response transform failed, using raw body", "error", err)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}
	}

	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: marshalToString(transformed)},
		},
	}
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

// marshalToString marshals v to a JSON string, or returns a string representation on error.
func marshalToString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
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
