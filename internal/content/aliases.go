// Package content re-exports from pkg/content. See pkg/content for documentation.
package content

import (
	"context"

	"github.com/itchyny/gojq"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	pkgcontent "github.com/gaarutyunov/mcp-anything/pkg/content"
)

// Format represents the x-mcp-response-format extension value.
// See pkg/content.Format.
type Format = pkgcontent.Format

// ProblemDetail represents an RFC 9457 problem+json response body.
// See pkg/content.ProblemDetail.
type ProblemDetail = pkgcontent.ProblemDetail

// Response format constants for x-mcp-response-format.
const (
	FormatJSON   = pkgcontent.FormatJSON
	FormatText   = pkgcontent.FormatText
	FormatImage  = pkgcontent.FormatImage
	FormatAudio  = pkgcontent.FormatAudio
	FormatBinary = pkgcontent.FormatBinary
	FormatAuto   = pkgcontent.FormatAuto
)

// Detect returns the effective format based on the configured format and Content-Type header.
// See pkg/content.Detect.
func Detect(configured Format, contentType string) Format {
	return pkgcontent.Detect(configured, contentType)
}

// ToMCPContent converts a response body to the appropriate MCP content type.
// See pkg/content.ToMCPContent.
func ToMCPContent(
	ctx context.Context,
	format Format,
	body []byte,
	contentType string,
	responseTransform *gojq.Code,
) ([]sdkmcp.Content, error) {
	return pkgcontent.ToMCPContent(ctx, format, body, contentType, responseTransform)
}

// ParseErrorBody parses an upstream error response body.
// See pkg/content.ParseErrorBody.
func ParseErrorBody(body []byte, contentType string) any {
	return pkgcontent.ParseErrorBody(body, contentType)
}

// ToErrorResult builds a CallToolResult with IsError: true by running the error transform.
// See pkg/content.ToErrorResult.
func ToErrorResult(
	ctx context.Context,
	body []byte,
	contentType string,
	statusCode int,
	errorTransform *gojq.Code,
) *sdkmcp.CallToolResult {
	return pkgcontent.ToErrorResult(ctx, body, contentType, statusCode, errorTransform)
}
