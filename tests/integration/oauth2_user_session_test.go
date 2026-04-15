//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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

// oauth2UserSessionOpenAPISpec is a minimal OpenAPI spec for oauth2_user_session tests.
const oauth2UserSessionOpenAPISpec = `openapi: "3.0.0"
info:
  title: OAuth2 User Session Test API
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

// TestOAuth2UserSession_NoToken verifies that calling a tool with no stored token
// returns IsError:true with an authorization URL.
func TestOAuth2UserSession_NoToken(t *testing.T) {
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

	// Stub: always 200, so the upstream works once the token is injected.
	registerStub(t, wiremockURL, `{
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(oauth2UserSessionOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
session_store:
  provider: memory
  hmac_key: test-hmac-key-for-integration-tests
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
      strategy: oauth2_user_session
      oauth2_user_session:
        provider: oauth2
        auth_url: http://wiremock:8080/oauth/authorize
        token_url: http://wiremock:8080/oauth/token
        client_id: test-client
        client_secret: test-secret
        callback_url: http://proxy:8080/oauth/callback/test
        scopes: [read]
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.NetworkAliases = map[string][]string{
		net.Name: {"proxy"},
	}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
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

	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool to return IsError:true (no token stored)")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Authorization required") {
		t.Errorf("expected 'Authorization required' in error text, got: %s", text)
	}
	if !strings.Contains(text, "http://wiremock:8080/oauth/authorize") {
		t.Errorf("expected auth URL in error text, got: %s", text)
	}
}

// TestOAuth2UserSession_FullFlow verifies the complete OAuth2 user session flow:
// 1. First call returns IsError:true with auth URL
// 2. Callback with code stores the token
// 3. Second call succeeds with Bearer token injected
func TestOAuth2UserSession_FullFlow(t *testing.T) {
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

	// Stub: token endpoint returns a valid access token.
	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/oauth/token"},
		"response": {
			"status": 200,
			"headers": {"Content-Type": "application/json"},
			"body": "{\"access_token\":\"user-access-token\",\"token_type\":\"bearer\",\"expires_in\":3600}"
		}
	}`)

	// Stub: data endpoint only succeeds with the correct Bearer token (priority 1).
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer user-access-token"}}
		},
		"response": {"status": 200, "body": "{\"ok\":true}", "headers": {"Content-Type": "application/json"}}
	}`)
	// Without correct auth → 401 (priority 5).
	registerStub(t, wiremockURL, `{
		"priority": 5,
		"request": {"method": "GET", "url": "/data"},
		"response": {"status": 401, "body": "unauthorized"}
	}`)

	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(oauth2UserSessionOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	cfgContent := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
session_store:
  provider: memory
  hmac_key: test-hmac-key-for-integration-tests
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
      strategy: oauth2_user_session
      oauth2_user_session:
        provider: oauth2
        auth_url: http://wiremock:8080/oauth/authorize
        token_url: http://wiremock:8080/oauth/token
        client_id: test-client
        client_secret: test-secret
        callback_url: http://proxy:8080/oauth/callback/test
        scopes: [read]
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{net.Name}
	proxyReq.NetworkAliases = map[string][]string{
		net.Name: {"proxy"},
	}
	proxyReq.Env = map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
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

	// Step 1: First call — should return IsError:true with auth URL.
	result, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("first call tool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected first call to return IsError:true (no token stored)")
	}
	text := contentText(result.Content)
	if !strings.Contains(text, "Authorization required") {
		t.Fatalf("expected 'Authorization required' in first call error, got: %s", text)
	}

	// Extract the authorization URL and its state parameter.
	authURL := extractAuthURL(t, text)
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL %q: %v", authURL, err)
	}
	state := parsedAuthURL.Query().Get("state")
	if state == "" {
		t.Fatalf("auth URL missing state parameter: %s", authURL)
	}

	// Step 2: Simulate the OAuth callback — send code + state to the proxy callback endpoint.
	// The proxy will exchange the code with WireMock's token endpoint and store the token.
	callbackURL := fmt.Sprintf("%s/oauth/callback/test?code=test-auth-code&state=%s",
		proxyURL, url.QueryEscape(state))
	callbackResp, err := http.Get(callbackURL) //nolint:noctx // integration test
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	defer func() { _ = callbackResp.Body.Close() }()
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("callback returned HTTP %d (expected 200)", callbackResp.StatusCode)
	}

	// Step 3: Second call — token is now stored; should succeed.
	result2, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("second call tool: %v", err)
	}
	if result2.IsError {
		t.Fatalf("second call returned error: %s", contentText(result2.Content))
	}

	// Verify WireMock received the Bearer token on the upstream request.
	headers := wiremockRequestHeaders(t, wiremockURL)
	found := false
	for _, h := range headers {
		if h == "Bearer user-access-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Bearer user-access-token' in upstream Authorization headers, got: %v", headers)
	}
}

