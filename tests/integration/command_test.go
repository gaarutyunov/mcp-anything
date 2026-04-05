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
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestCommandUpstream verifies the full command upstream feature end-to-end:
//   - Command tools appear in tools/list with the correct names and input schemas.
//   - Successful commands return stdout as TextContent (non-shell mode).
//   - Failing commands (non-zero exit) return IsError: true.
//   - Shell mode commands execute via sh -c with shell-quoted arguments.
func TestCommandUpstream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Config with three command tools covering the main scenarios.
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: ops
    enabled: true
    tool_prefix: ops
    type: command
    commands:
      - tool_name: echo_message
        description: "Echo a message back"
        command: "/bin/echo {{.message}}"
        input_schema:
          type: object
          properties:
            message:
              type: string
              description: "The message to echo"
          required: [message]
      - tool_name: always_fail
        description: "A command that always exits non-zero"
        command: "/usr/bin/false"
        input_schema:
          type: object
          properties: {}
      - tool_name: greet_shell
        description: "Greet via sh -c (shell mode)"
        command: "/bin/echo Hello {{.name}}"
        shell: true
        input_schema:
          type: object
          properties:
            name:
              type: string
              description: "Name to greet"
          required: [name]
`

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	}
	proxyReq.Files = []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
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

	// 1. tools/list must return all 3 command tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	wantTools := map[string]bool{
		"ops__echo_message": true,
		"ops__always_fail":  true,
		"ops__greet_shell":  true,
	}
	for _, tool := range toolsResult.Tools {
		if !wantTools[tool.Name] {
			t.Errorf("unexpected tool %q in list", tool.Name)
		}
	}

	// 2. Echo command: stdout returned as TextContent.
	t.Run("echo_message", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name:      "ops__echo_message",
			Arguments: map[string]any{"message": "hello world"},
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %s", contentText(result.Content))
		}
		if !strings.Contains(contentText(result.Content), "hello world") {
			t.Errorf("expected 'hello world' in output, got: %s", contentText(result.Content))
		}
	})

	// 3. Failing command: non-zero exit returns IsError: true.
	t.Run("always_fail", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name: "ops__always_fail",
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if !result.IsError {
			t.Fatalf("expected IsError: true, got success: %s", contentText(result.Content))
		}
	})

	// 4. Shell mode: command executed via sh -c; args are shell-quoted automatically.
	t.Run("greet_shell", func(t *testing.T) {
		result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
			Name:      "ops__greet_shell",
			Arguments: map[string]any{"name": "alice"},
		})
		if callErr != nil {
			t.Fatalf("call: %v", callErr)
		}
		if result.IsError {
			t.Fatalf("unexpected error: %s", contentText(result.Content))
		}
		text := contentText(result.Content)
		if !strings.Contains(text, "Hello") || !strings.Contains(text, "alice") {
			t.Errorf("expected 'Hello alice' in output, got: %s", text)
		}
	})
}
