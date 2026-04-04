package content

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/itchyny/gojq"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDetect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured Format
		ctHeader   string
		want       Format
	}{
		{name: "auto json", configured: FormatAuto, ctHeader: "application/json", want: FormatJSON},
		{name: "auto json with params", configured: FormatAuto, ctHeader: "application/json; charset=utf-8", want: FormatJSON},
		{name: "auto problem json", configured: FormatAuto, ctHeader: "application/problem+json", want: FormatJSON},
		{name: "auto fhir json", configured: FormatAuto, ctHeader: "application/fhir+json", want: FormatJSON},
		{name: "auto pdf", configured: FormatAuto, ctHeader: "application/pdf", want: FormatBinary},
		{name: "auto octet stream", configured: FormatAuto, ctHeader: "application/octet-stream", want: FormatBinary},
		{name: "auto png", configured: FormatAuto, ctHeader: "image/png", want: FormatImage},
		{name: "auto jpeg", configured: FormatAuto, ctHeader: "image/jpeg", want: FormatImage},
		{name: "auto mp3", configured: FormatAuto, ctHeader: "audio/mp3", want: FormatAudio},
		{name: "auto mpeg audio", configured: FormatAuto, ctHeader: "audio/mpeg", want: FormatAudio},
		{name: "auto text plain", configured: FormatAuto, ctHeader: "text/plain", want: FormatText},
		{name: "auto text html", configured: FormatAuto, ctHeader: "text/html", want: FormatText},
		// Explicit overrides auto-detection.
		{name: "explicit image overrides json ct", configured: FormatImage, ctHeader: "application/json", want: FormatImage},
		{name: "explicit text overrides binary ct", configured: FormatText, ctHeader: "application/octet-stream", want: FormatText},
		{name: "explicit json overrides image ct", configured: FormatJSON, ctHeader: "image/png", want: FormatJSON},
		{name: "explicit binary stays binary", configured: FormatBinary, ctHeader: "text/plain", want: FormatBinary},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Detect(tc.configured, tc.ctHeader)
			if got != tc.want {
				t.Errorf("Detect(%q, %q) = %q, want %q", tc.configured, tc.ctHeader, got, tc.want)
			}
		})
	}
}

