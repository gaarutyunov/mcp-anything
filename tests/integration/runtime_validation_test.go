//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

const validationPetsSpec = `openapi: "3.0.0"
info:
  title: Pets API
  version: "1.0"
paths:
  /pets/{petId}:
    get:
      operationId: getPet
      summary: Get a pet by ID
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, name]
                properties:
                  id:
                    type: integer
                  name:
                    type: string
`

const validationListPetsSpec = `openapi: "3.0.0"
info:
  title: Pets API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [pets]
                properties:
                  pets:
                    type: array
                    items:
                      type: object
                      required: [id, name]
                      properties:
                        id:
                          type: integer
                        name:
                          type: string
`

func buildValidationConfig(validateReq, validateResp bool, failureMode string) string {
	return fmt.Sprintf(`server:
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
    validation:
      validate_request: %v
      validate_response: %v
      response_validation_failure: %s
`, validateReq, validateResp, failureMode)
}

// startValidationProxy writes spec/config to a temp dir and starts the proxy container.
// Returns the proxy URL.
func startValidationProxy(ctx context.Context, t *testing.T, netName, specYAML, cfgYAML string) string {
	t.Helper()
	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
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

// connectValidationMCPClient connects an MCP client with a 60s timeout.
func connectValidationMCPClient(t *testing.T, proxyURL string) (*sdkmcp.ClientSession, context.CancelFunc) {
	t.Helper()
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session, cancel
}

// wireMockRequestCount returns the number of requests WireMock has received.
func wireMockRequestCount(t *testing.T, base string) int {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("get wiremock requests: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read wiremock requests: %v", err)
	}
	var result struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("parse wiremock requests: %v", err)
	}
	return len(result.Requests)
}

// startWireMock starts a WireMock container on the given network and returns the container
// and its external URL.
func startWireMock(ctx context.Context, t *testing.T, netName string) (testcontainers.Container, string) {
	t.Helper()
	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{netName},
		NetworkAliases: map[string][]string{
			netName: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	host, err := wm.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	port, err := wm.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	return wm, fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestRequestValidationBlocksInvalidArgs verifies that calling a tool without
// a required parameter causes IsError: true and zero upstream requests.
func TestRequestValidationBlocksInvalidArgs(t *testing.T) {
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

	// Register a catch-all stub so WireMock can respond if the proxy incorrectly forwards.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "urlPathPattern": "/pets/.*"},
		"response": {"status": 200, "body": "{\"id\":1,\"name\":\"Fido\"}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfg := buildValidationConfig(true, false, "warn")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	// Call the tool with empty arguments — petId is required but omitted.
	result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "test__getpet",
		Arguments: map[string]any{},
	})
	if callErr != nil {
		// MCP SDK may surface the error at the transport level — acceptable.
		t.Logf("CallTool returned transport error (acceptable): %v", callErr)
	} else if !result.IsError {
		t.Errorf("expected IsError=true for missing required parameter, got success: %s", contentText(result.Content))
	}

	// Validation (either MCP SDK or openapi3filter) must have blocked the upstream call.
	count := wireMockRequestCount(t, wiremockURL)
	if count != 0 {
		t.Errorf("expected 0 WireMock requests (validation should block), got %d", count)
	}
}

// TestResponseValidationWarnMode verifies that a schema-violating response is returned
// successfully when response_validation_failure is "warn".
func TestResponseValidationWarnMode(t *testing.T) {
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

	// Return id as string — violates schema (id should be integer).
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":\"not-an-integer\",\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfg := buildValidationConfig(true, true, "warn")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationListPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__listpets",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	// In warn mode, schema violation must not cause IsError.
	if result.IsError {
		t.Errorf("expected successful result in warn mode, got IsError=true: %s", contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Fido") {
		t.Errorf("expected response to contain 'Fido', got: %s", text)
	}
}

// TestResponseValidationFailMode verifies that a schema-violating response returns
// IsError: true when response_validation_failure is "fail".
func TestResponseValidationFailMode(t *testing.T) {
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

	// Return id as string — violates schema.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":\"not-an-integer\",\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfg := buildValidationConfig(true, true, "fail")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationListPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__listpets",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected IsError=true in fail mode, got success: %s", contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(strings.ToLower(text), "validation") && !strings.Contains(strings.ToLower(text), "invalid") {
		t.Errorf("expected error message to mention validation failure, got: %s", text)
	}
}

// TestResponseValidationOnlyWarnMode verifies that response validation works independently
// of request validation. With ValidateRequest=false and ValidateResponse=true in warn mode,
// a schema-violating response should still return success (warning is logged only).
func TestResponseValidationOnlyWarnMode(t *testing.T) {
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

	// Return id as string — violates schema (id should be integer).
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":\"not-an-integer\",\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Request validation disabled, response validation enabled in warn mode.
	cfg := buildValidationConfig(false, true, "warn")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationListPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__listpets",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	// In warn mode, schema violation must not cause IsError even when request validation is off.
	if result.IsError {
		t.Errorf("expected successful result in response-only warn mode, got IsError=true: %s", contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Fido") {
		t.Errorf("expected response to contain 'Fido', got: %s", text)
	}
}

// TestResponseValidationOnlyFailMode verifies that response validation works independently
// of request validation. With ValidateRequest=false and ValidateResponse=true in fail mode,
// a schema-violating response must return IsError: true.
func TestResponseValidationOnlyFailMode(t *testing.T) {
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

	// Return id as string — violates schema (id should be integer).
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":\"not-an-integer\",\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Request validation disabled, response validation enabled in fail mode.
	cfg := buildValidationConfig(false, true, "fail")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationListPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__listpets",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	// In fail mode, schema violation must cause IsError even when request validation is off.
	if !result.IsError {
		t.Errorf("expected IsError=true in response-only fail mode, got success: %s", contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(strings.ToLower(text), "validation") && !strings.Contains(strings.ToLower(text), "invalid") {
		t.Errorf("expected error message to mention validation failure, got: %s", text)
	}
}

// TestUnexpectedStatusReturnsError verifies that an HTTP status not in success_status
// or error_status returns IsError: true.
func TestUnexpectedStatusReturnsError(t *testing.T) {
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

	// Return HTTP 418 — not in the default success or error lists.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 418, "body": "I'm a teapot", "headers": {"Content-Type": "text/plain"}}
	}`)

	cfg := buildValidationConfig(false, false, "warn")
	proxyURL := startValidationProxy(ctx, t, net.Name, validationListPetsSpec, cfg)

	session, cancel := connectValidationMCPClient(t, proxyURL)
	defer cancel()

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__listpets",
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected IsError=true for unexpected HTTP 418, got success")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "418") {
		t.Errorf("expected error message to mention status 418, got: %s", text)
	}
}
