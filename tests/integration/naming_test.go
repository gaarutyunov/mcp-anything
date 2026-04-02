//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

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

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() {
		if err := net.Remove(ctx); err != nil {
			t.Logf("remove network: %v", err)
		}
	})

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

	_, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: proxyReq,
		Started:          true,
	})
	if err == nil {
		t.Error("expected proxy to fail to start due to duplicate tool_prefix, but it started successfully")
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
