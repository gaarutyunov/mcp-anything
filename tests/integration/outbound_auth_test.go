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

// outboundOpenAPISpec is a minimal OpenAPI spec used by outbound auth tests.
const outboundOpenAPISpec = `openapi: "3.0.0"
info:
  title: Outbound Auth Test API
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

// TestBearerTokenInjected verifies that the proxy injects the outbound Bearer token
// from an env var into the Authorization header of upstream requests (AC-18.4).
func TestBearerTokenInjected(t *testing.T) {
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

	// Stub: with correct Authorization header → 200 (priority 1, higher than default).
	// Without the correct header → 401 (priority 5, default — matched last).
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer static-test-token"}}
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
	if err := os.WriteFile(specPath, []byte(outboundOpenAPISpec), 0o644); err != nil {
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
    outbound_auth:
      strategy: bearer
      bearer:
        token_env: UPSTREAM_TOKEN
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH":    "/etc/mcp-anything/config.yaml",
		"UPSTREAM_TOKEN": "static-test-token",
	}
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

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received the correct Authorization header.
	headers := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range headers {
		if h == "Bearer static-test-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Authorization: Bearer static-test-token in upstream requests, got: %v", headers)
	}
}

// TestAPIKeyHeaderInjected verifies that the proxy injects the API key header into
// upstream requests from an env var (AC-18.5).
func TestAPIKeyHeaderInjected(t *testing.T) {
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

	// Stub: with correct X-API-Key → 200 (priority 1); without → 403 (priority 5).
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"X-API-Key": {"equalTo": "mysecret"}}
		},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)
	registerStub(t, wiremockURL, `{
		"priority": 5,
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 403, "body": "forbidden"}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(outboundOpenAPISpec), 0o644); err != nil {
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
    outbound_auth:
      strategy: api_key
      api_key:
        header: X-API-Key
        value_env: UPSTREAM_KEY
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH":  "/etc/mcp-anything/config.yaml",
		"UPSTREAM_KEY": "mysecret",
	}
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

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received the X-API-Key header.
	apiKeyHeaders := wiremockRequestAPIKeyHeaders(t, wiremockURL, "X-Api-Key")
	found := false
	for _, h := range apiKeyHeaders {
		if h == "mysecret" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected X-API-Key: mysecret in upstream requests, got: %v", apiKeyHeaders)
	}
}

// TestOAuth2ClientCredentials verifies that the proxy obtains a token via the
// OAuth2 client credentials flow and injects it as a Bearer token (AC-18.3).
func TestOAuth2ClientCredentials(t *testing.T) {
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

	// Start Keycloak for OAuth2 client credentials flow.
	kc := useSharedKeycloak(ctx, t, net.ID, net.Name)

	// Configure Keycloak: create a client with service accounts enabled.
	adminToken := kcAdminToken(t, kc.ExternalURL)
	clientUUID := kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-upstream")
	clientSecret := kcClientSecret(t, kc.ExternalURL, adminToken, kc.Realm, clientUUID)

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

	// Stub: any non-empty Bearer token → 200 (priority 1); without → 401 (priority 5).
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"matches": "Bearer .+"}}
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
	if err := os.WriteFile(specPath, []byte(outboundOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", kc.InternalURL, kc.Realm)
	cfgContent := fmt.Sprintf(`server:
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
      strategy: oauth2_client_credentials
      oauth2_client_credentials:
        token_url: %s
        client_id: mcp-upstream
        client_secret: ${OAUTH2_CLIENT_SECRET}
`, tokenURL)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH":          "/etc/mcp-anything/config.yaml",
		"OAUTH2_CLIENT_SECRET": clientSecret,
	}
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

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify WireMock received a non-empty Bearer token.
	headers := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range headers {
		if strings.HasPrefix(h, "Bearer ") && len(h) > len("Bearer ") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected non-empty Authorization: Bearer ... in upstream requests, got: %v", headers)
	}
}

// TestInboundTokenNotForwardedToUpstream verifies that the inbound MCP client Bearer
// token is never forwarded to the upstream API (SPEC.md AC-18.2).
func TestInboundTokenNotForwardedToUpstream(t *testing.T) {
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

	// Start Keycloak for inbound JWT validation.
	kc := useSharedKeycloak(ctx, t, net.ID, net.Name)

	// Create a client for generating inbound MCP client tokens.
	adminToken := kcAdminToken(t, kc.ExternalURL)
	inboundClientUUID := kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-client")
	kcAddAudienceMapper(t, kc.ExternalURL, adminToken, kc.Realm, inboundClientUUID, "mcp-client")
	inboundClientSecret := kcClientSecret(t, kc.ExternalURL, adminToken, kc.Realm, inboundClientUUID)

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

	// Stub: always respond 200.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(outboundOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	issuerInternal := fmt.Sprintf("%s/realms/%s", kc.InternalURL, kc.Realm)
	cfgContent := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: jwt
  jwt:
    issuer: %s
    audience: mcp-client
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
      strategy: bearer
      bearer:
        token_env: UPSTREAM_TOKEN
`, issuerInternal)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH":    "/etc/mcp-anything/config.yaml",
		"UPSTREAM_TOKEN": "outbound-static-token",
	}
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

	// Obtain an inbound JWT from Keycloak using client credentials.
	inboundToken := kcClientCredentialsToken(t, kc.ExternalURL, kc.Realm, "mcp-client", inboundClientSecret)

	// Connect MCP client with the inbound JWT.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, inboundToken)

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__getdata"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", contentText(result.Content))
	}

	// Verify that WireMock received ONLY the outbound static token, not the inbound JWT.
	headers := wiremockRequestHeaders(t, wiremockURL)
	for _, h := range headers {
		if h == "Bearer "+inboundToken {
			t.Error("inbound JWT was forwarded to upstream — AC-18.2 violation")
		}
	}
	found := false
	for _, h := range headers {
		if h == "Bearer outbound-static-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected outbound static token in upstream requests, got: %v", headers)
	}
}

// wiremockRequestAPIKeyHeaders fetches the WireMock request journal and returns all values
// of the specified header (case-insensitive key lookup).
func wiremockRequestAPIKeyHeaders(t *testing.T, base, header string) []string {
	t.Helper()
	resp, err := http.Get(base + "/__admin/requests") //nolint:noctx // test helper
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
