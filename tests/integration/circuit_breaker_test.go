//go:build integration

package integration_test

import (
	"context"
	"fmt"
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

// cbOpenAPISpec is a minimal spec for circuit breaker tests.
const cbOpenAPISpec = `openapi: "3.0.0"
info:
  title: Circuit Breaker Test API
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
`

// cbProxyConfig returns a config with a named circuit breaker attached to one upstream.
// fallbackSec controls how long the circuit stays open before transitioning to half-open.
func cbProxyConfig(wiremockAddr string, fallbackSec int) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
circuit_breakers:
  tight:
    threshold: 0.5
    min_requests: 2
    fallback_duration: %ds
    recovery_duration: 1s
upstreams:
  - name: cb
    enabled: true
    tool_prefix: cb
    base_url: http://%s
    timeout: 5s
    circuit_breaker: tight
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, fallbackSec, wiremockAddr)
}

// cbProxyConfigTwoUpstreams returns a config with two upstreams: one protected by a
// circuit breaker and one without, to verify isolation.
func cbProxyConfigTwoUpstreams(wiremockAddr string) string {
	return fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
circuit_breakers:
  tight:
    threshold: 0.5
    min_requests: 2
    fallback_duration: 60s
    recovery_duration: 1s
upstreams:
  - name: protected
    enabled: true
    tool_prefix: protected
    base_url: http://%s
    timeout: 5s
    circuit_breaker: tight
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
  - name: unprotected
    enabled: true
    tool_prefix: unprotected
    base_url: http://%s
    timeout: 5s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, wiremockAddr, wiremockAddr)
}

