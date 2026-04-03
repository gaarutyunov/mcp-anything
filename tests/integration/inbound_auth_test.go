//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// authOpenAPISpec is a minimal OpenAPI spec for auth tests.
// It has two operations: listPets (auth required by default) and healthCheck (public, x-mcp-auth-required: false via overlay).
const authOpenAPISpec = `openapi: "3.0.0"
info:
  title: Auth Test API
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
  /health:
    get:
      operationId: healthCheck
      summary: Public health check
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
`

// authPublicOverlay sets x-mcp-auth-required: false on the /health GET operation.
const authPublicOverlay = `overlay: 1.0.0
info:
  title: Auth overlay
  version: "1.0"
actions:
  - target: $.paths['/health'].get
    update:
      x-mcp-auth-required: false
`

// ---- Keycloak setup helpers ----

// keycloakSetup holds Keycloak configuration needed by tests.
type keycloakSetup struct {
	ExternalURL string // http://host:port (test machine access)
	InternalURL string // http://keycloak:8080 (Docker network access)
	Realm       string
}

// startKeycloak starts a Keycloak 25 container and returns the setup struct.
// In Keycloak 25, the management/health endpoint moved to port 9000; we wait
// on the realm discovery endpoint at port 8080 instead (no auth required).
func startKeycloak(ctx context.Context, t *testing.T, netName string) *keycloakSetup {
	t.Helper()
	kc := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "quay.io/keycloak/keycloak:25.0",
		Cmd:          []string{"start-dev"},
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{netName},
		NetworkAliases: map[string][]string{
			netName: {"keycloak"},
		},
		Env: map[string]string{
			"KEYCLOAK_ADMIN":          "admin",
			"KEYCLOAK_ADMIN_PASSWORD": "admin",
			// Set the frontend URL so that tokens' iss claim uses the Docker-internal hostname,
			// matching the proxy's configured issuer (http://keycloak:8080/realms/...).
			// KC_HOSTNAME_STRICT=false (default in start-dev) allows requests from any host,
			// so the test machine can still reach Keycloak via the mapped external port.
			"KC_HOSTNAME":        "http://keycloak:8080",
			"KC_HOSTNAME_STRICT": "false",
		},
		// Wait for the master realm discovery endpoint (available on port 8080, no auth needed).
		WaitingFor: wait.ForHTTP("/realms/master").WithPort("8080").WithStatusCodeMatcher(func(status int) bool {
			return status == http.StatusOK
		}).WithStartupTimeout(180 * time.Second),
	})
	host, err := kc.Host(ctx)
	if err != nil {
		t.Fatalf("get keycloak host: %v", err)
	}
	port, err := kc.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("get keycloak port: %v", err)
	}
	external := fmt.Sprintf("http://%s:%s", host, port.Port())

	setup := &keycloakSetup{
		ExternalURL: external,
		InternalURL: "http://keycloak:8080",
		Realm:       "test-realm",
	}

	// Configure Keycloak via REST API.
	adminToken := kcAdminToken(t, external)
	kcCreateRealm(t, external, adminToken, "test-realm")

	return setup
}