// TestOAuth2UserSessionPostgres verifies the PostgreSQL session store:
// token survives a proxy restart.
func TestOAuth2UserSessionPostgres(t *testing.T) {
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

	// Start PostgreSQL container.
	postgres := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"postgres"},
		},
		Env: map[string]string{
			"POSTGRES_USER":     "mcp",
			"POSTGRES_PASSWORD": "mcp",
			"POSTGRES_DB":       "sessions",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithStartupTimeout(60 * time.Second),
	})

	postgresHost, err := postgres.Host(ctx)
	if err != nil {
		t.Fatalf("get postgres host: %v", err)
	}
	postgresPort, err := postgres.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("get postgres port: %v", err)
	}
	// DSN used by the proxy container (internal network).
	_ = postgresHost
	_ = postgresPort

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

	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/oauth/token"},
		"response": {
			"status": 200,
			"headers": {"Content-Type": "application/json"},
			"body": "{\"access_token\":\"pg-access-token\",\"token_type\":\"bearer\",\"expires_in\":3600}"
		}
	}`)
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer pg-access-token"}}
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
	if err := os.WriteFile(specPath, []byte(oauth2UserSessionOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// 32-byte hex key for AES-256-GCM.
	encKey := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	cfgContent := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
session_store:
  provider: postgres
  hmac_key: test-hmac-key-postgres
  postgres:
    dsn: ${POSTGRES_DSN}
    encryption_key: %s
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
      strategy: oauth2_user_session
      oauth2_user_session:
        provider: oauth2
        auth_url: http://wiremock:8080/oauth/authorize
        token_url: http://wiremock:8080/oauth/token
        client_id: test-client
        client_secret: test-secret
        callback_url: http://proxy:8080/oauth/callback/test
        scopes: [read]
`, encKey)

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	postgresDSN := fmt.Sprintf("postgres://mcp:mcp@postgres:5432/sessions?sslmode=disable")

	startProxy := func(ctx context.Context) (testcontainers.Container, string) {
		t.Helper()
		proxyReq := proxyContainerRequest()
		proxyReq.ExposedPorts = []string{"8080/tcp"}
		proxyReq.Networks = []string{net.Name}
		proxyReq.NetworkAliases = map[string][]string{
			net.Name: {"proxy"},
		}
		proxyReq.Env = map[string]string{
			"CONFIG_PATH":  "/etc/mcp-anything/config.yaml",
			"POSTGRES_DSN": postgresDSN,
		}
		proxyReq.Files = []testcontainers.ContainerFile{
			{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
			{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		}
		proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
		c := startContainer(ctx, t, proxyReq)
		host, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("get proxy host: %v", err)
		}
		port, err := c.MappedPort(ctx, "8080")
		if err != nil {
			t.Fatalf("get proxy port: %v", err)
		}
		return c, fmt.Sprintf("http://%s:%s", host, port.Port())
	}

	// First proxy instance — authorize and get token.
	proxy1, proxyURL1 := startProxy(ctx)

	callCtx1, cancel1 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel1()

	transport1 := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL1 + "/mcp"}
	mcpClient1 := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	sess1, err := mcpClient1.Connect(callCtx1, transport1, nil)
	if err != nil {
		t.Fatalf("connect to first proxy: %v", err)
	}

	result1, err := sess1.CallTool(callCtx1, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !result1.IsError {
		t.Fatal("expected first call to require auth")
	}

	authURL := extractAuthURL(t, contentText(result1.Content))
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := parsedAuthURL.Query().Get("state")

	callbackURL := fmt.Sprintf("%s/oauth/callback/test?code=pg-code&state=%s",
		proxyURL1, url.QueryEscape(state))
	callbackResp, err := http.Get(callbackURL) //nolint:noctx
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("callback returned %d", callbackResp.StatusCode)
	}

	// Verify first proxy works.
	result1b, err := sess1.CallTool(callCtx1, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("second call on first proxy: %v", err)
	}
	if result1b.IsError {
		t.Fatalf("second call on first proxy returned error: %s", contentText(result1b.Content))
	}
	sess1.Close()

	// Terminate the first proxy.
	termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer termCancel()
	if err := proxy1.Terminate(termCtx); err != nil {
		t.Logf("terminate proxy1: %v", err)
	}

	// Start a second proxy instance pointing to the same PostgreSQL.
	_, proxyURL2 := startProxy(ctx)

	callCtx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()

	transport2 := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL2 + "/mcp"}
	mcpClient2 := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	sess2, err := mcpClient2.Connect(callCtx2, transport2, nil)
	if err != nil {
		t.Fatalf("connect to second proxy: %v", err)
	}
	defer sess2.Close()

	// Token should be loaded from PostgreSQL — no re-auth needed.
	result2, err := sess2.CallTool(callCtx2, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("call on second proxy: %v", err)
	}
	if result2.IsError {
		t.Fatalf("call on second proxy returned error (token should have survived restart): %s", contentText(result2.Content))
	}
}

