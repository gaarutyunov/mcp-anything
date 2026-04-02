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
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// proxyContainerRequest returns a ContainerRequest for the proxy.
// If PROXY_IMAGE is set, it pulls that image. Otherwise, it builds from source using the Dockerfile.
func proxyContainerRequest() testcontainers.ContainerRequest {
	if img := os.Getenv("PROXY_IMAGE"); img != "" {
		return testcontainers.ContainerRequest{Image: img}
	}
	return testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "../..",
			Dockerfile: "Dockerfile",
		},
	}
}

const testOpenAPISpec = `openapi: "3.0.0"
info:
  title: Test API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  pets:
                    type: array
                    items:
                      type: object
  /pets/{petId}:
    get:
      operationId: getPet
      summary: Get a pet by ID
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
`

func TestMVPHappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Create a shared network so the proxy container can reach WireMock by alias.
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	// 1. Start WireMock container.
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
	wiremockExternalURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	// 2. Register WireMock stubs.
	registerStub(t, wiremockExternalURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockExternalURL, `{
		"request": {"method": "GET", "url": "/pets/1"},
		"response": {"status": 200, "body": "{\"id\":1,\"name\":\"Fido\",\"species\":\"dog\"}", "headers": {"Content-Type": "application/json"}}
	}`)

	// 3. Write OpenAPI spec and config to a temp dir that will be mounted into the proxy container.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(testOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec file: %v", err)
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
`)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	// 4. Start the proxy container (pre-built image via PROXY_IMAGE, or build from Dockerfile).
	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
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
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	// 5. Assert health endpoints.
	assertHTTPStatus(t, proxyURL+"/healthz", http.StatusOK)
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)

	// 6. Connect MCP client to the proxy.
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// 7. Assert tools/list returns exactly 2 tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	const toolListPets = "test__list_pets"
	const toolGetPet = "test__get_pets_petid"

	nameSet := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	if !nameSet[toolListPets] {
		t.Errorf("expected tool %s, got %v", toolListPets, toolNames(toolsResult.Tools))
	}
	if !nameSet[toolGetPet] {
		t.Errorf("expected tool %s, got %v", toolGetPet, toolNames(toolsResult.Tools))
	}

	// 8. Assert tools/call list_pets succeeds.
	listResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: toolListPets,
	})
	if err != nil {
		t.Fatalf("call %s: %v", toolListPets, err)
	}
	if listResult.IsError {
		t.Fatalf("%s returned error: %v", toolListPets, contentText(listResult.Content))
	}
	text := contentText(listResult.Content)
	if !strings.Contains(text, "Fido") {
		t.Errorf("%s response missing Fido: %s", toolListPets, text)
	}

	// 9. Assert tools/call get_pets_petid with petId=1 succeeds.
	getResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      toolGetPet,
		Arguments: map[string]any{"petId": "1"},
	})
	if err != nil {
		t.Fatalf("call %s: %v", toolGetPet, err)
	}
	if getResult.IsError {
		t.Fatalf("%s returned error: %v", toolGetPet, contentText(getResult.Content))
	}
	text = contentText(getResult.Content)
	if !strings.Contains(text, "dog") {
		t.Errorf("%s response missing species: %s", toolGetPet, text)
	}

	// 10. Verify WireMock received the requests.
	verifyWireMockRequests(t, wiremockExternalURL)
}

func startContainer(ctx context.Context, t *testing.T, req testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if c != nil {
			logContainerOutput(ctx, t, c)
		}
		t.Fatalf("start container %q: %v", containerName(req), err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Terminate(termCtx); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})
	return c
}

// logContainerOutput dumps the container's stdout+stderr logs to the test log.
// Useful for diagnosing startup failures when the container exits with a non-zero code.
func logContainerOutput(ctx context.Context, t *testing.T, c testcontainers.Container) {
	t.Helper()
	logs, err := c.Logs(ctx)
	if err != nil {
		t.Logf("failed to retrieve container logs: %v", err)
		return
	}
	defer func() { _ = logs.Close() }()
	b, err := io.ReadAll(logs)
	if err != nil {
		t.Logf("failed to read container logs: %v", err)
		return
	}
	t.Logf("=== container logs ===\n%s\n=== end container logs ===", string(b))
}

func containerName(req testcontainers.ContainerRequest) string {
	if req.Image != "" {
		return req.Image
	}
	return "Dockerfile"
}

func registerStub(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/__admin/mappings", "application/json", bytes.NewBufferString(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("register wiremock stub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register wiremock stub: got %d: %s", resp.StatusCode, b)
	}
}

func assertHTTPStatus(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Errorf("GET %s: expected %d, got %d", url, wantStatus, resp.StatusCode)
	}
}

func verifyWireMockRequests(t *testing.T, base string) {
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
		Requests []struct {
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("parse wiremock requests: %v", err)
	}
	if len(result.Requests) < 2 {
		t.Errorf("expected at least 2 wiremock requests, got %d", len(result.Requests))
	}
}

func toolNames(tools []*sdkmcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func contentText(content []sdkmcp.Content) string {
	for _, c := range content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
