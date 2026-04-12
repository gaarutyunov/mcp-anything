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
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// cacheOpenAPISpec is a minimal OpenAPI spec with two operations for cache tests.
const cacheOpenAPISpec = `openapi: "3.0.0"
info:
  title: Cache Test API
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

// wiremockRequestCount returns the number of WireMock requests that matched urlPath.
func wiremockRequestCount(t *testing.T, base, urlPath string) int {
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
	count := 0
	for _, r := range result.Requests {
		if r.Request.URL == urlPath {
			count++
		}
	}
	return count
}

// callTool calls a tool via the MCP session and returns the result.
func callTool(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, toolName string) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("call tool %s: %v", toolName, err)
	}
	return result
}

// TestCacheMemoryHit verifies that a cached tool result is served without hitting
// WireMock on the second call.
func TestCacheMemoryHit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// Fresh WireMock per test.
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
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  long:
    ttl: 30s
cache_store:
  provider: memory
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: long
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
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	const tool = "test__listpets"

	// First call — cache miss, WireMock hit.
	r1 := callTool(t, callCtx, session, tool)
	if r1.IsError {
		t.Fatalf("first call error: %s", contentText(r1.Content))
	}
	if !contentContains(r1.Content, "Fido") {
		t.Errorf("first call missing Fido: %s", contentText(r1.Content))
	}

	// Second call — cache hit, WireMock NOT hit again.
	r2 := callTool(t, callCtx, session, tool)
	if r2.IsError {
		t.Fatalf("second call error: %s", contentText(r2.Content))
	}
	if !contentContains(r2.Content, "Fido") {
		t.Errorf("second call missing Fido: %s", contentText(r2.Content))
	}

	// WireMock should have been hit exactly once.
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 1 {
		t.Errorf("expected WireMock hit 1 time, got %d", count)
	}
}

// TestCacheTTLExpiry verifies that after the cache TTL expires, the next call
// hits WireMock again.
func TestCacheTTLExpiry(t *testing.T) {
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

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	// Use a very short TTL so we can test expiry quickly.
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  short:
    ttl: 2s
cache_store:
  provider: memory
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: short
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
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	const tool = "test__listpets"

	// First call — cache miss, WireMock hit 1.
	r1 := callTool(t, callCtx, session, tool)
	if r1.IsError {
		t.Fatalf("first call error: %s", contentText(r1.Content))
	}

	// Immediate second call — cache hit, WireMock NOT hit.
	r2 := callTool(t, callCtx, session, tool)
	if r2.IsError {
		t.Fatalf("second call error: %s", contentText(r2.Content))
	}
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 1 {
		t.Errorf("before expiry: expected WireMock hit 1 time, got %d", count)
	}

	// Wait for TTL to expire (TTL=2s; wait 5s to be safe inside the container).
	time.Sleep(5 * time.Second)

	// Third call — cache expired, WireMock hit again (hit 2).
	r3 := callTool(t, callCtx, session, tool)
	if r3.IsError {
		t.Fatalf("third call error: %s", contentText(r3.Content))
	}
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 2 {
		t.Errorf("after expiry: expected WireMock hit 2 times, got %d", count)
	}
}

// TestCachePerToolOverride verifies that a per-tool x-mcp-cache overlay extension
// overrides the upstream-level default.
func TestCachePerToolOverride(t *testing.T) {
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

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/orders"},
		"response": {"status": 200, "body": "{\"orders\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Overlay: x-mcp-cache: "" on /orders disables caching for that tool,
	// while /pets uses the upstream default (cached).
	const cacheOverlay = `overlay: 1.0.0
info:
  title: Cache override overlay
  version: "1.0"
actions:
  - target: $.paths['/orders'].get
    update:
      x-mcp-cache: ""
