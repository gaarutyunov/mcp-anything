//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

const jsAuthOpenAPISpec = `openapi: "3.0.0"
info:
  title: JS Auth Test API
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

// TestInboundJSAuthAllowsValidToken verifies that the proxy accepts requests with the
// token the JS script approves, and rejects others (strategy: js_script).
func TestInboundJSAuthAllowsValidToken(t *testing.T) {
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	jsScript := `
export default function(token, ctx) {
  if (token === "valid-token") {
    return { allowed: true, subject: "test-user" };
  }
  return { allowed: false, status: 401, error: "forbidden" };
}
`
	jsPath := filepath.Join(tmpDir, "auth.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: js_script
  js:
    script_path: /etc/mcp-anything/auth.js
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
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/auth.js", FileMode: 0o644},
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

// TestInboundJSExtraHeadersInjected verifies that extra_headers returned by the JS inbound
// script are forwarded to the upstream.
func TestInboundJSExtraHeadersInjected(t *testing.T) {
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// JS script returns extra_headers with X-User-ID.
	jsScript := `
export default function(token, ctx) {
  return { allowed: true, subject: "user-42", extra_headers: { "X-User-ID": "user-42" } };
}
`
	jsPath := filepath.Join(tmpDir, "auth.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: js_script
  js:
    script_path: /etc/mcp-anything/auth.js
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
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/auth.js", FileMode: 0o644},
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

// TestOutboundJSTokenInjected verifies that the proxy calls the JS outbound script
// and injects the returned token as Authorization: Bearer.
func TestOutboundJSTokenInjected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Stub: require Authorization: Bearer js-token → 200; else 401.
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer js-token"}}
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Outbound JS script returns a static token.
	jsScript := `
export default function(upstream, ctx) {
  return { token: "js-token" };
}
`
	jsPath := filepath.Join(tmpDir, "outbound.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
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
      strategy: js_script
      js:
        script_path: /etc/mcp-anything/outbound.js
        timeout: 500ms
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/outbound.js", FileMode: 0o644},
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

	// Verify WireMock received Bearer js-token.
	authHeaders := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range authHeaders {
		if h == "Bearer js-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Authorization: Bearer js-token, got: %v", authHeaders)
	}
}

// TestJSContextJwtDecode verifies that ctx.jwt.decode works inside a JS inbound auth script,
// decoding the bearer token and using the subject claim to drive the allow/deny decision.
func TestJSContextJwtDecode(t *testing.T) {
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// The script decodes the JWT and allows only if subject == "allowed-user".
	jsScript := `
export default function(token, ctx) {
  try {
    var claims = ctx.jwt.decode(token);
    if (claims && claims.sub === "allowed-user") {
      return { allowed: true, subject: claims.sub };
    }
    return { allowed: false, status: 401, error: "bad subject" };
  } catch (e) {
    return { allowed: false, status: 401, error: "invalid jwt" };
  }
}
`
	jsPath := filepath.Join(tmpDir, "auth.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: js_script
  js:
    script_path: /etc/mcp-anything/auth.js
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
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/auth.js", FileMode: 0o644},
	})

	// Build a minimal JWT: header.{"sub":"allowed-user"}.signature (no real sig needed).
	allowedJWT := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJhbGxvd2VkLXVzZXIifQ.sig"
	deniedJWT := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJiYWQtdXNlciJ9.sig"

	// Valid subject → allowed.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, allowedJWT)
	tools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools with allowed JWT: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("expected tools with allowed subject")
	}

	// Wrong subject → 401.
	resp := mcpPost(t, proxyURL, "tools/list", nil, "Bearer "+deniedJWT)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("denied JWT: got status %d, want 401", resp.StatusCode)
	}
}

// TestJSContextCacheAndFetch verifies that ctx.cache.get/set and ctx.fetch work inside a JS
// inbound auth script: the script fetches a token from an external endpoint (WireMock) and
// caches it, so the external call happens only once across multiple requests.
func TestJSContextCacheAndFetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Stub for the upstream API.
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)
	// Stub for the "token authority" that the JS script fetches from.
	registerStub(t, wiremockURL, `{
		"priority": 2,
		"request": {"method": "GET", "url": "/auth/token"},
		"response": {"status": 200, "body": "{\"secret\":\"magic\"}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()

	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Script: check bearer == "good-token", fetch a secret from /auth/token, cache it, validate.
	jsScript := `
export default function(token, ctx) {
  if (token !== "good-token") {
    return { allowed: false, status: 401, error: "bad token" };
  }
  var secret = ctx.cache.get("fetched_secret");
  if (!secret) {
    var resp = ctx.fetch("http://wiremock:8080/auth/token");
    secret = resp.secret;
    ctx.cache.set("fetched_secret", secret, 60);
  }
  if (secret === "magic") {
    return { allowed: true, subject: "cached-user" };
  }
  return { allowed: false, status: 403, error: "bad secret" };
}
`
	jsPath := filepath.Join(tmpDir, "auth.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: js_script
  js:
    script_path: /etc/mcp-anything/auth.js
    timeout: 2s
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
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/auth.js", FileMode: 0o644},
	})

	// Good token should succeed (fetches from /auth/token and caches the secret).
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, "good-token")
	tools, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("expected tools")
	}

	// Bad token → 401.
	resp := mcpPost(t, proxyURL, "tools/list", nil, "Bearer bad-token")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: got status %d, want 401", resp.StatusCode)
	}
}

