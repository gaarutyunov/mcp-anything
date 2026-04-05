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
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const luaAuthOpenAPISpec = `openapi: "3.0.0"
info:
  title: Lua Auth Test API
  version: "1.0"
paths:
  /data:
    get:
      operationId: getData
      summary: Get data
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// startLuaProxy starts a proxy container with the given config, spec, and extra files mounted.
// It joins the provided network.
func startLuaProxy(ctx context.Context, t *testing.T, netName, cfgPath, specPath string, extraFiles []testcontainers.ContainerFile) string {
	t.Helper()
	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = map[string]string{"CONFIG_PATH": "/etc/mcp-anything/config.yaml"}
	proxyReq.Files = append([]testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}, extraFiles...)
	proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
	proxy := startContainer(ctx, t, proxyReq)

	host, err := proxy.Host(ctx)
	if err != nil {
		t.Fatalf("get proxy host: %v", err)
	}
	port, err := proxy.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get proxy port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// startWiremockOnNetwork starts a WireMock container on the given network and returns
// (container, externalURL).
func startWiremockOnNetwork(ctx context.Context, t *testing.T, netName string) (externalURL string) {
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
	host, err := wm.Host(ctx)
	if err != nil {
		t.Fatalf("get wiremock host: %v", err)
	}
	port, err := wm.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get wiremock port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestInboundLuaAuthAllowsValidToken verifies that the proxy accepts requests with the
// token that the Lua script approves, and rejects others (AC-15.1, AC-15.4).
func TestInboundLuaAuthAllowsValidToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(luaAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	luaScript := `
local token = ...
if token == "valid-token" then
    return true, 200, {}, ""
end
return false, 401, {}, "forbidden"
`
	luaPath := filepath.Join(tmpDir, "auth.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: lua
  lua:
    script_path: /etc/mcp-anything/auth.lua
    timeout: 500ms
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

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/auth.lua", FileMode: 0o644},
	})

	// Valid token should succeed.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, "valid-token")
	tools, err := session.ListTools(callCtx, &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools with valid token: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("expected at least one tool with valid token")
	}

	// Wrong token should get 401.
	resp := mcpPost(t, proxyURL, "tools/list", nil, "Bearer wrong-token")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token: got status %d, want 401", resp.StatusCode)
	}
}

// TestInboundLuaExtraHeadersInjected verifies that extra_headers returned by the Lua script
// are forwarded to the upstream (AC-15.1).
func TestInboundLuaExtraHeadersInjected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Stub that accepts any request on /data and responds 200.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(luaAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Lua script returns extra_headers with X-User-ID.
	luaScript := `
local token = ...
return true, 200, {["X-User-ID"] = "user-42"}, ""
`
	luaPath := filepath.Join(tmpDir, "auth.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: lua
  lua:
    script_path: /etc/mcp-anything/auth.lua
    timeout: 500ms
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

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/auth.lua", FileMode: 0o644},
	})

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, "any-token")

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received X-User-ID header.
	vals := wiremockRequestHeader(t, wiremockURL, "X-User-Id")
	found := false
	for _, v := range vals {
		if v == "user-42" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected X-User-ID: user-42 forwarded to upstream, got: %v", vals)
	}
}

// TestOutboundLuaTokenInjected verifies that the proxy calls the Lua outbound script
// and injects the returned token as Authorization: Bearer (AC-18.6).
func TestOutboundLuaTokenInjected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Stub: require Authorization: Bearer lua-token → 200; else 401.
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer lua-token"}}
		},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"priority": 5,
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 401, "body": "unauthorized"}
	}`)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(luaAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Outbound Lua script returns a static token with short expiry.
	luaScript := `
local upstream, cached_token, cached_expiry = ...
return "lua-token", 0, {}, ""
`
	luaPath := filepath.Join(tmpDir, "outbound.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
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
    outbound_auth:
      strategy: lua
      lua:
        script_path: /etc/mcp-anything/outbound.lua
        timeout: 500ms
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/outbound.lua", FileMode: 0o644},
	})

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received Bearer lua-token.
	authHeaders := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range authHeaders {
		if h == "Bearer lua-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Authorization: Bearer lua-token, got: %v", authHeaders)
	}
}

// TestOutboundLuaRawHeadersInjected verifies that raw_headers returned by the Lua outbound
// script are injected verbatim into upstream requests (AC-18.6).
func TestOutboundLuaRawHeadersInjected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Stub: require both custom headers → 200.
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {
				"X-API-Key": {"equalTo": "key123"},
				"X-Tenant": {"equalTo": "acme"}
			}
		},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"priority": 5,
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 401, "body": "unauthorized"}
	}`)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(luaAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	luaScript := `
local upstream, cached_token, cached_expiry = ...
return "", 0, {["X-API-Key"] = "key123", ["X-Tenant"] = "acme"}, ""
`
	luaPath := filepath.Join(tmpDir, "outbound.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
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
    outbound_auth:
      strategy: lua
      lua:
        script_path: /etc/mcp-anything/outbound.lua
        timeout: 500ms
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/outbound.lua", FileMode: 0o644},
	})

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	transport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received both custom headers.
	apiKeyVals := wiremockRequestHeader(t, wiremockURL, "X-Api-Key")
	foundAPIKey := false
	for _, v := range apiKeyVals {
		if v == "key123" {
			foundAPIKey = true
			break
		}
	}
	if !foundAPIKey {
		t.Errorf("expected X-API-Key: key123, got: %v", apiKeyVals)
	}

	tenantVals := wiremockRequestHeader(t, wiremockURL, "X-Tenant")
	foundTenant := false
	for _, v := range tenantVals {
		if v == "acme" {
			foundTenant = true
			break
		}
	}
	if !foundTenant {
		t.Errorf("expected X-Tenant: acme, got: %v", tenantVals)
	}
}

// TestLuaSandboxPreventsFSAccess verifies that a Lua script that tries to open files
// fails at runtime, causing auth errors (AC-15.2).
func TestLuaSandboxPreventsFSAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	_ = startWiremockOnNetwork(ctx, t, net.Name)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(luaAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Script tries to use io.open — should fail since io is not in sandbox.
	luaScript := `
local token = ...
local f = io.open("/etc/passwd", "r")
return true, 200, {}, ""
`
	luaPath := filepath.Join(tmpDir, "auth.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: lua
  lua:
    script_path: /etc/mcp-anything/auth.lua
    timeout: 500ms
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

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/auth.lua", FileMode: 0o644},
	})

	// Any request with a token should fail auth (lua script errors at runtime).
	resp := mcpPost(t, proxyURL, "tools/list", nil, "Bearer any-token")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 from sandboxed script, got %d", resp.StatusCode)
	}
}

// wiremockRequestHeader returns all values of the given header (case-insensitive) from WireMock's request journal.
func wiremockRequestHeader(t *testing.T, base, header string) []string {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx
	if err != nil {
		t.Fatalf("get wiremock requests: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read requests body: %v", err)
	}

	var result struct {
		Requests []struct {
			Request struct {
				Headers map[string]string `json:"headers"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse requests: %v", err)
	}

	normalised := strings.ToLower(header)
	var vals []string
	for _, r := range result.Requests {
		for k, v := range r.Request.Headers {
			if strings.ToLower(k) == normalised && v != "" {
				vals = append(vals, v)
			}
		}
	}
	return vals
}