`

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	overlayPath := filepath.Join(tmpDir, "overlay.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(overlayPath, []byte(cacheOverlay), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  long:
    ttl: 30s
cache_store:
  provider: memory
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: long
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    overlay:
      source: /etc/mcp-anything/overlay.yaml
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		{HostFilePath: overlayPath, ContainerFilePath: "/etc/mcp-anything/overlay.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Call /pets twice — cached by upstream default, WireMock hit once.
	callTool(t, callCtx, session, "test__listpets")
	callTool(t, callCtx, session, "test__listpets")
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 1 {
		t.Errorf("pets: expected WireMock hit 1 time, got %d (overlay should NOT have disabled cache)", count)
	}

	// Call /orders twice — overlay disabled cache (x-mcp-cache: ""), WireMock hit twice.
	callTool(t, callCtx, session, "test__listorders")
	callTool(t, callCtx, session, "test__listorders")
	if count := wiremockRequestCount(t, wiremockURL, "/orders"); count != 2 {
		t.Errorf("orders: expected WireMock hit 2 times, got %d (overlay should have disabled cache)", count)
	}
}

// TestCacheErrorNotCached verifies that error results (IsError: true) are never cached.
func TestCacheErrorNotCached(t *testing.T) {
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

	// WireMock always returns 500 — all calls produce error results.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 500, "body": "Internal Server Error"}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  long:
    ttl: 30s
cache_store:
  provider: memory
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: long
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
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	const tool = "test__listpets"

	// Both calls should produce error results and both should hit WireMock.
	r1 := callTool(t, callCtx, session, tool)
	if !r1.IsError {
		t.Fatalf("first call should be an error (WireMock returns 500)")
	}
	r2 := callTool(t, callCtx, session, tool)
	if !r2.IsError {
		t.Fatalf("second call should be an error (WireMock returns 500)")
	}

	// Error was not cached — WireMock hit twice.
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 2 {
		t.Errorf("expected WireMock hit 2 times (errors not cached), got %d", count)
	}
}

// TestCacheNoConfigUnaffected verifies that tools without any cache configuration
// are unaffected by the caching layer.
func TestCacheNoConfigUnaffected(t *testing.T) {
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

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	// No cache config — upstream has no cache field.
	cfgContent := `server:
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
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// Both calls should succeed and both should hit WireMock (no cache).
	r1 := callTool(t, callCtx, session, "test__listpets")
	if r1.IsError {
		t.Fatalf("first call error: %s", contentText(r1.Content))
	}
	r2 := callTool(t, callCtx, session, "test__listpets")
	if r2.IsError {
		t.Fatalf("second call error: %s", contentText(r2.Content))
	}

	// No caching — WireMock hit twice.
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 2 {
		t.Errorf("expected WireMock hit 2 times (no cache), got %d", count)
	}
}

// TestCacheRedisHit verifies that a Redis-backed cache serves results on the
// second call without hitting WireMock.
func TestCacheRedisHit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// Start WireMock.
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
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Start Redis.
	startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"redis"},
		},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	})

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  long:
    ttl: 30s
cache_store:
  provider: redis
  redis:
    addr: redis:6379
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: long
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
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	const tool = "test__listpets"

	// First call — cache miss, WireMock hit.
	r1 := callTool(t, callCtx, session, tool)
	if r1.IsError {
		t.Fatalf("first call error: %s", contentText(r1.Content))
	}
	if !contentContains(r1.Content, "Fido") {
		t.Errorf("first call missing Fido: %s", contentText(r1.Content))
	}

	// Second call — Redis cache hit, WireMock NOT hit.
	r2 := callTool(t, callCtx, session, tool)
	if r2.IsError {
		t.Fatalf("second call error: %s", contentText(r2.Content))
	}
	if !contentContains(r2.Content, "Fido") {
		t.Errorf("second call missing Fido: %s", contentText(r2.Content))
	}

	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 1 {
		t.Errorf("expected WireMock hit 1 time (Redis cache), got %d", count)
	}
}

// TestCacheRedisTTLExpiry verifies that after the Redis TTL expires, the next call
// hits WireMock again.
func TestCacheRedisTTLExpiry(t *testing.T) {
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

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// Start Redis.
	startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"redis"},
		},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	})

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(cacheOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
caches:
  short:
    ttl: 2s
cache_store:
  provider: redis
  redis:
    addr: redis:6379
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    cache: short
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
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	proxyHost, _ := proxy.Host(ctx)
	proxyPort, _ := proxy.MappedPort(ctx, "8080")
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	const tool = "test__listpets"

	// First call — cache miss, WireMock hit 1.
	r1 := callTool(t, callCtx, session, tool)
	if r1.IsError {
		t.Fatalf("first call error: %s", contentText(r1.Content))
	}

	// Immediate second call — Redis cache hit, WireMock NOT hit.
	callTool(t, callCtx, session, tool)
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 1 {
		t.Errorf("before expiry: expected WireMock hit 1 time, got %d", count)
	}

	// Wait for Redis TTL to expire.
	time.Sleep(5 * time.Second)

	// Third call after expiry — cache miss again, WireMock hit 2.
	r3 := callTool(t, callCtx, session, tool)
	if r3.IsError {
		t.Fatalf("third call error: %s", contentText(r3.Content))
	}
	if count := wiremockRequestCount(t, wiremockURL, "/pets"); count != 2 {
		t.Errorf("after expiry: expected WireMock hit 2 times, got %d", count)
	}
}

// contentContains returns true if any text content item contains substr.
func contentContains(content []sdkmcp.Content, substr string) bool {
	for _, c := range content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			if len(tc.Text) > 0 && contains(tc.Text, substr) {
				return true
			}
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
