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
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// groupsPetsSpec is the pets OpenAPI spec with x-mcp-safe annotations for group filter tests.
// GET /pets and GET /pets/{petId} have x-mcp-safe: true; POST /pets does not.
const groupsPetsSpec = `openapi: "3.0.0"
info:
  title: Pets API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: list_pets
      summary: List pets
      x-mcp-safe: true
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
    post:
      operationId: create_pets
      summary: Create a pet
      requestBody:
        required: false
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
          content:
            application/json:
              schema:
                type: object
  /pets/{petId}:
    get:
      operationId: get_pet
      summary: Get a pet by ID
      x-mcp-safe: true
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// groupsOrdersSpec is the orders OpenAPI spec for group filter tests.
const groupsOrdersSpec = `openapi: "3.0.0"
info:
  title: Orders API
  version: "1.0"
paths:
  /orders:
    get:
      operationId: list_orders
      summary: List orders
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  orders:
                    type: array
                    items:
                      type: object
    post:
      operationId: create_orders
      summary: Create order
      requestBody:
        required: false
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
          content:
            application/json:
              schema:
                type: object
`

// startGroupsProxy starts a proxy container with the given groups config.
// Returns the proxy container and its external base URL.
func startGroupsProxy(
	ctx context.Context, t *testing.T,
	tmpDir, netName string,
	cfgContent string,
) (testcontainers.Container, string) {
	t.Helper()

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	if err := os.WriteFile(petsSpecPath, []byte(groupsPetsSpec), 0o644); err != nil {
		t.Fatalf("write pets spec: %v", err)
	}
	ordersSpecPath := filepath.Join(tmpDir, "orders.yaml")
	if err := os.WriteFile(ordersSpecPath, []byte(groupsOrdersSpec), 0o644); err != nil {
		t.Fatalf("write orders spec: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: petsSpecPath, ContainerFilePath: "/etc/mcp-anything/pets.yaml", FileMode: 0o644},
		{HostFilePath: ordersSpecPath, ContainerFilePath: "/etc/mcp-anything/orders.yaml", FileMode: 0o644},
	}
	if netName != "" {
		proxyReq.Networks = []string{netName}
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)

	proxy := startContainer(ctx, t, proxyReq)
	host, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	port, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	return proxy, fmt.Sprintf("http://%s:%s", host, port.Port())
}

// connectMCPClientToEndpoint connects an MCP client to the given endpoint path.
func connectMCPClientToEndpoint(ctx context.Context, t *testing.T, proxyURL, endpoint string) *sdkmcp.ClientSession {
	t.Helper()
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + endpoint}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client to %s: %v", endpoint, err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// groupsConfig returns a config YAML string for the groups tests using two WireMock aliases.
func groupsConfig(petsAlias, ordersAlias string) string {
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
    base_url: http://%s:8080
    timeout: 10s
    validation:
      validate_request: false
      validate_response: false
    openapi:
      source: /etc/mcp-anything/pets.yaml
      version: "3.0"
  - name: orders
    enabled: true
    tool_prefix: orders
    base_url: http://%s:8080
    timeout: 10s
    validation:
      validate_request: false
      validate_response: false
    openapi:
      source: /etc/mcp-anything/orders.yaml
      version: "3.0"
groups:
  - name: all
    endpoint: /mcp
    upstreams: [pets, orders]
  - name: readonly
    endpoint: /mcp/readonly
    upstreams: [pets]
    filter: "$.paths.*[?(@['x-mcp-safe'] == true)]"
  - name: premium
    endpoint: /mcp/premium
    upstreams: [pets, orders]
    filter: "$.paths.*[?(@['x-mcp-tier'] == 'premium')]"
`, petsAlias, ordersAlias)
}

// startGroupsWireMocks starts two WireMock containers and registers minimal stubs.
// Returns (netName, petsURL, ordersURL).
func startGroupsWireMocks(ctx context.Context, t *testing.T) (string, string, string) {
	t.Helper()
	net, err := tcnetwork.New(ctx, tcnetwork.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-pets")
	_, ordersURL := startNamedWireMock(ctx, t, net.Name, "wiremock-orders")

	// Register stubs for pets upstream.
	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{\"id\":1,\"name\":\"Fido\"}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"POST","url":"/pets"},"response":{"status":201,"body":"{\"id\":2,\"name\":\"Rex\"}","headers":{"Content-Type":"application/json"}}}`)

	// Register stubs for orders upstream.
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[{\"id\":100}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{\"id\":101,\"status\":\"pending\"}","headers":{"Content-Type":"application/json"}}}`)

	return net.Name, petsURL, ordersURL
}

