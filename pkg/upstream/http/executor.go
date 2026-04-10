package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gaarutyunov/mcp-anything/pkg/content"
	"github.com/gaarutyunov/mcp-anything/pkg/openapi"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	"github.com/gaarutyunov/mcp-anything/pkg/transform"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

// Executor executes an HTTP-backed tool by running the full request pipeline:
// request transform → URL build → request validation → HTTP call → response validation → response transform.
type Executor struct {
	entry *pkgupstream.RegistryEntry
}

// Execute runs the HTTP request pipeline for the given tool args.
func (e *Executor) Execute(ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, error) {
	entry := e.entry
	toolAttrs := metric.WithAttributes(attribute.String("mcp.tool.name", entry.PrefixedName))

	// Get the typed transforms (set by the HTTP builder).
	transforms, ok := entry.Transforms.(*transform.CompiledTransforms)
	if !ok || transforms == nil {
		return nil, fmt.Errorf("invalid transforms for tool %q: expected *transform.CompiledTransforms", entry.PrefixedName)
	}

	// Apply request transform jq → RequestEnvelope.
	reqStart := time.Now()
	envelope, err := transforms.RunRequest(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("request transform: %w", err)
	}
	if pkgtelemetry.TransformDuration != nil {
		pkgtelemetry.TransformDuration.Record(ctx, time.Since(reqStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", entry.PrefixedName),
				attribute.String("transform.stage", "request"),
			),
		)
	}

	// Build upstream URL from the envelope.
	upstreamURL, err := buildUpstreamURL(entry.Upstream.BaseURL, entry.PathTemplate, envelope)
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
	httpReq, err := nethttp.NewRequestWithContext(ctx, entry.Method, upstreamURL, bodyReader)
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

	// Get the typed validator (set by the HTTP builder; nil for non-HTTP tools).
	validator, _ := entry.Validator.(*openapi.Validator)

	// Validate the outbound request against the OpenAPI spec (if configured).
	// When only response validation is enabled, we still build the route metadata
	// (BuildRequestInput) so that ValidateResponse has the required context.
	var reqInput *openapi3filter.RequestValidationInput
	if validator != nil {
		if entry.ValidationCfg.ValidateRequest {
			ri, valErr := validator.ValidateRequest(ctx, httpReq)
			if valErr != nil {
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("request validation failed: %v", valErr)},
					},
				}, nil
			}
			reqInput = ri
		} else if entry.ValidationCfg.ValidateResponse {
			ri, routeErr := validator.BuildRequestInput(httpReq)
			if routeErr != nil {
				slog.Warn("could not resolve route for response validation", "tool", entry.PrefixedName, "error", routeErr)
			} else {
				reqInput = ri
			}
		}
	}

	// Inject outbound auth — no-op for now; TASK-10 fills this in.

	// Execute the upstream HTTP call.
	resp, err := entry.Upstream.Client.Do(httpReq)
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

	inSuccess := statusIn(entry.ValidationCfg.SuccessStatus, resp.StatusCode)
	inError := statusIn(entry.ValidationCfg.ErrorStatus, resp.StatusCode)

	if !inSuccess && !inError {
		result := &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprintf("unexpected HTTP %d", resp.StatusCode)},
			},
		}
		if pkgtelemetry.ToolCallErrors != nil {
			pkgtelemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	contentType := resp.Header.Get("Content-Type")

	if inError {
		errStart := time.Now()
		result := buildErrorResult(ctx, transforms, resp.StatusCode, contentType, body)
		if pkgtelemetry.TransformDuration != nil {
			pkgtelemetry.TransformDuration.Record(ctx, time.Since(errStart).Seconds(),
				metric.WithAttributes(
					attribute.String("mcp.tool.name", entry.PrefixedName),
					attribute.String("transform.stage", "error"),
				),
			)
		}
		if pkgtelemetry.ToolCallErrors != nil {
			pkgtelemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
		}
		return result, nil
	}

	// Success path: validate response if configured.
	if entry.ValidationCfg.ValidateResponse && reqInput != nil && validator != nil {
		if valErr := validator.ValidateResponse(ctx, reqInput, resp, body); valErr != nil {
			if entry.ValidationCfg.ResponseValidationFailure == "fail" {
				result := &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("response validation failed: %v", valErr)},
					},
				}
				if pkgtelemetry.ToolCallErrors != nil {
					pkgtelemetry.ToolCallErrors.Add(ctx, 1, toolAttrs)
				}
				return result, nil
			}
			slog.Warn("response validation failed", "tool", entry.PrefixedName, "error", valErr)
		}
	}

	respStart := time.Now()
	result := buildSuccessResult(ctx, transforms, entry.ResponseFormat, contentType, body)
	if pkgtelemetry.TransformDuration != nil {
		pkgtelemetry.TransformDuration.Record(ctx, time.Since(respStart).Seconds(),
			metric.WithAttributes(
				attribute.String("mcp.tool.name", entry.PrefixedName),
				attribute.String("transform.stage", "response"),
			),
		)
	}
	return result, nil
}

// buildErrorResult transforms an error response body and returns an error CallToolResult.
func buildErrorResult(ctx context.Context, transforms *transform.CompiledTransforms, statusCode int, contentType string, body []byte) *sdkmcp.CallToolResult {
	return content.ToErrorResult(ctx, body, contentType, statusCode, transforms.Error)
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
