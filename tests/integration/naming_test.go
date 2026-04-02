//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const namingTestSpec = `openapi: "3.0.0"
info:
  title: Shop API
  version: "1.0"
paths:
  /pets:
    get:
      summary: List pets
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
      operationId: fetchPet
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
  /orders:
    post:
      summary: Place an order
      responses:
        "201":
          description: Created
  /orders/{orderId}:
    delete:
      summary: Delete an order
      parameters:
        - name: orderId
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: No Content
`

const namingTestOverlay = `overlay: 1.0.0
info:
  title: Naming overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535
actions:
  - target: $.paths["/orders"].post
    update:
      x-mcp-tool-name: place_order
  - target: $.paths["/orders/{orderId}"].delete
    update:
      x-mcp-enabled: false
`

func TestToolNamingEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := mustCreateNetwork(ctx, t)

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

	wiremockHost, err := wiremock.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	wiremockPort, err := wiremock.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	wiremockURL := fmt.Sprintf("http://%s:%s", wiremockHost, wiremockPort.Port())

	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"[]","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, wiremockURL, `{"request":{"method":"GET","urlPattern":"/pets/.*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, wiremockURL, `{"request":{"method":"POST","url":"/orders"},"response":{"status":201,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(namingTestSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	overlayPath := filepath.Join(tmpDir, "overlay.yaml")
	if err := os.WriteFile(overlayPath, []byte(namingTestOverlay), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: shop
    enabled: true
    tool_prefix: shop
    base_url: http://wiremock:8080
    timeout: 10s
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
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	// Expect exactly 3 tools:
	// - shop__list_pets (GET /pets, no operationId → default slug)
	// - shop__fetchpet (GET /pets/{petId}, operationId: fetchPet → sanitized to fetchpet)
	// - shop__place_order (POST /orders, x-mcp-tool-name: place_order)
	// DELETE /orders/{orderId} is disabled by overlay.
	if len(toolsResult.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	nameSet := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}

	expectedTools := []string{"shop__list_pets", "shop__fetchpet", "shop__place_order"}
	for _, want := range expectedTools {
		if !nameSet[want] {
			t.Errorf("expected tool %q, got %v", want, toolNames(toolsResult.Tools))
		}
	}
}

func TestConflictDetectionFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := mustCreateNetwork(ctx, t)

	// A minimal spec used by both (conflicting) upstreams.
	minimalSpec := `openapi: "3.0.0"
info:
  title: Minimal API
  version: "1.0"
paths:
  /items:
    get:
      summary: List items
      responses:
        "200":
          description: OK
`

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(minimalSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Two upstreams sharing the same tool_prefix — should cause a fatal startup error.
	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: upstream1
    enabled: true
    tool_prefix: conflict
    base_url: http://localhost:9998
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
  - name: upstream2
    enabled: true
    tool_prefix: conflict
    base_url: http://localhost:9999
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
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(30 * time.Second)

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: proxyReq,
		Started:          true,
	})
	if err == nil {
		_ = ctr.Terminate(ctx)
		t.Fatal("expected proxy to fail to start due to duplicate tool_prefix, but it started successfully")
	}
	// Verify the failure is specifically due to duplicate tool_prefix validation by
	// inspecting the container logs, since err is a testcontainers wait error that
	// does not include the proxy's log output.
	if ctr != nil {
		logs, logErr := ctr.Logs(ctx)
		if logErr == nil {
			defer logs.Close()
			logBytes, _ := io.ReadAll(logs)
			if !strings.Contains(string(logBytes), "tool_prefix") {
				t.Errorf("expected startup failure to mention 'tool_prefix' in logs, got: %s", string(logBytes))
			}
		}
	}
}

const inputSchemaSpec = `openapi: "3.0.0"
info:
  title: Schema API
  version: "1.0"
paths:
  /items:
    get:
      summary: List items with filters
      parameters:
        - name: status
          in: query
          schema:
            type: string
            enum: [available, pending, sold]
            description: "Filter by status"
        - name: limit
          in: query
          schema:
            type: integer
            minimum: 1
            maximum: 100
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
`

