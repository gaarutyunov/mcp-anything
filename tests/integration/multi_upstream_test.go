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

// OpenAPI spec for the pets upstream.
const petsOpenAPISpec = `openapi: "3.0.0"
info:
  title: Pets API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: list_pets
      summary: List pets
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
      operationId: get_pet
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
          content:
            application/json:
              schema:
                type: object
`

// OpenAPI spec for the orders upstream.
const ordersOpenAPISpec = `openapi: "3.0.0"
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

// startNamedWireMock starts a WireMock container on the given network with the given alias.
func startNamedWireMock(ctx context.Context, t *testing.T, netName, alias string) (testcontainers.Container, string) {
	t.Helper()
	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{netName},
		NetworkAliases: map[string][]string{
			netName: {alias},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	host, err := wm.Host(ctx)
	if err != nil {
		t.Fatalf("get %s host: %v", alias, err)
	}
	port, err := wm.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get %s port: %v", alias, err)
	}
	return wm, fmt.Sprintf("http://%s:%s", host, port.Port())
}

// multiUpstreamConfig writes specs and config for two upstreams to tmpDir.
// Returns the config file path.
func multiUpstreamConfig(t *testing.T, tmpDir string, petsAlias, ordersAlias string) string {
	t.Helper()

	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	if err := os.WriteFile(petsSpecPath, []byte(petsOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write pets spec: %v", err)
	}
	ordersSpecPath := filepath.Join(tmpDir, "orders.yaml")
	if err := os.WriteFile(ordersSpecPath, []byte(ordersOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write orders spec: %v", err)
	}

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
`, petsAlias, ordersAlias)

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// startMultiUpstreamProxy starts the proxy with two WireMock containers.
// Returns the proxy container and its external URL, plus the two WireMock external URLs.
func startMultiUpstreamProxy(
	ctx context.Context, t *testing.T,
	petsWiremockURL, ordersWiremockURL string,
	netName, tmpDir string,
) (testcontainers.Container, string) {
	t.Helper()

	cfgPath := multiUpstreamConfig(t, tmpDir, "wiremock-a", "wiremock-b")
	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	ordersSpecPath := filepath.Join(tmpDir, "orders.yaml")

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: petsSpecPath, ContainerFilePath: "/etc/mcp-anything/pets.yaml", FileMode: 0o644},
		{HostFilePath: ordersSpecPath, ContainerFilePath: "/etc/mcp-anything/orders.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)

	_ = petsWiremockURL   // used for stub registration by the caller
	_ = ordersWiremockURL // used for stub registration by the caller

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

// connectMCPClient connects an MCP client to the proxy and returns the session.
func connectMCPClient(ctx context.Context, t *testing.T, proxyURL string) *sdkmcp.ClientSession {
	t.Helper()
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// wireMockJournalURLs returns all request URLs from the WireMock journal.
func wireMockJournalURLs(t *testing.T, base string) []string {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("get wiremock requests from %s: %v", base, err)
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
	urls := make([]string, len(result.Requests))
	for i, r := range result.Requests {
		urls[i] = r.Request.URL
	}
	return urls
}

func TestToolsListContainsAllUpstreams(t *testing.T) {
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

	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-a")
	_, ordersURL := startNamedWireMock(ctx, t, net.Name, "wiremock-b")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[{\"id\":1}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{\"id\":1,\"name\":\"Fido\"}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[{\"id\":100}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{\"id\":101,\"status\":\"pending\"}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	_, proxyURL := startMultiUpstreamProxy(ctx, t, petsURL, ordersURL, net.Name, tmpDir)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	wantTools := []string{"pets__list_pets", "pets__get_pet", "orders__list_orders", "orders__create_orders"}
	if len(toolsResult.Tools) != len(wantTools) {
		t.Fatalf("expected %d tools, got %d: %v", len(wantTools), len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	nameSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !nameSet[want] {
			t.Errorf("expected tool %s in list, got: %v", want, toolNames(toolsResult.Tools))
		}
	}
}

func TestDispatchRoutesToCorrectUpstream(t *testing.T) {
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

	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-a")
	_, ordersURL := startNamedWireMock(ctx, t, net.Name, "wiremock-b")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{\"id\":1,\"name\":\"Fido\"}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[{\"id\":100}]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{\"id\":101,\"status\":\"pending\"}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	_, proxyURL := startMultiUpstreamProxy(ctx, t, petsURL, ordersURL, net.Name, tmpDir)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	// Call pets__list_pets — should hit wiremock-a only.
	petsResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "pets__list_pets"})
	if err != nil {
		t.Fatalf("call pets__list_pets: %v", err)
	}
	if petsResult.IsError {
		t.Fatalf("pets__list_pets returned error: %s", contentText(petsResult.Content))
	}
	if !strings.Contains(contentText(petsResult.Content), "Fido") {
		t.Errorf("pets__list_pets response missing Fido: %s", contentText(petsResult.Content))
	}

	// Call orders__list_orders — should hit wiremock-b only.
	ordersResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "orders__list_orders"})
	if err != nil {
		t.Fatalf("call orders__list_orders: %v", err)
	}
	if ordersResult.IsError {
		t.Fatalf("orders__list_orders returned error: %s", contentText(ordersResult.Content))
	}
	if !strings.Contains(contentText(ordersResult.Content), "100") {
		t.Errorf("orders__list_orders response missing order 100: %s", contentText(ordersResult.Content))
	}

	// Verify wiremock-a received GET /pets and wiremock-b received GET /orders.
	petsJournal := wireMockJournalURLs(t, petsURL)
	if len(petsJournal) != 1 || petsJournal[0] != "/pets" {
		t.Errorf("wiremock-a: expected [/pets], got %v", petsJournal)
	}
	ordersJournal := wireMockJournalURLs(t, ordersURL)
	if len(ordersJournal) != 1 || ordersJournal[0] != "/orders" {
		t.Errorf("wiremock-b: expected [/orders], got %v", ordersJournal)
	}
}

func TestUnknownToolReturnsError(t *testing.T) {
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

	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-a")
	_, ordersURL := startNamedWireMock(ctx, t, net.Name, "wiremock-b")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	_, proxyURL := startMultiUpstreamProxy(ctx, t, petsURL, ordersURL, net.Name, tmpDir)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "nonexistent__tool"})
	// The MCP SDK may reject an unregistered tool at the protocol level (err != nil)
	// or our handler may return IsError=true — both are valid "error" responses.
	if err != nil {
		if !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("expected 'unknown tool' in error, got: %v", err)
		}
		return
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true, got false; content: %s", contentText(result.Content))
	}
	if !strings.Contains(contentText(result.Content), "unknown tool") {
		t.Errorf("expected 'unknown tool' in error content, got: %s", contentText(result.Content))
	}
}

