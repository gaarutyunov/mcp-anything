package openapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/jsonschema-go/jsonschema"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// ParamInfo holds metadata about a single OpenAPI parameter.
type ParamInfo struct {
	Name     string
	Required bool
	In       string // "query", "path", "header"
}

// GeneratedTool associates an MCP tool definition with routing information
// needed to construct and execute the upstream HTTP request.
type GeneratedTool struct {
	MCPTool      *mcp.Tool
	PrefixedName string
	OriginalName string
	Method       string   // HTTP method (GET, POST, etc.)
	PathTemplate string   // e.g. /pets/{petId}
	PathParams   []string // ordered list of path param names
	QueryParams  []ParamInfo
}

// GenerateTools walks all operations in the OpenAPI document and returns
// a list of MCP tools. Each tool is associated with the routing information
// needed to construct and execute the upstream HTTP request.
func GenerateTools(doc *openapi3.T, upstream *config.UpstreamConfig, sep string) ([]*GeneratedTool, error) {
	var tools []*GeneratedTool

	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			if op == nil {
				continue
			}

			// Skip operations with x-mcp-enabled: false.
			if val, ok := op.Extensions["x-mcp-enabled"]; ok {
				if enabled, ok := val.(bool); ok && !enabled {
					continue
				}
			}

			// Collect parameters from path item and operation (operation overrides path-level).
			allParams := mergeParams(pathItem.Parameters, op.Parameters)

			pathParams, queryParams := classifyParams(allParams)

			// Build slug from path + method verb.
			slug := buildSlug(method, path, pathParams)

			// Allow x-mcp-tool-name to override the generated slug.
			if val, ok := op.Extensions["x-mcp-tool-name"]; ok {
				if name, ok := val.(string); ok && name != "" {
					slug = name
				}
			}

			prefixedName := upstream.ToolPrefix + sep + slug

			// Build the MCP input schema.
			inputSchema, err := buildInputSchema(allParams)
			if err != nil {
				return nil, fmt.Errorf("building input schema for %s %s: %w", method, path, err)
			}

			description := op.Summary
			if description == "" {
				description = op.Description
			}

			tool := &mcp.Tool{
				Name:        prefixedName,
				Description: description,
				InputSchema: inputSchema,
			}

			gt := &GeneratedTool{
				MCPTool:      tool,
				PrefixedName: prefixedName,
				OriginalName: slug,
				Method:       method,
				PathTemplate: path,
				PathParams:   pathParams,
				QueryParams:  queryParams,
			}

			tools = append(tools, gt)
		}
	}

	return tools, nil
}

// mergeParams merges path-level and operation-level parameters.
// Operation-level parameters override path-level ones with the same name+in.
func mergeParams(pathParams, opParams openapi3.Parameters) openapi3.Parameters {
	merged := make(openapi3.Parameters, 0, len(pathParams)+len(opParams))
	merged = append(merged, pathParams...)
	for _, opRef := range opParams {
		if opRef == nil || opRef.Value == nil {
			continue
		}
		found := false
		for i, pRef := range merged {
			if pRef != nil && pRef.Value != nil &&
				pRef.Value.Name == opRef.Value.Name &&
				pRef.Value.In == opRef.Value.In {
				merged[i] = opRef
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, opRef)
		}
	}
	return merged
}

// classifyParams splits parameters into path param names and query ParamInfo.
func classifyParams(params openapi3.Parameters) ([]string, []ParamInfo) {
	var pathParams []string
	var queryParams []ParamInfo

	for _, ref := range params {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		switch p.In {
		case "path":
			pathParams = append(pathParams, p.Name)
		case "query":
			queryParams = append(queryParams, ParamInfo{
				Name:     p.Name,
				Required: p.Required,
				In:       p.In,
			})
		}
	}
	return pathParams, queryParams
}

// buildSlug creates a tool name slug from the HTTP method and path.
func buildSlug(method, path string, pathParams []string) string {
	// Strip leading slash.
	slug := strings.TrimPrefix(path, "/")
	// Replace path separators.
	slug = strings.ReplaceAll(slug, "/", "_")
	// Remove braces.
	slug = strings.ReplaceAll(slug, "{", "")
	slug = strings.ReplaceAll(slug, "}", "")
	// Lowercase.
	slug = strings.ToLower(slug)

	// Add method verb prefix.
	verb := methodVerb(method, pathParams)

	return verb + "_" + slug
}

// methodVerb returns the verb prefix for a given HTTP method.
func methodVerb(method string, pathParams []string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		if len(pathParams) == 0 {
			return "list"
		}
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodDelete:
		return "delete"
	case http.MethodPatch:
		return "patch"
	default:
		return strings.ToLower(method)
	}
}

// buildInputSchema constructs a JSON Schema object for the tool input.
func buildInputSchema(params openapi3.Parameters) (*jsonschema.Schema, error) {
	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: make(map[string]*jsonschema.Schema),
	}

	for _, ref := range params {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		// Only include path and query params.
		if p.In != "path" && p.In != "query" {
			continue
		}

		propSchema := paramSchema(p)
		schema.Properties[p.Name] = propSchema

		if p.Required {
			schema.Required = append(schema.Required, p.Name)
		}
	}

	return schema, nil
}

// paramSchema converts an OpenAPI parameter schema to a jsonschema.Schema.
func paramSchema(p *openapi3.Parameter) *jsonschema.Schema {
	s := &jsonschema.Schema{}
	if p.Schema != nil && p.Schema.Value != nil {
		v := p.Schema.Value
		if v.Type != nil && len(v.Type.Slice()) > 0 {
			s.Type = v.Type.Slice()[0]
		}
		if v.Description != "" {
			s.Description = v.Description
		}
	}
	if s.Type == "" {
		s.Type = "string"
	}
	return s
}
