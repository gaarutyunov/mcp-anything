package transform

import (
	"context"
	"testing"
)

func TestCompile_ValidExpressions(t *testing.T) {
	compiled, err := Compile("{path: {id: .id}}", ".", DefaultErrorExpr)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if compiled == nil {
		t.Fatal("expected non-nil CompiledTransforms")
	}
	if compiled.Request == nil || compiled.Response == nil || compiled.Error == nil {
		t.Error("expected all three compiled expressions to be non-nil")
	}
}

func TestCompile_InvalidExpression(t *testing.T) {
	_, err := Compile("{path: .NONEXISTENT | boom}", ".", DefaultErrorExpr)
	if err == nil {
		t.Error("expected error for invalid jq expression, got nil")
	}
}

func TestRunRequest_BasicTransform(t *testing.T) {
	compiled, err := Compile("{path: {petId: .petId}}", ".", DefaultErrorExpr)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	env, err := compiled.RunRequest(context.Background(), map[string]any{"petId": "42"})
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if env.Path["petId"] != "42" {
		t.Errorf("expected path.petId=42, got %q", env.Path["petId"])
	}
}

func TestRunRequest_NullQueryParamOmitted(t *testing.T) {
	compiled, err := Compile(
		"{query: {limit: (if .limit != null then (.limit | tostring) else null end)}}",
		".", DefaultErrorExpr,
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	env, err := compiled.RunRequest(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if _, ok := env.Query["limit"]; ok {
		t.Errorf("expected null query param to be omitted, got %q", env.Query["limit"])
	}
}

func TestRunRequest_IntegerToStringCoercion(t *testing.T) {
	compiled, err := Compile(
		"{query: {limit: (if .limit != null then (.limit | tostring) else null end)}}",
		".", DefaultErrorExpr,
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	env, err := compiled.RunRequest(context.Background(), map[string]any{"limit": 5})
	if err != nil {
		t.Fatalf("RunRequest: %v", err)
	}
	if env.Query["limit"] != "5" {
		t.Errorf("expected query.limit=5 (string), got %q", env.Query["limit"])
	}
}