// kcAdminToken obtains a Keycloak admin access token.
func kcAdminToken(t *testing.T, baseURL string) string {
	t.Helper()
	data := url.Values{
		"username":   {"admin"},
		"password":   {"admin"},
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
	}
	resp, err := http.PostForm(baseURL+"/realms/master/protocol/openid-connect/token", data) //nolint:noctx
	if err != nil {
		t.Fatalf("get keycloak admin token: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get keycloak admin token: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.AccessToken == "" {
		t.Fatalf("parse keycloak admin token: %v, body: %s", err, body)
	}
	return result.AccessToken
}

// kcCreateRealm creates a Keycloak realm.
func kcCreateRealm(t *testing.T, baseURL, adminToken, realm string) {
	t.Helper()
	payload := map[string]any{"realm": realm, "enabled": true}
	kcAdminRequest(t, http.MethodPost, baseURL+"/admin/realms", adminToken, payload, http.StatusCreated)
}

// kcCreateClient creates a Keycloak client with service accounts enabled and returns its internal UUID.
func kcCreateClient(t *testing.T, baseURL, adminToken, realm, clientID string) string {
	t.Helper()
	payload := map[string]any{
		"clientId":               clientID,
		"enabled":                true,
		"publicClient":           false,
		"serviceAccountsEnabled": true,
		"standardFlowEnabled":    false,
		"directAccessGrantsEnabled": false,
	}
	kcAdminRequest(t, http.MethodPost, fmt.Sprintf("%s/admin/realms/%s/clients", baseURL, realm), adminToken, payload, http.StatusCreated)
	return kcClientUUID(t, baseURL, adminToken, realm, clientID)
}

// kcClientUUID looks up a client's internal UUID by clientId.
func kcClientUUID(t *testing.T, baseURL, adminToken, realm, clientID string) string {
	t.Helper()
	respBody := kcAdminGet(t, fmt.Sprintf("%s/admin/realms/%s/clients?clientId=%s", baseURL, realm, url.QueryEscape(clientID)), adminToken)
	var clients []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &clients); err != nil || len(clients) == 0 {
		t.Fatalf("find keycloak client %q: %v, body: %s", clientID, err, respBody)
	}
	return clients[0].ID
}

// kcClientSecret retrieves the client secret for a given client UUID.
func kcClientSecret(t *testing.T, baseURL, adminToken, realm, clientUUID string) string {
	t.Helper()
	respBody := kcAdminGet(t, fmt.Sprintf("%s/admin/realms/%s/clients/%s/client-secret", baseURL, realm, clientUUID), adminToken)
	var result struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || result.Value == "" {
		t.Fatalf("get keycloak client secret: %v, body: %s", err, respBody)
	}
	return result.Value
}

// kcAddAudienceMapper adds a hardcoded audience mapper to a client so the token's aud includes the client ID.
func kcAddAudienceMapper(t *testing.T, baseURL, adminToken, realm, clientUUID, audience string) {
	t.Helper()
	payload := map[string]any{
		"name":            "audience-mapper",
		"protocol":        "openid-connect",
		"protocolMapper":  "oidc-audience-mapper",
		"consentRequired": false,
		"config": map[string]any{
			"included.client.audience": audience,
			"id.token.claim":           "false",
			"access.token.claim":       "true",
		},
	}
	kcAdminRequest(t, http.MethodPost,
		fmt.Sprintf("%s/admin/realms/%s/clients/%s/protocol-mappers/models", baseURL, realm, clientUUID),
		adminToken, payload, http.StatusCreated)
}

