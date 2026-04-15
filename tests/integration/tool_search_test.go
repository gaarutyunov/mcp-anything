//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
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

// embeddingResponseBody is a fixed embedding response body that WireMock returns for
// any POST /v1/embeddings request. The 3-dimensional vector [1, 0, 0] is used for
// every text, so all tools score identically (cosine similarity = 1.0). This lets
// the test verify that results are returned without depending on semantic ranking.
const embeddingResponseBody = `{"data":[{"embedding":[1.0,0.0,0.0],"index":0,"object":"embedding"}],"model":"test-model","object":"list","usage":{"prompt_tokens":1,"total_tokens":1}}`

// toolSearchConfig returns a proxy config YAML with tool_search enabled and the
// embedding provider pointed at WireMock's /v1 path inside the Docker network.
func toolSearchConfig(specPath string) string {
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
      source: %s
      version: "3.0"
tool_search:
  enabled: true
  limit: 5
  embedding:
    provider: openai_compat
    openai_compat:
      base_url: http://wiremock:8080/v1
      api_key: test-api-key
      model: test-model
`, specPath)
}

// TestToolSearchEnabled verifies the full semantic tool search flow:
//  1. tools/list returns only the search_tools meta-tool when search is enabled.
//  2. Calling search_tools returns a JSON array of ToolDef objects.
//  3. Actual tools remain callable via tools/call even when hidden from tools/list.
func TestToolSearchEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Create a shared bridge network.
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	// 2. Start WireMock.
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

	// 3. Register WireMock stubs.
	// Embedding endpoint — called during proxy startup (index build) and at search time.
	embeddingBodyEncoded, err := json.Marshal(embeddingResponseBody)
	if err != nil {
		t.Fatalf("marshal embedding body: %v", err)
	}
	registerStub(t, wiremockExternalURL, fmt.Sprintf(`{
		"request": {"method": "POST", "url": "/v1/embeddings"},
		"response": {"status": 200, "body": %s, "headers": {"Content-Type": "application/json"}}
	}`, embeddingBodyEncoded))
	// Upstream API endpoint — called when tools/call invokes an actual tool.
	registerStub(t, wiremockExternalURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// 4. Write spec and config to temp dir, then mount into the proxy container.
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(testOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	cfgContent := toolSearchConfig("/etc/mcp-anything/spec.yaml")
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	// 5. Start the proxy container.
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

	// 6. Connect MCP client.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// 7. Assert tools/list returns only search_tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 1 {
		t.Fatalf("expected exactly 1 tool (search_tools), got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	if toolsResult.Tools[0].Name != "search_tools" {
		t.Errorf("expected tool name %q, got %q", "search_tools", toolsResult.Tools[0].Name)
	}

	// 8. Assert search_tools returns a valid JSON array of tool definitions.
	searchResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "search_tools",
		Arguments: map[string]any{"query": "list pets"},
	})
	if err != nil {
		t.Fatalf("call search_tools: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("search_tools returned error: %s", contentText(searchResult.Content))
	}

	raw := contentText(searchResult.Content)
	var toolDefs []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal([]byte(raw), &toolDefs); err != nil {
		t.Fatalf("parse search_tools result as JSON: %v\nraw: %s", err, raw)
	}
	if len(toolDefs) == 0 {
		t.Fatal("search_tools returned empty results")
	}
	for _, td := range toolDefs {
		if td.Name == "" {
			t.Error("search result contains a tool with an empty name")
		}
		if td.InputSchema == nil {
			t.Errorf("tool %q has nil inputSchema", td.Name)
		}
	}

	// 9. Assert actual tools are callable via tools/call even though hidden from tools/list.
	callResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "test__list_pets",
	})
	if err != nil {
		t.Fatalf("call test__list_pets: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("test__list_pets returned error: %s", contentText(callResult.Content))
	}
	if !strings.Contains(contentText(callResult.Content), "Fido") {
		t.Errorf("test__list_pets response missing expected content: %s", contentText(callResult.Content))
	}
}
