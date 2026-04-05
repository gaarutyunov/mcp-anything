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

// TestTLSUpstream verifies that the proxy can call an upstream over HTTPS using
// transport.tls.insecure_skip_verify: true (WireMock's built-in self-signed cert).
func TestTLSUpstream(t *testing.T) {
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

	// Start WireMock with HTTPS enabled on port 8443.
	wiremock := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		Cmd:          []string{"--https-port=8443"},
		ExposedPorts: []string{"8080/tcp", "8443/tcp"},
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
	wiremockHTTPPort, err := wiremock.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock HTTP port: %v", err)
	}
	wiremockAdminURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockHTTPPort.Port())

	// Register stubs via HTTP admin (even though proxy will hit HTTPS).
	registerStub(t, wiremockAdminURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"TLSFido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(testOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}

	// Configure proxy to call WireMock over HTTPS with insecure_skip_verify.
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: tls-test
    enabled: true
    tool_prefix: tlstest
    base_url: https://wiremock:8443
    timeout: 10s
    transport:
      tls:
        insecure_skip_verify: true
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
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

	proxyHost, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	proxyPort, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	assertHTTPStatus(t, proxyURL+"/healthz", http.StatusOK)

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	// Find the listPets tool.
	const toolListPets = "tlstest__listpets"
	var found bool
	for _, tool := range toolsResult.Tools {
		if tool.Name == toolListPets {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tool %s, got %v", toolListPets, toolNames(toolsResult.Tools))
	}

	// Call the tool — proxy must reach WireMock over HTTPS.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: toolListPets})
	if err != nil {
		t.Fatalf("call %s: %v", toolListPets, err)
	}
	if result.IsError {
		t.Fatalf("%s returned error: %s", toolListPets, contentText(result.Content))
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "TLSFido") {
		t.Errorf("response missing TLSFido: %s", text)
	}
}
