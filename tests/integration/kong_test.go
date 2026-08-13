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

// kongContainerRequest returns a ContainerRequest for the Kong+mcp-anything plugin binary.
// If KONG_IMAGE is set, it pulls that pre-built image. Otherwise it builds from
// Dockerfile.kong using the local source tree.
func kongContainerRequest() testcontainers.ContainerRequest {
	if img := os.Getenv("KONG_IMAGE"); img != "" {
		return testcontainers.ContainerRequest{Image: img}
	}
	return testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "../..",
			Dockerfile: "Dockerfile.kong",
		},
	}
}

// TestKongPlugin verifies that:
//  1. The Kong container starts and serves MCP endpoints via the Go PDK plugin.
//  2. tools/list returns the expected tool names.
//  3. tools/call proxies through to WireMock and returns the correct response.
func TestKongPlugin(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Shared bridge network so Kong can reach WireMock by its network alias.
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := net.Remove(context.Background()); removeErr != nil {
			t.Logf("remove network: %v", removeErr)
		}
	})

	// 1. Start a fresh WireMock instance (own clean instance per test to avoid stub conflicts).
	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{
		"request":  {"method": "GET", "url": "/pets"},
		"response": {"status": 200,
		             "jsonBody": {"pets": [{"id": "1", "name": "Kong Cat"}]},
		             "headers": {"Content-Type": "application/json"}}
	}`)

	// 2. Write mcp-anything config and OpenAPI spec to a temp directory.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	cfgPath := filepath.Join(dir, "config.yaml")
	kongCfgPath := filepath.Join(dir, "kong.yaml")

	if err := os.WriteFile(specPath, []byte(kongTestSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(kongTestMCPConfig), 0o644); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}
	if err := os.WriteFile(kongCfgPath, []byte(kongDeclarativeConfig), 0o644); err != nil {
		t.Fatalf("write kong config: %v", err)
	}

	// 3. Start Kong container with the mcp-anything plugin binary embedded.
	kongReq := kongContainerRequest()
	kongReq.ExposedPorts = []string{"8000/tcp", "8001/tcp"}
	kongReq.Networks = []string{net.Name}
	kongReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: kongCfgPath, ContainerFilePath: "/etc/kong/kong.yaml", FileMode: 0o644},
	}
	// Wait for Kong admin API to signal readiness.
	kongReq.WaitingFor = wait.ForHTTP("/status").WithPort("8001").WithStartupTimeout(120 * time.Second)

	kong := startContainer(ctx, t, kongReq)

	kongHost, err := kong.Host(ctx)
	if err != nil {
		t.Fatalf("get kong host: %v", err)
	}
	kongPort, err := kong.MappedPort(ctx, "8000")
	if err != nil {
		t.Fatalf("get kong proxy port: %v", err)
	}
	kongBaseURL := fmt.Sprintf("http://%s:%s", kongHost, kongPort.Port())

	// 4. Connect MCP client to Kong proxy port.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: kongBaseURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "kong-test-client", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// 5. Assert tools/list returns the expected tool.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := toolNames(toolsResult.Tools)
	if len(names) != 1 || names[0] != "kong__list_pets" {
		t.Fatalf("expected [kong__list_pets], got %v", names)
	}

	// 6. Assert tools/call proxies through to WireMock and returns the correct body.
	callResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "kong__list_pets"})
	if err != nil {
		t.Fatalf("CallTool kong__list_pets: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("CallTool returned error: %s", contentText(callResult.Content))
	}
	text := contentText(callResult.Content)
	if !strings.Contains(text, "Kong Cat") {
		t.Errorf("CallTool response missing 'Kong Cat': %s", text)
	}
}

// ── test fixtures ──────────────────────────────────────────────────────────────

const kongTestSpec = `openapi: "3.0.0"
info:
  title: Kong Test API
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
`

const kongTestMCPConfig = `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: kong-mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: kong
    enabled: true
    tool_prefix: kong
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`

// kongDeclarativeConfig is the Kong DB-less declarative configuration.
// The upstream URL is intentionally unreachable; the mcp-anything plugin
// intercepts all /mcp requests via kong.Response.Exit before Kong proxies
// to the upstream.
const kongDeclarativeConfig = `_format_version: "3.0"

services:
  - name: mcp-anything
    url: http://localhost:1
    routes:
      - name: mcp-route
        paths:
          - /mcp
        strip_path: false

plugins:
  - name: mcp-anything
    service: mcp-anything
    config:
      config_path: /etc/mcp-anything/config.yaml
`