// TestOAuth2UserSessionRedis verifies the Redis session store:
// token survives a proxy restart and TTL is set.
func TestOAuth2UserSessionRedis(t *testing.T) {
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

	// Start Redis container.
	redisContainer := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{
			net.Name: {"redis"},
		},
		WaitingFor: wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	})
	_ = redisContainer

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

	registerStub(t, wiremockURL, `{
		"request": {"method": "POST", "url": "/oauth/token"},
		"response": {
			"status": 200,
			"headers": {"Content-Type": "application/json"},
			"body": "{\"access_token\":\"redis-access-token\",\"token_type\":\"bearer\",\"expires_in\":3600}"
		}
	}`)
	registerStub(t, wiremockURL, `{
		"priority": 1,
		"request": {
			"method": "GET",
			"url": "/data",
			"headers": {"Authorization": {"equalTo": "Bearer redis-access-token"}}
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
	if err := os.WriteFile(specPath, []byte(oauth2UserSessionOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	encKey := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"
	cfgContent := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
session_store:
  provider: redis
  hmac_key: test-hmac-key-redis
  redis:
    addr: redis:6379
    encryption_key: %s
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
      strategy: oauth2_user_session
      oauth2_user_session:
        provider: oauth2
        auth_url: http://wiremock:8080/oauth/authorize
        token_url: http://wiremock:8080/oauth/token
        client_id: test-client
        client_secret: test-secret
        callback_url: http://proxy:8080/oauth/callback/test
        scopes: [read]
`, encKey)

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	startProxy := func(ctx context.Context) (testcontainers.Container, string) {
		t.Helper()
		proxyReq := proxyContainerRequest()
		proxyReq.ExposedPorts = []string{"8080/tcp"}
		proxyReq.Networks = []string{net.Name}
		proxyReq.NetworkAliases = map[string][]string{
			net.Name: {"proxy"},
		}
		proxyReq.Env = map[string]string{
			"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
		}
		proxyReq.Files = []testcontainers.ContainerFile{
			{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
			{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
		}
		proxyReq.WaitingFor = wait.ForHTTP("/healthz").WithPort("8080").WithStartupTimeout(120 * time.Second)
		c := startContainer(ctx, t, proxyReq)
		host, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("get proxy host: %v", err)
		}
		port, err := c.MappedPort(ctx, "8080")
		if err != nil {
			t.Fatalf("get proxy port: %v", err)
		}
		return c, fmt.Sprintf("http://%s:%s", host, port.Port())
	}

	// First proxy instance — authorize and store token.
	proxy1, proxyURL1 := startProxy(ctx)

	callCtx1, cancel1 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel1()

	transport1 := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL1 + "/mcp"}
	mcpClient1 := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	sess1, err := mcpClient1.Connect(callCtx1, transport1, nil)
	if err != nil {
		t.Fatalf("connect to first proxy: %v", err)
	}

	result1, err := sess1.CallTool(callCtx1, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !result1.IsError {
		t.Fatal("expected first call to require auth")
	}

	authURL := extractAuthURL(t, contentText(result1.Content))
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := parsedAuthURL.Query().Get("state")

	callbackURL := fmt.Sprintf("%s/oauth/callback/test?code=redis-code&state=%s",
		proxyURL1, url.QueryEscape(state))
	callbackResp, err := http.Get(callbackURL) //nolint:noctx
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("callback returned %d", callbackResp.StatusCode)
	}

	// Verify first proxy works.
	result1b, err := sess1.CallTool(callCtx1, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("second call on first proxy: %v", err)
	}
	if result1b.IsError {
		t.Fatalf("call on first proxy returned error: %s", contentText(result1b.Content))
	}
	sess1.Close()

	// Terminate the first proxy.
	termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer termCancel()
	if err := proxy1.Terminate(termCtx); err != nil {
		t.Logf("terminate proxy1: %v", err)
	}

	// Start a second proxy instance pointing to the same Redis.
	_, proxyURL2 := startProxy(ctx)

	callCtx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()

	transport2 := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL2 + "/mcp"}
	mcpClient2 := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	sess2, err := mcpClient2.Connect(callCtx2, transport2, nil)
	if err != nil {
		t.Fatalf("connect to second proxy: %v", err)
	}
	defer sess2.Close()

	// Token should be loaded from Redis — no re-auth needed.
	result2, err := sess2.CallTool(callCtx2, &sdkmcp.CallToolParams{Name: "test__get_data"})
	if err != nil {
		t.Fatalf("call on second proxy: %v", err)
	}
	if result2.IsError {
		t.Fatalf("call on second proxy returned error (token should have survived restart): %s", contentText(result2.Content))
	}
}

// extractAuthURL parses the authorization URL from an error message produced by
// the oauth2_user_session strategy.
func extractAuthURL(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line
		}
	}
	t.Fatalf("could not find authorization URL in error text: %q", text)
	return ""
}
