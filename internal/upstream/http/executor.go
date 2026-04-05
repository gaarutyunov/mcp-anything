package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/content"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// Executor handles execution of HTTP/OpenAPI-backed tool calls.
type Executor struct {
	PrefixedName   string
	Client         *http.Client
	BaseURL        string
	Method         string
	PathTemplate   string
	Transforms     *transform.CompiledTransforms
	ResponseFormat string
	Validator      *openapi.Validator
	ValidationCfg  config.ValidationConfig
}

// Execute runs the full HTTP request pipeline for a single tool call.
func (e *Executor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", e.PrefixedName))

	// Apply request transform jq → RequestEnvelope.
	reqStart := time.Now()
	envelope, err := e.Transforms.RunRequest(ctx, args)
	if telemetry.TransformDuration != nil {
		telemetry.TransformDuration.Record(ctx, time.Since(reqStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", e.PrefixedName),
				attribute.String("transform.stage", "request"),
			),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("request transform: %w", err)
	}

	// Build upstream URL from the envelope.
	upstreamURL, err := buildUpstreamURL(e.BaseURL, e.PathTemplate, envelope)
	if err != nil {
		return nil, fmt.Errorf("building upstream URL: %w", err)
	}

	// Build request body if present.
	var bodyReader io.Reader
	if envelope.Body != nil {
		bodyBytes, marshalErr := json.Marshal(envelope.Body)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshalling request body: %w", marshalErr)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, e.Method, upstreamURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Add envelope headers; static upstream headers are injected by the RoundTripper.
	for k, v := range envelope.Headers {
		httpReq.Header.Set(k, v)
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Validate the outbound request against the OpenAPI spec (if configured).
	var reqInput *openapi3filter.RequestValidationInput
	if e.Validator != nil {
		if e.ValidationCfg.ValidateRequest {
			ri, valErr := e.Validator.ValidateRequest(ctx, httpReq)
			if valErr != nil {
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("request validation failed: %v", valErr)},
					},
				}, nil
			}
			reqInput = ri
		} else if e.ValidationCfg.ValidateResponse {
			ri, routeErr := e.Validator.BuildRequestInput(httpReq)
			if routeErr != nil {
				slog.Warn("could not resolve route for response validation", "tool", e.PrefixedName, "error", routeErr)
			} else {
				reqInput = ri
			}
		}
	}

	// Execute the upstream HTTP call.
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("closing response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	inSuccess := statusIn(e.ValidationCfg.SuccessStatus, resp.StatusCode)
	inError := statusIn(e.ValidationCfg.ErrorStatus, resp.StatusCode)

	if !inSuccess && !inError {
		result := &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprintf("unexpected HTTP %d", resp.StatusCode)},
			},
		}
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	contentType := resp.Header.Get("Content-Type")

	if inError {
		errStart := time.Now()
		result := content.ToErrorResult(ctx, body, contentType, resp.StatusCode, e.Transforms.Error)
		if telemetry.TransformDuration != nil {
			telemetry.TransformDuration.Record(ctx, time.Since(errStart).Seconds(),
				metric.WithAttributes(
					attribute.String("mcp.tool.name", e.PrefixedName),
					attribute.String("transform.stage", "error"),
				),
			)
		}
		if telemetry.ToolCallErrors != nil {
			telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	// Success path: validate response if configured.
	if e.ValidationCfg.ValidateResponse && reqInput != nil && e.Validator != nil {
		if valErr := e.Validator.ValidateResponse(ctx, reqInput, resp, body); valErr != nil {
			if e.ValidationCfg.ResponseValidationFailure == "fail" {
				result := &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("response validation failed: %v", valErr)},
					},
				}
				if telemetry.ToolCallErrors != nil {
					telemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
				}
				return result, nil
			}
			slog.Warn("response validation failed", "tool", e.PrefixedName, "error", valErr)
		}
	}

	respStart := time.Now()
	result := buildSuccessResult(ctx, e.Transforms, e.ResponseFormat, contentType, body)
	if telemetry.TransformDuration != nil {
		telemetry.TransformDuration.Record(ctx, time.Since(respStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", e.PrefixedName),
				attribute.String("transform.stage", "response"),
			),
		)
	}
	return result, nil
}

// buildSuccessResult converts a success response body to MCP content and returns a CallToolResult.
func buildSuccessResult(ctx context.Context, transforms *transform.CompiledTransforms, responseFormat, contentType string, body []byte) *sdkmcp.CallToolResult {
	format := content.Detect(content.Format(responseFormat), contentType)
	contents, err := content.ToMCPContent(ctx, format, body, contentType, transforms.Response)
	if err != nil {
		slog.Warn("response content conversion failed, using raw body", "error", err)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}
	}
	return &sdkmcp.CallToolResult{Content: contents}
}

// buildUpstreamURL constructs the upstream URL using the request envelope.
func buildUpstreamURL(baseURL, pathTemplate string, envelope *transform.RequestEnvelope) (string, error) {
	path := pathTemplate
	for name, val := range envelope.Path {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(val))
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	if len(envelope.Query) > 0 {
		q := u.Query()
		for k, v := range envelope.Query {
			if v != "" {
				q.Set(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

// statusIn reports whether status is in the list.
func statusIn(list []int, status int) bool {
	for _, s := range list {
		if s == status {
			return true
		}
	}
	return false
}

// extractResponseFormat reads x-mcp-response-format from an operation extension.
func extractResponseFormat(op *openapi3.Operation) string {
	if op == nil {
		return "json"
	}
	val, ok := op.Extensions["x-mcp-response-format"]
	if !ok {
		return "json"
	}
	if s, ok := val.(string); ok && s != "" {
		return s
	}
	return "json"
}

// extractAuthRequired reads x-mcp-auth-required from an operation extension (default true).
func extractAuthRequired(op *openapi3.Operation) bool {
	if op == nil {
		return true
	}
	val, ok := op.Extensions["x-mcp-auth-required"]
	if !ok {
		return true
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return strings.ToLower(v) != "false"
	}
	return true
}
