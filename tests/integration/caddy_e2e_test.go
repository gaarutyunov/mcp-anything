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

// caddyContainerRequest returns a ContainerRequest for the Caddy+mcp-anything binary.
// If CADDY_IMAGE is set, it pulls that pre-built image. Otherwise it builds from
// Dockerfile.caddy using the local source tree.
func caddyContainerRequest() testcontainers.ContainerRequest {
	if img := os.Getenv("CADDY_IMAGE"); img != "" {
		return testcontainers.ContainerRequest{Image: img}
	}
	return testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "../..",
			Dockerfile: "Dockerfile.caddy",
		},
	}
}

// TestCaddyModule verifies that:
//  1. The Caddy container starts and serves MCP endpoints.
//  2. tools/list returns the expected tool names.
//  3. tools/call proxies through to WireMock and returns the correct response.
//  4. Config hot-reload via fsnotify causes the tool list to update.
func TestCaddyModule(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Shared bridge network so the Caddy container can reach WireMock by alias.
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := net.Remove(context.Background()); removeErr != nil {
			t.Logf("remove network: %v", removeErr)
		}
	})

	// 1. Start WireMock (own clean instance per acceptance criterion).
	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{
		"request":  {"method": "GET", "url": "/pets"},
		"response": {"status": 200,
		             "jsonBody": {"pets": [{"id": "1", "name": "Caddy Cat"}]},
		             "headers": {"Content-Type": "application/json"}}
	}`)

	// 2. Write config files to a temp directory.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	cfgPath := filepath.Join(dir, "config.yaml")
	caddyfilePath := filepath.Join(dir, "Caddyfile")

	if err := os.WriteFile(specPath, []byte(caddyTestSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(caddyTestConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(caddyfilePath, []byte(caddyfileContent), 0o644); err != nil {
		t.Fatalf("write Caddyfile: %v", err)
	}

	// 3. Start Caddy container.
	caddyReq := caddyContainerRequest()
	caddyReq.ExposedPorts = []string{"8080/tcp"}
	caddyReq.Networks = []string{net.Name}
	caddyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: caddyfilePath, ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0o644},
	}
	caddyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)

	caddy := startContainer(ctx, t, caddyReq)

	caddyHost, err := caddy.Host(ctx)
	if err != nil {
		t.Fatalf("get caddy host: %v", err)
	}
	caddyPort, err := caddy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get caddy port: %v", err)
	}
	caddyBaseURL := fmt.Sprintf("http://%s:%s", caddyHost, caddyPort.Port())

	// 4. Connect MCP client.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: caddyBaseURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "caddy-test-client", Version: "v0.0.1"}, nil)

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
	if len(names) != 1 || names[0] != "caddy__listpets" {
		t.Fatalf("expected [caddy__listpets], got %v", names)
	}

	// 6. Assert tools/call proxies to WireMock correctly.
	callResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "caddy__listpets"})
	if err != nil {
		t.Fatalf("CallTool caddy__listpets: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("CallTool returned error: %s", contentText(callResult.Content))
	}
	text := contentText(callResult.Content)
	if !strings.Contains(text, "Caddy Cat") {
		t.Errorf("CallTool response missing 'Caddy Cat': %s", text)
	}

	// 7. Hot-reload: copy a new spec with an additional operation and update config.
	copyToContainer(ctx, t, caddy, "/etc/mcp-anything/spec_v2.yaml", []byte(caddyTestSpecV2))
	copyToContainer(ctx, t, caddy, "/etc/mcp-anything/config.yaml", []byte(caddyTestConfigV2))

	// Poll until 2 tools appear (up to 10 s).
	reloadedTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 2
	}, 10*time.Second)

	if len(reloadedTools) != 2 {
		t.Fatalf("expected 2 tools after hot-reload, got %d: %v", len(reloadedTools), toolNames(reloadedTools))
	}
}

// ── test fixtures ──────────────────────────────────────────────────────────────

const caddyTestSpec = `openapi: "3.0.0"
info:
  title: Caddy Test API
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

const caddyTestSpecV2 = `openapi: "3.0.0"
info:
  title: Caddy Test API
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
  /orders:
    get:
      operationId: listOrders
      summary: List all orders
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

const caddyTestConfig = `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: caddy-mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: caddy
    enabled: true
    tool_prefix: caddy
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`

const caddyTestConfigV2 = `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: caddy-mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: caddy
    enabled: true
    tool_prefix: caddy
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec_v2.yaml
      version: "3.0"
`

const caddyfileContent = `{
	order mcpanything before respond
	auto_https off
}

:8080 {
	handle /healthz {
		respond "OK" 200
	}

	handle /mcp* {
		mcpanything {
			config_path /etc/mcp-anything/config.yaml
		}
	}
}
`
