package mcp

import (
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
)

// New creates an MCP server and registers all provided tools.
func New(impl *sdkmcp.Implementation, tools []*openapi.GeneratedTool, upstream *config.UpstreamConfig, client *http.Client) *sdkmcp.Server {
	srv := sdkmcp.NewServer(impl, nil)

	for _, gt := range tools {
		registerTool(srv, gt, upstream, client)
	}

	return srv
}

// registerTool registers a single generated tool with the MCP server.
func registerTool(srv *sdkmcp.Server, gt *openapi.GeneratedTool, upstream *config.UpstreamConfig, client *http.Client) {
	tool := gt // capture loop variable
	srv.AddTool(tool.MCPTool, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		// Parse arguments from the request.
		args, err := parseArguments(req.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("parsing tool arguments: %w", err)
		}

		// Build the upstream URL.
		upstreamURL, err := buildURL(upstream.BaseURL, tool.PathTemplate, tool.PathParams, tool.QueryParams, args)
		if err != nil {
			return nil, fmt.Errorf("building upstream URL: %w", err)
		}

		// Make the HTTP request.
		httpReq, err := http.NewRequestWithContext(ctx, tool.Method, upstreamURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating HTTP request: %w", err)
		}

		// Add configured headers.
		for k, v := range upstream.Headers {
			httpReq.Header.Set(k, v)
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
			return &sdkmcp.CallToolResult{
				IsError: true,
				Content: []sdkmcp.Content{
					&sdkmcp.TextContent{
						Text: fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body)),
					},
				},
			}, nil
		}

		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: string(body)},
			},
		}, nil
	})
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

// buildURL constructs the upstream URL by substituting path params and
// appending query params.
func buildURL(baseURL, pathTemplate string, pathParams []string, queryParams []openapi.ParamInfo, args map[string]any) (string, error) {
	// Replace path parameters.
	path := pathTemplate
	for _, name := range pathParams {
		val, ok := args[name]
		if !ok {
			return "", fmt.Errorf("missing required path parameter %q", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", fmt.Sprintf("%v", val))
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	// Append query parameters.
	if len(queryParams) > 0 {
		q := u.Query()
		for _, qp := range queryParams {
			if val, ok := args[qp.Name]; ok {
				q.Set(qp.Name, fmt.Sprintf("%v", val))
			}
		}
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}
