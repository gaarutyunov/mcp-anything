package openapi

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/lucasjones/reggen"
)

// Generate produces a synthetic JSON value conforming to the given schema.
// It follows the priority order from SPEC.md §11.
// The visited map tracks schema pointer addresses to detect cycles.
func Generate(schema *openapi3.Schema, visited map[string]bool) any {
	if schema == nil {
		return nil
	}

	// Cycle detection using schema pointer address.
	key := fmt.Sprintf("%p", schema)
	if visited[key] {
		slog.Warn("circular schema reference detected, breaking cycle", "schema_addr", key)
		return map[string]any{"$cycle": true}
	}
	visited[key] = true
	defer delete(visited, key)

	// Priority 1: example
	if schema.Example != nil {
		return schema.Example
	}

	// Priority 2: x-example extension
	if val, ok := schema.Extensions["x-example"]; ok && val != nil {
		return val
	}

	// Priority 3: default
	if schema.Default != nil {
		return schema.Default
	}

	// Priority 4: enum[0]
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}

	// Priority 5: oneOf[0]
	if len(schema.OneOf) > 0 && schema.OneOf[0] != nil && schema.OneOf[0].Value != nil {
		return Generate(schema.OneOf[0].Value, visited)
	}

	// Priority 6: anyOf[0]
	if len(schema.AnyOf) > 0 && schema.AnyOf[0] != nil && schema.AnyOf[0].Value != nil {
		return Generate(schema.AnyOf[0].Value, visited)
	}

	// Priority 7: allOf — merge all schemas
	if len(schema.AllOf) > 0 {
		merged := map[string]any{}
		for _, ref := range schema.AllOf {
			if ref == nil || ref.Value == nil {
				continue
			}
			v := Generate(ref.Value, visited)
			if m, ok := v.(map[string]any); ok {
				for k, val := range m {
					merged[k] = val
				}
			}
		}
		return merged
	}

	// Priority 8: type-based fallback
	return generateByType(schema, visited)
}

// generateByType generates a value based on the schema's type field.
func generateByType(schema *openapi3.Schema, visited map[string]bool) any {
	schemaType := ""
	if schema.Type != nil {
		types := schema.Type.Slice()
		if len(types) > 0 {
			schemaType = types[0]
		}
	}

	switch schemaType {
	case "string":
		return generateString(schema)
	case "integer":
		return generateInteger(schema)
	case "number":
		return generateNumber(schema)
	case "boolean":
		return false
	case "object":
		return generateObject(schema, visited)
	case "array":
		return generateArray(schema, visited)
	default:
		return nil
	}
}

func generateString(schema *openapi3.Schema) any {
	switch schema.Format {
	case "email":
		return gofakeit.Email()
	case "uuid":
		return gofakeit.UUID()
	case "date-time":
		return time.Now().UTC().Format(time.RFC3339)
	case "uri":
		return "https://example.com/resource"
	}

	if schema.Pattern != "" {
		v, err := reggen.Generate(schema.Pattern, 10)
		if err != nil {
			slog.Warn("reggen failed for pattern, falling back to example_string", "pattern", schema.Pattern, "error", err)
			return "example_string"
		}
		return v
	}

	result := "example_string"
	// Respect MinLength: pad with 'a' if needed.
	if schema.MinLength > 0 && uint64(len(result)) < schema.MinLength {
		result += strings.Repeat("a", int(schema.MinLength)-len(result))
	}
	return result
}

func generateInteger(schema *openapi3.Schema) any {
	if schema.Min != nil {
		return int(*schema.Min) + 1
	}
	return 1
}

func generateNumber(schema *openapi3.Schema) any {
	if schema.Min != nil {
		v := *schema.Min + 0.1
		if v < 1.0 {
			v = 1.0
		}
		return v
	}
	return 1.0
}

func generateObject(schema *openapi3.Schema, visited map[string]bool) any {
	result := map[string]any{}

	// Generate values for all properties.
	for name, ref := range schema.Properties {
		if ref == nil || ref.Value == nil {
			result[name] = nil
			continue
		}
		result[name] = Generate(ref.Value, visited)
	}

	// Inject zero values for required fields not already in Properties.
	for _, req := range schema.Required {
		if _, exists := result[req]; !exists {
			result[req] = nil
		}
	}

	return result
}

func generateArray(schema *openapi3.Schema, visited map[string]bool) any {
	minItems := int(schema.MinItems)
	if minItems < 1 {
		minItems = 1
	}

	result := make([]any, minItems)
	for i := range result {
		if schema.Items != nil && schema.Items.Value != nil {
			result[i] = Generate(schema.Items.Value, visited)
		}
	}
	return result
}

// GenerateThreeInstances produces the three synthetic instances required for dry-run validation:
// 1. All properties populated (optional + required)
// 2. Required properties only
// 3. One instance per oneOf/anyOf variant (only if schema has oneOf or anyOf)
func GenerateThreeInstances(schema *openapi3.Schema) []any {
	if schema == nil {
		return []any{nil, nil}
	}

	// Instance 1: all properties.
	instance1 := Generate(schema, map[string]bool{})

	// Instance 2: required-only properties.
	instance2 := generateRequiredOnly(schema)

	result := []any{instance1, instance2}

	// Instance 3+: oneOf/anyOf variants.
	variants := schema.OneOf
	if len(variants) == 0 {
		variants = schema.AnyOf
	}
	for _, ref := range variants {
		if ref == nil || ref.Value == nil {
			continue
		}
		result = append(result, Generate(ref.Value, map[string]bool{}))
	}

	return result
}

// generateRequiredOnly generates an object with only the required properties populated.
func generateRequiredOnly(schema *openapi3.Schema) any {
	if schema == nil {
		return nil
	}

	schemaType := ""
	if schema.Type != nil {
		types := schema.Type.Slice()
		if len(types) > 0 {
			schemaType = types[0]
		}
	}

	if schemaType != "object" && len(schema.Properties) == 0 {
		// Non-object schema: just generate normally for the required instance too.
		return Generate(schema, map[string]bool{})
	}

	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}

	result := map[string]any{}
	for name, ref := range schema.Properties {
		if !required[name] {
			continue
		}
		if ref == nil || ref.Value == nil {
			result[name] = nil
			continue
		}
		result[name] = Generate(ref.Value, map[string]bool{})
	}
	return result
}
