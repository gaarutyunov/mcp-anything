package openapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// Validator holds a pre-built kin-openapi router for a single upstream spec.
type Validator struct {
	router routers.Router
}

// NewValidator creates a Validator from a parsed OpenAPI document and its pre-built router.
func NewValidator(_ *openapi3.T, router routers.Router) *Validator {
	return &Validator{router: router}
}

// BuildRequestInput resolves the route for r and returns a RequestValidationInput
// without performing request schema validation. This is used when only response
// validation is needed (ValidateRequest=false, ValidateResponse=true) to supply
// the route metadata required by ValidateResponse.
func (v *Validator) BuildRequestInput(r *http.Request) (*openapi3filter.RequestValidationInput, error) {
	route, pathParams, err := v.router.FindRoute(r)
	if err != nil {
		return nil, fmt.Errorf("route not found: %w", err)
	}
	return &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
	}, nil
}

// ValidateRequest validates an outbound HTTP request against the OpenAPI spec.
// It uses NoopAuthenticationFunc so upstream auth schemes are not re-validated.
// Returns the RequestValidationInput for use in a subsequent ValidateResponse call.
func (v *Validator) ValidateRequest(ctx context.Context, r *http.Request) (*openapi3filter.RequestValidationInput, error) {
	input, err := v.BuildRequestInput(r)
	if err != nil {
		return nil, err
	}
	return input, openapi3filter.ValidateRequest(ctx, input)
}

// ValidateResponse validates an upstream HTTP response against the OpenAPI spec.
// The reqInput must be the RequestValidationInput from a prior ValidateRequest call.
func (v *Validator) ValidateResponse(ctx context.Context, reqInput *openapi3filter.RequestValidationInput, resp *http.Response, body []byte) error {
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 resp.StatusCode,
		Header:                 resp.Header,
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                &openapi3filter.Options{MultiError: true},
	}
	return openapi3filter.ValidateResponse(ctx, input)
}

// ValidatedTool is a GeneratedTool with its compiled jq transforms and runtime validator.
type ValidatedTool struct {
	GeneratedTool
	Transforms *transform.CompiledTransforms
	Validator  *Validator
}

// ValidateUpstream runs full config-time validation for a single upstream.
// It loads the spec, applies overlays, generates tools, compiles jq expressions,
// and dry-runs all three transforms against synthetic data.
// Respects the provided context (for startup_validation_timeout).
// Returns the validated tools and the post-overlay YAML root node for JSONPath filter evaluation.
func ValidateUpstream(ctx context.Context, upstreamCfg *config.UpstreamConfig, namingCfg *config.NamingConfig) ([]*ValidatedTool, *yaml.Node, error) {
	doc, router, specYAMLRoot, err := LoadPipeline(ctx, upstreamCfg.OpenAPI, upstreamCfg.Overlay)
	if err != nil {
		return nil, nil, fmt.Errorf("loading spec for upstream %q: %w", upstreamCfg.Name, err)
	}

	validator := NewValidator(doc, router)

	tools, err := GenerateTools(doc, upstreamCfg, namingCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("generating tools for upstream %q: %w", upstreamCfg.Name, err)
	}

	// Populate OperationNode for each tool so it can be used in JSONPath group filter evaluation.
	for _, gt := range tools {
		gt.OperationNode = FindOperationYAMLNode(specYAMLRoot, gt.PathTemplate, strings.ToLower(gt.Method))
	}

	validated := make([]*ValidatedTool, 0, len(tools))
	for _, gt := range tools {
		vt, err := validateTool(ctx, gt, doc)
		if err != nil {
			return nil, nil, fmt.Errorf("validating tool %q: %w", gt.PrefixedName, err)
		}
		vt.Validator = validator
		validated = append(validated, vt)
	}

	return validated, specYAMLRoot, nil
}

// validateTool compiles jq expressions and runs dry-run validation for a single tool.
func validateTool(ctx context.Context, gt *GeneratedTool, doc *openapi3.T) (*ValidatedTool, error) {
	op := gt.Operation

	// Compute arg name mapping (handles collision renaming from DeriveInputSchema).
	argMap := DeriveArgMapping(op)

	// Determine request jq expression.
	reqExpr := transform.GenerateRequestJq(op, gt.PrefixedName, argMap)

	// Determine response jq expression.
	respExpr := transform.DefaultResponseExpr
	if val, ok := op.Extensions["x-mcp-response-transform"]; ok {
		if s, ok := val.(string); ok && s != "" {
			respExpr = s
		}
	}

	// Determine error jq expression.
	errExpr := transform.DefaultErrorExpr
	if val, ok := op.Extensions["x-mcp-error-transform"]; ok {
		if s, ok := val.(string); ok && s != "" {
			errExpr = s
		}
	}

	// Compile all three expressions.
	compiled, err := transform.Compile(reqExpr, respExpr, errExpr)
	if err != nil {
		return nil, fmt.Errorf("jq compilation failed for tool %q: %w", gt.PrefixedName, err)
	}

	// Build synthetic input schema for request dry-run.
	inputSchema := buildInputSchemaForSynthetic(op)
	instances := GenerateThreeInstances(inputSchema)

	// Dry-run the request transform against synthetic instances.
	if err := dryRunRequest(ctx, gt.PrefixedName, compiled, instances); err != nil {
		return nil, err
	}

	// Dry-run the response transform against the 200 response schema (warn only).
	dryRunResponse(ctx, gt.PrefixedName, compiled, op)

	// Dry-run the error transform against error response schemas (warn only).
	dryRunError(ctx, gt.PrefixedName, compiled, op)

	return &ValidatedTool{
		GeneratedTool: *gt,
		Transforms:    compiled,
	}, nil
}

