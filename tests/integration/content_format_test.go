//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// contentFormatSpec is a minimal OpenAPI spec covering all endpoints used by content format tests.
const contentFormatSpec = `openapi: "3.0.0"
info:
  title: Content Format API
  version: "1.0"
paths:
  /photo:
    get:
      operationId: getPhoto
      summary: Get a photo
      responses:
        "200":
          description: OK
  /file:
    get:
      operationId: getFile
      summary: Get a file
      responses:
        "200":
          description: OK
  /orders:
    post:
      operationId: createOrder
      summary: Create an order
      responses:
        "201":
          description: Created
        "422":
          description: Unprocessable Entity
  /broken:
    get:
      operationId: getBroken
      summary: Broken endpoint
      responses:
        "500":
          description: Internal Server Error
`

// startContentFormatProxy writes spec, optional overlay, and config to a temp dir,
// then starts the proxy container. Returns the proxy URL.
func startContentFormatProxy(ctx context.Context, t *testing.T, netName, overlayYAML string) string {
	t.Helper()
	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(contentFormatSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`

	if overlayYAML != "" {
		cfgContent += `    overlay:
      source: /etc/mcp-anything/overlay.yaml
`
	}

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}

	if overlayYAML != "" {
		overlayPath := filepath.Join(tmpDir, "overlay.yaml")
		if err := os.WriteFile(overlayPath, []byte(overlayYAML), 0o644); err != nil {
			t.Fatalf("write overlay: %v", err)
		}
		proxyReq.Files = append(proxyReq.Files, testcontainers.ContainerFile{
			HostFilePath:      overlayPath,
			ContainerFilePath: "/etc/mcp-anything/overlay.yaml",
			FileMode:          0o644,
		})
	}

	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	proxyPort, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())
}

// TestImageResponseReturnedAsImageContent verifies that x-mcp-response-format: image
// causes binary image responses to be returned as ImageContent with correct MIMEType.
func TestImageResponseReturnedAsImageContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, wiremockURL := startWireMock(ctx, t, net.Name)

	// A minimal 1x1 transparent PNG encoded as base64.
	pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	registerStub(t, wiremockURL, fmt.Sprintf(`{
		"request": {"method": "GET", "url": "/photo"},
		"response": {
			"status": 200,
			"base64Body": %q,
			"headers": {"Content-Type": "image/png"}
		}
	}`, pngBase64))

	overlay := `overlay: 1.0.0
info:
  title: Image format overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/photo"].get
    update:
      x-mcp-response-format: image
`

	proxyURL := startContentFormatProxy(ctx, t, net.Name, overlay)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_photo"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", contentText(result.Content))
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	ic, ok := result.Content[0].(*sdkmcp.ImageContent)
	if !ok {
		t.Fatalf("expected *ImageContent, got %T (text: %s)", result.Content[0], contentText(result.Content))
	}
	if ic.MIMEType != "image/png" {
		t.Errorf("expected MIMEType image/png, got %s", ic.MIMEType)
	}
	if len(ic.Data) == 0 {
		t.Error("expected non-empty image Data")
	}
}

// TestAutoDetectBinaryAsBinary verifies that x-mcp-response-format: auto with
// application/octet-stream Content-Type returns EmbeddedResource content.
func TestAutoDetectBinaryAsBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, wiremockURL := startWireMock(ctx, t, net.Name)

	// Some binary data encoded as base64.
	binaryBase64 := "AQIDBA==" // [0x01, 0x02, 0x03, 0x04]
	registerStub(t, wiremockURL, fmt.Sprintf(`{
		"request": {"method": "GET", "url": "/file"},
		"response": {
			"status": 200,
			"base64Body": %q,
			"headers": {"Content-Type": "application/octet-stream"}
		}
	}`, binaryBase64))

	overlay := `overlay: 1.0.0
info:
  title: Auto format overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/file"].get
    update:
      x-mcp-response-format: auto
`

	proxyURL := startContentFormatProxy(ctx, t, net.Name, overlay)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_file"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", contentText(result.Content))
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	er, ok := result.Content[0].(*sdkmcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected *EmbeddedResource, got %T (text: %s)", result.Content[0], contentText(result.Content))
	}
	if er.Resource == nil {
		t.Fatal("expected non-nil Resource")
	}
	if er.Resource.MIMEType != "application/octet-stream" {
		t.Errorf("expected application/octet-stream, got %s", er.Resource.MIMEType)
	}
}

// TestProblemJSONErrorParsed verifies that application/problem+json error bodies
// are parsed and the default error transform extracts title/detail/status.
func TestProblemJSONErrorParsed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, wiremockURL := startWireMock(ctx, t, net.Name)

	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/orders"},
		"response": {
			"status": 422,
			"jsonBody": {"type": "urn:example:bad-input", "title": "Invalid order", "status": 422, "detail": "quantity must be > 0"},
			"headers": {"Content-Type": "application/problem+json"}
		}
	}`)

	proxyURL := startContentFormatProxy(ctx, t, net.Name, "")
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__create_order"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Invalid order") {
		t.Errorf("expected 'Invalid order' in error text, got: %s", text)
	}
	if !strings.Contains(text, "quantity must be > 0") {
		t.Errorf("expected 'quantity must be > 0' in error text, got: %s", text)
	}
}

// TestCustomErrorTransform verifies that x-mcp-error-transform is applied to error responses.
func TestCustomErrorTransform(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, wiremockURL := startWireMock(ctx, t, net.Name)

	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/orders"},
		"response": {
			"status": 422,
			"jsonBody": {"type": "urn:example:bad-input", "title": "Invalid order", "status": 422, "detail": "quantity must be > 0"},
			"headers": {"Content-Type": "application/problem+json"}
		}
	}`)

	overlay := `overlay: 1.0.0
info:
  title: Custom error transform overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/orders"].post
    update:
      x-mcp-error-transform: '{code: .status, message: .title, hint: .detail}'
`

	proxyURL := startContentFormatProxy(ctx, t, net.Name, overlay)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__create_order"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "code") {
		t.Errorf("expected 'code' in error text, got: %s", text)
	}
	if !strings.Contains(text, "422") {
		t.Errorf("expected '422' in error text, got: %s", text)
	}
	if !strings.Contains(text, "message") {
		t.Errorf("expected 'message' in error text, got: %s", text)
	}
	if !strings.Contains(text, "Invalid order") {
		t.Errorf("expected 'Invalid order' in error text, got: %s", text)
	}
	if !strings.Contains(text, "hint") {
		t.Errorf("expected 'hint' in error text, got: %s", text)
	}
	if !strings.Contains(text, "quantity must be > 0") {
		t.Errorf("expected 'quantity must be > 0' in error text, got: %s", text)
	}
}

// TestPlainTextErrorResponse verifies that non-JSON error bodies are handled correctly.
func TestPlainTextErrorResponse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, wiremockURL := startWireMock(ctx, t, net.Name)

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/broken"},
		"response": {
			"status": 500,
			"body": "Internal Server Error",
			"headers": {"Content-Type": "text/plain"}
		}
	}`)

	proxyURL := startContentFormatProxy(ctx, t, net.Name, "")
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_broken"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Internal Server Error") {
		t.Errorf("expected 'Internal Server Error' in error text, got: %s", text)
	}
}
