package openapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

const testValidatorSpec = `openapi: "3.0.0"
info:
  title: Test API
  version: "1.0"
paths:
  /pets/{petId}:
    get:
      operationId: getPet
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, name]
                properties:
                  id:
                    type: integer
                  name:
                    type: string
`

// buildTestValidator parses testValidatorSpec and returns a Validator.
func buildTestValidator(t *testing.T) *Validator {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(testValidatorSpec))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	ctx := context.Background()
	if err := doc.Validate(ctx); err != nil {
		t.Fatalf("validate spec: %v", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	return NewValidator(doc, router)
}

func TestValidateRequest_ValidRequestPasses(t *testing.T) {
	v := buildTestValidator(t)
	req := httptest.NewRequest(http.MethodGet, "/pets/123", nil)
	_, err := v.ValidateRequest(context.Background(), req)
	if err != nil {
		t.Errorf("expected no error for valid request, got: %v", err)
	}
}

func TestValidateRequest_MissingRouteReturnsError(t *testing.T) {
	v := buildTestValidator(t)
	// Path not in spec.
	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	_, err := v.ValidateRequest(context.Background(), req)
	if err == nil {
		t.Error("expected error for unrecognised path, got nil")
	}
}

func TestValidateRequest_InvalidMethodReturnsError(t *testing.T) {
	v := buildTestValidator(t)
	// POST is not defined for /pets/{petId}.
	req := httptest.NewRequest(http.MethodPost, "/pets/123", nil)
	_, err := v.ValidateRequest(context.Background(), req)
	if err == nil {
		t.Error("expected error for invalid method, got nil")
	}
}

func TestValidateResponse_ValidResponsePasses(t *testing.T) {
	v := buildTestValidator(t)

	// First build a valid reqInput.
	req := httptest.NewRequest(http.MethodGet, "/pets/123", nil)
	reqInput, err := v.ValidateRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("request validation failed: %v", err)
	}

	// Build a valid response.
	body := []byte(`{"id": 1, "name": "Fido"}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	if err := v.ValidateResponse(context.Background(), reqInput, resp, body); err != nil {
		t.Errorf("expected no error for valid response, got: %v", err)
	}
}

func TestValidateResponse_WrongTypeFieldFails(t *testing.T) {
	v := buildTestValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/pets/123", nil)
	reqInput, err := v.ValidateRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("request validation failed: %v", err)
	}

	// id should be integer but is a string.
	body := []byte(`{"id": "not-an-integer", "name": "Fido"}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	err = v.ValidateResponse(context.Background(), reqInput, resp, body)
	if err == nil {
		t.Error("expected validation error for wrong type field, got nil")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Errorf("expected error to mention field 'id', got: %v", err)
	}
}
