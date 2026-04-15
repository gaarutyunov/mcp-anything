//go:build integration

package integration_test

import (
	"context"
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

// petsOnlySpec has a single GET /pets operation.
const petsOnlySpec = `openapi: "3.0.0"
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
`

// petsAndOrdersSpec has GET /pets and GET /orders.
const petsAndOrdersSpec = `openapi: "3.0.0"
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

// twoToolsSpec has GET /pets and GET /cats (both operations present from the start).
const twoToolsSpec = `openapi: "3.0.0"
info:
  title: Test API
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
  /cats:
    get:
      operationId: listCats
      summary: List all cats
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// reloadConfig returns a config YAML string that uses the given spec filename.
func reloadConfig(specFile string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: pets
    enabled: true
    tool_prefix: pets
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/%s
      version: "3.0"
`, specFile)
}

// copyToContainer writes content to a file inside the running container.
// This triggers an inotify event in the container's filesystem.
func copyToContainer(ctx context.Context, t *testing.T, c testcontainers.Container, containerPath string, content []byte) {
	t.Helper()
	if err := c.CopyToContainer(ctx, content, containerPath, 0o644); err != nil {
		t.Fatalf("copy to container %s: %v", containerPath, err)
	}
}

// startReloadProxy writes initial config files to a temp dir, copies them into the proxy
// container at startup, and returns the proxy container and its base URL.
func startReloadProxy(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork, initialSpec, initialConfig string) (testcontainers.Container, string) {
	t.Helper()

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(specPath, []byte(initialSpec), 0o644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(initialConfig), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

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
	return proxy, fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())
}

// pollToolsUntil polls tools/list every 100 ms (up to timeout) until predicate returns true.
func pollToolsUntil(t *testing.T, session *sdkmcp.ClientSession, callCtx context.Context, predicate func([]*sdkmcp.Tool) bool, timeout time.Duration) []*sdkmcp.Tool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := session.ListTools(callCtx, nil)
		if err == nil && predicate(result.Tools) {
			return result.Tools
		}
		time.Sleep(100 * time.Millisecond)
	}
	result, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools after poll timeout: %v", err)
	}
	return result.Tools
}

// readReloadMetrics fetches the /metrics/reload endpoint and returns the body.
func readReloadMetrics(t *testing.T, proxyURL string) string {
	t.Helper()
	resp, err := http.Get(proxyURL + "/metrics/reload") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET /metrics/reload: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics/reload body: %v", err)
	}
	return string(b)
}

// startWiremock starts a WireMock container and returns its external URL.
func startWiremock(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork) string {
	t.Helper()
	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, err := wm.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	wmPort, err := wm.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
}

// TestConfigHotReloadAddsNewTool verifies that adding a new operation to the spec
// causes the proxy to expose an additional MCP tool without restart.
func TestConfigHotReloadAddsNewTool(t *testing.T) {
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

	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	proxy, proxyURL := startReloadProxy(ctx, t, net, petsOnlySpec, reloadConfig("spec.yaml"))

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Assert initial tool list has 1 tool.
	initialTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools (initial): %v", err)
	}
	if len(initialTools.Tools) != 1 {
		t.Fatalf("expected 1 initial tool, got %d: %v", len(initialTools.Tools), toolNames(initialTools.Tools))
	}
	if initialTools.Tools[0].Name != "pets__list_pets" {
		t.Errorf("expected tool pets__list_pets, got %s", initialTools.Tools[0].Name)
	}

	// Trigger reload: copy updated spec with new operation, then update config.
	copyToContainer(ctx, t, proxy, "/etc/mcp-anything/spec_v2.yaml", []byte(petsAndOrdersSpec))
	copyToContainer(ctx, t, proxy, "/etc/mcp-anything/config.yaml", []byte(reloadConfig("spec_v2.yaml")))

	// Poll until 2 tools appear (up to 5 seconds).
	reloadedTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 2
	}, 5*time.Second)

	if len(reloadedTools) != 2 {
		t.Fatalf("expected 2 tools after reload, got %d: %v", len(reloadedTools), toolNames(reloadedTools))
	}
	nameSet := make(map[string]bool, len(reloadedTools))
	for _, tool := range reloadedTools {
		nameSet[tool.Name] = true
	}
	if !nameSet["pets__list_pets"] {
		t.Errorf("missing tool pets__list_pets after reload; got: %v", toolNames(reloadedTools))
	}
	if !nameSet["pets__list_orders"] {
		t.Errorf("missing tool pets__list_orders after reload; got: %v", toolNames(reloadedTools))
	}
}

// TestConfigHotReloadRemovesTool verifies that removing an operation from the spec
// causes the proxy to remove the corresponding MCP tool without restart.
func TestConfigHotReloadRemovesTool(t *testing.T) {
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

	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	proxy, proxyURL := startReloadProxy(ctx, t, net, twoToolsSpec, reloadConfig("spec.yaml"))

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Assert initial tool list has 2 tools.
	initialTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools (initial): %v", err)
	}
	if len(initialTools.Tools) != 2 {
		t.Fatalf("expected 2 initial tools, got %d: %v", len(initialTools.Tools), toolNames(initialTools.Tools))
	}

	// Reload with spec that has only 1 tool.
	copyToContainer(ctx, t, proxy, "/etc/mcp-anything/spec_v2.yaml", []byte(petsOnlySpec))
	copyToContainer(ctx, t, proxy, "/etc/mcp-anything/config.yaml", []byte(reloadConfig("spec_v2.yaml")))

	// Poll until 1 tool remains (up to 5 seconds).
	reloadedTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 1
	}, 5*time.Second)

	if len(reloadedTools) != 1 {
		t.Fatalf("expected 1 tool after reload, got %d: %v", len(reloadedTools), toolNames(reloadedTools))
	}
	if reloadedTools[0].Name != "pets__list_pets" {
		t.Errorf("expected pets__list_pets to remain, got %s", reloadedTools[0].Name)
	}
}

// TestInvalidConfigReloadKeepsOldConfig verifies that writing invalid YAML does not
// replace the active config: the old tools remain available and /readyz stays 200.
func TestInvalidConfigReloadKeepsOldConfig(t *testing.T) {
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

	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	proxy, proxyURL := startReloadProxy(ctx, t, net, petsOnlySpec, reloadConfig("spec.yaml"))

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Verify initial state.
	initialTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools (initial): %v", err)
	}
	if len(initialTools.Tools) != 1 {
		t.Fatalf("expected 1 initial tool, got %d: %v", len(initialTools.Tools), toolNames(initialTools.Tools))
	}

	// Trigger a failed reload: valid YAML but references a nonexistent spec file so
	// manager.Rebuild fails during upstream validation.
	invalidConfig := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: pets
    enabled: true
    tool_prefix: pets
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/nonexistent.yaml
      version: "3.0"
`
	copyToContainer(ctx, t, proxy, "/etc/mcp-anything/config.yaml", []byte(invalidConfig))

	// Wait for the reload attempt to settle (debounce 500ms + processing time).
	time.Sleep(2 * time.Second)

	// Old tools still present.
	afterTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools after failed reload: %v", err)
	}
	if len(afterTools.Tools) != 1 {
		t.Fatalf("expected 1 tool after failed reload, got %d: %v", len(afterTools.Tools), toolNames(afterTools.Tools))
	}
	if afterTools.Tools[0].Name != "pets__list_pets" {
		t.Errorf("expected pets__list_pets to remain, got %s", afterTools.Tools[0].Name)
	}

	// /readyz must return 200 (AC-30.4).
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)

	// Reload error counter must be > 0.
	metrics := readReloadMetrics(t, proxyURL)
	if metrics == "" {
		t.Fatal("empty /metrics/reload response")
	}
	var totalCount, errorCount int
	if _, scanErr := fmt.Sscanf(metrics, "mcp_anything_config_reload_total %d\nmcp_anything_config_reload_errors_total %d", &totalCount, &errorCount); scanErr != nil {
		t.Fatalf("parsing reload metrics %q: %v", metrics, scanErr)
	}
	if errorCount == 0 {
		t.Errorf("expected reload error counter > 0 after failed reload, got 0; metrics: %s", metrics)
	}
}

