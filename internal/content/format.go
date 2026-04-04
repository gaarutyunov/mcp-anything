package content

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Format represents the x-mcp-response-format extension value.
type Format string

// Response format constants for x-mcp-response-format.
const (
	FormatJSON   Format = "json"
	FormatText   Format = "text"
	FormatImage  Format = "image"
	FormatAudio  Format = "audio"
	FormatBinary Format = "binary"
	FormatAuto   Format = "auto"
)

// Detect returns the effective format based on the configured format and
// the actual Content-Type header from the upstream response.
// If configured format is FormatAuto, it inspects contentType.
// Otherwise it returns the configured format.
func Detect(configured Format, contentType string) Format {
	if configured != FormatAuto {
		return configured
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		return FormatJSON
	case strings.HasPrefix(ct, "text/"):
		return FormatText
	case strings.HasPrefix(ct, "image/"):
		return FormatImage
	case strings.HasPrefix(ct, "audio/"):
		return FormatAudio
	default:
		return FormatBinary
	}
}

// ToMCPContent converts a response body to the appropriate MCP content type.
// For JSON and text formats, it returns TextContent.
// For image, audio, binary formats, it returns the appropriate MCP content.
// The responseTransform jq code is applied only for JSON and text formats.
// For binary formats, body is passed as raw bytes (the SDK handles base64 encoding in JSON).
func ToMCPContent(
	ctx context.Context,
	format Format,
	body []byte,
	contentType string,
	responseTransform *gojq.Code,
) ([]sdkmcp.Content, error) {
	mimeType := stripMIMEParams(contentType)
	switch format {
	case FormatJSON:
		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("unmarshal JSON body: %w", err)
		}
		if responseTransform != nil {
			transformed, err := runOnce(ctx, responseTransform, parsed)
			if err != nil {
				return nil, fmt.Errorf("response transform: %w", err)
			}
			parsed = transformed
		}
		return []sdkmcp.Content{&sdkmcp.TextContent{Text: marshalToString(parsed)}}, nil

	case FormatText:
		if responseTransform != nil {
			transformed, err := runOnce(ctx, responseTransform, string(body))
			if err != nil {
				return nil, fmt.Errorf("response transform: %w", err)
			}
			return []sdkmcp.Content{&sdkmcp.TextContent{Text: marshalToString(transformed)}}, nil
		}
		return []sdkmcp.Content{&sdkmcp.TextContent{Text: string(body)}}, nil

	case FormatImage:
		return []sdkmcp.Content{&sdkmcp.ImageContent{Data: body, MIMEType: mimeType}}, nil

	case FormatAudio:
		return []sdkmcp.Content{&sdkmcp.AudioContent{Data: body, MIMEType: mimeType}}, nil

	case FormatBinary:
		return []sdkmcp.Content{&sdkmcp.EmbeddedResource{
			Resource: &sdkmcp.ResourceContents{
				MIMEType: mimeType,
				Blob:     body,
			},
		}}, nil

	case FormatAuto:
		// FormatAuto should have been resolved by Detect before calling ToMCPContent.
		// Fall back to JSON as a safe default.
		return ToMCPContent(ctx, FormatJSON, body, contentType, responseTransform)
	default:
		return ToMCPContent(ctx, FormatJSON, body, contentType, responseTransform)
	}
}

// stripMIMEParams removes parameters from a Content-Type header value (splits on ";").
func stripMIMEParams(contentType string) string {
	return strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
}

// marshalToString marshals v to a JSON string without HTML escaping,
// or returns a string representation on error.
func marshalToString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	// json.Encoder.Encode appends a newline; trim it.
	return strings.TrimRight(buf.String(), "\n")
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
