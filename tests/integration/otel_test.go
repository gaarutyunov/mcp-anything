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

const otelCollectorConfig = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

exporters:
  debug:
    verbosity: detailed

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      exporters: [debug]
`

const otelTestOpenAPISpec = `openapi: "3.0.0"
info:
  title: OTel Test API
  version: "1.0"
paths:
  /items:
    get:
      operationId: listItems
      summary: List items
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
  /fail:
    get:
      operationId: failItem
      summary: Fail endpoint
      responses:
        "500":
          description: Error
          content:
            application/json:
              schema:
                type: object
`

func otelProxyConfig(collectorAlias string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
  otlp_endpoint: "%s:4317"
  insecure: true
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    validation:
      success_status: [200]
      error_status: [500]
`, collectorAlias)
}

func otelProxyConfigNoCollector() string {
	return `server:
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
    validation:
      success_status: [200]
      error_status: [500]
`
}

// startOTelCollector starts an OTel collector container on the given network.
// Returns the container and its external port.
func startOTelCollector(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork, alias string) testcontainers.Container {
	t.Helper()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "otelcol-config.yaml")
	if err := os.WriteFile(cfgPath, []byte(otelCollectorConfig), 0o644); err != nil {
		t.Fatalf("write otel collector config: %v", err)
	}

	return startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "otel/opentelemetry-collector-contrib:0.120.0",
		ExposedPorts: []string{"4317/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {alias},
		},
		Files: []testcontainers.ContainerFile{
			{HostFilePath: cfgPath, ContainerFilePath: "/etc/otelcol-contrib/config.yaml", FileMode: 0o644},
		},
		WaitingFor: wait.ForListeningPort("4317/tcp").WithStartupTimeout(120 * time.Second),
	})
}

// startOTelProxy starts the proxy container.
// extraEnv is merged with default env vars; pass nil for no extras.
func startOTelProxy(ctx context.Context, t *testing.T, net *testcontainers.DockerNetwork, cfgContent, specContent string, extraEnv map[string]string) (testcontainers.Container, string) {
	t.Helper()

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	env := map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
		// Reduce batch span processor delay so traces flush faster in tests.
		"OTEL_BSP_SCHEDULE_DELAY": "1000",
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = env
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

// readContainerLogs reads all available logs from a container as a string.
func readContainerLogs(ctx context.Context, t *testing.T, c testcontainers.Container) string {
	t.Helper()
	logs, err := c.Logs(ctx)
	if err != nil {
		t.Fatalf("read container logs: %v", err)
	}
	defer logs.Close()
	b, err := io.ReadAll(logs)
	if err != nil {
		t.Fatalf("read container logs body: %v", err)
	}
	return string(b)
}

// TestTracesExportedOnToolCall verifies that making an MCP tools/call results in
// traces exported to the OTel collector (AC-28.1, AC-28.2, AC-28.3, AC-28.5).
func TestTracesExportedOnToolCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// 1. Start OTel collector with OTLP gRPC receiver + debug exporter.
	const collectorAlias = "otel-collector"
	collector := startOTelCollector(ctx, t, net, collectorAlias)

	// 2. Start WireMock with a stub for /items.
	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wiremockHost, _ := wiremock.Host(ctx)
	wiremockPort, _ := wiremock.MappedPort(ctx, "8080")
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// 3. Start proxy configured to send traces to the collector.
	_, proxyURL := startOTelProxy(ctx, t, net, otelProxyConfig(collectorAlias), otelTestOpenAPISpec, nil)

	// 4. Connect MCP client and make a tool call.
	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__list_items"})
	if err != nil {
		t.Fatalf("call test__list_items: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool call returned error: %s", contentText(result.Content))
	}

	// 5. Wait for traces to flush. OTEL_BSP_SCHEDULE_DELAY=1000ms so allow 3s buffer.
	time.Sleep(4 * time.Second)

	// 6. Assert collector logs contain expected span names and attributes.
	logs := readContainerLogs(ctx, t, collector)
	t.Logf("collector logs (first 2000 chars): %.2000s", logs)

	if !strings.Contains(logs, "tools/call") {
		t.Errorf("expected collector logs to contain 'tools/call' span name, got logs:\n%.2000s", logs)
	}
	if !strings.Contains(logs, "mcp.tool.name") {
		t.Errorf("expected collector logs to contain 'mcp.tool.name' attribute")
	}
	if !strings.Contains(logs, "http.request.method") {
		t.Errorf("expected collector logs to contain upstream HTTP span with 'http.request.method'")
	}
}

