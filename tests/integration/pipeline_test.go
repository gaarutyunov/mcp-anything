//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const overlayTestOpenAPISpec = `openapi: "3.0.0"
info:
  title: Pets API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
    post:
      operationId: createPet
      summary: Create a pet
      responses:
        "201":
          description: Created
  /pets/{petId}:
    delete:
      operationId: deletePet
      summary: Delete a pet
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: No Content
`

const overlayRemoveDeleteOp = `overlay: 1.0.0
info:
  title: Remove delete and rename list
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/pets/{petId}"].delete
    update:
      x-mcp-enabled: false
  - target: $.paths["/pets"].get
    update:
      x-mcp-tool-name: list_all_pets
`

func TestOverlayRemovesOperation(t *testing.T) {
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

	// Start WireMock.
	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})

	wiremockHost, err := wiremock.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	wiremockPort, err := wiremock.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "[]", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/pets"},
		"response": {"status": 201, "body": "{}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Write spec and overlay files.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(overlayTestOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	overlayPath := filepath.Join(tmpDir, "overlay.yaml")
	if err := os.WriteFile(overlayPath, []byte(overlayRemoveDeleteOp), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	cfgContent := fmt.Sprintf(`server:
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
    overlay:
      source: /etc/mcp-anything/overlay.yaml
`)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		{HostFilePath: overlayPath, ContainerFilePath: "/etc/mcp-anything/overlay.yaml", FileMode: 0o644},
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
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	nameSet := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	if !nameSet["test__list_all_pets"] {
		t.Errorf("expected tool test__list_all_pets, got %v", toolNames(toolsResult.Tools))
	}
	if !nameSet["test__create_pets"] {
		t.Errorf("expected tool test__create_pets, got %v", toolNames(toolsResult.Tools))
	}
}

func TestURLSpecLoadingWithAuthHeader(t *testing.T) {
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

	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})

	wiremockHost, err := wiremock.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	wiremockPort, err := wiremock.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	// Stub: /openapi.yaml with auth → spec; without auth → 401.
	registerStub(t, wiremockURL, `{
		"request": {
			"method": "GET",
			"url": "/openapi.yaml",
			"headers": {"Authorization": {"equalTo": "Bearer test-token"}}
		},
		"response": {
			"status": 200,
			"body": `+jsonEscape(testOpenAPISpec)+`,
			"headers": {"Content-Type": "application/yaml"}
		}
	}`)
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/openapi.yaml"},
		"response": {"status": 401, "body": "Unauthorized"}
	}`)

	// Stubs for the API itself.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "[]", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
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
      source: http://wiremock:8080/openapi.yaml
      auth_header: "Bearer test-token"
      version: "3.0"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
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
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Fatal("expected at least 1 tool, got 0")
	}

	// Verify the auth header was sent to WireMock.
	resp, err := http.Get(wiremockURL + "/__admin/requests") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("get wiremock requests: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read requests: %v", err)
	}
	var result struct {
		Requests []struct {
			Request struct {
				Headers map[string]string `json:"headers"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse requests: %v", err)
	}

	authSent := false
	for _, r := range result.Requests {
		if r.Request.Headers["Authorization"] == "Bearer test-token" {
			authSent = true
			break
		}
	}
	if !authSent {
		t.Error("expected Authorization: Bearer test-token header to be sent to WireMock")
	}
}

func TestOverlayFromURL(t *testing.T) {
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

	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})

	wiremockHost, err := wiremock.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	wiremockPort, err := wiremock.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	// Serve spec from WireMock.
	registerStub(t, wiremockURL, fmt.Sprintf(`{
		"request": {"method": "GET", "url": "/spec.yaml"},
		"response": {
			"status": 200,
			"body": %s,
			"headers": {"Content-Type": "application/yaml"}
		}
	}`, jsonEscape(overlayTestOpenAPISpec)))

	// Overlay URL requires auth.
	registerStub(t, wiremockURL, fmt.Sprintf(`{
		"request": {
			"method": "GET",
			"url": "/overlay.yaml",
			"headers": {"Authorization": {"equalTo": "Bearer overlay-token"}}
		},
		"response": {
			"status": 200,
			"body": %s,
			"headers": {"Content-Type": "application/yaml"}
		}
	}`, jsonEscape(overlayRemoveDeleteOp)))

	// Stubs for actual API calls.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "[]", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
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
      source: http://wiremock:8080/spec.yaml
      version: "3.0"
    overlay:
      source: http://wiremock:8080/overlay.yaml
      auth_header: "Bearer overlay-token"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
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
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	// Overlay removes DELETE and renames GET→list_all_pets; only 2 tools remain.
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	nameSet := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	if !nameSet["test__list_all_pets"] {
		t.Errorf("expected test__list_all_pets, got %v", toolNames(toolsResult.Tools))
	}
}

func TestInvalidOverlayFails(t *testing.T) {
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

	// Invalid overlay: missing required info field.
	invalidOverlay := `overlay: 1.0.0
actions:
  - target: $.paths
    remove: true
`

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(overlayTestOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	overlayPath := filepath.Join(tmpDir, "overlay.yaml")
	if err := os.WriteFile(overlayPath, []byte(invalidOverlay), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	cfgContent := fmt.Sprintf(`server:
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
    base_url: http://localhost:9999
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    overlay:
      source: /etc/mcp-anything/overlay.yaml
`)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		{HostFilePath: overlayPath, ContainerFilePath: "/etc/mcp-anything/overlay.yaml", FileMode: 0o644},
	}
	// Use a short startup timeout: the container should fail, not start successfully.
	proxyReq.WaitingFor = wait.ForExit().WithExitTimeout(30 * time.Second)

	_, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: proxyReq,
		Started:          true,
	})
	if err == nil {
		t.Error("expected proxy to fail to start with invalid overlay, but it started successfully")
	}
}

func TestInvalidSpecFails(t *testing.T) {
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

	// Invalid spec: references a non-existent $ref.
	invalidSpec := `openapi: "3.0.0"
info:
  title: Invalid API
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
                $ref: "#/components/schemas/NonExistent"
`

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(invalidSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgContent := fmt.Sprintf(`server:
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
    base_url: http://localhost:9999
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForExit().WithExitTimeout(30 * time.Second)

	_, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: proxyReq,
		Started:          true,
	})
	if err == nil {
		t.Error("expected proxy to fail to start with invalid spec, but it started successfully")
	}
}

// jsonEscape returns a JSON string literal for embedding inside JSON bodies.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// wiremockRequestHeaders fetches the WireMock request journal and returns all Authorization headers seen.
func wiremockRequestHeaders(t *testing.T, base string) []string {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("get wiremock requests: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read requests body: %v", err)
	}

	var result struct {
		Requests []struct {
			Request struct {
				Headers map[string]string `json:"headers"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse requests: %v", err)
	}

	var headers []string
	for _, r := range result.Requests {
		if h := r.Request.Headers["Authorization"]; h != "" {
			headers = append(headers, h)
		}
	}
	return headers
}

// registerWireMockStubJSON is an alias for clarity in this test file.
func registerWireMockStubJSON(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/__admin/mappings", "application/json", bytes.NewBufferString(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("register wiremock stub: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register wiremock stub: got %d: %s", resp.StatusCode, b)
	}
}

var _ = wiremockRequestHeaders
var _ = registerWireMockStubJSON
