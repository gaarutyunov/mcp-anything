package openapi

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestGenerate_ReturnsExample(t *testing.T) {
	schema := &openapi3.Schema{Example: "my_example"}
	v := Generate(schema, map[string]bool{})
	if v != "my_example" {
		t.Errorf("expected 'my_example', got %v", v)
	}
}

func TestGenerate_ReturnsXExample(t *testing.T) {
	schema := &openapi3.Schema{
		Extensions: map[string]any{"x-example": "x_example_value"},
	}
	v := Generate(schema, map[string]bool{})
	if v != "x_example_value" {
		t.Errorf("expected 'x_example_value', got %v", v)
	}
}

func TestGenerate_ReturnsDefault(t *testing.T) {
	schema := &openapi3.Schema{Default: 42}
	v := Generate(schema, map[string]bool{})
	if v != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestGenerate_ReturnsEnum0(t *testing.T) {
	schema := &openapi3.Schema{Enum: []any{"first", "second"}}
	v := Generate(schema, map[string]bool{})
	if v != "first" {
		t.Errorf("expected 'first', got %v", v)
	}
}

func TestGenerate_ObjectPopulatesAllProperties(t *testing.T) {
	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: openapi3.Schemas{
			"name": openapi3.NewSchemaRef("", &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			}),
			"age": openapi3.NewSchemaRef("", &openapi3.Schema{
				Type: &openapi3.Types{"integer"},
			}),
		},
		Required: []string{"name"},
	}

	v := Generate(schema, map[string]bool{})
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	if _, hasName := m["name"]; !hasName {
		t.Error("expected 'name' property in generated object")
	}
	if _, hasAge := m["age"]; !hasAge {
		t.Error("expected 'age' property in generated object")
	}
}

func TestGenerate_EmailFormat(t *testing.T) {
	schema := &openapi3.Schema{
		Type:   &openapi3.Types{"string"},
		Format: "email",
	}
	v := Generate(schema, map[string]bool{})
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	if !strings.Contains(s, "@") {
		t.Errorf("expected email format, got %q", s)
	}
}

func TestGenerate_PatternString(t *testing.T) {
	pattern := `^[a-z]{3}$`
	schema := &openapi3.Schema{
		Type:    &openapi3.Types{"string"},
		Pattern: pattern,
	}
	v := Generate(schema, map[string]bool{})
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	re := regexp.MustCompile(pattern)
	if !re.MatchString(s) {
		t.Errorf("expected value matching pattern %q, got %q", pattern, s)
	}
}

func TestGenerate_CycleDetection(t *testing.T) {
	schema := &openapi3.Schema{Type: &openapi3.Types{"string"}}

	// Pre-populate visited with this schema's address to simulate a cycle.
	visited := map[string]bool{
		fmt.Sprintf("%p", schema): true,
	}
	v := Generate(schema, visited)
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for cycle sentinel, got %T", v)
	}
	if m["$cycle"] != true {
		t.Errorf("expected {$cycle: true}, got %v", m)
	}
}

func TestGenerateThreeInstances_PlainObject(t *testing.T) {
	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: openapi3.Schemas{
			"required_field": openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			"optional_field": openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"integer"}}),
		},
		Required: []string{"required_field"},
	}

	instances := GenerateThreeInstances(schema)
	// Plain object: should return exactly 2 instances (all-props, required-only).
	if len(instances) != 2 {
		t.Errorf("expected 2 instances for plain object, got %d", len(instances))
	}

	// Instance 1: all properties.
	m1, ok := instances[0].(map[string]any)
	if !ok {
		t.Fatalf("instance 1 should be map[string]any, got %T", instances[0])
	}
	if _, has := m1["optional_field"]; !has {
		t.Error("instance 1 should contain optional_field")
	}

	// Instance 2: required only.
	m2, ok := instances[1].(map[string]any)
	if !ok {
		t.Fatalf("instance 2 should be map[string]any, got %T", instances[1])
	}
	if _, has := m2["required_field"]; !has {
		t.Error("instance 2 should contain required_field")
	}
	if _, has := m2["optional_field"]; has {
		t.Error("instance 2 should NOT contain optional_field")
	}
}

func TestGenerateThreeInstances_OneOfSchema(t *testing.T) {
	schema := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{
			openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"integer"}}),
		},
	}

	instances := GenerateThreeInstances(schema)
	// oneOf with 2 variants: 2 (base instances) + 2 (variants) = 4
	if len(instances) < 3 {
		t.Errorf("expected at least 3 instances for oneOf schema, got %d", len(instances))
	}
}
