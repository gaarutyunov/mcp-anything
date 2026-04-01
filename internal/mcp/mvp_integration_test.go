//go:build integration

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	mcppkg "github.com/gaarutyunov/mcp-anything/internal/mcp"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/testutil"
)

const testOpenAPISpec = `openapi: "3.0.0"
info:
  title: Test API
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
                type: object
                properties:
                  pets:
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

func TestMVPHappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// 1. Start WireMock container.
	req := testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		WaitingFor:   wait.ForHTTP("/__admin/mappings").WithStartupTimeout(60 * time.Second),
	}
	c := testutil.MustStartContainer(ctx, t, req)

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("get container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get mapped port: %v", err)
	}
	wireMockBase := "http://" + host + ":" + port.Port()

	// 2. Register WireMock stubs.
	registerStub(t, wireMockBase, `{
		"request": {"method": "GET", "url": "/pets"},
		"response": {"status": 200, "body": "{\"pets\":[{\"id\":1,\"name\":\"Fido\"}]}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wireMockBase, `{
		"request": {"method": "GET", "url": "/pets/1"},
		"response": {"status": 200, "body": "{\"id\":1,\"name\":\"Fido\",\"species\":\"dog\"}", "headers": {"Content-Type": "application/json"}}
	}`)

	// 3. Write OpenAPI spec to temp file.
	specFile, err := os.CreateTemp(t.TempDir(), "spec-*.yaml")
	if err != nil {
		t.Fatalf("create spec file: %v", err)
	}
	if _, err := specFile.WriteString(testOpenAPISpec); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	specFile.Close()

	// 4. Write minimal config to temp file.
	cfgContent := fmt.Sprintf(`server:
  port: 0
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: %s
    timeout: 10s
    openapi:
      source: %s
      version: "3.0"
`, wireMockBase, specFile.Name())

	cfgFile, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("create config file: %v", err)
	}
	if _, err := cfgFile.WriteString(cfgContent); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	cfgFile.Close()

	// 5. Start the proxy.
	cfg, err := config.Load(cfgFile.Name())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	upstream := cfg.Upstreams[0]
	doc, _, err := openapi.Load(ctx, upstream.OpenAPI)
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}

	tools, err := openapi.GenerateTools(doc, &upstream, cfg.Naming.Separator)
	if err != nil {
		t.Fatalf("generate tools: %v", err)
	}

	httpClient := &http.Client{Timeout: upstream.Timeout}
	mcpSrv := mcppkg.New(
		&sdkmcp.Implementation{Name: "mcp-anything", Version: cfg.Telemetry.ServiceVersion},
		tools, &upstream, httpClient,
	)

	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server { return mcpSrv }, nil)

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route /mcp to the MCP handler; /healthz and /readyz return 200.
		switch {
		case strings.HasPrefix(r.URL.Path, "/mcp"):
			http.StripPrefix("/mcp", handler).ServeHTTP(w, r)
		case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(proxyServer.Close)

	// 6. Create MCP client.
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyServer.URL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// 7. Assert tools/list returns exactly 2 tools.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(toolsResult.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))
	}

	// Verify the expected tool names are present.
	// Naming convention: {prefix}{sep}{verb}_{path_slug}
	// - GET /pets           → list_pets     → test__list_pets
	// - GET /pets/{petId}   → get_pets_petid → test__get_pets_petid
	const toolListPets = "test__list_pets"
	const toolGetPet = "test__get_pets_petid"

	nameSet := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		nameSet[tool.Name] = true
	}
	if !nameSet[toolListPets] {
		t.Errorf("expected tool %s, got %v", toolListPets, toolNames(toolsResult.Tools))
	}
	if !nameSet[toolGetPet] {
		t.Errorf("expected tool %s, got %v", toolGetPet, toolNames(toolsResult.Tools))
	}

	// Verify limit param on list_pets is optional integer.
	for _, tool := range toolsResult.Tools {
		if tool.Name == toolListPets {
			schema := tool.InputSchema
			if schema == nil {
				t.Error("list_pets has nil InputSchema")
				continue
			}
			limitProp, ok := schema.Properties["limit"]
			if !ok {
				t.Error("list_pets missing limit property in InputSchema")
				continue
			}
			if limitProp.Type != "integer" {
				t.Errorf("limit param type: got %q, want %q", limitProp.Type, "integer")
			}
			// limit should not be in required.
			for _, req := range schema.Required {
				if req == "limit" {
					t.Error("limit should not be required")
				}
			}
		}
		if tool.Name == toolGetPet {
			schema := tool.InputSchema
			if schema == nil {
				t.Error("get_pets_petid has nil InputSchema")
				continue
			}
			_, ok := schema.Properties["petId"]
			if !ok {
				t.Error("get_pets_petid missing petId property in InputSchema")
				continue
			}
			found := false
			for _, req := range schema.Required {
				if req == "petId" {
					found = true
					break
				}
			}
			if !found {
				t.Error("petId should be required in get_pets_petid")
			}
		}
	}

	// 8. Assert tools/call list_pets succeeds.
	listResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: toolListPets,
	})
	if err != nil {
		t.Fatalf("call %s: %v", toolListPets, err)
	}
	if listResult.IsError {
		t.Fatalf("%s returned error: %v", toolListPets, contentText(listResult.Content))
	}
	if len(listResult.Content) == 0 {
		t.Fatalf("%s returned empty content", toolListPets)
	}
	text := contentText(listResult.Content)
	if !strings.Contains(text, "Fido") {
		t.Errorf("%s response missing Fido: %s", toolListPets, text)
	}

	// 9. Assert tools/call get_pets_petid with petId=1 succeeds.
	getResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      toolGetPet,
		Arguments: map[string]any{"petId": "1"},
	})
	if err != nil {
		t.Fatalf("call %s: %v", toolGetPet, err)
	}
	if getResult.IsError {
		t.Fatalf("%s returned error: %v", toolGetPet, contentText(getResult.Content))
	}
	text = contentText(getResult.Content)
	if !strings.Contains(text, "dog") {
		t.Errorf("%s response missing species: %s", toolGetPet, text)
	}

	// 10. Assert GET /healthz returns 200.
	checkHealth(t, proxyServer.URL+"/healthz")

	// 11. Assert GET /readyz returns 200.
	checkHealth(t, proxyServer.URL+"/readyz")

	// Verify WireMock received the requests.
	verifyWireMockRequests(t, wireMockBase)
}

// registerStub registers a WireMock stub mapping.
func registerStub(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/__admin/mappings", "application/json", bytes.NewBufferString(body)) //nolint:noctx // test setup, context not needed here
	if err != nil {
		t.Fatalf("register wiremock stub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register wiremock stub: got %d: %s", resp.StatusCode, b)
	}
}

// checkHealth asserts that a GET to url returns 200.
func checkHealth(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // health check, no context needed
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: expected 200, got %d", url, resp.StatusCode)
	}
}

// verifyWireMockRequests checks the WireMock request journal.
func verifyWireMockRequests(t *testing.T, base string) {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx // verification, no context needed
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
	if len(result.Requests) < 2 {
		t.Errorf("expected at least 2 wiremock requests, got %d", len(result.Requests))
	}
}

// toolNames returns a slice of tool names for error messages.
func toolNames(tools []*sdkmcp.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// contentText extracts the text from the first TextContent in a result.
func contentText(content []sdkmcp.Content) string {
	for _, c := range content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
