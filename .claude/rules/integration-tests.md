---
paths:
  - "tests/integration/**/*.go"
---

# Integration Tests

Integration tests run the proxy as a Docker container and test it end-to-end via HTTP and MCP protocol. They do NOT import any `internal/` packages.

## Build tag

Every file must start with `//go:build integration` and use package `integration_test`.

## Test infrastructure

- **WireMock** (`wiremock/wiremock:3.9.1`) serves as the mock upstream API
- **Testcontainers** orchestrates Docker containers
- **Bridge network** connects proxy and WireMock containers so proxy reaches WireMock by alias `wiremock`
- **Proxy container** is built from `Dockerfile` (local dev) or pulled from `PROXY_IMAGE` env var (CI)

## Test structure (follow this pattern for every new test)

1. Create a bridge network via `network.New(ctx, network.WithDriver("bridge"))` with `t.Cleanup` for removal
2. Start a **fresh WireMock container** per test with `startContainer()` — each test gets its own clean instance to avoid stub conflicts between tests
3. Register WireMock stubs via `registerStub()` helper
4. Write OpenAPI spec, overlay, and config YAML to `t.TempDir()`, mount into proxy container via `proxyReq.Files`
5. Start proxy via `proxyContainerRequest()` + `startContainer()` — uses `/healthz` wait strategy with 120s timeout
6. Connect MCP client via `sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}`
7. Assert: tool listing (`session.ListTools`), tool calling (`session.CallTool`), HTTP verification (WireMock journal)

## WireMock isolation

Every test must start its own WireMock container. Never share a WireMock instance between tests — this avoids stub conflicts and ordering issues.

## Existing test helpers (in `mvp_test.go`)

- `proxyContainerRequest()` — returns ContainerRequest (from Dockerfile or PROXY_IMAGE)
- `startContainer(ctx, t, req)` — starts container, logs output on failure, registers cleanup
- `logContainerOutput(ctx, t, c)` — dumps container stdout/stderr for debugging
- `registerStub(t, baseURL, jsonBody)` — registers a WireMock stub mapping
- `assertHTTPStatus(t, url, wantStatus)` — simple GET + status assertion
- `verifyWireMockRequests(t, baseURL)` — checks WireMock journal for received requests
- `wiremockRequestHeaders(t, baseURL)` — returns all Authorization headers from WireMock journal
- `toolNames(tools)` — extracts tool names for error messages
- `contentText(content)` — extracts text from MCP content blocks
- `jsonEscape(s)` — JSON string literal for embedding in stub bodies

## Config template

All integration tests use this minimal config structure:
```yaml
server:
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
      source: /etc/mcp-anything/spec.yaml   # or http://wiremock:8080/openapi.yaml for URL tests
      version: "3.0"
```

## Proxy container file mount path

Always mount config and spec files under `/etc/mcp-anything/` with `CONFIG_PATH` env var set to `/etc/mcp-anything/config.yaml`.

## Testing startup failures

For tests that expect the proxy to fail to start (invalid spec, invalid overlay):
- Use `testcontainers.GenericContainer` directly instead of `startContainer()` helper
- Set a short `WaitingFor` timeout (30s)
- Assert `err != nil` — container exits before health check passes

## Timeouts

- WireMock startup: 60s
- Proxy startup (health check): 120s
- MCP client operations: 60s (`context.WithTimeout`)
- Container cleanup: 30s

## Tool naming convention

Tools are named as `{tool_prefix}__{slugified_operation}`, e.g. `test__list_pets`, `test__get_pets_petid`.