// kcClientCredentialsToken obtains an access token via the client_credentials grant.
func kcClientCredentialsToken(t *testing.T, baseURL, realm, clientID, clientSecret string) string {
	t.Helper()
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	resp, err := http.PostForm( //nolint:noctx
		fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", baseURL, realm), data)
	if err != nil {
		t.Fatalf("get client credentials token: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get client credentials token: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.AccessToken == "" {
		t.Fatalf("parse client credentials token: %v, body: %s", err, body)
	}
	return result.AccessToken
}

// kcAdminRequest sends an authenticated Admin REST API request.
func kcAdminRequest(t *testing.T, method, url, token string, payload map[string]any, wantStatus int) {
	t.Helper()
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(method, url, bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		t.Fatalf("build keycloak admin request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req) //nolint:noctx
	if err != nil {
		t.Fatalf("keycloak admin request %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("keycloak admin request %s %s: got %d, want %d: %s", method, url, resp.StatusCode, wantStatus, body)
	}
}

// kcAdminGet sends an authenticated GET and returns the response body.
func kcAdminGet(t *testing.T, apiURL, token string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil) //nolint:noctx
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req) //nolint:noctx
	if err != nil {
		t.Fatalf("keycloak admin GET %s: %v", apiURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keycloak admin GET %s: got %d: %s", apiURL, resp.StatusCode, body)
	}
	return body
}

// ---- MCP raw HTTP helpers ----

// mcpPost sends a raw JSON-RPC request to the MCP endpoint and returns the HTTP response.
// This is used for tests that expect HTTP-level errors (e.g. 401) before the MCP protocol runs.
func mcpPost(t *testing.T, proxyURL, method string, params map[string]any, authHeader string) *http.Response {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, proxyURL+"/mcp", bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		t.Fatalf("build MCP request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("MCP request failed: %v", err)
	}
	return resp
}

// mcpPostWithHeader sends a raw JSON-RPC request with a custom header value.
// This is used to test custom header authentication (e.g. API keys).
func mcpPostWithHeader(t *testing.T, proxyURL, method string, params map[string]any, headerName, headerValue string) *http.Response {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  method,
		"params":  params,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, proxyURL+"/mcp", bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		t.Fatalf("build MCP request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if headerName != "" && headerValue != "" {
		req.Header.Set(headerName, headerValue)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("MCP request failed: %v", err)
	}
	return resp
}

// authRoundTripper injects a fixed Authorization header into every request.
type authRoundTripper struct {
	base  http.RoundTripper
	value string // e.g. "Bearer <token>"
}

func (t *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", t.value)
	return t.base.RoundTrip(req)
}

// headerRoundTripper injects a custom header into every request.
type headerRoundTripper struct {
	base   http.RoundTripper
	header string
	value  string
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(t.header, t.value)
	return t.base.RoundTrip(req)
}

// connectMCPClientWithBearer connects the MCP SDK client using an HTTP transport that injects
// an Authorization: Bearer header on every request. This is needed because tools/list requires
// the MCP initialize handshake to complete first, and we want the auth header on all requests.
func connectMCPClientWithBearer(ctx context.Context, t *testing.T, proxyURL, token string) *sdkmcp.ClientSession {
	t.Helper()
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &authRoundTripper{
				base:  http.DefaultTransport,
				value: "Bearer " + token,
			},
		},
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client with bearer token: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// connectMCPClientWithHeader connects the MCP SDK client using a custom header transport.
func connectMCPClientWithHeader(ctx context.Context, t *testing.T, proxyURL, headerName, headerValue string) *sdkmcp.ClientSession {
	t.Helper()
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &headerRoundTripper{
				base:   http.DefaultTransport,
				header: headerName,
				value:  headerValue,
			},
		},
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client with header: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// ---- Proxy setup helpers ----

// authProxyFiles writes the spec and config to tmpDir and returns a slice of ContainerFiles.
func authProxyFiles(t *testing.T, tmpDir, cfgContent string, withOverlay bool) []testcontainers.ContainerFile {
	t.Helper()
	specPath := filepath.Join(tmpDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(authOpenAPISpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	files := []testcontainers.ContainerFile{
		{HostFilePath: cfgPath, ContainerFilePath: "/etc/mcp-anything/config.yaml", FileMode: 0o644},
		{HostFilePath: specPath, ContainerFilePath: "/etc/mcp-anything/spec.yaml", FileMode: 0o644},
	}
	if withOverlay {
		overlayPath := filepath.Join(tmpDir, "overlay.yaml")
		if err := os.WriteFile(overlayPath, []byte(authPublicOverlay), 0o644); err != nil {
			t.Fatalf("write overlay: %v", err)
		}
		files = append(files, testcontainers.ContainerFile{
			HostFilePath: overlayPath, ContainerFilePath: "/etc/mcp-anything/overlay.yaml", FileMode: 0o644,
		})
	}
	return files
}

// startAuthProxy starts the proxy with the given config content and environment.
func startAuthProxy(ctx context.Context, t *testing.T, netName string, files []testcontainers.ContainerFile, env map[string]string) string {
	t.Helper()
	proxyReq := proxyContainerRequest()
	proxyReq.ExposedPorts = []string{"8080/tcp"}
	proxyReq.Networks = []string{netName}
	proxyReq.Env = env
	proxyReq.Files = files
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

// ---- Tests ----

func TestJWTAuthAllowsValidToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	// Start WireMock for the upstream API.
	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)

	// Start Keycloak and configure a client.
	kc := startKeycloak(ctx, t, net.Name)
	adminToken := kcAdminToken(t, kc.ExternalURL)
	clientUUID := kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-anything")
	kcAddAudienceMapper(t, kc.ExternalURL, adminToken, kc.Realm, clientUUID, "mcp-anything")
	clientSecret := kcClientSecret(t, kc.ExternalURL, adminToken, kc.Realm, clientUUID)

	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: jwt
  jwt:
    issuer: %s/realms/%s
    audience: mcp-anything
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, kc.InternalURL, kc.Realm)

	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	})

	// Obtain a valid JWT from Keycloak.
	jwt := kcClientCredentialsToken(t, kc.ExternalURL, kc.Realm, "mcp-anything", clientSecret)

	// Connect MCP client with Bearer token injected on every request (including initialize).
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, jwt)

	// Call tools/list — should succeed with valid JWT.
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("tools/list with valid JWT: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Errorf("expected tools in list, got none")
	}
}

func TestJWTAuthRejects401OnInvalidToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	kc := startKeycloak(ctx, t, net.Name)
	adminToken := kcAdminToken(t, kc.ExternalURL)
	kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-anything")

	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: jwt
  jwt:
    issuer: %s/realms/%s
    audience: mcp-anything
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, kc.InternalURL, kc.Realm)

	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	})

	// Use an invalid token.
	resp := mcpPost(t, proxyURL, "tools/list", map[string]any{}, "Bearer invalid.token.here")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, "resource_metadata") {
		t.Errorf("expected WWW-Authenticate to contain resource_metadata, got: %s", wwwAuth)
	}
}

func TestJWTAuthRejectsMissingToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	kc := startKeycloak(ctx, t, net.Name)
	adminToken := kcAdminToken(t, kc.ExternalURL)
	kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-anything")

	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: jwt
  jwt:
    issuer: %s/realms/%s
    audience: mcp-anything
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, kc.InternalURL, kc.Realm)

	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	})

	// No Authorization header.
	resp := mcpPost(t, proxyURL, "tools/list", map[string]any{}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestIntrospectionAuthAllowsActiveToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)

	kc := startKeycloak(ctx, t, net.Name)
	adminToken := kcAdminToken(t, kc.ExternalURL)

	// Create the introspection resource server client.
	intrClientUUID := kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "introspection-client")
	intrSecret := kcClientSecret(t, kc.ExternalURL, adminToken, kc.Realm, intrClientUUID)

	// Create a separate client whose tokens will be introspected.
	tokenClientUUID := kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "token-client")
	tokenSecret := kcClientSecret(t, kc.ExternalURL, adminToken, kc.Realm, tokenClientUUID)

	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: introspection
  introspection:
    issuer: %s/realms/%s
    client_id: introspection-client
    client_secret: %s
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
`, kc.InternalURL, kc.Realm, intrSecret)

	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	})

	// Get a token from the token-client.
	token := kcClientCredentialsToken(t, kc.ExternalURL, kc.Realm, "token-client", tokenSecret)

	// Connect MCP client with Bearer token — introspection should validate it.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithBearer(callCtx, t, proxyURL, token)

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("tools/list with introspection: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Errorf("expected tools in list, got none")
	}
}

func TestAPIKeyAuthWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	cfg := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: apikey
  apikey:
    header: X-API-Key
    keys_env: TEST_MCP_KEYS
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
	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH":   "/etc/mcp-anything/config.yaml",
		"TEST_MCP_KEYS": "secret1,secret2",
	})

	// Valid API key — connect via MCP SDK with the header injected on all requests.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session := connectMCPClientWithHeader(callCtx, t, proxyURL, "X-API-Key", "secret1")
	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("expected success with valid API key: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Errorf("expected tools in list, got none")
	}
	session.Close()

	// Invalid API key — use raw HTTP to verify 401 (before MCP session is established).
	resp := mcpPostWithHeader(t, proxyURL, "tools/list", map[string]any{}, "X-API-Key", "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong API key, got %d", resp.StatusCode)
	}
}

func TestPerOperationAuthBypass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/health"},"response":{"status":200,"body":"{\"ok\":true}","headers":{"Content-Type":"application/json"}}}`)
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{\"pets\":[]}","headers":{"Content-Type":"application/json"}}}`)

	kc := startKeycloak(ctx, t, net.Name)
	adminToken := kcAdminToken(t, kc.ExternalURL)
	kcCreateClient(t, kc.ExternalURL, adminToken, kc.Realm, "mcp-anything")

	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: jwt
  jwt:
    issuer: %s/realms/%s
    audience: mcp-anything
upstreams:
  - name: test
    enabled: true
    tool_prefix: test
    base_url: http://wiremock:8080
    timeout: 10s
    openapi:
      source: /etc/mcp-anything/spec.yaml
      version: "3.0"
    overlay:
      source: /etc/mcp-anything/overlay.yaml
`, kc.InternalURL, kc.Realm)

	files := authProxyFiles(t, tmpDir, cfg, true)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH": "/etc/mcp-anything/config.yaml",
	})

	// Call the protected tool (listPets) with NO Authorization header — should get 401.
	// Use raw HTTP because we expect the middleware to return 401 before MCP initialization.
	respProtected := mcpPost(t, proxyURL, "tools/call", map[string]any{"name": "test__listpets", "arguments": map[string]any{}}, "")
	defer respProtected.Body.Close()
	if respProtected.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(respProtected.Body)
		t.Fatalf("protected tool call without auth: expected 401, got %d: %s", respProtected.StatusCode, body)
	}

	// Call the bypassed tool (healthcheck) with NO Authorization header.
	// The middleware should detect x-mcp-auth-required=false and pass through.
	// The MCP handler will process the call, but since there's no session, we
	// verify at HTTP level that the middleware does NOT return 401 (i.e., HTTP != 401).
	respPublic := mcpPost(t, proxyURL, "tools/call", map[string]any{"name": "test__healthcheck", "arguments": map[string]any{}}, "")
	defer respPublic.Body.Close()
	if respPublic.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(respPublic.Body)
		t.Fatalf("public tool call should not be blocked by auth middleware, got 401: %s", body)
	}
	// HTTP 200 means the request passed the auth middleware (MCP may return a protocol-level error).
	t.Logf("public tool call HTTP status: %d (expected non-401)", respPublic.StatusCode)
}

func TestWellKnownEndpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	net, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(ctx) })

	wm := startContainer(ctx, t, testcontainers.ContainerRequest{
		Image:        "wiremock/wiremock:3.9.1",
		ExposedPorts: []string{"8080/tcp"},
		Networks:     []string{net.Name},
		NetworkAliases: map[string][]string{net.Name: {"wiremock"}},
		WaitingFor: wait.ForHTTP("/__admin/mappings").WithPort("8080").WithStartupTimeout(60 * time.Second),
	})
	wmHost, _ := wm.Host(ctx)
	wmPort, _ := wm.MappedPort(ctx, "8080")
	wmURL := fmt.Sprintf("http://%s:%s", wmHost, wmPort.Port())
	registerStub(t, wmURL, `{"request":{"method":"GET","url":"/pets"},"response":{"status":200,"body":"{}","headers":{"Content-Type":"application/json"}}}`)

	tmpDir := t.TempDir()
	cfg := `server:
  port: 8080
naming:
  separator: "__"
telemetry:
  service_name: mcp-anything
  service_version: v0.0.0-test
inbound_auth:
  strategy: apikey
  apikey:
    header: X-API-Key
    keys_env: WELL_KNOWN_KEYS
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
	files := authProxyFiles(t, tmpDir, cfg, false)
	proxyURL := startAuthProxy(ctx, t, net.Name, files, map[string]string{
		"CONFIG_PATH":    "/etc/mcp-anything/config.yaml",
		"WELL_KNOWN_KEYS": "test-key",
	})

	// GET /.well-known/oauth-protected-resource — no auth header needed.
	resp, err := http.Get(proxyURL + "/.well-known/oauth-protected-resource") //nolint:noctx
	if err != nil {
		t.Fatalf("GET well-known: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse well-known response: %v, body: %s", err, body)
	}
	if result["resource"] == nil {
		t.Errorf("well-known response missing 'resource' field: %s", body)
	}
	if result["authorization_servers"] == nil {
		t.Errorf("well-known response missing 'authorization_servers' field: %s", body)
	}
}
