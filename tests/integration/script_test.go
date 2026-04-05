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

// TestScriptUpstream verifies the full script upstream feature end-to-end:
//   - Script tools appear in tools/list with correct names and input schemas.
//   - Successful scripts return JSON-serialised return value as TextContent.
//   - ctx.env exposes configured environment variables to scripts.
//   - ctx.fetch makes HTTP requests via the proxy's HTTP client.
//   - JS exceptions return IsError: true.
//   - Script timeout interrupts long-running scripts.
func TestScriptUpstream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Create a bridge network so the proxy container can reach WireMock by alias.
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

	// Start WireMock as the mock HTTP upstream for ctx.fetch.
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

	// Register WireMock stubs for ctx.fetch tests.
	registerStub(t, wiremockExternalURL, `{
		"request": {"method": "GET", "url": "/users/42"},
		"response": {
			"status": 200,
			"body": "{\"id\":42,\"name\":\"Alice\",\"spend\":1500}",
			"headers": {"Content-Type": "application/json"}
		}
	}`)

	tmpDir := t.TempDir()

	// Script 1: simple transform — returns processed args.
	greetScript := `export default function(args, ctx) {
    return {greeting: "Hello, " + args.name + "!"};
}`
	greetPath := filepath.Join(tmpDir, "greet.js")
	if err := os.WriteFile(greetPath, []byte(greetScript), 0o644); err != nil {
		t.Fatalf("write greet script: %v", err)
	}

	// Script 2: uses ctx.fetch and ctx.env to call WireMock.
	enrichScript := `export default function(args, ctx) {
    var user = ctx.fetch(ctx.env.API_BASE + "/users/" + args.user_id);
    var tier = user.spend > 1000 ? "premium" : "standard";
    return {id: user.id, name: user.name, tier: tier};
}`
	enrichPath := filepath.Join(tmpDir, "enrich.js")
	if err := os.WriteFile(enrichPath, []byte(enrichScript), 0o644); err != nil {
		t.Fatalf("write enrich script: %v", err)
	}

	// Script 3: throws a JS exception.
	errorScript := `export default function(args, ctx) {
    throw new Error("intentional error");
}`
	errorPath := filepath.Join(tmpDir, "error.js")
	if err := os.WriteFile(errorPath, []byte(errorScript), 0o644); err != nil {
		t.Fatalf("write error script: %v", err)
	}

	// Script 4: infinite loop — should be interrupted by timeout.
	loopScript := `export default function(args, ctx) {
    var n = 0;
    while (true) { n++; }
    return n;
}`
	loopPath := filepath.Join(tmpDir, "loop.js")
	if err := os.WriteFile(loopPath, []byte(loopScript), 0o644); err != nil {
		t.Fatalf("write loop script: %v", err)
	}

	cfgContent := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: js
    enabled: true
    tool_prefix: js
    type: script
    runtime: sobek
    scripts:
      - tool_name: greet
        description: "Return a greeting for a given name"
        script_path: /etc/mcp-anything/greet.js
        input_schema:
          type: object
          properties:
            name:
              type: string
              description: "Name to greet"
          required: [name]
      - tool_name: enrich_user
        description: "Fetch user from API and enrich with tier"
        script_path: /etc/mcp-anything/enrich.js
        timeout: 10s
        env:
          API_BASE: "http://wiremock:8080"
        input_schema:
          type: object
          properties:
            user_id:
              type: string
              description: "User ID to fetch"
          required: [user_id]
      - tool_name: always_error
        description: "A script that always throws"
        script_path: /etc/mcp-anything/error.js
        input_schema:
          type: object
          properties: {}
      - tool_name: infinite_loop
        description: "A script that runs forever (timeout test)"
        script_path: /etc/mcp-anything/loop.js
        timeout: %s
        input_schema:
          type: object
          properties: {}
`, "500ms")

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
		{HostFilePath: greetPath, ContainerFilePath: "/etc/mcp-anything/greet.js", FileMode: 0o644},
		{HostFilePath: enrichPath, ContainerFilePath: "/etc/mcp-anything/enrich.js", FileMode: 0o644},
		{HostFilePath: errorPath, ContainerFilePath: "/etc/mcp-anything/error.js", FileMode: 0o644},
		{HostFilePath: loopPath, ContainerFilePath: "/etc/mcp-anything/loop.js", FileMode: 0o644},
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

	// 1. tools/list must return all 4 script tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	wantTools := map[string]bool{
		"js__greet":        true,
		"js__enrich_user":  true,
		"js__always_error": true,
		"js__infinite_loop": true,
	}
	for _, tool := range toolsResult.Tools {
		if !wantTools[tool.Name] {
			t.Errorf("unexpected tool %q in list", tool.Name)
		}
	}

	// 2. Simple greeting script.
	t.Run("greet", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name:      "js__greet",
			Arguments: map[string]any{"name": "World"},
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %s", contentText(result.Content))
		}
		text := contentText(result.Content)
		if !strings.Contains(text, "Hello") || !strings.Contains(text, "World") {
			t.Errorf("expected greeting in output, got: %s", text)
		}
	})

	// 3. ctx.fetch + ctx.env: enrich user from WireMock.
	t.Run("enrich_user", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name:      "js__enrich_user",
			Arguments: map[string]any{"user_id": "42"},
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %s", contentText(result.Content))
		}
		text := contentText(result.Content)
		if !strings.Contains(text, "Alice") {
			t.Errorf("expected Alice in output, got: %s", text)
		}
		if !strings.Contains(text, "premium") {
			t.Errorf("expected premium tier in output, got: %s", text)
		}
	})

	// 4. JS exception: IsError: true.
	t.Run("always_error", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name: "js__always_error",
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if !result.IsError {
			t.Fatalf("expected IsError: true, got success: %s", contentText(result.Content))
		}
	})

	// 5. Timeout: infinite loop is interrupted.
	t.Run("infinite_loop", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name: "js__infinite_loop",
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if !result.IsError {
			t.Fatalf("expected IsError: true for timed-out script, got: %s", contentText(result.Content))
		}
	})
}
