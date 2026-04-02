package openapi

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/jsonschema-go/jsonschema"
)

// DeriveInputSchema builds the MCP tool InputSchema from an OpenAPI operation.
// It includes path params (always required), query params (required if marked),
// header params (optional), and request body properties (application/json, merged
// at top level). Constraints preserved: type, format, minimum, maximum,
// minLength, maxLength, enum, pattern, description.
//
// Name collisions between parameters from different locations (path, query,
// header, body) are resolved by appending "_{source}" to all conflicting keys
// (e.g. "id" in both path and body becomes "id_path" and "id_body"). Names that
// do not collide are kept as-is.
func DeriveInputSchema(op *openapi3.Operation) (*jsonschema.Schema, error) {
	type entry struct {
		source     string // "path", "query", "header", or "body"
		propSchema *jsonschema.Schema
		required   bool
	}

	// Collect all parameters keyed by name. Multiple entries for the same name
	// indicate a collision that will be resolved with _{source} suffixes.
	collected := make(map[string][]entry)

	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		switch p.In {
		case "path", "query", "header":
			collected[p.Name] = append(collected[p.Name], entry{
				source:     p.In,
				propSchema: paramSchemaFull(p),
				required:   p.Required || p.In == "path",
			})
		}
	}

	// Merge request body (application/json) properties.
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if ct, ok := op.RequestBody.Value.Content["application/json"]; ok &&
			ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
			bodySchema := ct.Schema.Value
			bodyRequired := make(map[string]bool, len(bodySchema.Required))
			for _, r := range bodySchema.Required {
				bodyRequired[r] = true
			}
			for propName, propRef := range bodySchema.Properties {
				if propRef == nil || propRef.Value == nil {
					continue
				}
				collected[propName] = append(collected[propName], entry{
					source:     "body",
					propSchema: openAPISchemaToJSONSchema(propRef.Value),
					required:   bodyRequired[propName],
				})
			}
		}
	}

	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: make(map[string]*jsonschema.Schema),
	}

	for name, entries := range collected {
		if len(entries) == 1 {
			// No collision — use the original name.
			schema.Properties[name] = entries[0].propSchema
			if entries[0].required {
				schema.Required = append(schema.Required, name)
			}
		} else {
			// Collision — rename every conflicting entry with "_{source}" suffix.
			for _, e := range entries {
				key := name + "_" + e.source
				schema.Properties[key] = e.propSchema
				if e.required {
					schema.Required = append(schema.Required, key)
				}
			}
		}
	}

	return schema, nil
}

// paramSchemaFull converts an OpenAPI parameter to a jsonschema.Schema,
// preserving all supported constraints.
func paramSchemaFull(p *openapi3.Parameter) *jsonschema.Schema {
	var s *jsonschema.Schema
	if p.Schema != nil && p.Schema.Value != nil {
		s = openAPISchemaToJSONSchema(p.Schema.Value)
	} else {
		s = &jsonschema.Schema{}
	}
	if s.Type == "" {
		s.Type = "string"
	}
	// Prefer the parameter-level description over the schema-level one.
	if p.Description != "" {
		s.Description = p.Description
	}
	return s
}

// openAPISchemaToJSONSchema converts an OpenAPI 3 schema to a jsonschema.Schema,
// preserving type, format, description, minimum, maximum, minLength, maxLength,
// pattern, and enum.
func openAPISchemaToJSONSchema(v *openapi3.Schema) *jsonschema.Schema {
	s := &jsonschema.Schema{}
	if v == nil {
		return s
	}

	if v.Type != nil && len(v.Type.Slice()) > 0 {
		s.Type = v.Type.Slice()[0]
	}
	if v.Description != "" {
		s.Description = v.Description
	}
	if v.Format != "" {
		s.Format = v.Format
	}
	if v.Min != nil {
		s.Minimum = v.Min
	}
	if v.Max != nil {
		s.Maximum = v.Max
	}
	if v.MinLength != 0 {
		ml := int(v.MinLength)
		s.MinLength = &ml
	}
	if v.MaxLength != nil {
		ml := int(*v.MaxLength)
		s.MaxLength = &ml
	}
	if v.Pattern != "" {
		s.Pattern = v.Pattern
	}
	if len(v.Enum) > 0 {
		s.Enum = make([]any, len(v.Enum))
		copy(s.Enum, v.Enum)
	}

	return s
}
