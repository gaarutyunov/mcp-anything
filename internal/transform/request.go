package transform

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// DefaultResponseExpr is the identity jq expression used when no response transform is specified.
const DefaultResponseExpr = "."

// DefaultErrorExpr is the default error transform that handles problem+json and generic errors.
const DefaultErrorExpr = `if .title then
  {error: .title, detail: (.detail // ""), status: .status}
else
  {error: ("upstream error: HTTP " + (.status // "unknown" | tostring)), body: .}
end`

// GenerateRequestJq generates a jq expression string from OpenAPI operation metadata.
// If the operation has an x-mcp-request-transform extension, that value is returned verbatim.
// Otherwise, a jq expression is auto-generated from the operation's parameters and request body.
//
// argMap maps "location:originalName" to the actual MCP argument name.
// This accounts for collision renaming performed by DeriveInputSchema.
// Pass nil to use original parameter names directly.
func GenerateRequestJq(op *openapi3.Operation, toolName string, argMap map[string]string) string {
	// Check for explicit override.
	if val, ok := op.Extensions["x-mcp-request-transform"]; ok {
		if s, ok := val.(string); ok && s != "" {
			slog.Debug("using x-mcp-request-transform override", "tool", toolName, "expr", s)
			return s
		}
	}

	resolve := func(location, name string) string {
		if argMap != nil {
			if n, ok := argMap[location+":"+name]; ok {
				return n
			}
		}
		return name
	}

	var sections []string

	// Collect path parameters.
	var pathEntries []string
	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil || ref.Value.In != "path" {
			continue
		}
		name := ref.Value.Name
		argName := resolve("path", name)
		pathEntries = append(pathEntries, fmt.Sprintf("%s: (%s | tostring)", jqKey(name), jqAccess(argName)))
	}
	if len(pathEntries) > 0 {
		sections = append(sections, fmt.Sprintf("path: {%s}", strings.Join(pathEntries, ", ")))
	}

	// Collect query parameters.
	var queryEntries []string
	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil || ref.Value.In != "query" {
			continue
		}
		p := ref.Value
		argName := resolve("query", p.Name)
		schemaType := paramSchemaType(p)
		isNumeric := schemaType == "integer" || schemaType == "number"

		if p.Required {
			if isNumeric {
				queryEntries = append(queryEntries, fmt.Sprintf("%s: (%s | tostring)", jqKey(p.Name), jqAccess(argName)))
			} else {
				queryEntries = append(queryEntries, fmt.Sprintf("%s: %s", jqKey(p.Name), jqAccess(argName)))
			}
		} else {
			if isNumeric {
				queryEntries = append(queryEntries, fmt.Sprintf(
					"%s: (if %s != null then (%s | tostring) else null end)",
					jqKey(p.Name), jqAccess(argName), jqAccess(argName),
				))
			} else {
				queryEntries = append(queryEntries, fmt.Sprintf("%s: %s", jqKey(p.Name), jqAccess(argName)))
			}
		}
	}
	if len(queryEntries) > 0 {
		sections = append(sections, fmt.Sprintf("query: {%s}", strings.Join(queryEntries, ", ")))
	}

	// Collect request body properties (application/json only).
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if ct, ok := op.RequestBody.Value.Content["application/json"]; ok &&
			ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
			bodySchema := ct.Schema.Value
			var bodyEntries []string
			for propName := range bodySchema.Properties {
				argName := resolve("body", propName)
				bodyEntries = append(bodyEntries, fmt.Sprintf("%s: %s", jqKey(propName), jqAccess(argName)))
			}
			if len(bodyEntries) > 0 {
				sections = append(sections, fmt.Sprintf("body: {%s}", strings.Join(bodyEntries, ", ")))
			}
		}
	}

	// Collect header parameters.
	var headerEntries []string
	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil || ref.Value.In != "header" {
			continue
		}
		p := ref.Value
		argName := resolve("header", p.Name)
		headerEntries = append(headerEntries, fmt.Sprintf("%s: %s", jqKey(p.Name), jqAccess(argName)))
	}
	if len(headerEntries) > 0 {
		sections = append(sections, fmt.Sprintf("headers: {%s}", strings.Join(headerEntries, ", ")))
	}

	// Build the final jq expression.
	expr := "{}"
	if len(sections) > 0 {
		expr = "{" + strings.Join(sections, ", ") + "}"
	}

	slog.Debug("generated request jq", "tool", toolName, "expr", expr)
	return expr
}

// jqKey returns a valid jq object key for the given name.
// Names containing non-identifier characters are quoted with double quotes.
func jqKey(name string) string {
	if isJqIdentifier(name) {
		return name
	}
	return fmt.Sprintf("%q", name)
}

// jqAccess returns the jq field access expression for the given arg name.
// Names containing non-identifier characters use bracket notation: .["name"].
// Simple identifiers use dot notation: .name.
func jqAccess(name string) string {
	if isJqIdentifier(name) {
		return "." + name
	}
	return fmt.Sprintf(`.["%s"]`, name)
}

// isJqIdentifier reports whether s is a valid jq identifier.
// jq restricts identifiers to ASCII letters (a-z, A-Z), digits (0-9), and
// underscore; the first character must not be a digit.
func isJqIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if i > 0 && !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// paramSchemaType returns the first schema type of an OpenAPI parameter,
// or empty string if not set.
func paramSchemaType(p *openapi3.Parameter) string {
	if p.Schema == nil || p.Schema.Value == nil || p.Schema.Value.Type == nil {
		return ""
	}
	types := p.Schema.Value.Type.Slice()
	if len(types) == 0 {
		return ""
	}
	return types[0]
}
