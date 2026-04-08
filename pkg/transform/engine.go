package transform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/itchyny/gojq"
)

// CompiledTransforms holds the three compiled jq expressions for one tool.
type CompiledTransforms struct {
	Request  *gojq.Code // auto-generated or x-mcp-request-transform
	Response *gojq.Code // x-mcp-response-transform or identity "."
	Error    *gojq.Code // x-mcp-error-transform or default error handler
}

// RequestEnvelope is the output of the request transform.
type RequestEnvelope struct {
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Path    map[string]string `json:"path,omitempty"`
	Body    any               `json:"body,omitempty"`
}

// rawEnvelope is an intermediate type for unmarshalling jq output that may
// contain null values in query/path/headers maps.
type rawEnvelope struct {
	Query   map[string]any `json:"query,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
	Path    map[string]any `json:"path,omitempty"`
	Body    any            `json:"body,omitempty"`
}

// Compile compiles all three jq expressions for a tool.
// Returns an error if any expression fails to parse or compile.
func Compile(reqExpr, respExpr, errExpr string) (*CompiledTransforms, error) {
	reqCode, err := compileExpr(reqExpr)
	if err != nil {
		return nil, fmt.Errorf("compiling request expression %q: %w", reqExpr, err)
	}
	respCode, err := compileExpr(respExpr)
	if err != nil {
		return nil, fmt.Errorf("compiling response expression %q: %w", respExpr, err)
	}
	errCode, err := compileExpr(errExpr)
	if err != nil {
		return nil, fmt.Errorf("compiling error expression %q: %w", errExpr, err)
	}
	return &CompiledTransforms{
		Request:  reqCode,
		Response: respCode,
		Error:    errCode,
	}, nil
}

func compileExpr(expr string) (*gojq.Code, error) {
	q, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("parsing jq expression: %w", err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("compiling jq query: %w", err)
	}
	return code, nil
}

// RunRequest executes the request transform with the given MCP arguments.
// Returns a RequestEnvelope or an error.
func (c *CompiledTransforms) RunRequest(ctx context.Context, args map[string]any) (*RequestEnvelope, error) {
	v, err := runOnce(ctx, c.Request, args)
	if err != nil {
		return nil, fmt.Errorf("request transform: %w", err)
	}

	// Convert jq output to RequestEnvelope via JSON round-trip to handle type coercion.
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshalling request transform output: %w", err)
	}

	var raw rawEnvelope
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshalling request transform output: %w", err)
	}

	env := &RequestEnvelope{
		Body: raw.Body,
	}

	// Convert map[string]any → map[string]string, skipping null values.
	if len(raw.Query) > 0 {
		env.Query = make(map[string]string, len(raw.Query))
		for k, val := range raw.Query {
			if val != nil {
				env.Query[k] = fmt.Sprintf("%v", val)
			}
		}
	}
	if len(raw.Path) > 0 {
		env.Path = make(map[string]string, len(raw.Path))
		for k, val := range raw.Path {
			if val != nil {
				env.Path[k] = fmt.Sprintf("%v", val)
			}
		}
	}
	if len(raw.Headers) > 0 {
		env.Headers = make(map[string]string, len(raw.Headers))
		for k, val := range raw.Headers {
			if val != nil {
				env.Headers[k] = fmt.Sprintf("%v", val)
			}
		}
	}

	return env, nil
}

// RunResponse executes the response transform with the given JSON body.
func (c *CompiledTransforms) RunResponse(ctx context.Context, body any) (any, error) {
	v, err := runOnce(ctx, c.Response, body)
	if err != nil {
		return nil, fmt.Errorf("response transform: %w", err)
	}
	return v, nil
}

// RunError executes the error transform with the given error body.
func (c *CompiledTransforms) RunError(ctx context.Context, body any) (any, error) {
	v, err := runOnce(ctx, c.Error, body)
	if err != nil {
		return nil, fmt.Errorf("error transform: %w", err)
	}
	return v, nil
}

// runOnce runs a compiled jq expression and returns the first output value.
// The iterator is fully drained to catch runtime errors that occur after the first value.
func runOnce(ctx context.Context, code *gojq.Code, input any) (any, error) {
	iter := code.RunWithContext(ctx, input)
	var (
		first any
		have  bool
	)
	for {
		v, ok := iter.Next()
		if !ok {
			if !have {
				return nil, fmt.Errorf("jq expression produced no output")
			}
			return first, nil
		}
		if err, isErr := v.(error); isErr {
			return nil, fmt.Errorf("jq runtime error: %w", err)
		}
		if !have {
			first = v
			have = true
		}
	}
}