func TestInputSchemaPreservesConstraints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	net := mustCreateNetwork(ctx, t)

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
	registerStub(t, wiremockURL, `{"request":{"method":"GET","url":"/items"},"response":{"status":200,"body":"[]","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(inputSchemaSpec), 0o644); err != nil {
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
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	tool := toolsResult.Tools[0]
	// Marshal the InputSchema to JSON so we can inspect constraints.
	schemaBytes, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'properties' in schema, got: %v", schema)
	}

	// Assert status field has enum and description.
	statusProp, ok := props["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'status' property in schema, got props: %v", props)
	}
	enumVals, ok := statusProp["enum"].([]any)
	if !ok {
		t.Errorf("expected 'enum' on status property, got: %v", statusProp)
	} else {
		wantEnum := []string{"available", "pending", "sold"}
		for i, v := range wantEnum {
			if i >= len(enumVals) || enumVals[i] != v {
				t.Errorf("enum[%d]: want %q, got %v", i, v, enumVals[i])
			}
		}
	}
	if statusProp["description"] == "" || statusProp["description"] == nil {
		t.Errorf("expected non-empty 'description' on status property, got: %v", statusProp)
	}

	// Assert limit field has type integer with minimum and maximum.
	limitProp, ok := props["limit"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'limit' property in schema, got props: %v", props)
	}
	if limitProp["type"] != "integer" {
		t.Errorf("expected limit type 'integer', got: %v", limitProp["type"])
	}
	if limitProp["minimum"] != float64(1) {
		t.Errorf("expected limit minimum 1, got: %v", limitProp["minimum"])
	}
	if limitProp["maximum"] != float64(100) {
		t.Errorf("expected limit maximum 100, got: %v", limitProp["maximum"])
	}
}

// collisionProxySetup spins up a proxy with the given spec YAML and returns the
// MCP session and a cancel func. It registers a catch-all WireMock stub.
func collisionProxySetup(t *testing.T, specYAML string) (*sdkmcp.ClientSession, context.CancelFunc) {
	t.Helper()
	ctx := context.Background()

	net := mustCreateNetwork(ctx, t)

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
	// Catch-all stub for any HTTP method / URL.
	registerStub(t, wiremockURL, `{"request":{"method":"ANY","urlPattern":".*"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o644); err != nil {
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
  - name: collision
    enabled: true
    tool_prefix: col
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
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return session, cancel
}

// toolRawSchema fetches the single tool's InputSchema as a raw map for inspection.
func toolRawSchema(t *testing.T, session *sdkmcp.ClientSession, callCtx context.Context) map[string]any {
	t.Helper()
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}
	schemaBytes, err := json.Marshal(toolsResult.Tools[0].InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	return schema
}

// toolInputSchema returns the InputSchema "properties" map of the single tool.
func toolInputSchema(t *testing.T, session *sdkmcp.ClientSession, callCtx context.Context) map[string]any {
	t.Helper()
	schema := toolRawSchema(t, session, callCtx)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'properties' in schema, got: %v", schema)
	}
	return props
}

// toolRequiredFields returns the "required" fields of the single tool's InputSchema as a set.
func toolRequiredFields(t *testing.T, session *sdkmcp.ClientSession, callCtx context.Context) map[string]bool {
	t.Helper()
	schema := toolRawSchema(t, session, callCtx)
	rawReq, _ := schema["required"].([]any)
	required := make(map[string]bool, len(rawReq))
	for _, r := range rawReq {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}
	return required
}

// TestInputSchemaCollisionPathAndBody checks that when a path parameter and a
// request body property share the same name, the schema renames them to
// {name}_path and {name}_body respectively.
func TestInputSchemaCollisionPathAndBody(t *testing.T) {
	t.Parallel()

	spec := `openapi: "3.0.0"
info:
  title: Collision API
  version: "1.0"
paths:
  /items/{id}:
    post:
      summary: Create item
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            description: path ID
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [id]
              properties:
                id:
                  type: integer
                  description: body ID
      responses:
        "200":
          description: OK
`

	session, cancel := collisionProxySetup(t, spec)
	defer cancel()

	props := toolInputSchema(t, session, context.Background())

	if _, ok := props["id"]; ok {
		t.Errorf("expected 'id' to be renamed on collision, but found plain 'id' key")
	}
	if _, ok := props["id_path"]; !ok {
		t.Errorf("expected 'id_path' key from path param collision, got keys: %v", mapKeys(props))
	}
	if _, ok := props["id_body"]; !ok {
		t.Errorf("expected 'id_body' key from body property collision, got keys: %v", mapKeys(props))
	}

	// Path param must be string, body property must be integer.
	if idPath, ok := props["id_path"].(map[string]any); ok {
		if idPath["type"] != "string" {
			t.Errorf("id_path: expected type 'string', got %v", idPath["type"])
		}
	}
	if idBody, ok := props["id_body"].(map[string]any); ok {
		if idBody["type"] != "integer" {
			t.Errorf("id_body: expected type 'integer', got %v", idBody["type"])
		}
	}

	// Both id_path (path param) and id_body (required in body) must appear in required.
	required := toolRequiredFields(t, session, context.Background())
	if !required["id_path"] {
		t.Errorf("expected 'id_path' to be required (path param), got required: %v", required)
	}
	if !required["id_body"] {
		t.Errorf("expected 'id_body' to be required (body field marked required), got required: %v", required)
	}
}

// TestInputSchemaCollisionQueryAndBody checks that when a query parameter and a
// request body property share the same name, the schema renames them to
// {name}_query and {name}_body.
func TestInputSchemaCollisionQueryAndBody(t *testing.T) {
	t.Parallel()

	spec := `openapi: "3.0.0"
info:
  title: Collision API
  version: "1.0"
paths:
  /search:
    post:
      summary: Search
      parameters:
        - name: filter
          in: query
          schema:
            type: string
            description: query filter
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                filter:
                  type: integer
                  description: body filter
      responses:
        "200":
          description: OK
`

	session, cancel := collisionProxySetup(t, spec)
	defer cancel()

	props := toolInputSchema(t, session, context.Background())

	if _, ok := props["filter"]; ok {
		t.Errorf("expected 'filter' to be renamed on collision, but found plain 'filter' key")
	}
	if _, ok := props["filter_query"]; !ok {
		t.Errorf("expected 'filter_query' key, got keys: %v", mapKeys(props))
	}
	if _, ok := props["filter_body"]; !ok {
		t.Errorf("expected 'filter_body' key, got keys: %v", mapKeys(props))
	}

	if fq, ok := props["filter_query"].(map[string]any); ok {
		if fq["type"] != "string" {
			t.Errorf("filter_query: expected type 'string', got %v", fq["type"])
		}
	}
	if fb, ok := props["filter_body"].(map[string]any); ok {
		if fb["type"] != "integer" {
			t.Errorf("filter_body: expected type 'integer', got %v", fb["type"])
		}
	}
}

// TestInputSchemaCollisionHeaderAndBody checks that when a header parameter and
// a request body property share the same name, the schema renames them to
// {name}_header and {name}_body.
func TestInputSchemaCollisionHeaderAndBody(t *testing.T) {
	t.Parallel()

	spec := `openapi: "3.0.0"
info:
  title: Collision API
  version: "1.0"
paths:
  /upload:
    post:
      summary: Upload
      parameters:
        - name: X-Checksum
          in: header
          schema:
            type: string
            description: header checksum
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                X-Checksum:
                  type: integer
                  description: body checksum
      responses:
        "200":
          description: OK
`

	session, cancel := collisionProxySetup(t, spec)
	defer cancel()

	props := toolInputSchema(t, session, context.Background())

	if _, ok := props["X-Checksum"]; ok {
		t.Errorf("expected 'X-Checksum' to be renamed on collision, but found plain key")
	}
	if _, ok := props["X-Checksum_header"]; !ok {
		t.Errorf("expected 'X-Checksum_header' key, got keys: %v", mapKeys(props))
	}
	if _, ok := props["X-Checksum_body"]; !ok {
		t.Errorf("expected 'X-Checksum_body' key, got keys: %v", mapKeys(props))
	}

	if hdr, ok := props["X-Checksum_header"].(map[string]any); ok {
		if hdr["type"] != "string" {
			t.Errorf("X-Checksum_header: expected type 'string', got %v", hdr["type"])
		}
	}
	if body, ok := props["X-Checksum_body"].(map[string]any); ok {
		if body["type"] != "integer" {
			t.Errorf("X-Checksum_body: expected type 'integer', got %v", body["type"])
		}
	}
}

// TestInputSchemaMultipleCollisions checks that multiple simultaneous collisions
// (path+body for "name", query+body for "tag") are all resolved with suffixes,
// while non-colliding params keep their original names.
func TestInputSchemaMultipleCollisions(t *testing.T) {
	t.Parallel()

	spec := `openapi: "3.0.0"
info:
  title: Collision API
  version: "1.0"
paths:
  /items/{name}:
    post:
      summary: Create item
      parameters:
        - name: name
          in: path
          required: true
          schema:
            type: string
        - name: tag
          in: query
          schema:
            type: string
        - name: page
          in: query
          schema:
            type: integer
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name:
                  type: integer
                tag:
                  type: boolean
                description:
                  type: string
      responses:
        "200":
          description: OK
`

	session, cancel := collisionProxySetup(t, spec)
	defer cancel()

	props := toolInputSchema(t, session, context.Background())

	// Colliding params must be renamed.
	for _, plain := range []string{"name", "tag"} {
		if _, ok := props[plain]; ok {
			t.Errorf("expected %q to be renamed on collision, but found plain key", plain)
		}
	}

	// Renamed keys must exist.
	expected := []string{"name_path", "name_body", "tag_query", "tag_body"}
	for _, key := range expected {
		if _, ok := props[key]; !ok {
			t.Errorf("expected renamed key %q, got keys: %v", key, mapKeys(props))
		}
	}

	// Non-colliding params must keep original names.
	for _, plain := range []string{"page", "description"} {
		if _, ok := props[plain]; !ok {
			t.Errorf("expected non-colliding param %q to keep original name, got keys: %v", plain, mapKeys(props))
		}
	}

	// Type assertions.
	if p, ok := props["name_path"].(map[string]any); ok {
		if p["type"] != "string" {
			t.Errorf("name_path: want type 'string', got %v", p["type"])
		}
	}
	if p, ok := props["name_body"].(map[string]any); ok {
		if p["type"] != "integer" {
			t.Errorf("name_body: want type 'integer', got %v", p["type"])
		}
	}
	if p, ok := props["tag_query"].(map[string]any); ok {
		if p["type"] != "string" {
			t.Errorf("tag_query: want type 'string', got %v", p["type"])
		}
	}
	if p, ok := props["tag_body"].(map[string]any); ok {
		if p["type"] != "boolean" {
			t.Errorf("tag_body: want type 'boolean', got %v", p["type"])
		}
	}

	// name_path (path param) and name_body (required in body) must appear in required.
	required := toolRequiredFields(t, session, context.Background())
	if !required["name_path"] {
		t.Errorf("expected 'name_path' to be required (path param), got required: %v", required)
	}
	if !required["name_body"] {
		t.Errorf("expected 'name_body' to be required (body field marked required), got required: %v", required)
	}
}

// mustCreateNetwork creates a Docker bridge network and registers cleanup with t.
func mustCreateNetwork(ctx context.Context, t *testing.T) *testcontainers.DockerNetwork {
	t.Helper()
	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})
	return net
}

// mapKeys returns the keys of a map as a slice for readable error messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
