//go:build integration

package integration_test

import (
	"context"
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

// tokenCountingOpenAPISpec defines a minimal API used by token counting tests.
const tokenCountingOpenAPISpec = `openapi: "3.0.0"
info:
  title: Token Counting Test API
  version: "1.0"
paths:
  /data:
    get:
      operationId: getData
      summary: Get data
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// tokenCountingProxyConfig returns a proxy config with token counting enabled.
func tokenCountingProxyConfig(encoding string) string {
	encodingLine := ""
	if encoding != "" {
		encodingLine = fmt.Sprintf("  encoding: %s\n", encoding)
	}
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
token_counting:
  enabled: true
%supstreams:
  - name: tc
    enabled: true
    tool_prefix: tc
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    validation:
      success_status: [200]
`, encodingLine)
}

// tokenCountingProxyConfigDisabled returns a proxy config with token counting disabled.
func tokenCountingProxyConfigDisabled() string {
	return `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: tc
    enabled: true
    tool_prefix: tc
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    validation:
      success_status: [200]
`
}

func startTokenCountingProxy(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork, cfgContent string) (testcontainers.Container, string) {
	t.Helper()

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(tokenCountingOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
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
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	host, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("proxy host: %v", err)
	}
	port, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("proxy port: %v", err)
	}
	return proxy, fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestTokenCountingMetricEmitted verifies that with token_counting.enabled: true,
// a successful tool call emits the mcp_tool_result_tokens histogram metric.
func TestTokenCountingMetricEmitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// Start WireMock with a known JSON response body.
	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wiremock.Host(ctx)
	wmPort, _ := wiremock.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())

	// Register a stub that returns a known JSON response (~15 tokens).
	registerStub(t, wmURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {
			"status": 200,
			"body": "{\"result\":\"hello world this is a test response\"}",
			"headers": {"Content-Type": "application/json"}
		}
	}`)

	// Start proxy with token counting enabled (default cl100k_base encoding).
	_, proxyURL := startTokenCountingProxy(ctx, t, net, tokenCountingProxyConfig(""))

	// Connect MCP client and make a tool call.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tc-test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "tc__get_data"})
	if err != nil {
		t.Fatalf("call tc__get_data: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", contentText(result.Content))
	}

	// Fetch Prometheus metrics and assert the token histogram is present.
	resp, err := http.Get(proxyURL + "/metrics") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	metrics := string(body)
	t.Logf("/metrics (first 3000 chars): %.3000s", metrics)

	if !strings.Contains(metrics, "mcp_tool_result_tokens_bucket") {
		t.Errorf("expected mcp_tool_result_tokens_bucket in /metrics; token counting not emitting histogram")
	}
	if !strings.Contains(metrics, `tool_name="tc__get_data"`) {
		t.Errorf("expected tool_name label in mcp_tool_result_tokens metric")
	}
	if !strings.Contains(metrics, `upstream_name="tc"`) {
		t.Errorf("expected upstream_name label in mcp_tool_result_tokens metric")
	}
}

// TestTokenCountingDisabledNoMetric verifies that when token counting is absent
// from the config, the mcp_tool_result_tokens histogram has no observations.
func TestTokenCountingDisabledNoMetric(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wiremock.Host(ctx)
	wmPort, _ := wiremock.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())

	registerStub(t, wmURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {
			"status": 200,
			"body": "{\"result\":\"hello\"}",
			"headers": {"Content-Type": "application/json"}
		}
	}`)

	_, proxyURL := startTokenCountingProxy(ctx, t, net, tokenCountingProxyConfigDisabled())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tc-disabled-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "tc__get_data"})
	if err != nil {
		t.Fatalf("call tc__get_data: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", contentText(result.Content))
	}

	resp, err := http.Get(proxyURL + "/metrics") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	metrics := string(body)

	// The histogram is always registered, but should have no observations (count == 0).
	// With no observations, the _bucket lines will appear but the _count should be 0 or absent.
	if strings.Contains(metrics, `mcp_tool_result_tokens_count{`) &&
		strings.Contains(metrics, `upstream_name="tc"`) {
		t.Errorf("expected no mcp_tool_result_tokens observations when token counting is disabled")
	}
}

// TestTokenCountingO200kEncoding verifies that the o200k_base encoding works.
func TestTokenCountingO200kEncoding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wiremock.Host(ctx)
	wmPort, _ := wiremock.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())

	registerStub(t, wmURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {
			"status": 200,
			"body": "{\"result\":\"hello world\"}",
			"headers": {"Content-Type": "application/json"}
		}
	}`)

	_, proxyURL := startTokenCountingProxy(ctx, t, net, tokenCountingProxyConfig("o200k_base"))

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tc-o200k-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "tc__get_data"})
	if err != nil {
		t.Fatalf("call tc__get_data: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", contentText(result.Content))
	}

	resp, err := http.Get(proxyURL + "/metrics") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	metrics := string(body)
	t.Logf("/metrics (first 3000 chars): %.3000s", metrics)

	if !strings.Contains(metrics, "mcp_tool_result_tokens_bucket") {
		t.Errorf("expected mcp_tool_result_tokens_bucket in /metrics with o200k_base encoding")
	}
}
