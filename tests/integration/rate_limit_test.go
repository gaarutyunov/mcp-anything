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

// rateLimitOpenAPISpec is a minimal spec with two operations for rate limit tests.
const rateLimitOpenAPISpec = `openapi: "3.0.0"
info:
  title: Rate Limit Test API
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
  /status:
    get:
      operationId: getStatus
      summary: Get status
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// rateLimitOverlay applies x-mcp-rate-limit: strict to /items only.
// /status inherits the upstream default.
const rateLimitOverlay = `overlay: 1.0.0
info:
  title: Rate limit overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/items"].get
    update:
      x-mcp-rate-limit: strict
`

// proxyConfigWithOverlayRateLimit returns a config with named rate limits, upstream default
// (standard) and per-tool overlay (strict) for the /items operation.
func proxyConfigWithOverlayRateLimit(wiremockAddr string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
rate_limits:
  standard:
    average: 10
    period: 1m
    burst: 5
    source: ip
  strict:
    average: 2
    period: 1m
    burst: 0
    source: ip
upstreams:
  - name: rl
    enabled: true
    tool_prefix: rl
    base_url: http://%s
    timeout: 10s
    rate_limit: standard
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    overlay:
      source: /etc/mcp-anything/overlay.yaml
`, wiremockAddr)
}

// proxyConfigWithUpstreamRateLimit returns a config with two upstreams:
// "limited" has strict rate limit, "unlimited" has no rate limit.
func proxyConfigWithUpstreamRateLimit(wiremockAddr string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
rate_limits:
  strict:
    average: 2
    period: 1m
    burst: 0
    source: ip
upstreams:
  - name: limited
    enabled: true
    tool_prefix: limited
    base_url: http://%s
    timeout: 10s
    rate_limit: strict
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
  - name: unlimited
    enabled: true
    tool_prefix: unlimited
    base_url: http://%s
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, wiremockAddr, wiremockAddr)
}

// proxyConfigWithRedisRateLimit returns a config using a Redis rate limit store.
func proxyConfigWithRedisRateLimit(wiremockAddr, redisAddr string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
rate_limits:
  strict:
    average: 2
    period: 1m
    burst: 0
    source: ip
rate_limit_store:
  redis:
    addr: %s
upstreams:
  - name: rl
    enabled: true
    tool_prefix: rl
    base_url: http://%s
    timeout: 10s
    rate_limit: strict
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, redisAddr, wiremockAddr)
}

// startRateLimitProxy starts a proxy container for rate limit tests, optionally
// mounting an overlay file.
func startRateLimitProxy(
	ctx context.Context,
	t *testing.T,
	netName string,
	specContent, cfgContent, overlayContent string,
) (testcontainers.Container, string) {
	t.Helper()

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}

	if overlayContent != "" {
		overlayPath := filepath.Join(tmpDir, "overlay.yaml")
		if err := os.WriteFile(overlayPath, []byte(overlayContent), 0o644); err != nil {
			t.Fatalf("write overlay: %v", err)
		}
		proxyReq.Files = append(proxyReq.Files, testcontainers.ContainerFile{
			HostFilePath:      overlayPath,
			ContainerFilePath: "/etc/mcp-anything/overlay.yaml",
			FileMode:          0o644,
		})
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

// startWireMockForRateLimit starts a fresh WireMock container for rate limit tests.
func startWireMockForRateLimit(ctx context.Context, t *testing.T, netName string) (string, string) {
	t.Helper()
	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{netName},
		NetworkAliases: map[string][]string{
			netName: {"wiremock"},
		},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmExternalURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	return wmExternalURL, "wiremock:8080"
}

// TestRateLimitInMemory_ToolSucceedsUnderLimit verifies that a tool call succeeds
// when the rate limit has not been exceeded.
func TestRateLimitInMemory_ToolSucceedsUnderLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfgContent := proxyConfigWithUpstreamRateLimit(wmInternalAddr)
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, "")

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	// Call once — well under the strict limit of 2.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "limited__listitems"})
	if err != nil {
		t.Fatalf("call limited__listitems: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", contentText(result.Content))
	}
}

// TestRateLimitInMemory_ExceedLimitReturnsError verifies that exceeding the per-upstream
// rate limit causes the tool call to return an IsError result.
func TestRateLimitInMemory_ExceedLimitReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfgContent := proxyConfigWithUpstreamRateLimit(wmInternalAddr)
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, "")

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	const toolName = "limited__listitems"

	// First 2 calls succeed (strict limit: average=2, burst=0 → capacity=2).
	for i := 0; i < 2; i++ {
		result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if result.IsError {
			t.Fatalf("call %d: expected success, got error: %s", i+1, contentText(result.Content))
		}
	}

	// Third call must be rejected.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("call 3: unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("call 3: expected rate limit error, got success")
	}
	msg := contentText(result.Content)
	if !strings.Contains(msg, "rate limit exceeded") {
		t.Errorf("expected 'rate limit exceeded' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "strict") {
		t.Errorf("expected limit name 'strict' in message, got: %s", msg)
	}
}

