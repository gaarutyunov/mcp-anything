package openapi

import (
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/jsonschema-go/jsonschema"
)

// DeriveInputSchema builds the MCP tool InputSchema from an OpenAPI operation.
// It includes path params (required), query params (required if marked), header params
// (optional), and request body properties (application/json, merged at top level).
// Constraints preserved: type, format, minimum, maximum, minLength, maxLength, enum,
// pattern, description.
func DeriveInputSchema(op *openapi3.Operation) (*jsonschema.Schema, error) {
	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: make(map[string]*jsonschema.Schema),
	}

	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value

		switch p.In {
		case "path", "query", "header":
			propSchema := paramSchemaFull(p)
			schema.Properties[p.Name] = propSchema
			if p.Required {
				schema.Required = append(schema.Required, p.Name)
			}
		}
	}

	// Merge request body (application/json) properties at the top level.
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if ct, ok := op.RequestBody.Value.Content["application/json"]; ok &&
			ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
			bodySchema := ct.Schema.Value
			for propName, propRef := range bodySchema.Properties {
				if propRef == nil || propRef.Value == nil {
					continue
				}
				if _, exists := schema.Properties[propName]; exists {
					return nil, fmt.Errorf("input schema collision: request body property %q conflicts with a path/query/header parameter of the same name", propName)
				}
				schema.Properties[propName] = openAPISchemaToJSONSchema(propRef.Value)
			}
			schema.Required = append(schema.Required, bodySchema.Required...)
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
