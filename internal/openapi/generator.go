package openapi

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// ParamInfo holds metadata about a single OpenAPI parameter used for HTTP routing.
type ParamInfo struct {
	Name       string
	Required   bool
	In         string // "query", "path", "header"
	SchemaType string // "string", "integer", "number", etc.
}

// GeneratedTool associates an MCP tool definition with routing information
// needed to construct and execute the upstream HTTP request.
type GeneratedTool struct {
	MCPTool       *mcp.Tool
	PrefixedName  string
	OriginalName  string
	Method        string   // HTTP method (GET, POST, etc.)
	PathTemplate  string   // e.g. /pets/{petId}
	PathParams    []string // ordered list of path param names
	QueryParams   []ParamInfo
	HeaderParams  []ParamInfo
	Operation     *openapi3.Operation // original operation for jq generation and response schema extraction
	OperationNode *yaml.Node          // YAML node for this operation, used for JSONPath group filter evaluation
}

// GenerateTools walks all operations in the OpenAPI document and returns a list of MCP
// tools with routing metadata. It applies the full naming pipeline (ToolBaseName →
// PrefixedName → TruncateDescription) and runs conflict detection before returning.
func GenerateTools(doc *openapi3.T, upstream *config.UpstreamConfig, naming *config.NamingConfig) ([]*GeneratedTool, error) {
	var prefixedList []PrefixedTool
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
			pathParamNames, queryParams, headerParams := classifyParams(allParams)
			hasPathParams := len(pathParamNames) > 0

			// Derive the base name using naming rules.
			baseName := ToolBaseName(op, method, path, hasPathParams, naming.DefaultSlugRules)

			// Build the fully-prefixed tool name.
			prefixedName := PrefixedName(baseName, upstream.ToolPrefix, naming.Separator, naming.MaxLength)

			// Build the MCP input schema.
			inputSchema, err := DeriveInputSchema(op)
			if err != nil {
				return nil, fmt.Errorf("building input schema for %s %s: %w", method, path, err)
			}

			// Pick description (summary preferred) and truncate.
			description := op.Summary
			if description == "" {
				description = op.Description
			}
			description = TruncateDescription(description, naming.DescriptionMaxLength, naming.DescriptionTruncationSuffix)

			tool := &mcp.Tool{
				Name:        prefixedName,
				Description: description,
				InputSchema: inputSchema,
			}

			// Copy the operation and replace Parameters with the merged list so
			// downstream helpers (GenerateRequestJq, DeriveArgMapping) always see
			// path-item parameters alongside operation-level parameters.
			mergedOp := *op
			mergedOp.Parameters = allParams

			gt := &GeneratedTool{
				MCPTool:      tool,
				PrefixedName: prefixedName,
				OriginalName: baseName,
				Method:       method,
				PathTemplate: path,
				PathParams:   pathParamNames,
				QueryParams:  queryParams,
				HeaderParams: headerParams,
				Operation:    &mergedOp,
			}

			tools = append(tools, gt)
			prefixedList = append(prefixedList, PrefixedTool{
				PrefixedName:   prefixedName,
				OriginalPath:   path,
				OriginalMethod: method,
			})
		}
	}

	// Run conflict detection and filter tools to only the surviving set.
	surviving, err := DetectConflicts(prefixedList, naming.ConflictResolution)
	if err != nil {
		return nil, fmt.Errorf("conflict detection: %w", err)
	}

	if len(surviving) != len(prefixedList) {
		type toolKey struct{ name, path, method string }
		survivingSet := make(map[toolKey]bool, len(surviving))
		for _, t := range surviving {
			survivingSet[toolKey{t.PrefixedName, t.OriginalPath, t.OriginalMethod}] = true
		}
		filtered := make([]*GeneratedTool, 0, len(surviving))
		for _, gt := range tools {
			if survivingSet[toolKey{gt.PrefixedName, gt.PathTemplate, gt.Method}] {
				filtered = append(filtered, gt)
			}
		}
		tools = filtered
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

// classifyParams splits parameters into path param names, query ParamInfo, and header ParamInfo.
func classifyParams(params openapi3.Parameters) ([]string, []ParamInfo, []ParamInfo) {
	var pathParams []string
	var queryParams []ParamInfo
	var headerParams []ParamInfo

	for _, ref := range params {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		schemaType := ""
		if p.Schema != nil && p.Schema.Value != nil && p.Schema.Value.Type != nil {
			types := p.Schema.Value.Type.Slice()
			if len(types) > 0 {
				schemaType = types[0]
			}
		}
		switch p.In {
		case "path":
			pathParams = append(pathParams, p.Name)
		case "query":
			queryParams = append(queryParams, ParamInfo{
				Name:       p.Name,
				Required:   p.Required,
				In:         p.In,
				SchemaType: schemaType,
			})
		case "header":
			headerParams = append(headerParams, ParamInfo{
				Name:       p.Name,
				Required:   p.Required,
				In:         p.In,
				SchemaType: schemaType,
			})
		}
	}
	return pathParams, queryParams, headerParams
}

// FindOperationYAMLNode navigates a parsed YAML spec tree to find the yaml.Node
// for a specific HTTP method operation under the given path.
// method should be lowercase (e.g. "get", "post").
// Returns nil if the path or method is not found.
func FindOperationYAMLNode(root *yaml.Node, path, method string) *yaml.Node {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	pathsNode := yamlMappingGet(node, "paths")
	if pathsNode == nil {
		return nil
	}
	pathNode := yamlMappingGet(pathsNode, path)
	if pathNode == nil {
		return nil
	}
	return yamlMappingGet(pathNode, strings.ToLower(method))
}

// yamlMappingGet returns the value node for the given key in a YAML mapping node.
// Returns nil if node is not a mapping or the key is not found.
func yamlMappingGet(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