// TestAllGroupContainsAllTools verifies the /mcp endpoint (all group) returns all tools from both upstreams.
func TestAllGroupContainsAllTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName, _, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName, groupsConfig("wiremock-pets", "wiremock-orders"))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp")
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools at /mcp: %v", err)
	}

	// Expect 5 tools: list_pets, create_pets, get_pet (pets) + list_orders, create_orders (orders).
	wantTools := []string{"pets__list_pets", "pets__create_pets", "pets__get_pet", "orders__list_orders", "orders__create_orders"}
	if len(toolsResult.Tools) != len(wantTools) {
		t.Fatalf("expected %d tools at /mcp, got %d: %v", len(wantTools), len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	nameSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !nameSet[want] {
			t.Errorf("expected tool %s in /mcp list, got: %v", want, toolNames(toolsResult.Tools))
		}
	}
}

// TestReadonlyGroupFiltersCorrectly verifies the /mcp/readonly endpoint returns only
// GET operations with x-mcp-safe: true from the pets upstream.
func TestReadonlyGroupFiltersCorrectly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName, _, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName, groupsConfig("wiremock-pets", "wiremock-orders"))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp/readonly")
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools at /mcp/readonly: %v", err)
	}

	// Expect exactly 2 tools: list_pets and get_pet (both have x-mcp-safe: true).
	wantTools := []string{"pets__list_pets", "pets__get_pet"}
	if len(toolsResult.Tools) != len(wantTools) {
		t.Fatalf("expected %d tools at /mcp/readonly, got %d: %v", len(wantTools), len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	nameSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !nameSet[want] {
			t.Errorf("expected tool %s in /mcp/readonly, got: %v", want, toolNames(toolsResult.Tools))
		}
	}
	// Explicitly check excluded tools.
	for _, excluded := range []string{"pets__create_pets", "orders__list_orders", "orders__create_orders"} {
		if nameSet[excluded] {
			t.Errorf("tool %s should NOT be in /mcp/readonly, but was", excluded)
		}
	}
}

// TestPremiumGroupIsEmpty verifies the /mcp/premium endpoint returns 0 tools
// because no operations have x-mcp-tier: premium.
func TestPremiumGroupIsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName, _, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName, groupsConfig("wiremock-pets", "wiremock-orders"))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp/premium")
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools at /mcp/premium: %v", err)
	}

	if len(toolsResult.Tools) != 0 {
		t.Fatalf("expected 0 tools at /mcp/premium, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
}

// TestCrossGroupToolCallBlocked verifies that calling a tool not in a group via that group's
// endpoint returns an error (either via SDK error or IsError: true).
func TestCrossGroupToolCallBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName, _, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName, groupsConfig("wiremock-pets", "wiremock-orders"))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Connect to /mcp/readonly which only has list_pets and get_pet.
	// Try to call pets__create_pets which is NOT in the readonly group.
	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp/readonly")

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "pets__create_pets",
	})
	// The MCP SDK may reject an unregistered tool at the protocol level (err != nil)
	// or DispatchForGroup may return IsError=true — both are acceptable error responses.
	if err != nil {
		// SDK-level rejection is acceptable.
		return
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true when calling pets__create_pets via /mcp/readonly, got false; content: %s", contentText(result.Content))
	}
	errText := contentText(result.Content)
	if !strings.Contains(strings.ToLower(errText), "group") && !strings.Contains(strings.ToLower(errText), "unknown") {
		t.Errorf("expected group-related error message, got: %s", errText)
	}
}

