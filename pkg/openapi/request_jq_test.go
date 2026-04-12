package openapi

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/itchyny/gojq"
)

func TestGenerateRequestJq_PathParamsOnly(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:   "petId",
				In:     "path",
				Schema: openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			}},
		},
	}

	expr := GenerateRequestJq(op, "test_tool", nil)

	if !strings.Contains(expr, "path:") {
		t.Errorf("expected jq to contain 'path:', got: %s", expr)
	}
	if strings.Contains(expr, "query:") {
		t.Errorf("unexpected 'query:' in jq, got: %s", expr)
	}
	if strings.Contains(expr, "body:") {
		t.Errorf("unexpected 'body:' in jq, got: %s", expr)
	}

	assertJqParseable(t, expr)
}

func TestGenerateRequestJq_QueryParamsOnly(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:     "limit",
				In:       "query",
				Required: true,
				Schema:   openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"integer"}}),
			}},
			{Value: &openapi3.Parameter{
				Name:     "offset",
				In:       "query",
				Required: false,
				Schema:   openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"integer"}}),
			}},
			{Value: &openapi3.Parameter{
				Name:     "filter",
				In:       "query",
				Required: false,
				Schema:   openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			}},
		},
	}

	expr := GenerateRequestJq(op, "test_tool", nil)

	// Required integer: should use tostring without null-check.
	if !strings.Contains(expr, "limit: (.limit | tostring)") {
		t.Errorf("expected required integer query param with tostring, got: %s", expr)
	}
	// Optional integer: should use null-check with tostring.
	if !strings.Contains(expr, "if .offset != null then (.offset | tostring)") {
		t.Errorf("expected optional integer query param with null-check and tostring, got: %s", expr)
	}
	// Optional string: no tostring needed.
	if !strings.Contains(expr, "filter: .filter") {
		t.Errorf("expected optional string query param, got: %s", expr)
	}
	if strings.Contains(expr, "path:") {
		t.Errorf("unexpected 'path:' in jq, got: %s", expr)
	}

	assertJqParseable(t, expr)
}

func TestGenerateRequestJq_PathQueryAndBody(t *testing.T) {
	bodySchema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: openapi3.Schemas{
			"name":    openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			"species": openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
		},
	}
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:   "petId",
				In:     "path",
				Schema: openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			}},
			{Value: &openapi3.Parameter{
				Name:     "version",
				In:       "query",
				Required: false,
				Schema:   openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			}},
		},
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": openapi3.NewMediaType().WithSchema(bodySchema),
				},
			},
		},
	}

	expr := GenerateRequestJq(op, "test_tool", nil)

	if !strings.Contains(expr, "path:") {
		t.Errorf("expected 'path:' in jq, got: %s", expr)
	}
	if !strings.Contains(expr, "query:") {
		t.Errorf("expected 'query:' in jq, got: %s", expr)
	}
	if !strings.Contains(expr, "body:") {
		t.Errorf("expected 'body:' in jq, got: %s", expr)
	}

	assertJqParseable(t, expr)
}

func TestGenerateRequestJq_CustomExtensionOverride(t *testing.T) {
	customExpr := `{path: {id: .myId}}`
	op := &openapi3.Operation{
		Extensions: map[string]any{
			"x-mcp-request-transform": customExpr,
		},
	}

	expr := GenerateRequestJq(op, "test_tool", nil)

	if expr != customExpr {
		t.Errorf("expected custom expression %q, got %q", customExpr, expr)
	}
}

func TestGenerateRequestJq_ParseableByGojq(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:     "limit",
				In:       "query",
				Required: false,
				Schema:   openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"integer"}}),
			}},
			{Value: &openapi3.Parameter{
				Name:   "petId",
				In:     "path",
				Schema: openapi3.NewSchemaRef("", &openapi3.Schema{Type: &openapi3.Types{"string"}}),
			}},
		},
	}

	expr := GenerateRequestJq(op, "test_tool", nil)
	assertJqParseable(t, expr)
}

// assertJqParseable checks that the given jq expression can be parsed by gojq.
func assertJqParseable(t *testing.T, expr string) {
	t.Helper()
	if _, err := gojq.Parse(expr); err != nil {
		t.Errorf("generated jq expression is not parseable: %v\nexpr: %s", err, expr)
	}
}