// TestRateLimitInMemory_PerToolOverrideOverridesUpstreamDefault verifies that
// x-mcp-rate-limit overlay extension overrides the upstream default.
// /items → strict (2/min), /status → standard (15/min upstream default).
func TestRateLimitInMemory_PerToolOverrideOverridesUpstreamDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/status"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfgContent := proxyConfigWithOverlayRateLimit(wmInternalAddr)
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, rateLimitOverlay)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	// Exhaust the strict limit on rl__listitems (capacity=2).
	for i := 0; i < 2; i++ {
		result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "rl__listitems"})
		if err != nil {
			t.Fatalf("listitems call %d: %v", i+1, err)
		}
		if result.IsError {
			t.Fatalf("listitems call %d: expected success, got error: %s", i+1, contentText(result.Content))
		}
	}

	// Third listitems call must be rejected (strict limit exhausted).
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "rl__listitems"})
	if err != nil {
		t.Fatalf("listitems call 3: %v", err)
	}
	if !result.IsError {
		t.Fatal("listitems call 3: expected rate limit error (strict limit), got success")
	}

	// rl__getstatus uses standard (capacity=15) — still well under limit.
	statusResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "rl__getstatus"})
	if err != nil {
		t.Fatalf("getstatus: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("getstatus: expected success (standard limit not reached), got: %s", contentText(statusResult.Content))
	}
}

// TestRateLimitInMemory_ToolWithoutRateLimitIsUnaffected verifies that a tool with
// no rate limit config is not affected by other tools' limits.
func TestRateLimitInMemory_ToolWithoutRateLimitIsUnaffected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfgContent := proxyConfigWithUpstreamRateLimit(wmInternalAddr)
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, "")

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	// Exhaust the strict limit on limited__listitems.
	for i := 0; i < 3; i++ {
		session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "limited__listitems"}) //nolint:errcheck
	}

	// unlimited__listitems has no rate limit — should always succeed.
	for i := 0; i < 5; i++ {
		result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "unlimited__listitems"})
		if err != nil {
			t.Fatalf("unlimited call %d: %v", i+1, err)
		}
		if result.IsError {
			t.Fatalf("unlimited call %d: expected success (no rate limit), got: %s", i+1, contentText(result.Content))
		}
	}
}

// TestRateLimitRedis_EnforcedViaRedisStore verifies that rate limiting works with a
// real Redis store (via Testcontainers).
func TestRateLimitRedis_EnforcedViaRedisStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// Start Redis.
	_ = startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"redis"},
		},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
	})

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// redis:6379 is the internal address within the container network.
	cfgContent := proxyConfigWithRedisRateLimit(wmInternalAddr, "redis:6379")
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, "")

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	const toolName = "rl__listitems"

	// First 2 calls succeed.
	for i := 0; i < 2; i++ {
		result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if result.IsError {
			t.Fatalf("call %d: expected success, got: %s", i+1, contentText(result.Content))
		}
	}

	// Third call is rejected.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("call 3: %v", err)
	}
	if !result.IsError {
		t.Fatal("call 3: expected rate limit error (Redis store), got success")
	}
	if !strings.Contains(contentText(result.Content), "rate limit exceeded") {
		t.Errorf("unexpected error message: %s", contentText(result.Content))
	}
}

// TestRateLimitRedis_SharedCounterAcrossConnections verifies that the Redis rate limit
// counter is shared across two separate MCP sessions (different connections).
func TestRateLimitRedis_SharedCounterAcrossConnections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	_ = startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"redis"},
		},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
	})

	wmExternalURL, wmInternalAddr := startWireMockForRateLimit(ctx, t, net.Name)

	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	cfgContent := proxyConfigWithRedisRateLimit(wmInternalAddr, "redis:6379")
	_, proxyURL := startRateLimitProxy(ctx, t, net.Name, rateLimitOpenAPISpec, cfgContent, "")

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	const toolName = "rl__listitems"

	// Session 1: make 1 call (consumes 1 of 2 limit slots).
	session1 := connectMCPClient(callCtx, t, proxyURL)
	result, err := session1.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("session1 call: %v", err)
	}
	if result.IsError {
		t.Fatalf("session1: expected success, got: %s", contentText(result.Content))
	}

	// Session 2 (different connection, same IP): consumes 2nd slot.
	session2 := connectMCPClient(callCtx, t, proxyURL)
	result, err = session2.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("session2 call: %v", err)
	}
	if result.IsError {
		t.Fatalf("session2: expected success, got: %s", contentText(result.Content))
	}

	// Session 2: third call should fail — Redis counter is shared across connections.
	result, err = session2.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("session2 third call: %v", err)
	}
	if !result.IsError {
		t.Fatal("session2 third call: expected rate limit error (shared Redis counter), got success")
	}
}
