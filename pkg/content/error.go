package content

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProblemDetail represents an RFC 9457 problem+json response body.
type ProblemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// ParseErrorBody parses an upstream error response body.
// If contentType is application/problem+json, it returns a ProblemDetail as map[string]any.
// Otherwise, it attempts JSON parse and returns the result, or falls back to raw string.
func ParseErrorBody(body []byte, contentType string) any {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))

	if ct == "application/problem+json" {
		var pd ProblemDetail
		if err := json.Unmarshal(body, &pd); err == nil {
			return map[string]any{
				"type":     pd.Type,
				"title":    pd.Title,
				"status":   pd.Status,
				"detail":   pd.Detail,
				"instance": pd.Instance,
			}
		}
	}

	var result any
	if err := json.Unmarshal(body, &result); err == nil {
		return result
	}

	return string(body)
}

// ToErrorResult builds a CallToolResult with IsError: true by running the error transform.
func ToErrorResult(
	ctx context.Context,
	body []byte,
	contentType string,
	statusCode int,
	errorTransform *gojq.Code,
) *sdkmcp.CallToolResult {
	parsed := ParseErrorBody(body, contentType)

	// Ensure status is set for map results.
	if m, ok := parsed.(map[string]any); ok {
		if m["status"] == nil {
			m["status"] = statusCode
		}
	}

	transformed, err := runOnce(ctx, errorTransform, parsed)
	if err != nil {
		transformed = map[string]any{
			"error": fmt.Sprintf("upstream returned %d", statusCode),
			"body":  string(body),
		}
	}

	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: marshalToString(transformed)},
		},
	}
}