func compileJq(t *testing.T, expr string) *gojq.Code {
	t.Helper()
	q, err := gojq.Parse(expr)
	if err != nil {
		t.Fatalf("parse jq %q: %v", expr, err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		t.Fatalf("compile jq %q: %v", expr, err)
	}
	return code
}

func TestToMCPContent_JSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte(`{"name":"Fido","species":"dog"}`)
	identity := compileJq(t, ".")

	contents, err := ToMCPContent(ctx, FormatJSON, body, "application/json", identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	tc, ok := contents[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", contents[0])
	}
	if !strings.Contains(tc.Text, "Fido") {
		t.Errorf("expected Fido in text, got: %s", tc.Text)
	}
}

func TestToMCPContent_JSONTransform(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte(`{"name":"Fido","species":"dog"}`)
	xform := compileJq(t, "{pet: .name}")

	contents, err := ToMCPContent(ctx, FormatJSON, body, "application/json", xform)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := contents[0].(*sdkmcp.TextContent)
	if !strings.Contains(tc.Text, "pet") || !strings.Contains(tc.Text, "Fido") {
		t.Errorf("unexpected text: %s", tc.Text)
	}
	if strings.Contains(tc.Text, "species") {
		t.Errorf("species should be filtered out: %s", tc.Text)
	}
}

func TestToMCPContent_Text(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte("hello world")
	contents, err := ToMCPContent(ctx, FormatText, body, "text/plain", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := contents[0].(*sdkmcp.TextContent)
	if tc.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", tc.Text)
	}
}

func TestToMCPContent_TextWithTransform(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte("hello")
	xform := compileJq(t, `"got: " + .`)

	contents, err := ToMCPContent(ctx, FormatText, body, "text/plain", xform)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := contents[0].(*sdkmcp.TextContent)
	if tc.Text != "got: hello" {
		t.Errorf("expected 'got: hello', got %q", tc.Text)
	}
}

func TestToMCPContent_Image(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte{0x89, 0x50, 0x4e, 0x47} // PNG header bytes
	contents, err := ToMCPContent(ctx, FormatImage, body, "image/png", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ic, ok := contents[0].(*sdkmcp.ImageContent)
	if !ok {
		t.Fatalf("expected *ImageContent, got %T", contents[0])
	}
	if ic.MIMEType != "image/png" {
		t.Errorf("expected image/png, got %s", ic.MIMEType)
	}
	if len(ic.Data) == 0 {
		t.Error("expected non-empty image data")
	}
}

func TestToMCPContent_Audio(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte{0x49, 0x44, 0x33} // ID3 header bytes
	contents, err := ToMCPContent(ctx, FormatAudio, body, "audio/mpeg", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac, ok := contents[0].(*sdkmcp.AudioContent)
	if !ok {
		t.Fatalf("expected *AudioContent, got %T", contents[0])
	}
	if ac.MIMEType != "audio/mpeg" {
		t.Errorf("expected audio/mpeg, got %s", ac.MIMEType)
	}
	if len(ac.Data) == 0 {
		t.Error("expected non-empty audio data")
	}
}

func TestToMCPContent_Binary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte{0x25, 0x50, 0x44, 0x46} // %PDF header
	contents, err := ToMCPContent(ctx, FormatBinary, body, "application/pdf", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	er, ok := contents[0].(*sdkmcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected *EmbeddedResource, got %T", contents[0])
	}
	if er.Resource == nil {
		t.Fatal("expected non-nil Resource")
	}
	if er.Resource.MIMEType != "application/pdf" {
		t.Errorf("expected application/pdf, got %s", er.Resource.MIMEType)
	}
	if len(er.Resource.Blob) == 0 {
		t.Error("expected non-empty blob")
	}
}

func TestToMCPContent_MIMEParamsStripped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte{0x01, 0x02}
	contents, err := ToMCPContent(ctx, FormatImage, body, "image/png; charset=utf-8", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ic := contents[0].(*sdkmcp.ImageContent)
	if ic.MIMEType != "image/png" {
		t.Errorf("expected stripped mime 'image/png', got %q", ic.MIMEType)
	}
}

// ParseErrorBody tests

func TestParseErrorBody_ProblemJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"urn:example:error","title":"Not Found","status":404,"detail":"item missing","instance":"/items/1"}`)
	result := ParseErrorBody(body, "application/problem+json")

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["title"] != "Not Found" {
		t.Errorf("expected title 'Not Found', got %v", m["title"])
	}
	if m["detail"] != "item missing" {
		t.Errorf("expected detail 'item missing', got %v", m["detail"])
	}
	if m["status"] != 404 {
		t.Errorf("expected status 404, got %v", m["status"])
	}
}

func TestParseErrorBody_ProblemJSONWithParams(t *testing.T) {
	t.Parallel()

	body := []byte(`{"title":"Bad Request","status":400}`)
	result := ParseErrorBody(body, "application/problem+json; charset=utf-8")

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["title"] != "Bad Request" {
		t.Errorf("expected 'Bad Request', got %v", m["title"])
	}
}

func TestParseErrorBody_RegularJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":"forbidden","code":403}`)
	result := ParseErrorBody(body, "application/json")

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["error"] != "forbidden" {
		t.Errorf("expected 'forbidden', got %v", m["error"])
	}
}

func TestParseErrorBody_PlainText(t *testing.T) {
	t.Parallel()

	body := []byte("Internal Server Error")
	result := ParseErrorBody(body, "text/plain")

	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if s != "Internal Server Error" {
		t.Errorf("expected 'Internal Server Error', got %q", s)
	}
}

func TestParseErrorBody_NonJSONFallback(t *testing.T) {
	t.Parallel()

	body := []byte("not valid json {{{")
	result := ParseErrorBody(body, "application/json")

	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string fallback, got %T", result)
	}
	if s != "not valid json {{{" {
		t.Errorf("expected raw string, got %q", s)
	}
}

func TestToErrorResult_ProblemJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte(`{"title":"Invalid order","status":422,"detail":"quantity must be > 0"}`)
	errTransform := compileJq(t, `if .title then {error: .title, detail: (.detail // ""), status: (.status // 0)} else {error: ("upstream error: HTTP " + (if .status then (.status | tostring) else "unknown" end)), body: .} end`)

	result := ToErrorResult(ctx, body, "application/problem+json", 422, errTransform)

	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	tc, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", result.Content[0])
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v\ntext: %s", err, tc.Text)
	}
	if out["error"] != "Invalid order" {
		t.Errorf("expected error='Invalid order', got %v", out["error"])
	}
	if out["detail"] != "quantity must be > 0" {
		t.Errorf("expected detail='quantity must be > 0', got %v", out["detail"])
	}
}