// dryRunRequest runs the request transform against synthetic instances.
// Returns a fatal error if ALL instances fail; logs a warning if only some fail.
func dryRunRequest(ctx context.Context, toolName string, compiled *transform.CompiledTransforms, instances []any) error {
	var failures []error
	for _, inst := range instances {
		if ctx.Err() != nil {
			return fmt.Errorf("request transform dry-run cancelled: %w", ctx.Err())
		}
		args := toStringAnyMap(inst)
		if _, err := compiled.RunRequest(ctx, args); err != nil {
			failures = append(failures, err)
		}
	}

	if len(failures) == len(instances) && len(instances) > 0 {
		return fmt.Errorf("request transform dry-run failed on all %d instances for tool %q: %w", len(instances), toolName, failures[0])
	}
	if len(failures) > 0 {
		slog.Warn("request transform dry-run failed on some instances",
			"tool", toolName,
			"failed", len(failures),
			"total", len(instances),
			"error", failures[0])
	}
	return nil
}

// dryRunResponse runs the response transform against the 200 response schema (warn only).
func dryRunResponse(ctx context.Context, toolName string, compiled *transform.CompiledTransforms, op *openapi3.Operation) {
	schema := extract200Schema(op)
	if schema == nil {
		return
	}
	instances := GenerateThreeInstances(schema)
	for _, inst := range instances {
		if _, err := compiled.RunResponse(ctx, inst); err != nil {
			slog.Warn("response transform dry-run failed",
				"tool", toolName,
				"error", err)
			return
		}
	}
}

// dryRunError runs the error transform against error response schemas (warn only).
func dryRunError(ctx context.Context, toolName string, compiled *transform.CompiledTransforms, op *openapi3.Operation) {
	schema := extractErrorSchema(op)
	if schema == nil {
		// Use a generic error object for dry-run.
		genericErr := map[string]any{"status": 422, "title": "Unprocessable Entity"}
		if _, err := compiled.RunError(ctx, genericErr); err != nil {
			slog.Warn("error transform dry-run failed", "tool", toolName, "error", err)
		}
		return
	}
	instances := GenerateThreeInstances(schema)
	for _, inst := range instances {
		if _, err := compiled.RunError(ctx, inst); err != nil {
			slog.Warn("error transform dry-run failed", "tool", toolName, "error", err)
			return
		}
	}
}

// buildInputSchemaForSynthetic constructs an openapi3.Schema representing
// the tool's flat input object (all parameters + body properties), using the
// same collision renaming as DeriveInputSchema so synthetic arg names match
// real MCP call arg names.
func buildInputSchemaForSynthetic(op *openapi3.Operation) *openapi3.Schema {
	argMap := DeriveArgMapping(op)

	schema := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}

	for _, ref := range op.Parameters {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		argName := argMap.ArgName(p.In, p.Name)
		if p.Schema != nil {
			schema.Properties[argName] = p.Schema
		} else {
			schema.Properties[argName] = openapi3.NewSchemaRef("", &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			})
		}
		if p.Required || p.In == "path" {
			schema.Required = append(schema.Required, argName)
		}
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if ct, ok := op.RequestBody.Value.Content["application/json"]; ok &&
			ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
			bodySchema := ct.Schema.Value
			bodyRequired := make(map[string]bool, len(bodySchema.Required))
			for _, r := range bodySchema.Required {
				bodyRequired[r] = true
			}
			for name, ref := range bodySchema.Properties {
				argName := argMap.ArgName("body", name)
				schema.Properties[argName] = ref
				if bodyRequired[name] {
					schema.Required = append(schema.Required, argName)
				}
			}
		}
	}

	return schema
}

// extract200Schema returns the first JSON schema from the 200 response, or nil.
func extract200Schema(op *openapi3.Operation) *openapi3.Schema {
	if op.Responses == nil {
		return nil
	}
	resp := op.Responses.Value("200")
	if resp == nil || resp.Value == nil {
		return nil
	}
	if ct, ok := resp.Value.Content["application/json"]; ok && ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
		return ct.Schema.Value
	}
	return nil
}

// extractErrorSchema returns the first JSON schema from 422/400/500 responses, or nil.
func extractErrorSchema(op *openapi3.Operation) *openapi3.Schema {
	if op.Responses == nil {
		return nil
	}
	for _, code := range []string{"422", "400", "500"} {
		resp := op.Responses.Value(code)
		if resp == nil || resp.Value == nil {
			continue
		}
		if ct, ok := resp.Value.Content["application/json"]; ok && ct != nil && ct.Schema != nil && ct.Schema.Value != nil {
			return ct.Schema.Value
		}
	}
	return nil
}

// toStringAnyMap converts any value to map[string]any, returning an empty map if not possible.
func toStringAnyMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