// startCBProxy starts a proxy container for circuit breaker tests.
func startCBProxy(ctx context.Context, t *testing.T, netName, specContent, cfgContent string) (testcontainers.Container, string) {
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

// startWireMockForCB starts a fresh WireMock container for circuit breaker tests.
func startWireMockForCB(ctx context.Context, t *testing.T, netName string) (string, string) {
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

// resetWireMock clears all WireMock stubs and the request journal.
func resetWireMock(t *testing.T, baseURL string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/__admin/reset", "application/json", nil) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("reset wiremock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset wiremock: got %d", resp.StatusCode)
	}
}

// TestCircuitBreaker_OpensAfterFailures verifies that the circuit opens after enough
// upstream failures and subsequent calls fail immediately without reaching the upstream.
func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForCB(ctx, t, net.Name)

	// Register a 500 stub to simulate upstream failures.
	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 500, "body": "internal server error", "headers": {"Content-Type": "application/json"}}
	}`)

	// Use a long fallback duration so the circuit stays open throughout the test.
	_, proxyURL := startCBProxy(ctx, t, net.Name, cbOpenAPISpec, cbProxyConfig(wmInternalAddr, 60))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	const toolName = "cb__listitems"

	// First two calls reach WireMock and return 500 — the second one trips the circuit
	// (2 requests, 100% failure rate ≥ threshold 50%).
	for i := 0; i < 2; i++ {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
		if callErr != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, callErr)
		}
		if !result.IsError {
			t.Fatalf("call %d: expected error result (upstream is returning 500), got success", i+1)
		}
	}

	// Circuit should now be open. The third call must fail fast without reaching WireMock.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("call 3: unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("call 3: expected circuit breaker error, got success")
	}
	msg := contentText(result.Content)
	if !strings.Contains(msg, "circuit breaker is open") {
		t.Errorf("expected 'circuit breaker is open' in message, got: %s", msg)
	}
	if !strings.Contains(msg, `"cb"`) {
		t.Errorf("expected upstream name 'cb' in message, got: %s", msg)
	}

	// WireMock must have received exactly 2 requests (not 3): the third was short-circuited.
	count := wireMockRequestCount(t, wmExternalURL)
	if count != 2 {
		t.Errorf("expected exactly 2 WireMock requests (circuit should block the 3rd), got %d", count)
	}
}

// TestCircuitBreaker_ReadyzReflectsCircuitState verifies that GET /readyz returns 503
// while a circuit breaker is open and 200 after the upstream recovers.
func TestCircuitBreaker_ReadyzReflectsCircuitState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForCB(ctx, t, net.Name)

	// Use a short fallback duration (3s) to allow recovery within the test.
	const fallbackSec = 3
	_, proxyURL := startCBProxy(ctx, t, net.Name, cbOpenAPISpec, cbProxyConfig(wmInternalAddr, fallbackSec))

	// Readyz should be healthy before any failures.
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)

	// Register a 500 stub to trigger circuit failures.
	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 500, "body": "error", "headers": {"Content-Type": "application/json"}}
	}`)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	const toolName = "cb__listitems"

	// Trigger 2 failures to open the circuit (min_requests=2, threshold=0.5).
	for i := 0; i < 2; i++ {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
		if callErr != nil {
			t.Fatalf("failure call %d: %v", i+1, callErr)
		}
		if !result.IsError {
			t.Fatalf("failure call %d: expected error, got success", i+1)
		}
	}

	// Circuit is now open — readyz must return 503.
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusServiceUnavailable)

	// Wait for fallback_duration to expire so the circuit transitions to half-open.
	time.Sleep(time.Duration(fallbackSec+1) * time.Second)

	// Reset WireMock stubs and register a 200 response for recovery.
	resetWireMock(t, wmExternalURL)
	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 200, "body": "{\"items\":[]}", "headers": {"Content-Type": "application/json"}}
	}`)

	// In half-open state, one test request is allowed through. This success closes the circuit.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolName})
	if err != nil {
		t.Fatalf("recovery call: %v", err)
	}
	if result.IsError {
		t.Fatalf("recovery call: expected success after circuit recovery, got: %s", contentText(result.Content))
	}

	// Circuit is now closed — readyz must return 200.
	assertHTTPStatus(t, proxyURL+"/readyz", http.StatusOK)
}

// TestCircuitBreaker_UnprotectedUpstreamUnaffected verifies that a circuit breaker
// on one upstream does not affect tool calls from a different upstream that has no
// circuit breaker configured.
func TestCircuitBreaker_UnprotectedUpstreamUnaffected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wmExternalURL, wmInternalAddr := startWireMockForCB(ctx, t, net.Name)

	// Register a 500 stub for failures and a 200 stub for the unprotected upstream.
	registerStub(t, wmExternalURL, `{
		"request": {"method": "GET", "url": "/items"},
		"response": {"status": 500, "body": "error", "headers": {"Content-Type": "application/json"}}
	}`)

	_, proxyURL := startCBProxy(ctx, t, net.Name, cbOpenAPISpec, cbProxyConfigTwoUpstreams(wmInternalAddr))

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClient(callCtx, t, proxyURL)

	// Exhaust protected upstream circuit (2 failures → circuit opens).
	for i := 0; i < 2; i++ {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "protected__listitems"})
		if callErr != nil {
			t.Fatalf("protected call %d: %v", i+1, callErr)
		}
		if !result.IsError {
			t.Fatalf("protected call %d: expected error (upstream returns 500)", i+1)
		}
	}

	// Protected circuit is open — further calls must fail fast with circuit message.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "protected__listitems"})
	if err != nil {
		t.Fatalf("protected open-circuit call: %v", err)
	}
	if !result.IsError {
		t.Fatal("protected open-circuit call: expected circuit breaker error, got success")
	}
	if !strings.Contains(contentText(result.Content), "circuit breaker is open") {
		t.Errorf("expected circuit breaker message, got: %s", contentText(result.Content))
	}

	// WireMock should have received exactly 2 requests for the protected upstream
	// (the 3rd was short-circuited).
	wmCountAfterProtected := wireMockRequestCount(t, wmExternalURL)
	if wmCountAfterProtected != 2 {
		t.Errorf("expected 2 WireMock requests for protected upstream, got %d", wmCountAfterProtected)
	}

	// Calls to the unprotected upstream must still reach WireMock — it has no circuit breaker.
	// The upstream still returns 500 (error result), but the call must NOT be short-circuited.
	for i := 0; i < 3; i++ {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "unprotected__listitems"})
		if callErr != nil {
			t.Fatalf("unprotected call %d: %v", i+1, callErr)
		}
		// The upstream returns 500 → IsError but it should NOT say "circuit breaker is open"
		if strings.Contains(contentText(result.Content), "circuit breaker is open") {
			t.Fatalf("unprotected call %d: unexpected circuit breaker message; upstream has no circuit breaker", i+1)
		}
	}

	// WireMock should have received 2 (protected) + 3 (unprotected) = 5 requests.
	wmCountAfterAll := wireMockRequestCount(t, wmExternalURL)
	if wmCountAfterAll != 5 {
		t.Errorf("expected 5 total WireMock requests (2 protected + 3 unprotected), got %d", wmCountAfterAll)
	}
}
