package mcp

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

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// New creates an MCP server and registers all provided tools.
func New(impl *sdkmcp.Implementation, tools []*openapi.ValidatedTool, upstream *config.UpstreamConfig, client *http.Client) *sdkmcp.Server {
	srv := sdkmcp.NewServer(impl, nil)

	for _, vt := range tools {
		registerTool(srv, vt, upstream, client)
	}

	return srv
}

// registerTool registers a single validated tool with the MCP server.
func registerTool(srv *sdkmcp.Server, vt *openapi.ValidatedTool, upstream *config.UpstreamConfig, client *http.Client) {
	tool := vt // capture loop variable
	srv.AddTool(tool.MCPTool, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		// Parse arguments from the request.
		args, err := parseArguments(req.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("parsing tool arguments: %w", err)
		}

		// Run the request transform jq to get the request envelope.
		envelope, err := tool.Transforms.RunRequest(ctx, args)
		if err != nil {
			return nil, fmt.Errorf("request transform: %w", err)
		}

		// Build the upstream URL from the envelope.
		upstreamURL, err := buildURL(upstream.BaseURL, tool.PathTemplate, envelope)
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

		// Make the HTTP request.
		httpReq, err := http.NewRequestWithContext(ctx, tool.Method, upstreamURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("creating HTTP request: %w", err)
		}

		// Add configured headers.
		for k, v := range upstream.Headers {
			httpReq.Header.Set(k, v)
		}
		// Add envelope headers (override configured headers).
		for k, v := range envelope.Headers {
			httpReq.Header.Set(k, v)
		}

		if bodyReader != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(httpReq)
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

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return errorResult(ctx, tool.Transforms, resp.StatusCode, body), nil
		}

		return successResult(ctx, tool.Transforms, body), nil
	})
}

// errorResult transforms the error response body and returns an error CallToolResult.
func errorResult(ctx context.Context, transforms *transform.CompiledTransforms, statusCode int, body []byte) *sdkmcp.CallToolResult {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// If body is not JSON, wrap it.
		parsed = map[string]any{"status": statusCode, "body": string(body)}
	} else if m, ok := parsed.(map[string]any); ok && m["status"] == nil {
		m["status"] = statusCode
	}

	transformed, err := transforms.RunError(ctx, parsed)
	if err != nil {
		slog.Warn("error transform failed, using raw body", "error", err)
		transformed = map[string]any{"error": fmt.Sprintf("upstream returned %d", statusCode), "body": string(body)}
	}

	text := marshalToString(transformed)
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// successResult transforms the success response body and returns a CallToolResult.
func successResult(ctx context.Context, transforms *transform.CompiledTransforms, body []byte) *sdkmcp.CallToolResult {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// If body is not JSON, return it as-is.
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}
	}

	transformed, err := transforms.RunResponse(ctx, parsed)
	if err != nil {
		slog.Warn("response transform failed, using raw body", "error", err)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}
	}

	text := marshalToString(transformed)
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// marshalToString marshals v to a JSON string, or returns a string representation on error.
func marshalToString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// parseArguments unmarshals the tool call arguments into a string map.
func parseArguments(raw any) (map[string]any, error) {
	if raw == nil {
		return make(map[string]any), nil
	}

	// Arguments is json.RawMessage after server-side unmarshalling.
	b, ok := raw.(json.RawMessage)
	if !ok {
		// Try JSON round-trip for other types.
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-marshalling arguments: %w", err)
		}
		b = data
	}

	if len(b) == 0 || string(b) == "null" {
		return make(map[string]any), nil
	}

	var args map[string]any
	if err := json.Unmarshal(b, &args); err != nil {
		return nil, fmt.Errorf("unmarshalling arguments: %w", err)
	}
	return args, nil
}

// buildURL constructs the upstream URL using the request envelope.
// Path parameters are substituted into the template; query parameters are appended.
func buildURL(baseURL, pathTemplate string, envelope *transform.RequestEnvelope) (string, error) {
	// Substitute path parameters.
	path := pathTemplate
	for name, val := range envelope.Path {
		path = strings.ReplaceAll(path, "{"+name+"}", val)
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	// Append non-empty query parameters.
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