// TestReloadRespectsDebounce verifies that writing multiple config files in rapid
// succession results in only a single reload, thanks to the 500 ms debounce.
func TestReloadRespectsDebounce(t *testing.T) {
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

	wiremockURL := startWiremock(ctx, t, net)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	proxy, proxyURL := startReloadProxy(ctx, t, net, petsOnlySpec, reloadConfig("spec.yaml"))

	// Read baseline reload total before triggering.
	baseMetrics := readReloadMetrics(t, proxyURL)
	var baseTotal int
	fmt.Sscanf(baseMetrics, "mcp_anything_config_reload_total %d", &baseTotal) //nolint:errcheck // best-effort parse

	// Write 5 config files in rapid succession (simulating ConfigMap churn).
	// Each write triggers a fsnotify event; debounce should collapse them into one reload.
	for i := 0; i < 5; i++ {
		copyToContainer(ctx, t, proxy, "/etc/mcp-anything/config.yaml", []byte(reloadConfig("spec.yaml")))
		time.Sleep(50 * time.Millisecond) // well within the 500 ms debounce window
	}

	// Wait for the single debounced reload to complete (debounce 500ms + processing).
	time.Sleep(3 * time.Second)

	afterMetrics := readReloadMetrics(t, proxyURL)
	var afterTotal int
	fmt.Sscanf(afterMetrics, "mcp_anything_config_reload_total %d", &afterTotal) //nolint:errcheck // best-effort parse

	reloads := afterTotal - baseTotal
	if reloads == 0 {
		t.Errorf("expected at least 1 reload, got 0; metrics: %s", afterMetrics)
	}
	// Allow up to 3 reloads (debounce is best-effort; exact count may vary by platform and container I/O speed).
	if reloads > 3 {
		t.Errorf("expected at most 3 reloads for 5 rapid writes (debounce), got %d; metrics: %s", reloads, afterMetrics)
	}
}
