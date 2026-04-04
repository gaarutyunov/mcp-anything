# mcp-anything

[![Website](https://img.shields.io/badge/website-mcp--anything.ai-blue)](https://mcp-anything.ai)
[![License](https://img.shields.io/github/license/gaarutyunov/mcp-anything)](LICENSE)
[![CI](https://github.com/gaarutyunov/mcp-anything/actions/workflows/ci.yml/badge.svg)](https://github.com/gaarutyunov/mcp-anything/actions/workflows/ci.yml)

A stateless Go proxy that turns **anything** — REST APIs, shell commands, JavaScript scripts — into [MCP](https://modelcontextprotocol.io) tools. No code, no plugins — just config.

## Features

- **OpenAPI → MCP tools** — auto-generates tools from OpenAPI 3.0 specs with input schemas, jq transforms, and dry-run validation
- **Shell commands** — expose `kubectl`, `aws`, or any CLI as MCP tools with template argument interpolation
- **JavaScript scripts** — custom tool logic in a sandboxed [Sobek](https://github.com/nicholasgasior/goja) runtime
- **Multi-upstream routing** — proxy multiple backends from a single instance with prefix-based namespacing
- **Pluggable auth** — JWT, OAuth2 Client Credentials, API key, token introspection, Lua, JavaScript (inbound + outbound)
- **OpenAPI Overlays** — RFC 9535 JSONPath to customize tools without touching the original spec
- **Tool groups** — multiple MCP endpoints with JSONPath filters (read-only, premium, admin, etc.)
- **Config hot-reload** — fsnotify-based, Kubernetes ConfigMap-aware, zero downtime
- **Background spec refresh** — periodic re-fetch with ETag/conditional GET and atomic swaps
- **OpenTelemetry** — OTLP traces, Prometheus metrics, W3C Trace Context propagation
- **Kubernetes operator** — `MCPProxy` and `MCPUpstream` CRDs with annotation-based auto-discovery
- **Library-first SDK** — embed as a Go library and register custom tool providers
- **Gateway transport** — connection pooling, mTLS, HTTP/2, TLS session caching

## Quick start

### Install

```bash
go install github.com/gaarutyunov/mcp-anything/cmd/proxy@latest
```

### Or run with Docker

```bash
docker run -v $(pwd)/config.yaml:/etc/mcp-anything/config.yaml \
  ghcr.io/gaarutyunov/mcp-anything:latest
```

### Minimal config

```yaml
upstreams:
  - name: petstore
    type: http
    tool_prefix: pets
    base_url: https://petstore.swagger.io/v2
    openapi:
      source: https://petstore.swagger.io/v2/swagger.json
```

This exposes every operation in the Petstore API as an MCP tool. Connect any MCP client to `http://localhost:8080/mcp`.

## Configuration

A single YAML file defines everything. Environment variables are expanded with `${VAR}` syntax.

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 60s
  transport:
    - streamable-http
    - sse

upstreams:
  - name: petstore
    type: http
    tool_prefix: pets
    base_url: https://api.petstore.io/v2
    openapi:
      source: /etc/mcp-anything/specs/petstore.yaml
      refresh_interval: 5m
    overlay:
      source: /etc/mcp-anything/overlays/petstore.yaml
    outbound_auth:
      strategy: oauth2_client_credentials
      oauth2_client_credentials:
        token_url: https://idp.example.com/oauth/token
        client_id: ${CLIENT_ID}
        client_secret: ${CLIENT_SECRET}
        scopes: [read:pets, write:pets]

  - name: cluster
    type: command
    tool_prefix: k8s
    tools:
      - name: get_pods
        description: List pods in a namespace
        command: kubectl get pods -n {{namespace}} -o json
        args:
          namespace:
            type: string
            required: true
        timeout: 30s

  - name: custom
    type: script
    tool_prefix: ops
    tools:
      - name: health_check
        description: Check service health
        source: scripts/health_check.js
        args:
          service:
            type: string
        timeout: 10s

groups:
  - name: all
    endpoint: /mcp
    upstreams: [petstore, cluster, custom]

  - name: readonly
    endpoint: /mcp/readonly
    upstreams: [petstore]
    filter: "$.paths.*.get[?(@['x-mcp-safe'] == true)]"
```

See [deploy/example/](deploy/example/) for a complete example with auth, overlays, and groups.

## OpenAPI Overlays

Customize how operations are exposed as MCP tools without modifying the original spec:

```yaml
overlay: 1.0.0
info:
  title: Petstore overlay
  version: 1.0.0
actions:
  # Remove internal endpoints
  - target: $.paths['/internal/metrics']
    remove: true

  # Disable an operation
  - target: $.paths['/pets/{petId}'].delete
    update:
      x-mcp-enabled: false

  # Skip auth for public endpoints
  - target: $.paths['/health'].get
    update:
      x-mcp-auth-required: false

  # Override tool name and mark as safe
  - target: $.paths['/pets'].get
    update:
      x-mcp-tool-name: list_pets
      x-mcp-safe: true

  # Binary response format
  - target: $.paths['/pets/{petId}/photo'].get
    update:
      x-mcp-response-format: image
```

### x-mcp extensions

| Extension | Type | Default | Purpose |
|-----------|------|---------|---------|
| `x-mcp-enabled` | bool | `true` | Exclude operation from tool generation |
| `x-mcp-tool-name` | string | derived | Override generated tool name |
| `x-mcp-auth-required` | bool | `true` | Per-operation auth bypass |
| `x-mcp-safe` | bool | `false` | Mark as read-only (for group filters) |
| `x-mcp-response-format` | string | `json` | `json` \| `text` \| `image` \| `audio` \| `binary` \| `auto` |
| `x-mcp-request-transform` | string | generated | Custom jq request expression |
| `x-mcp-response-transform` | string | `.` | Custom jq response expression |
| `x-mcp-error-transform` | string | default | Custom jq error handler |
| `x-mcp-tier` | string | — | Arbitrary label for group filters |

## Authentication

### Inbound (MCP clients → proxy)

| Strategy | Description |
|----------|-------------|
| `jwt` | OIDC JWT validation with JWKS auto-rotation |
| `introspection` | Token introspection via OIDC server |
| `apikey` | Static API key from a custom header |
| `lua` | Custom validation logic in Lua |
| `none` | No authentication |

### Outbound (proxy → upstream APIs)

| Strategy | Description |
|----------|-------------|
| `bearer` | Static Bearer token from environment |
| `oauth2_client_credentials` | OAuth2 CC flow with token caching |
| `api_key` | API key header injection |
| `lua` | Custom token acquisition in Lua |

Per-upstream overrides and per-operation bypass (`x-mcp-auth-required: false`) are supported.

## Architecture

```
MCP Client ──→ mcp-anything proxy ──→ HTTP API (OpenAPI)
               (single binary)    ──→ Shell command
                                  ──→ JavaScript script
```

The proxy is a single stateless binary. On startup it loads the config, fetches OpenAPI specs, applies overlays, generates MCP tools, compiles jq transforms, and validates everything with dry-run. At runtime it handles MCP protocol, routes tool calls to the right upstream, manages auth, and validates requests/responses.

Key design properties:
- **Stateless** — no shared mutable state; each request is self-contained
- **Atomic hot-reload** — config changes don't drop in-flight requests
- **Overlay-as-configuration** — all per-operation behavior via OpenAPI Overlay extensions
- **Uniform pipeline** — auto-generated and manual tools use identical code paths

## Deployment

### Docker

```bash
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/etc/mcp-anything/config.yaml \
  ghcr.io/gaarutyunov/mcp-anything:latest
```

### Kubernetes

The proxy is designed for Kubernetes. Mount config as a ConfigMap, specs and overlays as separate ConfigMaps for independent update cycles:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-anything
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: proxy
          image: ghcr.io/gaarutyunov/mcp-anything:latest
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/mcp-anything
      volumes:
        - name: config
          configMap:
            name: mcp-anything-config
```

## Development

Requirements: Go 1.25+, Docker (for integration tests)

```bash
make check        # lint + vet + unit tests + build
make integration  # integration tests (Docker + Testcontainers)
```

Set `TC_CLOUD_TOKEN` to run containers via [Testcontainers Cloud](https://testcontainers.com/cloud/).

## Design

See [SPEC.md](SPEC.md) for the full architecture document covering all design decisions, configuration model, and acceptance criteria.

## License

[Apache License 2.0](LICENSE)
