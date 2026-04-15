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

// specV1 has a single GET /pets operation.
const specV1 = `openapi: "3.0.0"
info:
  title: Refresh Test API
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

// specV2 adds GET /orders alongside GET /pets.
const specV2 = `openapi: "3.0.0"
info:
  title: Refresh Test API
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

// refreshConfig returns a proxy config that fetches the OpenAPI spec from WireMock.
func refreshConfig(refreshInterval, maxFailures string) string {
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
      source: http://wiremock:8080/specs/openapi.yaml
      version: "3.0"
      refresh_interval: %s
      max_refresh_failures: %s
`, refreshInterval, maxFailures)
}

// startRefreshProxy starts a proxy container with a URL-based OpenAPI spec and background refresh.
func startRefreshProxy(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork, cfg string) (testcontainers.Container, string) {
	t.Helper()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
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

// updateWireMockStub updates (replaces) a WireMock stub by ID.
func updateWireMockStub(ctx context.Context, t *testing.T, base, stubID, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/__admin/mappings/"+stubID, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build update stub request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update wiremock stub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update wiremock stub: got %d: %s", resp.StatusCode, b)
	}
}

// registerStubGetID registers a WireMock stub and returns its assigned ID.
func registerStubGetID(ctx context.Context, t *testing.T, base, body string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/__admin/mappings", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build register stub request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register wiremock stub: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register wiremock stub: got %d: %s", resp.StatusCode, b)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("parse stub registration response: %v", err)
	}
	return result.ID
}

// wiremockSpecRequests returns the If-None-Match header values seen in WireMock requests
// to the given URL path.
func wiremockSpecRequests(ctx context.Context, t *testing.T, base, path string) []string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__admin/requests", nil)
	if err != nil {
		t.Fatalf("build wiremock requests request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
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
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("parse wiremock requests: %v", err)
	}

	var ifNoneMatchValues []string
	for _, req := range result.Requests {
		if req.Request.URL != path {
			continue
		}
		for k, v := range req.Request.Headers {
			if k == "If-None-Match" {
				ifNoneMatchValues = append(ifNoneMatchValues, v)
			}
		}
	}
	return ifNoneMatchValues
}

// assertHTTPStatusBody asserts the status code and optionally checks the body contains a substring.
func assertHTTPStatusBody(ctx context.Context, t *testing.T, url string, wantStatus int, wantBodyContains string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s request: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Errorf("GET %s: expected %d, got %d; body: %s", url, wantStatus, resp.StatusCode, body)
	}
	if wantBodyContains != "" && !bytes.Contains(body, []byte(wantBodyContains)) {
		t.Errorf("GET %s body: expected to contain %q, got %q", url, wantBodyContains, body)
	}
}

// TestBackgroundRefreshAddsNewTool verifies that a changed spec (new ETag) is detected
// by the background refresh goroutine and the new tool appears in tools/list.
func TestBackgroundRefreshAddsNewTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremock(ctx, t, net)

	// Register spec v1 stub.
	specStubID := registerStubGetID(ctx, t, wiremockURL, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV1)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"v1\""}
		}
	}`)

	// Register upstream stubs for pets API.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{}","headers": {"Content-Type": "application/json"}}
	}`)

	// Start proxy with 500ms refresh interval.
	_, proxyURL := startRefreshProxy(ctx, t, net, refreshConfig("500ms", "5"))

	// Connect MCP client.
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

	// Update WireMock stub to return spec v2 with new ETag.
	// When the proxy sends If-None-Match: "v1", the server returns 200 with v2.
	updateWireMockStub(ctx, t, wiremockURL, specStubID, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV2)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"v2\""}
		}
	}`)

	// Register upstream stub for orders API.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/orders"},
		"response": {"status": 200, "body": "{}","headers": {"Content-Type": "application/json"}}
	}`)

	// Poll until 2 tools appear (up to 5 seconds).
	updatedTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 2
	}, 5*time.Second)

	if len(updatedTools) != 2 {
		t.Fatalf("expected 2 tools after refresh, got %d: %v", len(updatedTools), toolNames(updatedTools))
	}
	nameSet := make(map[string]bool, len(updatedTools))
	for _, tool := range updatedTools {
		nameSet[tool.Name] = true
	}
	if !nameSet["pets__list_pets"] {
		t.Errorf("missing pets__list_pets; got: %v", toolNames(updatedTools))
	}
	if !nameSet["pets__list_orders"] {
		t.Errorf("missing pets__list_orders; got: %v", toolNames(updatedTools))
	}
}

// TestConditionalGetSkipsUnchangedSpec verifies that when the spec server returns 304,
// the proxy does not reload the spec and sends If-None-Match on subsequent requests.
func TestConditionalGetSkipsUnchangedSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremock(ctx, t, net)

	// Register spec stub returning ETag "stable".
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV1)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"stable\""}
		}
	}`)

	// Register a 304 stub for conditional GET — matches when If-None-Match header is present.
	// WireMock evaluates the more specific stub (with header match) first.
	registerStub(t, wiremockURL, `{
		"request": {
			"method": "GET",
			"url": "/specs/openapi.yaml",
			"headers": {"If-None-Match": {"equalTo": "\"stable\""}}
		},
		"response": {"status": 304},
		"priority": 1
	}`)

	// Register upstream stub.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{}","headers": {"Content-Type": "application/json"}}
	}`)

	// Start proxy with 300ms refresh interval (short enough to fire quickly).
	_, proxyURL := startRefreshProxy(ctx, t, net, refreshConfig("300ms", "5"))

	// Connect MCP client.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Verify initial tool list.
	initialTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools (initial): %v", err)
	}
	if len(initialTools.Tools) != 1 {
		t.Fatalf("expected 1 initial tool, got %d", len(initialTools.Tools))
	}

	// Poll WireMock journal until at least one conditional request is seen.
	var ifNoneMatchValues []string
	pollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(pollDeadline) {
		ifNoneMatchValues = wiremockSpecRequests(ctx, t, wiremockURL, "/specs/openapi.yaml")
		if len(ifNoneMatchValues) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Check WireMock journal: the second request to the spec URL should include If-None-Match.
	if len(ifNoneMatchValues) == 0 {
		t.Error("expected proxy to send If-None-Match header on spec refresh, but none seen in journal")
	} else {
		foundStable := false
		for _, v := range ifNoneMatchValues {
			if v == `"stable"` {
				foundStable = true
				break
			}
		}
		if !foundStable {
			t.Errorf("expected If-None-Match: \"stable\" in journal, got: %v", ifNoneMatchValues)
		}
	}

	// Tools list should still be 1 (spec unchanged).
	afterTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools after 304: %v", err)
	}
	if len(afterTools.Tools) != 1 {
		t.Errorf("expected 1 tool after 304 refresh, got %d: %v", len(afterTools.Tools), toolNames(afterTools.Tools))
	}
}

// TestMaxRefreshFailuresRemovesTools verifies that after max_refresh_failures consecutive
// failures, tools are removed from tools/list and /readyz returns 503.
func TestMaxRefreshFailuresRemovesTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremock(ctx, t, net)

	// Register spec stub for initial successful load.
	specStubID := registerStubGetID(ctx, t, wiremockURL, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV1)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"v1\""}
		}
	}`)

	// Register upstream stub.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{}","headers": {"Content-Type": "application/json"}}
	}`)

	// Start proxy with 200ms refresh interval and max_refresh_failures=3.
	_, proxyURL := startRefreshProxy(ctx, t, net, refreshConfig("200ms", "3"))

	// Connect MCP client.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Assert initial state: 1 tool, readyz 200.
	initialTools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools (initial): %v", err)
	}
	if len(initialTools.Tools) != 1 {
		t.Fatalf("expected 1 initial tool, got %d: %v", len(initialTools.Tools), toolNames(initialTools.Tools))
	}
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)

	// Make spec server return 500 to simulate failures.
	updateWireMockStub(ctx, t, wiremockURL, specStubID, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {"status": 500, "body": "internal server error"}
	}`)

	// Poll until tools are removed (max_refresh_failures exceeded).
	degradedTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 0
	}, 5*time.Second)
	if len(degradedTools) != 0 {
		t.Errorf("expected 0 tools after max_refresh_failures, got %d: %v", len(degradedTools), toolNames(degradedTools))
	}

	// /readyz should return 503.
	assertHTTPStatusBody(ctx, t, proxyURL+"/readyz", http.StatusServiceUnavailable, "pets")
}

