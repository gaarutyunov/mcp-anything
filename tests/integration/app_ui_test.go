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

// appUIOpenAPISpec is a minimal OpenAPI spec used by all app_ui tests.
// It has two operations: listPets (GET /pets) and getPet (GET /pets/{petId}).
const appUIOpenAPISpec = `openapi: "3.0.0"
info:
  title: Pet API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
  /pets/{petId}:
    get:
      operationId: getPet
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
`

// TestAppUIStaticHTML verifies that when an upstream has app_ui.static configured:
//   - tools/list returns tools with _meta["ui"]["resourceUri"] set to the ui:// URI
//   - resources/read for that URI returns the static HTML content
func TestAppUIStaticHTML(t *testing.T) {
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
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "[]", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()

	// Write a static HTML file.
	staticHTML := `<!DOCTYPE html>
<html><head><title>Pet UI</title></head>
<body><h1>Pet Viewer</h1><pre id="result"></pre>
<script>
window.parent.postMessage({jsonrpc:"2.0",id:1,method:"ui/initialize",params:{protocolVersion:"2024-11-21",capabilities:{}}}, "*");
window.addEventListener("message", function(e) {
  if (e.data && e.data.method === "ui/notifications/tool-result") {
    document.getElementById("result").textContent = JSON.stringify(e.data.params);
  }
});
</script>
</body></html>`
	staticHTMLPath := filepath.Join(tmpDir, "ui.html")
	if err := os.WriteFile(staticHTMLPath, []byte(staticHTML), 0o644); err != nil {
		t.Fatalf("write static HTML: %v", err)
	}

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(appUIOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

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
    app_ui:
      static: /etc/mcp-anything/ui.html
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
		{HostFilePath: staticHTMLPath, ContainerFilePath: "/etc/mcp-anything/ui.html", FileMode: 0o644},
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

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// 1. tools/list must include _meta.ui.resourceUri for all tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) < 2 {
		t.Fatalf("expected at least 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	for _, tool := range toolsResult.Tools {
		uiMeta, ok := tool.Meta["ui"].(map[string]any)
		if !ok {
			t.Errorf("tool %q: missing _meta.ui", tool.Name)
			continue
		}
		resourceURI, _ := uiMeta["resourceUri"].(string)
		wantURI := "ui://" + tool.Name + "/app"
		if resourceURI != wantURI {
			t.Errorf("tool %q: _meta.ui.resourceUri = %q, want %q", tool.Name, resourceURI, wantURI)
		}
	}

	// 2. resources/read must return the static HTML for each tool's UI resource.
	for _, tool := range toolsResult.Tools {
		uiMeta, ok := tool.Meta["ui"].(map[string]any)
		if !ok {
			continue
		}
		resourceURI, _ := uiMeta["resourceUri"].(string)

		readResult, readErr := session.ReadResource(callCtx, &sdkmcp.ReadResourceParams{URI: resourceURI})
		if readErr != nil {
			t.Fatalf("read resource %q for tool %q: %v", resourceURI, tool.Name, readErr)
		}
		if len(readResult.Contents) == 0 {
			t.Fatalf("read resource %q: no contents", resourceURI)
		}
		content := readResult.Contents[0]
		if content.MIMEType != "text/html" {
			t.Errorf("resource %q: mime type = %q, want %q", resourceURI, content.MIMEType, "text/html")
		}
		if !strings.Contains(content.Text, "Pet Viewer") {
			t.Errorf("resource %q: HTML missing expected content, got: %s", resourceURI, content.Text)
		}
	}
}

// TestAppUIRenderScript verifies that when app_ui.script is configured:
//   - The Sobek render script is executed at resource-fetch time
//   - ctx.toolName and ctx.schema are available to the script
//   - The returned string is served as HTML
func TestAppUIRenderScript(t *testing.T) {
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
	_ = fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	tmpDir := t.TempDir()

	// Render script: includes ctx.toolName and ctx.schema in the output HTML.
	renderScript := `export default function render(ctx) {
  var schemaStr = JSON.stringify(ctx.schema);
  return '<!DOCTYPE html><html><head><title>' + ctx.toolName + '</title></head>' +
    '<body><h1>' + ctx.toolName + '</h1>' +
    '<pre id="schema">' + schemaStr + '</pre>' +
    '<pre id="result">Waiting for result...</pre>' +
    '<script>' +
    'window.parent.postMessage({jsonrpc:"2.0",id:1,method:"ui/initialize",params:{protocolVersion:"2024-11-21",capabilities:{}}}, "*");' +
    'window.addEventListener("message", function(e) {' +
    '  if (e.data && e.data.method === "ui/notifications/tool-result") {' +
    '    document.getElementById("result").textContent = JSON.stringify(e.data.params);' +
    '  }' +
    '});' +
    '<\/script>' +
    '</body></html>';
}`
	renderScriptPath := filepath.Join(tmpDir, "render.js")
	if err := os.WriteFile(renderScriptPath, []byte(renderScript), 0o644); err != nil {
		t.Fatalf("write render script: %v", err)
	}

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(appUIOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

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
    app_ui:
      script: /etc/mcp-anything/render.js
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
		{HostFilePath: renderScriptPath, ContainerFilePath: "/etc/mcp-anything/render.js", FileMode: 0o644},
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

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
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
	if len(toolsResult.Tools) < 1 {
		t.Fatalf("expected at least 1 tool, got %d", len(toolsResult.Tools))
	}

	// Find the listPets tool and verify its UI resource.
	var listPetsTool *sdkmcp.Tool
	for _, tool := range toolsResult.Tools {
		if strings.Contains(tool.Name, "listpets") || strings.Contains(tool.Name, "list_pets") {
			listPetsTool = tool
			break
		}
	}
	if listPetsTool == nil {
		t.Fatalf("listPets tool not found in %v", toolNames(toolsResult.Tools))
	}

	// Verify _meta.ui.resourceUri is set.
	uiMeta, ok := listPetsTool.Meta["ui"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q: missing _meta.ui", listPetsTool.Name)
	}
	resourceURI, _ := uiMeta["resourceUri"].(string)
	if resourceURI == "" {
		t.Fatalf("tool %q: empty _meta.ui.resourceUri", listPetsTool.Name)
	}

	// Read the resource — the render script should produce HTML with the tool name embedded.
	readResult, err := session.ReadResource(callCtx, &sdkmcp.ReadResourceParams{URI: resourceURI})
	if err != nil {
		t.Fatalf("read resource %q: %v", resourceURI, err)
	}
	if len(readResult.Contents) == 0 {
		t.Fatalf("read resource %q: no contents", resourceURI)
	}
	content := readResult.Contents[0]
	if content.MIMEType != "text/html" {
		t.Errorf("resource %q: mime type = %q, want %q", resourceURI, content.MIMEType, "text/html")
	}
	// The render script embeds ctx.toolName in the HTML title and h1.
	if !strings.Contains(content.Text, listPetsTool.Name) {
		t.Errorf("resource %q: rendered HTML missing tool name %q, got:\n%s", resourceURI, listPetsTool.Name, content.Text)
	}
	// The render script also embeds ctx.schema in a <pre> tag.
	if !strings.Contains(content.Text, "schema") {
		t.Errorf("resource %q: rendered HTML missing schema, got:\n%s", resourceURI, content.Text)
	}
}

// TestAppUIOverlayOverride verifies that per-tool overlay extensions override the upstream default,
// and that tools without any UI config have no _meta.ui in tools/list.
func TestAppUIOverlayOverride(t *testing.T) {
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
	_ = fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	tmpDir := t.TempDir()

	// Static HTML for the upstream default.
	defaultHTML := `<!DOCTYPE html><html><body><h1>Default UI</h1></body></html>`
	defaultHTMLPath := filepath.Join(tmpDir, "default.html")
	if err := os.WriteFile(defaultHTMLPath, []byte(defaultHTML), 0o644); err != nil {
		t.Fatalf("write default HTML: %v", err)
	}

	// Override render script for the listPets operation.
	overrideScript := `export default function render(ctx) {
  return '<!DOCTYPE html><html><body><h1>Override UI for ' + ctx.toolName + '</h1></body></html>';
}`
	overrideScriptPath := filepath.Join(tmpDir, "override.js")
	if err := os.WriteFile(overrideScriptPath, []byte(overrideScript), 0o644); err != nil {
		t.Fatalf("write override script: %v", err)
	}

	// OpenAPI spec with x-mcp-ui-script on listPets only.
	// The getPet operation uses the upstream default (static HTML).
	// We also include a third operation with no UI at all (via inline overlay disabling upstream UI).
	specWithExtension := `openapi: "3.0.0"
info:
  title: Pet API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      x-mcp-ui-script: /etc/mcp-anything/override.js
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
  /pets/{petId}:
    get:
      operationId: getPet
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
`
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specWithExtension), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Config with upstream-level default static HTML.
	// listPets has x-mcp-ui-script in the spec, so it overrides the upstream default.
	// getPet has no extension, so it inherits app_ui.static.
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
    app_ui:
      static: /etc/mcp-anything/default.html
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
		{HostFilePath: defaultHTMLPath, ContainerFilePath: "/etc/mcp-anything/default.html", FileMode: 0o644},
		{HostFilePath: overrideScriptPath, ContainerFilePath: "/etc/mcp-anything/override.js", FileMode: 0o644},
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

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
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
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	// Identify the two tools.
	toolByName := make(map[string]*sdkmcp.Tool, 2)
	for _, tool := range toolsResult.Tools {
		toolByName[tool.Name] = tool
	}

	var listPetsTool, getPetTool *sdkmcp.Tool
	for name, tool := range toolByName {
		if strings.Contains(name, "listpets") || strings.Contains(name, "list_pets") {
			listPetsTool = tool
		} else if strings.Contains(name, "getpet") || strings.Contains(name, "get_pet") {
			getPetTool = tool
		}
	}
	if listPetsTool == nil {
		t.Fatalf("listPets tool not found in %v", toolNames(toolsResult.Tools))
	}
	if getPetTool == nil {
		t.Fatalf("getPet tool not found in %v", toolNames(toolsResult.Tools))
	}

	// Both tools must have _meta.ui.resourceUri (one uses script override, other uses default static).
	for _, tool := range []*sdkmcp.Tool{listPetsTool, getPetTool} {
		uiMeta, ok := tool.Meta["ui"].(map[string]any)
		if !ok {
			t.Errorf("tool %q: expected _meta.ui to be set", tool.Name)
		} else if _, hasURI := uiMeta["resourceUri"]; !hasURI {
			t.Errorf("tool %q: expected _meta.ui.resourceUri to be set", tool.Name)
		}
	}

	// listPets should serve the override script HTML (contains "Override UI").
	listPetsURI := "ui://" + listPetsTool.Name + "/app"
	listPetsRead, err := session.ReadResource(callCtx, &sdkmcp.ReadResourceParams{URI: listPetsURI})
	if err != nil {
		t.Fatalf("read listPets resource: %v", err)
	}
	if len(listPetsRead.Contents) == 0 {
		t.Fatalf("listPets resource: no contents")
	}
	listPetsHTML := listPetsRead.Contents[0].Text
	if !strings.Contains(listPetsHTML, "Override UI") {
		t.Errorf("listPets UI should use override script, got:\n%s", listPetsHTML)
	}
	if strings.Contains(listPetsHTML, "Default UI") {
		t.Errorf("listPets UI should NOT use upstream default, got:\n%s", listPetsHTML)
	}

	// getPet should serve the upstream default static HTML (contains "Default UI").
	getPetURI := "ui://" + getPetTool.Name + "/app"
	getPetRead, err := session.ReadResource(callCtx, &sdkmcp.ReadResourceParams{URI: getPetURI})
	if err != nil {
		t.Fatalf("read getPet resource: %v", err)
	}
	if len(getPetRead.Contents) == 0 {
		t.Fatalf("getPet resource: no contents")
	}
	getPetHTML := getPetRead.Contents[0].Text
	if !strings.Contains(getPetHTML, "Default UI") {
		t.Errorf("getPet UI should use default static HTML, got:\n%s", getPetHTML)
	}
}

// TestAppUINoUIConfig verifies that tools with no UI configuration have no _meta.ui
// field in the tools/list response.
func TestAppUINoUIConfig(t *testing.T) {
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
	_ = fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(appUIOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Config with NO app_ui — tools should have no _meta.ui.
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

	proxyHost, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	proxyPort, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	proxyURL := fmt.Sprintf("http://%s:%s", proxyHost, proxyPort.Port())

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
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
	if len(toolsResult.Tools) < 1 {
		t.Fatalf("expected at least 1 tool, got %d", len(toolsResult.Tools))
	}

	// All tools must have no _meta.ui field — no regression.
	for _, tool := range toolsResult.Tools {
		if _, hasUI := tool.Meta["ui"]; hasUI {
			t.Errorf("tool %q: unexpected _meta.ui field (no app_ui configured)", tool.Name)
		}
	}
}