func TestMissingPrefixSeparatorReturnsError(t *testing.T) {
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

	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-a")
	_, ordersURL := startNamedWireMock(ctx, t, net.Name, "wiremock-b")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"GET","url":"/orders"},"response":{"status":200,"body":"{\"orders\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, ordersURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	_, proxyURL := startMultiUpstreamProxy(ctx, t, petsURL, ordersURL, net.Name, tmpDir)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session := connectMCPClient(callCtx, t, proxyURL)

	// The MCP SDK may reject unregistered tool names at the protocol level; that is acceptable.
	_, _ = session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "noprefixatall"})

	// Directly call the proxy's /mcp endpoint via raw HTTP to bypass SDK client-side filtering.
	// The MCP framework returns a JSON-RPC error for unknown tools (before Dispatch is called),
	// so we assert the proxy responds gracefully (no 5xx) and returns an error payload.
	type rawMCPReq struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}
	rawBodyBytes, marshalErr := json.Marshal(rawMCPReq{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "tools/call",
		Params:  map[string]any{"name": "noprefixatall", "arguments": map[string]any{}},
	})
	if marshalErr != nil {
		t.Fatalf("marshal raw MCP request: %v", marshalErr)
	}

	rawHTTPReq, reqErr := http.NewRequestWithContext(callCtx, http.MethodPost, proxyURL+"/mcp", strings.NewReader(string(rawBodyBytes)))
	if reqErr != nil {
		t.Fatalf("build raw MCP request: %v", reqErr)
	}
	rawHTTPReq.Header.Set("Content-Type", "application/json")
	rawHTTPReq.Header.Set("Accept", "application/json, text/event-stream")

	rawHTTPClient := &http.Client{Timeout: 30 * time.Second}
	rawHTTPResp, doErr := rawHTTPClient.Do(rawHTTPReq)
	if doErr != nil {
		t.Fatalf("raw MCP request failed: %v", doErr)
	}
	defer rawHTTPResp.Body.Close()
	rawRespBody, _ := io.ReadAll(rawHTTPResp.Body)

	// Proxy must not return 5xx for unknown tool names.
	if rawHTTPResp.StatusCode >= 500 {
		t.Errorf("expected non-5xx for unknown tool, got HTTP %d; body: %s", rawHTTPResp.StatusCode, string(rawRespBody))
	}
	// Response must contain some error indication.
	respStr := string(rawRespBody)
	if !strings.Contains(respStr, "error") && !strings.Contains(respStr, "Error") {
		t.Errorf("expected error response for unknown tool name, got: %s", respStr)
	}
}

func TestSharedPrefixIsStartupError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Write a minimal (but valid) spec file reused by both upstreams.
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(petsOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Config where both upstreams share tool_prefix "shared".
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: upstream-a
    enabled: true
    tool_prefix: shared
    base_url: http://nowhere:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
  - name: upstream-b
    enabled: true
    tool_prefix: shared
    base_url: http://nowhere:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
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
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
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
		t.Fatal("expected proxy container to fail to start due to shared tool_prefix, but it succeeded")
	}
}

func TestDisabledUpstreamToolsNotInList(t *testing.T) {
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

	// Only start the pets WireMock; orders is disabled so won't be contacted.
	_, petsURL := startNamedWireMock(ctx, t, net.Name, "wiremock-a")

	registerStub(t, petsURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, petsURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()

	petsSpecPath := filepath.Join(tmpDir, "pets.yaml")
	if err := os.WriteFile(petsSpecPath, []byte(petsOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write pets spec: %v", err)
	}
	ordersSpecPath := filepath.Join(tmpDir, "orders.yaml")
	if err := os.WriteFile(ordersSpecPath, []byte(ordersOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write orders spec: %v", err)
	}

	// Config with orders upstream disabled.
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
    base_url: http://wiremock-a:8080
    timeout: 10s
    validation:
      validate_request: false
      validate_response: false
    openapi:
      source: /etc/mcp-anything/pets.yaml
      version: "3.0"
  - name: orders
    enabled: false
    tool_prefix: orders
    base_url: http://nowhere:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/orders.yaml
      version: "3.0"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
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

	session := connectMCPClient(callCtx, t, proxyURL)

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	// Only pets tools should be present (2 tools).
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools (pets only), got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	for _, tool := range toolsResult.Tools {
		if !strings.HasPrefix(tool.Name, "pets__") {
			t.Errorf("unexpected tool %q: should be pets__ only", tool.Name)
		}
	}
}