// TestLuaJSCoexistence verifies that Lua inbound auth and JS outbound auth can operate
// together: the Lua script validates the bearer, and the JS script provides the upstream token.
func TestLuaJSCoexistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wiremockURL := startWiremockOnNetwork(ctx, t, net.Name)
	// Upstream requires a specific token injected by the JS outbound script.
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer js-outbound-token"}}
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Lua inbound: only allow "lua-client-token".
	luaScript := `
local token = ...
if token == "lua-client-token" then
    return true, 200, {}, ""
end
return false, 401, {}, "forbidden"
`
	luaPath := filepath.Join(tmpDir, "auth.lua")
	if err := os.WriteFile(luaPath, []byte(luaScript), 0o644); err != nil {
		t.Fatalf("write lua script: %v", err)
	}

	// JS outbound: returns a fixed upstream token.
	jsScript := `
export default function(upstream, ctx) {
  return { token: "js-outbound-token" };
}
`
	jsPath := filepath.Join(tmpDir, "outbound.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
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
    outbound_auth:
      strategy: js_script
      js:
        script_path: /etc/mcp-anything/outbound.js
        timeout: 500ms
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: luaPath, ContainerFilePath: "/etc/mcp-anything/auth.lua", FileMode: 0o644},
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/outbound.js", FileMode: 0o644},
	})

	// Valid inbound token → Lua accepts, JS injects outbound token → upstream 200.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, "lua-client-token")

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received Authorization: Bearer js-outbound-token.
	authHeaders := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range authHeaders {
		if h == "Bearer js-outbound-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Authorization: Bearer js-outbound-token forwarded to upstream, got: %v", authHeaders)
	}

	// Wrong inbound token → 401 (Lua rejects it).
	resp := mcpPost(t, proxyURL, "tools/list", nil, "Bearer wrong-token")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong inbound token: got status %d, want 401", resp.StatusCode)
	}
}

// TestOutboundJSRawHeadersInjected verifies that raw_headers returned by the JS outbound
// script are injected verbatim into upstream requests.
func TestOutboundJSRawHeadersInjected(t *testing.T) {
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
	if err := os.WriteFile(specPath, []byte(jsAuthOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	jsScript := `
export default function(upstream, ctx) {
  return { raw_headers: { "X-API-Key": "key123", "X-Tenant": "acme" } };
}
`
	jsPath := filepath.Join(tmpDir, "outbound.js")
	if err := os.WriteFile(jsPath, []byte(jsScript), 0o644); err != nil {
		t.Fatalf("write js script: %v", err)
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
      strategy: js_script
      js:
        script_path: /etc/mcp-anything/outbound.js
        timeout: 500ms
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyURL := startLuaProxy(ctx, t, net.Name, cfgPath, specPath, []testcontainers.ContainerFile{
		{HostFilePath: jsPath, ContainerFilePath: "/etc/mcp-anything/outbound.js", FileMode: 0o644},
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