// TestRefreshRecoveryAfterFailures verifies that the proxy recovers when the spec server
// becomes available again after having exceeded max_refresh_failures.
func TestRefreshRecoveryAfterFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremock(ctx, t, net)

	// Register spec stub for initial load.
	specStubID := registerStubGetID(ctx, t, wiremockURL, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV1)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"v1\""}
		}
	}`)

	// Register upstream stub.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{}","headers": {"Content-Type": "application/json"}}
	}`)

	// Start proxy with 200ms refresh interval and max_refresh_failures=3.
	_, proxyURL := startRefreshProxy(ctx, t, net, refreshConfig("200ms", "3"))

	// Connect MCP client.
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
		t.Fatalf("expected 1 initial tool, got %d", len(initialTools.Tools))
	}

	// Break the spec server.
	updateWireMockStub(ctx, t, wiremockURL, specStubID, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {"status": 500, "body": "error"}
	}`)

	// Poll until tools are removed (degradation).
	pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) == 0
	}, 5*time.Second)
	assertHTTPStatusBody(ctx, t, proxyURL+"/readyz", http.StatusServiceUnavailable, "pets")

	// Restore the spec server.
	updateWireMockStub(ctx, t, wiremockURL, specStubID, `{
		"request": {"method": "GET", "url": "/specs/openapi.yaml"},
		"response": {
			"status": 200,
			"body": `+jsonEscape(specV1)+`,
			"headers": {"Content-Type": "application/yaml", "ETag": "\"v1-restored\""}
		}
	}`)

	// Poll until tools come back (up to 5 seconds).
	recoveredTools := pollToolsUntil(t, session, callCtx, func(tools []*sdkmcp.Tool) bool {
		return len(tools) > 0
	}, 5*time.Second)

	if len(recoveredTools) == 0 {
		t.Fatal("expected tools to recover after spec server restored, but still 0 tools")
	}

	// /readyz should return 200 again.
	// Poll readyz since the failure counter reset happens after a successful refresh.
	readyzDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(readyzDeadline) {
		req, _ := http.NewRequestWithContext(callCtx, http.MethodGet, proxyURL+"/readyz", nil)
		resp, getErr := http.DefaultClient.Do(req)
		if getErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)
}