// TestInvalidFilterIsStartupError verifies that a group with an invalid JSONPath filter
// causes the proxy to fail to start.
func TestInvalidFilterIsStartupError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tmpDir := t.TempDir()

	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	if err := os.WriteFile(petsSpecPath, []byte(groupsPetsSpec), 0o644); err != nil {
		t.Fatalf("write pets spec: %v", err)
	}

	cfgContent := `server:
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
    base_url: http://nowhere:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/pets.yaml
      version: "3.0"
groups:
  - name: bad
    endpoint: /mcp
    upstreams: [pets]
    filter: "this is not valid jsonpath !!!"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: petsSpecPath, ContainerFilePath: "/etc/mcp-anything/pets.yaml", FileMode: 0o644},
	}
	// Short timeout — proxy should exit before health check passes.
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(30 * time.Second)

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: proxyReq,
		Started:          true,
	})
	if c != nil {
		logContainerOutput(ctx, t, c)
		termCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	}
	if err == nil {
		t.Fatal("expected proxy container to fail to start due to invalid JSONPath filter, but it succeeded")
	}
}

// TestDefaultGroupCreatedWhenNoneConfigured verifies that when no groups are configured,
// a default group is created at /mcp with all upstreams.
func TestDefaultGroupCreatedWhenNoneConfigured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	defaultNet, err := tcnetwork.New(ctx, tcnetwork.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := defaultNet.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	_, petsURL := startNamedWireMock(ctx, t, defaultNet.Name, "wiremock-pets2")
	_, ordersURL := startNamedWireMock(ctx, t, defaultNet.Name, "wiremock-orders2")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"POST","url":"/pets"},"response":{"status":201,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	// Config with NO groups section — proxy should create a default group at /mcp.
	cfgContent := fmt.Sprintf(`server:
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
    base_url: http://wiremock-pets2:8080
    timeout: 10s
    validation:
      validate_request: false
      validate_response: false
    openapi:
      source: /etc/mcp-anything/pets.yaml
      version: "3.0"
  - name: orders
    enabled: true
    tool_prefix: orders
    base_url: http://wiremock-orders2:8080
    timeout: 10s
    validation:
      validate_request: false
      validate_response: false
    openapi:
      source: /etc/mcp-anything/orders.yaml
      version: "3.0"
`)
	_ = ordersURL // not needed for assertion

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	if err := os.WriteFile(petsSpecPath, []byte(groupsPetsSpec), 0o644); err != nil {
		t.Fatalf("write pets spec: %v", err)
	}
	ordersSpecPath := filepath.Join(tmpDir, "orders.yaml")
	if err := os.WriteFile(ordersSpecPath, []byte(groupsOrdersSpec), 0o644); err != nil {
		t.Fatalf("write orders spec: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{defaultNet.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: petsSpecPath, ContainerFilePath: "/etc/mcp-anything/pets.yaml", FileMode: 0o644},
		{HostFilePath: ordersSpecPath, ContainerFilePath: "/etc/mcp-anything/orders.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)

	proxy := startContainer(ctx, t, proxyReq)
	host, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	port, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	proxyURL := fmt.Sprintf("http://%s:%s", host, port.Port())

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Default group is at /mcp — should return all 5 tools from both upstreams.
	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp")
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools at /mcp (default group): %v", err)
	}

	wantCount := 5 // 3 pets tools + 2 orders tools
	if len(toolsResult.Tools) != wantCount {
		t.Fatalf("expected %d tools at /mcp (default group), got %d: %v", wantCount, len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
}

// TestReadonlyGroupCallSucceeds verifies that calling a tool that IS in the readonly group works.
func TestReadonlyGroupCallSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName2, petsURL, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName2, groupsConfig("wiremock-pets", "wiremock-orders"))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClientToEndpoint(callCtx, t, proxyURL, "/mcp/readonly")

	// Call pets__list_pets which is in the readonly group (has x-mcp-safe: true).
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "pets__list_pets"})
	if err != nil {
		t.Fatalf("call pets__list_pets via /mcp/readonly: %v", err)
	}
	if result.IsError {
		t.Fatalf("pets__list_pets returned error: %s", contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Fido") {
		t.Errorf("expected Fido in response, got: %s", text)
	}

	// Verify WireMock received the request.
	urls := wireMockJournalURLs(t, petsURL)
	found := false
	for _, u := range urls {
		if u == "/pets" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected /pets request in WireMock journal, got: %v", urls)
	}
}

// TestGroupsProxyStartsWithValidFilter verifies the proxy starts successfully with valid group filters.
func TestGroupsProxyStartsWithValidFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName3, _, _ := startGroupsWireMocks(ctx, t)
	tmpDir := t.TempDir()

	// This should succeed — valid filter.
	_, proxyURL := startGroupsProxy(ctx, t, tmpDir, netName3, groupsConfig("wiremock-pets", "wiremock-orders"))
	assertHTTPStatus(t, proxyURL+"/healthz", 200)
}
