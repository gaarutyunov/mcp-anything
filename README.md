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

### Connect to Kraken Market Data

The Kraken cryptocurrency exchange exposes a public REST API — no API key required. Download the example config files and start the proxy in three commands:

```bash
# 1. Download config, OpenAPI spec, and overlay
mkdir -p kraken && cd kraken
curl -sLO https://raw.githubusercontent.com/gaarutyunov/mcp-anything/main/deploy/examples/kraken/config.yaml
curl -sLO https://raw.githubusercontent.com/gaarutyunov/mcp-anything/main/deploy/examples/kraken/spec.yaml
curl -sLO https://raw.githubusercontent.com/gaarutyunov/mcp-anything/main/deploy/examples/kraken/overlay.yaml

# 2. Install and run
go install github.com/gaarutyunov/mcp-anything/cmd/proxy@latest
CONFIG_PATH=config.yaml proxy
```

Or with Docker:

```bash
docker run -p 8080:8080 \
  -v $(pwd):/etc/mcp-anything \
  -w /etc/mcp-anything \
  ghcr.io/gaarutyunov/mcp-anything:latest
```

Connect any MCP client to `http://localhost:8080/mcp`. Five tools are now available:

| Tool | Description |
|------|-------------|
| `kraken__get_system_status` | Exchange status and server time |
| `kraken__get_ticker` | Real-time ask/bid/last price for any pair (e.g. `XBTUSD`) |
| `kraken__get_ohlc` | OHLC candlestick data at any interval |
| `kraken__get_order_book` | Live order book with top 5 bid/ask levels |
| `kraken__get_recent_trades` | Most recent 10 public trades |

The overlay in [deploy/examples/kraken/overlay.yaml](deploy/examples/kraken/overlay.yaml) applies jq transforms that flatten Kraken's nested response format into clean named fields — no client-side parsing required.

#### Use with Claude Code

```bash
CONFIG_PATH=config.yaml proxy &
claude mcp add --transport http kraken http://localhost:8080/mcp
```

Claude can now call Kraken tools directly — ask it to check Bitcoin prices, pull OHLC data, or read the order book.

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
  # HTTP upstream backed by an OpenAPI spec
  - name: kraken
    type: http
    tool_prefix: kraken
    base_url: https://api.kraken.com
    openapi:
      source: deploy/examples/kraken/spec.yaml
      refresh_interval: 5m
    overlay:
      source: deploy/examples/kraken/overlay.yaml

  # Shell command upstream — wraps any CLI tool
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

  # JavaScript script upstream — custom logic in Sobek runtime
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

# Serve different tool sets at different endpoints
groups:
  - name: all
    endpoint: /mcp
    upstreams: [kraken, cluster, custom]

  - name: market
    endpoint: /mcp/market
    upstreams: [kraken]
    filter: "$.paths[?(@['x-mcp-safe'] == true)]"
```

See [deploy/examples/](deploy/examples/) for complete examples including Kubernetes CRDs and Helm values.

## OpenAPI Overlays

Customize how operations are exposed as MCP tools without modifying the original spec. The [Kraken overlay](deploy/examples/kraken/overlay.yaml) shows realistic use:

```yaml
overlay: 1.0.0
info:
  title: Kraken Market Data Overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535

actions:
  # Override tool name and flatten Kraken's nested result wrapper with jq
  - target: "$.paths[\"/0/public/Ticker\"].get"
    update:
      x-mcp-tool-name: get_ticker
      description: >
        Get current ticker information for a trading pair (e.g. XBTUSD, ETHUSD).
        Returns ask, bid, last price, 24h volume, VWAP, high, low, and trade count.
      x-mcp-response-transform: >
        .result | to_entries[0] | {
          pair: .key,
          ask: .value.a[0],
          bid: .value.b[0],
          lastPrice: .value.c[0],
          volume24h: .value.v[1],
          vwap24h: .value.p[1],
          high24h: .value.h[1],
          low24h: .value.l[1],
          trades24h: .value.t[1],
          openingPrice: .value.o
        }

  # Limit order book depth to top 5 levels
  - target: "$.paths[\"/0/public/Depth\"].get"
    update:
      x-mcp-tool-name: get_order_book
      x-mcp-response-transform: >
        .result | to_entries[0] | {
          pair: .key,
          asks: .value.asks[:5],
          bids: .value.bids[:5]
        }
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
| `introspection` | Token introspection via OIDC discovery endpoint |
| `apikey` | Static API key from a custom header |
| `lua` | Custom validation logic in Lua |
| `none` | No authentication |

### Outbound (proxy → upstream APIs)

| Strategy | Description |
|----------|-------------|
| `bearer` | Static Bearer token from environment variable |
| `oauth2_client_credentials` | OAuth2 CC flow with automatic token caching and refresh |
| `api_key` | API key header injection |
| `lua` | Custom token acquisition logic in Lua |

Per-upstream overrides and per-operation bypass (`x-mcp-auth-required: false`) are supported.

## Architecture

```
MCP Client ──→ mcp-anything proxy ──→ HTTP API (OpenAPI)
               (single binary)    ──→ Shell command
                                  ──→ JavaScript script
```

The proxy is a single stateless binary. On startup it loads the config, fetches OpenAPI specs, applies overlays, generates MCP tools, compiles jq transforms, and validates everything with dry-run. At runtime it handles the MCP protocol, routes tool calls to the right upstream, manages auth, and validates requests and responses.

Key design properties:
- **Stateless** — no shared mutable state; each request is self-contained
- **Atomic hot-reload** — config changes don't drop in-flight requests
- **Overlay-as-configuration** — all per-operation behavior via OpenAPI Overlay extensions
- **Uniform pipeline** — auto-generated and manual tools use identical code paths
- **Double-underscore namespacing** — tool names follow `{prefix}__{operation}` convention

## Deployment

### Docker

```bash
docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/etc/mcp-anything/config.yaml \
  ghcr.io/gaarutyunov/mcp-anything:latest
```

### Kubernetes

The proxy is designed for Kubernetes. Mount config, specs, and overlays as separate ConfigMaps for independent update cycles:

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

Use the `MCPProxy` and `MCPUpstream` CRDs with the included Helm chart for production deployments. See [deploy/examples/kraken/](deploy/examples/kraken/) for a complete Kubernetes example including CRD manifests and Helm values.

## Development

Requirements: Go 1.25+, Docker (for integration tests)

```bash
make check        # lint + vet + unit tests + build
make integration  # integration tests (Docker + Testcontainers)
```

## Design

See [SPEC.md](SPEC.md) for the full architecture document covering all design decisions, configuration model, and acceptance criteria.

## License

[Apache License 2.0](LICENSE)