// TestW3CTracePropagation verifies that a traceparent HTTP header is propagated
// through the proxy and appears in the exported traces with the same trace ID (AC-28.4).
func TestW3CTracePropagation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	const collectorAlias = "otel-collector"
	collector := startOTelCollector(ctx, t, net, collectorAlias)

	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wiremockHost, _ := wiremock.Host(ctx)
	wiremockPort, _ := wiremock.MappedPort(ctx, "8080")
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	_, proxyURL := startOTelProxy(ctx, t, net, otelProxyConfig(collectorAlias), otelTestOpenAPISpec, nil)

	// Use a known trace ID and parent span ID injected via traceparent header.
	const knownTraceID = "0af7651916cd43dd8448eb211c80319c"
	const knownSpanID = "b7ad6b7169203331"
	traceparent := fmt.Sprintf("00-%s-%s-01", knownTraceID, knownSpanID)

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
		HTTPClient: &http.Client{
			// Inject the traceparent header using the shared headerRoundTripper (single-header variant).
			Transport: &headerRoundTripper{
				base:   http.DefaultTransport,
				header: "traceparent",
				value:  traceparent,
			},
		},
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "w3c-test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__list_items"})
	if err != nil {
		t.Fatalf("call test__list_items: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool call returned error: %s", contentText(result.Content))
	}

	// Wait for traces to flush. OTEL_BSP_SCHEDULE_DELAY=1000ms so allow 3s buffer.
	time.Sleep(4 * time.Second)

	logs := readContainerLogs(ctx, t, collector)
	t.Logf("collector logs (first 2000 chars): %.2000s", logs)

	// The exported trace must use the same trace ID from the inbound traceparent header.
	if !strings.Contains(logs, knownTraceID) {
		t.Errorf("expected collector logs to contain trace ID %s (from traceparent header); logs:\n%.2000s",
			knownTraceID, logs)
	}
}

// TestMetricsEmitted verifies that the proxy exposes Prometheus metrics at /metrics
// and that MCP-specific metrics are emitted after tool calls (AC-29.1, AC-29.3, AC-29.4).
func TestMetricsEmitted(t *testing.T) {
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
	wiremockHost, _ := wiremock.Host(ctx)
	wiremockPort, _ := wiremock.MappedPort(ctx, "8080")
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	// Register stubs: one success endpoint and one failure endpoint.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/fail"},
		"response": {"status": 500, "body": "{\"error\":\"internal\"}", "headers": {"Content-Type": "application/json"}}
	}`)

	_, proxyURL := startOTelProxy(ctx, t, net, otelProxyConfigNoCollector(), otelTestOpenAPISpec, nil)

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "metrics-test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Make 3 successful tool calls.
	for i := 0; i < 3; i++ {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__list_items"})
		if callErr != nil {
			t.Fatalf("success call %d: %v", i, callErr)
		}
		if result.IsError {
			t.Fatalf("success call %d returned error: %s", i, contentText(result.Content))
		}
	}

	// Make 1 failing tool call (upstream returns 500).
	failResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__fail_item"})
	if err != nil {
		t.Fatalf("fail call: %v", err)
	}
	if !failResult.IsError {
		t.Fatalf("expected fail call to return error result")
	}

	// Fetch Prometheus metrics from /metrics.
	resp, err := http.Get(proxyURL + "/metrics") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	metricsBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	metrics := string(metricsBody)
	t.Logf("/metrics (first 3000 chars): %.3000s", metrics)

	// Assert MCP tool call duration histogram is present.
	// OTel Prometheus bridge converts unit "s" → "_seconds" suffix, dots → underscores.
	if !strings.Contains(metrics, "mcp_tool_call_duration_seconds_bucket") {
		t.Errorf("expected mcp_tool_call_duration_seconds_bucket in /metrics")
	}

	// Assert MCP tool call errors counter is present.
	// Counter with unit "{call}" → no unit suffix; counter → "_total" suffix.
	if !strings.Contains(metrics, "mcp_tool_call_errors_total") {
		t.Errorf("expected mcp_tool_call_errors_total in /metrics")
	}

	// Assert HTTP server request duration histogram is present (emitted by otelhttp middleware).
	if !strings.Contains(metrics, "http_server_request_duration_seconds_bucket") {
		t.Errorf("expected http_server_request_duration_seconds_bucket in /metrics")
	}
}
