# mcp-anything: Design Document

**Version:** 0.3.0-draft  
**Status:** Pre-implementation  
**Repository:** `github.com/your-org/mcp-anything`

-----

## Table of Contents

1. [Overview](#1-overview)
1. [Goals and Non-Goals](#2-goals-and-non-goals)
1. [Architecture](#3-architecture)
1. [Technology Decisions](#4-technology-decisions)
1. [Monorepo Structure](#5-monorepo-structure)
1. [Configuration Model](#6-configuration-model)
1. [OpenAPI Loading and Overlay Pipeline](#7-openapi-loading-and-overlay-pipeline)
1. [Automatic Tool Generation](#8-automatic-tool-generation)
1. [Multi-Upstream Routing](#9-multi-upstream-routing)
1. [JSON Transformation Engine](#10-json-transformation-engine)
1. [Synthetic Data Generation](#11-synthetic-data-generation)
1. [Non-JSON Response Handling](#12-non-json-response-handling)
1. [Error Response Handling](#13-error-response-handling)
1. [MCP Protocol Layer](#14-mcp-protocol-layer)
1. [Authentication Layer](#15-authentication-layer)
1. [OpenTelemetry Observability](#16-opentelemetry-observability)
1. [Request Lifecycle](#17-request-lifecycle)
1. [Kubernetes Integration](#18-kubernetes-integration)
1. [Future: CRD Controller](#19-future-crd-controller)
1. [Error Handling](#20-error-handling)
1. [Security Considerations](#21-security-considerations)
1. [Acceptance Criteria (EARS)](#22-acceptance-criteria-ears)

-----

## 1. Overview

`mcp-anything` is a stateless, pure-proxy Go server that converts any number of existing HTTP APIs into a single Model Context Protocol (MCP) server. Operators describe each upstream API via an OpenAPI 3.0 specification and optionally an OpenAPI Overlay document to filter, rename, and annotate operations before they become MCP tools. The proxy auto-generates MCP tools from OpenAPI operations — including generating jq request-construction expressions from parameter metadata — authenticates incoming MCP clients via pluggable middleware, routes tool calls to the correct upstream, handles both JSON and binary responses, and emits fully standardized OpenTelemetry traces and metrics.

The initial deployment target is **Kubernetes-native via ConfigMap-mounted configuration**, with the architecture leaving explicit room for a future CRD-based controller.

No database. No persistent state. Every pod is identical.

-----

## 2. Goals and Non-Goals

### Goals

- Auto-generate MCP tools from all OpenAPI operations, including synthesising per-tool jq request-construction expressions from OpenAPI parameter metadata
- Support **multiple upstream servers** in a single proxy instance, each with their own base URL, auth, OpenAPI spec, overlay, and tool prefix
- Prefix tool names per upstream using `{prefix}__{tool}` to guarantee uniqueness and enable routing
- Apply OpenAPI Overlays (v1.0.0 spec) — with RFC 9535 JSONPath — to filter, rename, and annotate operations before tool generation
- Support inline `x-mcp-*` OpenAPI extensions for auth bypass, response format, tool naming, and description overrides
- Validate all jq expressions at **config load time** via dry-run against synthetic data; validate requests and responses at **runtime** against OpenAPI schemas
- Handle non-JSON upstream responses (images, audio, binary) via MCP’s `ImageContent`, `AudioContent`, and `ResourceContent` types
- Handle non-2xx upstream responses with a dedicated `error_transform` jq expression and automatic `application/problem+json` parsing
- Support pluggable **inbound authentication** (JWT, introspection, API key, Lua) with per-operation bypass via `x-mcp-auth-required: false`
- Support pluggable **outbound authentication** per upstream (Bearer, API key, OAuth2 client credentials, Lua)
- Use JSONPath (RFC 9535) for expressive group filtering, consistent with the overlay syntax
- Emit OpenTelemetry traces and metrics using official HTTP and MCP semantic conventions
- Deploy as a single stateless binary; load OpenAPI specs and overlays from file paths or HTTPS URLs with background refresh

### Non-Goals

- Acting as an authorization server — the proxy is a Resource Server only
- Aggregating other MCP servers (HTTP REST upstreams only)
- Persistent session storage or stateful routing
- GraphQL, gRPC, or WebSocket upstream support in v1
- OpenAPI 3.1 in v1 (3.0 only; 3.1 upgrade path documented)

-----

## 3. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        MCP Client (LLM)                             │
└──────────────────────────────┬──────────────────────────────────────┘
                               │  Streamable HTTP or SSE
                               │  Bearer token in Authorization header
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                       mcp-anything proxy                            │
│                                                                     │
│  ┌──────────────┐  ┌────────────────┐  ┌───────────────────────┐   │
│  │ Inbound Auth │  │ OTel Middleware │  │  MCP Protocol Layer   │   │
│  │ Middleware   │─▶│ (traces+metrics)│─▶│  (go-sdk)             │   │
│  │ (pluggable)  │  └────────────────┘  └──────────┬────────────┘   │
│  └──────────────┘                                 │                 │
│                                                   ▼                 │
│                                    ┌──────────────────────────┐     │
│                                    │ Multi-Upstream Tool Router│     │
│                                    │ prefix__tool → upstream  │     │
│                                    │ (strip prefix, dispatch) │     │
│                                    └──────────┬───────────────┘     │
│                              ┌────────────────┼────────────┐        │
│                              ▼                ▼            ▼        │
│                   ┌─────────────────┐  ┌─────────────────┐ ...     │
│                   │   Upstream A    │  │   Upstream B    │          │
│                   │ ┌─────────────┐ │  │ ┌─────────────┐ │          │
│                   │ │Transform Eng│ │  │ │Transform Eng│ │          │
│                   │ │req/resp/err │ │  │ │req/resp/err │ │          │
│                   │ └──────┬──────┘ │  │ └──────┬──────┘ │          │
│                   │ ┌──────▼──────┐ │  │ ┌──────▼──────┐ │          │
│                   │ │ OpenAPI     │ │  │ │ OpenAPI     │ │          │
│                   │ │ Validator   │ │  │ │ Validator   │ │          │
│                   │ └──────┬──────┘ │  │ └──────┬──────┘ │          │
│                   │ ┌──────▼──────┐ │  │ ┌──────▼──────┐ │          │
│                   │ │ Outbound    │ │  │ │ Outbound    │ │          │
│                   │ │ Auth        │ │  │ │ Auth        │ │          │
│                   │ └──────┬──────┘ │  │ └──────┬──────┘ │          │
│                   └────────┼────────┘  └────────┼────────┘          │
└────────────────────────────┼────────────────────┼───────────────────┘
                             ▼                    ▼
               ┌──────────────────┐  ┌──────────────────┐
               │  Upstream API A  │  │  Upstream API B  │
               └──────────────────┘  └──────────────────┘

Config / Spec plane:
  ┌──────────────────────┐   ┌────────────────────────────────────────────┐
  │  ConfigMap (now)     │──▶│ Config Loader (koanf)                      │
  │  CRD (future)        │   │ → Overlay pipeline (speakeasy-overlay)     │
  │  URL specs (fetched) │   │ → Auto tool + jq generation                │
  └──────────────────────┘   │ → jq compile + synthetic dry-run           │
                             └────────────────────────────────────────────┘
```

### Key design properties

**Stateless per request.** Each tool call is fully self-contained. No shared mutable state between requests except the compiled upstream snapshots (immutable until hot-reload).

**Uniform transform pipeline.** Auto-generated and manually overridden tools go through identical code paths. Auto-generation produces a concrete jq expression from OpenAPI parameter metadata; manual overrides replace it. Both are compiled and dry-run the same way.

**Double-underscore namespacing.** All tool names are `{upstream.tool_prefix}__{slugified_name}`. Routing splits on `__`, dispatches to the upstream, and strips the prefix before constructing the upstream HTTP request.

**Overlay-as-configuration.** All per-operation behaviour (auth bypass, response format, naming, description, group membership) is injectable via OpenAPI Overlay using standard `x-mcp-*` extensions. No separate tool config list is required.

-----

## 4. Technology Decisions

|Concern                     |Selected Library                                                 |Version |Rationale                                                                      |
|----------------------------|-----------------------------------------------------------------|--------|-------------------------------------------------------------------------------|
|MCP protocol                |`github.com/modelcontextprotocol/go-sdk`                         |v1.3.0  |Official SDK, v1 stable, dynamic tool registration with auto-notification      |
|JSON transformation         |`github.com/itchyny/gojq`                                        |v0.12.18|Pure Go, compile-once/run-many, 5,400+ importers, used by Redpanda Connect     |
|OpenAPI parsing + validation|`github.com/getkin/kin-openapi`                                  |v0.134.0|Dominant Go OpenAPI library; `openapi3filter` for runtime req/resp validation  |
|OpenAPI Overlay             |`github.com/speakeasy-api/openapi-overlay`                       |v0.10.3 |Only Go library with RFC 9535 JSONPath on `*yaml.Node`; adopted by oapi-codegen|
|JSONPath (RFC 9535)         |`github.com/speakeasy-api/jsonpath`                              |latest  |Powers overlay evaluation; also used for group filters                         |
|JSON Schema validation      |`github.com/santhosh-tekuri/jsonschema`                          |v6.0.2  |Draft 4 → 2020-12 support, compile-then-validate                               |
|Synthetic data              |Custom recursive generator + `github.com/lucasjones/reggen`      |—       |No production-ready schema-to-data library in Go                               |
|JWT validation              |`github.com/coreos/go-oidc/v3`                                   |v3.17.0 |JWKS auto-rotation, deduplicates parallel key fetches                          |
|Token introspection         |`github.com/zitadel/oidc/v3`                                     |v3.45.1 |OpenID certified, generics-based `ResourceServer` interface                    |
|OAuth2 client               |`golang.org/x/oauth2`                                            |v0.33.0 |Client-side token acquisition and refresh for upstream calls                   |
|Lua scripting               |`github.com/yuin/gopher-lua`                                     |v1.1.1  |Pure Go Lua 5.1, context timeout, KrakenD-proven, pool-safe                    |
|Config loading              |`github.com/knadh/koanf/v2`                                      |v2.1.2  |Preserves key casing, modular providers                                        |
|File watching               |`github.com/fsnotify/fsnotify`                                   |v1.9.0  |Parent-directory watching for ConfigMap symlink pattern                        |
|OTel SDK                    |`go.opentelemetry.io/otel`                                       |v1.35.0 |Standard Go OTel SDK                                                           |
|OTel HTTP instrumentation   |`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`  |v0.67.0 |Automatic HTTP server + client span/metric emission                            |
|OTel exporter               |`go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`|v1.35.0 |OTLP gRPC, port 4317                                                           |
|HTTP router                 |`net/http` + `github.com/go-chi/chi/v5`                          |v5.2.0  |Lightweight, pattern matching, middleware chain                                |

-----

## 5. Monorepo Structure

Single Go module. `go.work` is never committed; set `GOWORK=off` in CI.

```
mcp-anything/
├── go.mod                                    # module github.com/your-org/mcp-anything
├── go.sum
├── cmd/
│   └── proxy/
│       └── main.go
├── internal/
│   ├── config/
│   │   ├── config.go                         # All config structs
│   │   ├── loader.go                         # koanf loading + fsnotify watching
│   │   └── validator.go                      # Config-time validation orchestration
│   ├── openapi/
│   │   ├── loader.go                         # Spec loading (file + URL + conditional GET)
│   │   ├── overlay.go                        # Overlay parsing, validation, application
│   │   ├── generator.go                      # Auto tool + jq generation from operations
│   │   ├── naming.go                         # Slugification + conflict detection
│   │   ├── validator.go                      # Runtime request/response validation
│   │   └── synthetic.go                      # Synthetic data generator
│   ├── transform/
│   │   ├── engine.go                         # gojq compile + run
│   │   ├── request.go                        # Generated + custom request jq
│   │   ├── response.go                       # Success response transform
│   │   └── error.go                          # Error response + problem+json handling
│   ├── upstream/
│   │   ├── registry.go                       # ToolRegistry keyed by prefixed name
│   │   ├── client.go                         # Per-upstream HTTP client with OTel
│   │   └── refresh.go                        # Background URL spec + overlay refresh
│   ├── auth/
│   │   ├── inbound/
│   │   │   ├── middleware.go                 # TokenValidator interface + middleware
│   │   │   ├── jwt.go
│   │   │   ├── introspection.go
│   │   │   ├── apikey.go
│   │   │   ├── lua.go                        # Pooled, sandboxed, pre-compiled Lua VMs
│   │   │   └── wellknown.go                  # /.well-known/oauth-protected-resource
│   │   └── outbound/
│   │       ├── provider.go                   # OutboundTokenProvider interface
│   │       ├── bearer.go
│   │       ├── apikey.go
│   │       ├── oauth2.go
│   │       └── lua.go
│   ├── content/
│   │   └── format.go                         # Non-JSON response → MCP content type mapping
│   ├── mcp/
│   │   ├── server.go                         # MCP server bootstrap + tool registry
│   │   ├── handler.go                        # Tool call pipeline
│   │   └── router.go                         # prefix__ splitting + upstream dispatch
│   ├── telemetry/
│   │   ├── provider.go
│   │   ├── middleware.go
│   │   └── attributes.go
│   └── server/
│       └── server.go                         # net/http wiring + graceful shutdown
├── pkg/
│   └── crd/                                  # Future: CRD types
│       └── types.go
├── deploy/
│   ├── helm/
│   └── example/
│       ├── config.yaml
│       └── overlay-petstore.yaml
└── Dockerfile
```

-----

## 6. Configuration Model

A single YAML file, mounted from a ConfigMap at `/etc/mcp-anything/config.yaml`. Environment variables are expanded via `${VAR}` syntax. Sensitive values are always env refs, never plain text.

### Full config schema

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 60s
  shutdown_timeout: 10s
  max_request_body: 1MB
  startup_validation_timeout: 30s   # per-upstream timeout for jq dry-run at startup
                                    # on hot-reload: exceeded → abandon reload, keep old config
  transport:                        # list of enabled transports
    - streamable-http               # primary: POST+GET on group endpoint paths
    - sse                           # legacy: GET on {endpoint}/sse paths

telemetry:
  otlp_endpoint: "otel-collector:4317"
  service_name: "mcp-anything"
  service_version: "v0.1.0"
  insecure: false

# Default inbound auth — applies to all operations unless x-mcp-auth-required: false
inbound_auth:
  strategy: jwt   # jwt | introspection | apikey | lua | none
  jwt:
    issuer: "https://idp.example.com"
    audience: "https://mcp.example.com"
    jwks_url: ""                    # optional; if empty, uses issuer discovery
  introspection:
    issuer: "https://idp.example.com"
    client_id: "${INTROSPECTION_CLIENT_ID}"
    client_secret: "${INTROSPECTION_CLIENT_SECRET}"
    audience: "https://mcp.example.com"
  apikey:
    header: "X-API-Key"
    keys_env: "MCP_API_KEYS"        # comma-separated list in env var
  lua:
    script_path: "/etc/mcp-anything/auth.lua"
    timeout: 500ms

naming:
  separator: "__"
  max_length: 128
  conflict_resolution: error        # error | first_wins | skip
  description_max_length: 1024     # 0 = no truncation
  description_truncation_suffix: "..."
  default_slug_rules:
    replace_slashes: true
    replace_braces: true
    lowercase: true
    collapse_separators: true

upstreams:
  - name: petstore
    enabled: true
    tool_prefix: pets
    base_url: "https://api.petstore.io/v2"
    max_response_body: 10MB
    timeout: 10s
    tls_skip_verify: false
    headers:
      X-Proxy-Source: "mcp-anything"

    openapi:
      source: "/etc/mcp-anything/specs/petstore.yaml"  # file path or https:// URL
      auth_header: ""               # "Bearer ${TOKEN}" for private docs
      refresh_interval: 0           # 0 = no refresh; >0 = background URL refresh
      max_refresh_failures: 5
      allow_external_refs: false
      version: "3.0"

    # Overlay is always applied after spec load and re-applied on every refresh.
    # source may be a file path or https:// URL.
    # auth_header and refresh_interval are independent of the spec's values.
    overlay:
      source: "/etc/mcp-anything/overlays/petstore-overlay.yaml"
      auth_header: ""
      refresh_interval: 0           # 0 = refresh together with openapi.refresh_interval

    outbound_auth:
      strategy: oauth2_client_credentials
      oauth2_client_credentials:
        token_url: "https://idp.example.com/oauth/token"
        client_id: "${PETSTORE_CLIENT_ID}"
        client_secret: "${PETSTORE_CLIENT_SECRET}"
        scopes: ["read:pets", "write:pets"]
      bearer:
        token_env: "UPSTREAM_TOKEN"
      api_key:
        header: "X-API-Key"
        value_env: "UPSTREAM_API_KEY"
        prefix: ""                  # prepended to env value, e.g. "ApiKey "
      lua:
        script_path: "/etc/mcp-anything/upstream-auth.lua"
        timeout: 500ms

    validation:
      validate_request: true
      validate_response: true
      response_validation_failure: warn   # warn | fail
      success_status: [200, 201, 202, 204]
      error_status: [400, 401, 403, 404, 422, 429, 500, 502, 503]

  - name: github
    enabled: true
    tool_prefix: gh
    base_url: "https://api.github.com"
    openapi:
      source: "https://raw.githubusercontent.com/github/rest-api-description/main/descriptions/api.github.com/api.github.com.yaml"
      refresh_interval: 1h
    overlay:
      source: "https://raw.githubusercontent.com/github/rest-api-description/main/descriptions/api.github.com/mcp-overlay.yaml"
      refresh_interval: 1h
    outbound_auth:
      strategy: bearer
      bearer:
        token_env: "GITHUB_TOKEN"

groups:
  - name: all
    endpoint: /mcp
    upstreams: [petstore, github]
    # no filter = all tools from all listed upstreams

  - name: readonly
    endpoint: /mcp/readonly
    upstreams: [petstore]
    # RFC 9535 JSONPath filter evaluated against each operation node
    filter: "$.paths.*.get[?(@['x-mcp-safe'] == true)]"

  - name: premium
    endpoint: /mcp/premium
    upstreams: [petstore, github]
    filter: "$.paths.*.*[?(@['x-mcp-tier'] == 'premium')]"
```

### x-mcp-* extension reference

All extensions are injectable via overlay. They are evaluated at tool generation time (not per-request) except `x-mcp-auth-required` which is checked per-request.

|Extension                 |Type     |Default  |Purpose                                                |
|--------------------------|---------|---------|-------------------------------------------------------|
|`x-mcp-enabled`           |`boolean`|`true`   |Set `false` to exclude operation from tool generation  |
|`x-mcp-tool-name`         |`string` |derived  |Override the generated tool base name                  |
|`x-mcp-auth-required`     |`boolean`|`true`   |Set `false` to bypass inbound auth for this tool       |
|`x-mcp-safe`              |`boolean`|`false`  |Mark operation as read-only/safe; used by group filters|
|`x-mcp-response-format`   |`string` |`json`   |`json` | `text` | `image` | `audio` | `binary` | `auto`|
|`x-mcp-request-transform` |`string` |generated|Override the auto-generated jq request expression      |
|`x-mcp-response-transform`|`string` |`.`      |Override the default jq response expression            |
|`x-mcp-error-transform`   |`string` |see §13  |Override the default error response jq expression      |
|`x-mcp-tier`              |`string` |—        |Arbitrary tier label; used by group filters            |

-----

## 7. OpenAPI Loading and Overlay Pipeline

Each upstream goes through a deterministic pipeline at config load time:

```
[1]  Load spec bytes       → file (os.ReadFile) or URL (custom ReadFromURIFunc)
[2]  Unmarshal yaml.Node   → gopkg.in/yaml.v3
[3]  Load overlay          → file or URL (independent auth + refresh)
[4]  Apply overlay         → speakeasy-api/openapi-overlay ApplyToStrict(*yaml.Node)
[5]  Marshal to bytes      → yaml.Marshal
[6]  Load into kin-openapi → openapi3.Loader.LoadFromDataWithPath(bytes, baseURI)
[7]  Validate spec         → doc.Validate(ctx)
[8]  Build router          → gorillamux.NewRouter(doc)
[9]  Generate tools + jq   → walk operations, slugify, generate request jq (§8)
[10] Compile jq            → gojq.Compile per tool (request, response, error)
[11] Dry-run validation    → three synthetic instances per tool → jq → schema.VisitJSON
```

Steps 10 and 11 respect `server.startup_validation_timeout` per upstream. On timeout during hot-reload, the reload is abandoned and the previous snapshot stays live.

### URL loading with auth

`openapi3.Loader.ReadFromURIFunc` is overridden to inject auth headers on every HTTP fetch — both the root spec and all `$ref` URLs it references:

```go
loader.ReadFromURIFunc = openapi3.URIMapCache(
    openapi3.ReadFromURIs(
        func(loader *openapi3.Loader, uri *url.URL) ([]byte, error) {
            req, _ := http.NewRequestWithContext(loader.Context, "GET", uri.String(), nil)
            if cfg.AuthHeader != "" {
                req.Header.Set("Authorization", expandEnv(cfg.AuthHeader))
            }
            if etag := current.Load().ETag; etag != "" {
                req.Header.Set("If-None-Match", etag)
            }
            resp, _ := http.DefaultClient.Do(req)
            if resp.StatusCode == 304 { return nil, errNotModified }
            return io.ReadAll(resp.Body)
        },
        openapi3.ReadFromFile,
    ),
)
```

### Background refresh

For upstreams with `openapi.refresh_interval > 0`, a background goroutine fetches the spec and overlay together on the interval, re-applies the full pipeline, and atomically swaps via `atomic.Pointer[UpstreamSnapshot]`. The overlay is re-fetched on its own `refresh_interval`; if `overlay.refresh_interval` is `0`, it refreshes together with the spec.

```go
type UpstreamSnapshot struct {
    Doc           *openapi3.T
    Router        routers.Router
    CompiledTools map[string]*CompiledTool
    ETag          string
    OverlayETag   string
    FetchedAt     time.Time
}
```

### Overlay application

```go
func ApplyOverlay(specBytes []byte, cfg *OverlayConfig) ([]byte, []string, error) {
    ol, _ := overlay.Parse(cfg.Source)   // or ParseReader for inline
    ol.Validate()

    var root yaml.Node
    yaml.Unmarshal(specBytes, &root)

    applyErr, warnings := ol.ApplyToStrict(&root)
    out, _ := yaml.Marshal(&root)
    return out, warnings, applyErr
}
```

Overlay warnings (unmatched JSONPath targets) are logged at WARN level and do not fail startup.

### Example overlay

```yaml
overlay: 1.0.0
info:
  title: Pet Store MCP Overlay
  version: 1.0.0
x-speakeasy-jsonpath: rfc9535

actions:
  # Remove internal endpoints entirely
  - target: $.paths['/internal/metrics']
    remove: true

  # Disable destructive operation — stays in spec but excluded from tools
  - target: $.paths['/pets/{petId}'].delete
    update:
      x-mcp-enabled: false

  # Public health endpoint — no auth required
  - target: $.paths['/health'].get
    update:
      x-mcp-auth-required: false

  # Override tool names and descriptions
  - target: $.paths['/pets'].get
    update:
      x-mcp-tool-name: list_pets
      description: "List all available pets with optional status filter."
  - target: $.paths['/pets'].post
    update:
      x-mcp-tool-name: create_pet

  # Override response format for a binary endpoint
  - target: $.paths['/pets/{petId}/photo'].get
    update:
      x-mcp-response-format: image

  # Bulk-mark all GETs as safe for group filtering
  - target: $.paths.*.get
    update:
      x-mcp-safe: true
      x-mcp-tier: standard
```

-----

## 8. Automatic Tool Generation

The proxy derives MCP tools automatically from OpenAPI operations after the overlay is applied. **No manual tool list is required.**

### Operation iteration

For each path + method combination in the post-overlay spec:

1. Check `x-mcp-enabled` — if `false`, skip
1. Resolve tool name — use `x-mcp-tool-name` or slugify (see below)
1. Apply prefix — `{upstream.tool_prefix}__{tool_name}`
1. Generate request jq expression from OpenAPI parameter metadata
1. Resolve response/error transforms — use `x-mcp-*-transform` extension if present, else defaults
1. Build `InputSchema` from parameters and request body
1. Compile and dry-run all three jq expressions

### Default tool name slugification

|Method + Path         |Generated base name|
|----------------------|-------------------|
|`GET /pets`           |`list_pets`        |
|`POST /pets`          |`create_pets`      |
|`GET /pets/{petId}`   |`get_pets_petId`   |
|`PUT /pets/{petId}`   |`update_pets_petId`|
|`DELETE /pets/{petId}`|`delete_pets_petId`|
|`PATCH /pets/{petId}` |`patch_pets_petId` |

Slugification rules applied in order:

|Rule                                                              |Example input   |Example output|
|------------------------------------------------------------------|----------------|--------------|
|Method verb prefix (non-GET)                                      |`POST /items`   |`create_items`|
|`GET` with no path params → `list_` prefix                        |`GET /pets`     |`list_pets`   |
|`GET` with path params → `get_` prefix                            |`GET /pets/{id}`|`get_pets_id` |
|Strip leading slash                                               |`/pets/{id}`    |`pets/{id}`   |
|Remove `{` and `}`                                                |`pets/{id}`     |`pets/id`     |
|Replace `/` with `_`                                              |`pets/id`       |`pets_id`     |
|Replace remaining non-alphanumeric with `_`                       |`pets.id-v2`    |`pets_id_v2`  |
|Lowercase                                                         |`Pets_ID`       |`pets_id`     |
|Collapse consecutive `_`                                          |`pets__id`      |`pets_id`     |
|Truncate: max = `naming.max_length - len(prefix) - len(separator)`|—               |—             |

`operationId` is used as the base when present and `x-mcp-tool-name` is absent, then the same sanitisation rules are applied.

### Auto-generated request jq expression

The proxy synthesises a concrete jq expression from the operation’s OpenAPI parameter metadata. This expression maps MCP tool arguments to the request envelope. It is compiled and dry-run identically to user-supplied expressions. Operators can see the generated expression in the DEBUG log and copy it as a starting point for `x-mcp-request-transform`.

**Example — `GET /pets` with query params `limit` (integer) and `status` (string):**

```jq
{
  query: {
    limit: (if .limit then (.limit | tostring) else null end),
    status: .status
  }
}
```

**Example — `GET /pets/{petId}` with path param `petId` (string):**

```jq
{
  path: { petId: .petId }
}
```

**Example — `POST /pets` with JSON request body `{name: string, tag?: string}`:**

```jq
{
  body: {
    name: .name,
    tag: .tag
  }
}
```

**Example — mixed path param + query param + body (`PUT /stores/{storeId}/inventory`):**

```jq
{
  path:  { storeId: .storeId },
  query: { dryRun: (if .dryRun then "true" else null end) },
  body:  { items: .items, notes: .notes }
}
```

The generation rules:

- Path params → `path` map; values always coerced to `tostring`
- Query params → `query` map; optional params wrapped in `if .x then ... else null end`; integer/number params coerced to `tostring`
- Required body properties → included directly; optional body properties → included as-is (null if absent is valid JSON)
- Header params → `headers` map

User-supplied `x-mcp-request-transform` (from overlay) replaces the generated expression entirely.

### InputSchema derivation

The MCP `InputSchema` is a JSON Schema object derived from:

- Path parameters → required string fields
- Query parameters → required if `parameter.Required`, otherwise optional
- Request body (`application/json`) → merged as top-level properties
- Header parameters → optional fields

All type, format, minimum, maximum, minLength, maxLength, enum, pattern, and description constraints from the OpenAPI schema are preserved.

### Tool conflict detection

After generating all prefixed names across all upstreams, duplicates are detected. Two upstreams sharing the same `tool_prefix` is always fatal. Within a single upstream, within-spec slug collisions are resolved per `naming.conflict_resolution` (`error`, `first_wins`, or `skip`).

### Description truncation

Operation descriptions are truncated to `naming.description_max_length` characters (default 1024) after overlay application. The suffix `naming.description_truncation_suffix` (default `"..."`) is appended when truncation occurs. Set `description_max_length: 0` to disable truncation.

-----

## 9. Multi-Upstream Routing

### Tool registry

```go
type ToolRegistry struct {
    byPrefixedName map[string]*UpstreamTool  // routing map
    byPrefix       map[string]*Upstream       // O(1) prefix lookup
    toolList       []*mcp.Tool                // ordered for tools/list
    mu             sync.RWMutex
}

type UpstreamTool struct {
    PrefixedName  string
    OriginalName  string          // without prefix; used for upstream HTTP path
    Upstream      *Upstream
    CompiledReq   *gojq.Code     // auto-generated or x-mcp-request-transform
    CompiledResp  *gojq.Code     // x-mcp-response-transform or identity
    CompiledErr   *gojq.Code     // x-mcp-error-transform or default
    ResponseFormat string         // x-mcp-response-format value
    AuthRequired  bool            // x-mcp-auth-required value
}
```

### Routing

On `tools/call` with tool name `{prefix}__{original}`:

```go
idx := strings.Index(name, cfg.Naming.Separator)
prefix, _ := name[:idx], name[idx+len(sep):]

tool := registry.byPrefixedName[name]
if tool == nil { return errorResult("unknown tool") }
return tool.Upstream.Call(ctx, tool.OriginalName, args, tool)
```

### Per-operation auth bypass

`x-mcp-auth-required: false` is stored in `UpstreamTool.AuthRequired`. The inbound auth middleware reads this flag after routing and skips token validation for public tools. The well-known endpoint is always public. All other operations default to `AuthRequired: true`.

### Outbound auth

Each upstream has an independent `OutboundTokenProvider`:

```go
type OutboundTokenProvider interface {
    Token(ctx context.Context) (string, error)
    // RawHeaders returns headers to inject instead of (or in addition to) Token.
    // If non-empty, takes precedence over Token for Authorization.
    RawHeaders(ctx context.Context) (map[string]string, error)
}
```

Inbound MCP client tokens are **never** forwarded to any upstream.

-----

## 10. JSON Transformation Engine

### Three jq expressions per tool

Each tool has up to three compiled jq expressions:

|Expression|Input                                        |Applied when                            |
|----------|---------------------------------------------|----------------------------------------|
|`request` |MCP tool arguments (`map[string]any`)        |Always, before upstream HTTP call       |
|`response`|Parsed JSON response body                    |Upstream returns a `success_status` code|
|`error`   |Parsed response body (or problem+json struct)|Upstream returns an `error_status` code |

All three are compiled at load time. All three are dry-run against synthetic data. The `request` expression is auto-generated if `x-mcp-request-transform` is absent. The `response` expression defaults to `.` (identity). The `error` expression defaults to the problem+json handler (see §13).

### Request envelope schema

The `request` transform output must be an object with optional keys:

```json
{
  "query":   { "key": "string_value" },
  "headers": { "X-Foo": "value" },
  "path":    { "petId": "42" },
  "body":    { }
}
```

### Runtime execution

Compiled `*gojq.Code` objects are stored in `UpstreamTool` (read-only after load). Each execution passes the request’s `context.Context` so that a runaway jq expression is cancelled when the request deadline is reached.

```go
iter := tool.CompiledReq.RunWithContext(ctx, mcpArgs)
v, ok := iter.Next()
if err, isErr := v.(error); isErr { return nil, err }
```

-----

## 11. Synthetic Data Generation

No production-ready Go library exists for schema-to-data generation. The proxy uses a custom recursive generator in `internal/openapi/synthetic.go`.

### Generation priority

For each schema node (in order): `example` → `x-example` extension → `default` → `enum[0]` → first `oneOf`/`anyOf` branch → merged `allOf` → type-based fallback.

### Type-based fallbacks

|Type     |Format       |Generated value                                           |
|---------|-------------|----------------------------------------------------------|
|`string` |`email`      |`gofakeit.Email()`                                        |
|`string` |`uuid`       |`gofakeit.UUID()`                                         |
|`string` |`date-time`  |RFC3339 timestamp                                         |
|`string` |`uri`        |`https://example.com/resource`                            |
|`string` |(pattern set)|`reggen.Generate(pattern, 10)`                            |
|`string` |other        |`"example_string"` (respects min/maxLength)               |
|`integer`|—            |`max(schema.Min, 1)`                                      |
|`number` |—            |`max(schema.Min, 1.0)`                                    |
|`boolean`|—            |`false`                                                   |
|`object` |—            |recurse properties; inject zero values for required fields|
|`array`  |—            |one element respecting `minItems`                         |

### Three instances per tool

The dry-run generates three instances per schema and all must pass:

1. **All properties populated** — every optional field present
1. **Required fields only** — optional fields absent (tests nil-handling in jq)
1. **OneOf/AnyOf variants** — one instance per alternative schema branch

### Cycle detection

Recursive schemas are detected via a visited-path set. Cycles are broken with `{"$cycle": true}` and logged at WARN. Generation continues.

-----

## 12. Non-JSON Response Handling

MCP supports four content types natively: `TextContent`, `ImageContent`, `AudioContent`, and `ResourceContent`. The proxy maps upstream responses to these types based on the `x-mcp-response-format` extension.

### Response format options

|`x-mcp-response-format`|MCP content type |Body handling                                                      |
|-----------------------|-----------------|-------------------------------------------------------------------|
|`json` (default)       |`TextContent`    |Parse as JSON, run `response_transform` jq, re-serialise to string |
|`text`                 |`TextContent`    |Return raw body as string; `response_transform` receives raw string|
|`image`                |`ImageContent`   |Base64-encode body; `mimeType` from `Content-Type` header          |
|`audio`                |`AudioContent`   |Base64-encode body; `mimeType` from `Content-Type` header          |
|`binary`               |`ResourceContent`|Base64-encode body as `blob`; `mimeType` from `Content-Type`       |
|`auto`                 |detected         |Inspect `Content-Type` at runtime; map to above                    |

### `auto` detection rules

|`Content-Type` prefix                   |Mapped format|
|----------------------------------------|-------------|
|`application/json`, `application/*+json`|`json`       |
|`text/*`                                |`text`       |
|`image/*`                               |`image`      |
|`audio/*`                               |`audio`      |
|anything else                           |`binary`     |

The `response_transform` jq expression is **skipped** for `image`, `audio`, and `binary` formats. It **runs** for `json` and `text`, receiving the raw body string for `text`.

-----

## 13. Error Response Handling

### Status code routing

`validation.success_status` and `validation.error_status` define which HTTP status codes are success versus error. Any status code not in either list is treated as an unexpected error and returned as `CallToolResult{IsError: true}` with the raw body.

### Default error handling

When the upstream returns an `error_status` code:

1. **Detect `application/problem+json`** (RFC 9457) — if `Content-Type` matches, parse and extract:
   
   ```go
   type ProblemDetail struct {
       Type     string `json:"type"`
       Title    string `json:"title"`
       Status   int    `json:"status"`
       Detail   string `json:"detail"`
       Instance string `json:"instance"`
   }
   ```
   
   This structured value becomes the input to `error_transform`.
1. **Other content types** — the raw body (parsed as JSON if possible, otherwise as a string) becomes the input to `error_transform`.
1. **Default `error_transform`** (applied when `x-mcp-error-transform` is absent):
   
   ```jq
   if .title then
     {error: .title, detail: (.detail // ""), status: .status}
   else
     {error: ("upstream error: HTTP " + (.status // "unknown" | tostring)), body: .}
   end
   ```
1. **Return** `CallToolResult{IsError: true, Content: [{type:"text", text: JSON(transformed)}]}`

### Custom error transform

Operators can override via overlay:

```yaml
- target: $.paths['/pets'].post
  update:
    x-mcp-error-transform: |
      {
        error: .errors[0].message,
        field: .errors[0].field,
        code:  .errors[0].code
      }
```

The `error_transform` jq expression is compiled and dry-run at load time using the OpenAPI spec’s non-2xx response schemas (e.g., the `422` schema) as synthetic input.

-----

## 14. MCP Protocol Layer

### Server bootstrap

One `mcp.Server` per group endpoint. On config hot-reload, `server.AddTool`/`server.RemoveTools` automatically emit `notifications/tools/list_changed` to all connected clients.

```go
srv := mcp.NewServer(&mcp.Implementation{
    Name:    "mcp-anything",
    Version: cfg.Server.Version,
}, nil)
for _, tool := range registry.ToolsForGroup(groupName) {
    srv.AddTool(tool.MCPTool, registry.MakeHandler(tool))
}
```

### Transports

`server.transport` is a list. Each enabled transport binds to the same port with distinct path patterns:

|Transport        |Path pattern          |Notes                                          |
|-----------------|----------------------|-----------------------------------------------|
|`streamable-http`|`{group.endpoint}`    |POST (JSON-RPC) + GET (SSE stream) on same path|
|`sse`            |`{group.endpoint}/sse`|Legacy; GET opens SSE stream                   |

### Well-known endpoint

Served at `GET /.well-known/oauth-protected-resource` per RFC 9728. Always public (no auth required).

```json
{
  "resource": "https://mcp.example.com",
  "authorization_servers": ["https://idp.example.com"],
  "scopes_supported": ["read:tools", "write:tools"],
  "bearer_methods_supported": ["header"]
}
```

-----

## 15. Authentication Layer

### Inbound: TokenValidator interface

```go
type TokenInfo struct {
    Subject  string
    Scopes   []string
    Audience []string
    Extra    map[string]any
}

type TokenValidator interface {
    ValidateToken(ctx context.Context, token string) (*TokenInfo, error)
}
```

Implementations: `JWTValidator` (coreos/go-oidc, JWKS auto-rotation), `IntrospectionValidator` (zitadel/oidc), `APIKeyValidator`, `LuaValidator`.

**Per-operation auth bypass:** After routing, the middleware checks `UpstreamTool.AuthRequired`. If `false`, token validation is skipped for that specific tool call. The endpoint still accepts the request; it simply does not challenge for a token.

**Inbound Lua script contract:**

```lua
-- token: string — raw Bearer token value (may be empty for public endpoints)
-- Returns: allowed (bool), status (int), extra_headers (table), error_msg (string)
function check_auth(token)
    if not token or #token == 0 then
        return false, 401, {}, "missing token"
    end
    -- custom logic; network calls only via injected Go functions
    return true, 200, {["X-User-ID"] = "user-123"}, ""
end
```

### Outbound: OutboundTokenProvider interface

```go
type OutboundTokenProvider interface {
    Token(ctx context.Context) (string, error)
    RawHeaders(ctx context.Context) (map[string]string, error)
}
```

**Outbound Lua script contract:**

```lua
-- upstream: string — upstream name from config
-- cached_token: string — current cached token or ""
-- cached_expiry: int — unix timestamp, or 0 if no cache
--
-- Returns:
--   token: string — bearer token value ("" if using raw_headers)
--   expiry_unix: int — unix timestamp; 0 = no caching (call every request)
--   raw_headers: table — arbitrary headers to inject (takes precedence over token)
--   error_msg: string — non-empty aborts the upstream request
function get_upstream_token(upstream, cached_token, cached_expiry)
    -- Option A: bearer token with 1h TTL
    return "eyJhbGc...", os.time() + 3600, {}, ""

    -- Option B: arbitrary headers (proprietary auth), no caching
    return "", 0, {
        ["X-API-Key"]    = "secret-key",
        ["X-Tenant-ID"]  = "acme-corp"
    }, ""

    -- Option C: failure
    return "", 0, {}, "could not fetch token from vault"
end
```

Caching is managed in Go: if `expiry_unix > now`, the cached token is reused without calling the script. `raw_headers` takes precedence over `token` for the `Authorization` header.

-----

## 16. OpenTelemetry Observability

### Standard HTTP metrics

|Metric                        |Type         |Unit     |Key Attributes                                                      |
|------------------------------|-------------|---------|--------------------------------------------------------------------|
|`http.server.request.duration`|Histogram    |s        |`http.route`, `http.request.method`, `http.response.status_code`    |
|`http.server.active_requests` |UpDownCounter|{request}|`http.route`, `http.request.method`                                 |
|`http.client.request.duration`|Histogram    |s        |`server.address`, `http.request.method`, `http.response.status_code`|

### MCP-specific metrics

|Metric                            |Type     |Unit     |Key Attributes                                             |
|----------------------------------|---------|---------|-----------------------------------------------------------|
|`mcp.tool.call.duration`          |Histogram|s        |`mcp.tool.name`, `mcp.method`, `error` (bool)              |
|`mcp.tool.call.errors.total`      |Counter  |{call}   |`mcp.tool.name`, `error.type`                              |
|`mcp_anything.transform.duration` |Histogram|s        |`mcp.tool.name`, `transform.stage` (request/response/error)|
|`mcp_anything.config_reload.total`|Counter  |{reload} |`status` (success/failure)                                 |
|`mcp_anything.spec_refresh.total` |Counter  |{refresh}|`upstream`, `status`                                       |

### Span attributes per tool call

Each `tools/call` span is named `tools/call {prefixed_tool_name}` and carries:

- `mcp.method` = `tools/call`
- `mcp.tool.name` = prefixed name (e.g., `pets__list_pets`)
- `mcp.request.id`, `mcp.session.id`
- `upstream.name` on the upstream HTTP child span
- `http.response.status_code` on the upstream HTTP child span

W3C Trace Context propagates via inbound `traceparent` headers and MCP `params._meta.traceparent`.

-----

## 17. Request Lifecycle

```
MCP Client
  │  POST /mcp  (Streamable HTTP, Bearer token)
  ▼
[1]  OTel middleware             → start SERVER span, active_requests++
[2]  MCP SDK handler             → decode JSON-RPC, dispatch by method
[3]  tools/list                  → return registry.ToolsForGroup(), done
[4]  tools/call {prefix}__name
     ├── [4a] Route              → look up UpstreamTool by prefixed name
     ├── [4b] Auth check        → if AuthRequired: validate inbound token
     ├── [4c] Input validation  → validate MCP args against InputSchema
     ├── [4d] Request transform → compiled request jq → RequestEnvelope
     ├── [4e] Build HTTP req    → apply base_url, path params, headers
     ├── [4f] Req validation    → openapi3filter.ValidateRequest()
     ├── [4g] Outbound auth     → OutboundTokenProvider.Token() or RawHeaders()
     ├── [4h] HTTP call         → start CLIENT span, call upstream
     ├── [4i] Read response     → buffer (max_response_body)
     ├── [4j] Status routing    → success_status → response path
     │                             error_status   → error path
     │                             other          → IsError: true, raw body
     │
     │  Success path:
     ├── [4k] Resp validation   → openapi3filter.ValidateResponse()
     ├── [4l] Format check      → x-mcp-response-format routing
     ├── [4m] Resp transform    → compiled response jq (json/text only)
     └── [4n] Return result     → CallToolResult{Content: [...]}
     │
     │  Error path:
     ├── [4k'] Problem+JSON?   → detect and parse RFC 9457 body
     ├── [4l'] Error transform → compiled error jq
     └── [4m'] Return result   → CallToolResult{IsError: true, Content: [...]}
     │
[5]  OTel middleware             → end SERVER span, emit request.duration
```

-----

## 18. Kubernetes Integration

### ConfigMap strategy

Three separate ConfigMaps for independent update cycles:

```yaml
volumes:
  - name: proxy-config
    configMap: { name: mcp-anything-config }    # config.yaml
  - name: openapi-specs
    configMap: { name: mcp-anything-specs }     # one key per upstream spec
  - name: overlays
    configMap: { name: mcp-anything-overlays }  # one key per upstream overlay
```

No `subPath` mounts — full directory mounts required for symlink-aware hot-reload. Each ConfigMap key becomes a file in the mounted directory. The 1MB ConfigMap size limit applies per key; large specs must use URL loading.

### Hot-reload trigger

fsnotify watches the parent directory of `config.yaml`. Kubernetes atomic symlink swaps emit `CREATE` events on the `..data` directory. Events are debounced 500ms before reload is attempted.

### Health endpoints

|Endpoint      |Type     |Returns 200 when                                                                      |
|--------------|---------|--------------------------------------------------------------------------------------|
|`GET /healthz`|Liveness |Process is alive                                                                      |
|`GET /readyz` |Readiness|All upstreams loaded, compiled, dry-run passed                                        |
|`GET /readyz` |—        |Returns 503 with affected upstream name if any upstream exceeds `max_refresh_failures`|

During startup validation, `/readyz` returns 503. On failed reload, `/readyz` returns 200 (previous config is still serving).

-----

## 19. Future: CRD Controller

`pkg/crd/` is a placeholder for `v1alpha1` API types. The controller watches `MCPUpstream` and `MCPOverlay` CRDs, constructs config YAML, and writes it to the proxy ConfigMap. The proxy binary is unchanged.

```yaml
apiVersion: mcp.your-org.io/v1alpha1
kind: MCPUpstream
metadata:
  name: petstore
spec:
  toolPrefix: pets
  baseURL: "https://api.petstore.io/v2"
  openapi:
    configMapRef: { name: petstore-spec, key: openapi.yaml }
  overlay:
    configMapRef: { name: petstore-overlay, key: overlay.yaml }
  outboundAuth:
    type: oauth2ClientCredentials
    secretRef: { name: petstore-oauth2-creds }
```

-----

## 20. Error Handling

### Fatal (proxy exits with non-zero code)

- Config YAML unparseable
- Required field missing
- OpenAPI spec not found / URL non-200 / fails `doc.Validate()`
- Overlay not found / unparseable / fails `ol.Validate()`
- Any jq expression (request, response, or error) fails to parse or compile
- Any jq dry-run errors on all three synthetic inputs
- Tool name conflict with `conflict_resolution: error`
- Two upstreams share the same `tool_prefix`

### Non-fatal runtime → `CallToolResult{IsError: true}`

|Condition                                   |Content                                        |
|--------------------------------------------|-----------------------------------------------|
|Input validation failure                    |Schema error message                           |
|Request jq runtime error                    |jq error with expression excerpt               |
|Upstream timeout                            |`upstream timeout after {n}s`                  |
|Upstream status not in success or error list|`unexpected HTTP {status}` + raw body excerpt  |
|Response jq runtime error                   |jq error with expression excerpt               |
|Response validation failure (`fail` mode)   |Validation error details                       |
|Error jq runtime error                      |`error transform failed: {jq error}` + raw body|

### Non-fatal config reload failures

Log structured ERROR, increment counter, keep previous config live, `/readyz` stays 200.

### Non-fatal spec refresh failures

Log WARN. After `max_refresh_failures` consecutive failures: remove upstream tools from `tools/list`, `/readyz` returns 503 for that upstream.

-----

## 21. Security Considerations

**Token isolation:** Inbound MCP client Bearer tokens are never forwarded to upstream APIs. Outbound credentials are always independently configured.

**Lua sandbox:** All scripts (inbound and outbound) run with `io`, `os`, `package`, `debug` stdlib disabled. Network calls require explicitly registered Go functions. Hard timeouts via `context.WithTimeout`. Pre-compiled bytecode, pooled VMs reset between requests.

**Secret management:** All sensitive values use `${ENV_VAR}` references. ConfigMaps are safe to commit; Secrets are mounted as env vars.

**Request size limits:** `server.max_request_body` (default 1MB inbound) and per-upstream `max_response_body` (default 10MB) prevent OOM attacks.

**TLS:** All connections use TLS by default. `tls_skip_verify` logs WARNING and is blocked in production mode (`server.mode: production`).

**Overlay safety:** Overlays modify spec structure at config load time only. No executable code. No runtime JSONPath evaluation.

**Auth bypass safety:** `x-mcp-auth-required: false` marks individual operations as public. The global default is always authenticated. Public operations are logged at INFO on startup.

-----

## 22. Acceptance Criteria (EARS)

EARS notation:

- **Ubiquitous:** `The <s> shall <response>`
- **Event-driven:** `When <trigger>, the <s> shall <response>`
- **State-driven:** `While <state>, the <s> shall <response>`
- **Conditional:** `If <condition>, the <s> shall <response>`
- **Unwanted behaviour:** `If <condition>, when <trigger>, the <s> shall <response>`

-----

### AC-01: OpenAPI Spec Loading — File

**AC-01.1** The proxy shall load an OpenAPI 3.0 spec from a local file path specified in `openapi.source` at startup.

**AC-01.2** If the file path does not exist or is not readable, the proxy shall exit with a non-zero exit code and log the file path and OS error.

**AC-01.3** When `openapi.allow_external_refs` is `true`, the proxy shall resolve `$ref` values referencing relative local paths relative to the spec file’s directory.

**AC-01.4** If the spec file contains invalid YAML syntax, the proxy shall exit with a non-zero exit code and log the YAML parse error with line number.

-----

### AC-02: OpenAPI Spec Loading — URL

**AC-02.1** If `openapi.source` begins with `http://` or `https://`, the proxy shall fetch the spec over HTTP at startup.

**AC-02.2** If `openapi.auth_header` is non-empty, the proxy shall include the expanded header value as `Authorization` on every HTTP request made to fetch the spec and all its external `$ref` URLs.

**AC-02.3** If the URL returns a non-200 status and no cached spec exists, the proxy shall exit with a non-zero exit code and log the HTTP status and URL.

**AC-02.4** When `openapi.refresh_interval` is greater than zero, the proxy shall periodically refetch the spec using conditional GET with `If-None-Match` set to the last received ETag.

**AC-02.5** When a conditional GET returns HTTP 304, the proxy shall retain the current spec without triggering a reload.

**AC-02.6** When a URL spec refresh succeeds with changed content, the proxy shall atomically swap the upstream snapshot without dropping in-flight requests.

**AC-02.7** If a background URL refresh fails, the proxy shall log at WARN, retain the previous spec, and increment `mcp_anything.spec_refresh.total` with `status=failure`.

**AC-02.8** If consecutive URL refresh failures exceed `openapi.max_refresh_failures`, the proxy shall remove that upstream’s tools from `tools/list` and emit a structured ERROR.

-----

### AC-03: OpenAPI Spec Validation

**AC-03.1** After loading a spec, the proxy shall call `doc.Validate()` and treat failure as a fatal startup error or a failed reload.

**AC-03.2** If a spec declares `openapi: "3.1"`, the proxy shall log a warning that 3.1 support is partial and continue with best-effort parsing.

-----

### AC-04: Overlay Loading

**AC-04.1** If `overlay.source` is a file path, the proxy shall read the overlay file at startup and re-read it on every spec refresh.

**AC-04.2** If `overlay.source` is an HTTPS URL, the proxy shall fetch it using the `overlay.auth_header` credential and the `overlay.refresh_interval`.

**AC-04.3** If `overlay.refresh_interval` is `0`, the proxy shall re-fetch the overlay together with the spec on every spec refresh cycle.

**AC-04.4** If `overlay.inline` is specified and `overlay.source` is absent, the proxy shall parse the inline YAML string as the overlay document.

-----

### AC-05: Overlay Parsing and Application

**AC-05.1** The proxy shall validate the overlay document structure using `ol.Validate()` and treat failure as a fatal startup error.

**AC-05.2** The proxy shall apply the overlay to the raw spec `*yaml.Node` before passing the result to kin-openapi.

**AC-05.3** When `x-speakeasy-jsonpath: rfc9535` is present in the overlay document, the proxy shall use RFC 9535-compliant JSONPath evaluation for all `target` expressions.

**AC-05.4** If an overlay `target` expression matches zero nodes, the proxy shall log a WARN and continue without error.

**AC-05.5** When an overlay action specifies `remove: true`, the proxy shall remove all matched nodes before tool generation.

**AC-05.6** When an overlay action specifies an `update` object, the proxy shall recursively merge it into all matched nodes.

**AC-05.7** The overlay shall be re-applied on every spec refresh cycle using the freshly fetched overlay bytes.

-----

### AC-06: Automatic Tool Generation — Naming

**AC-06.1** The proxy shall generate one MCP tool for each HTTP operation in the post-overlay spec.

**AC-06.2** If an operation has `x-mcp-enabled: false`, the proxy shall skip it.

**AC-06.3** If an operation has `x-mcp-tool-name`, the proxy shall use that value as the base name without slugification.

**AC-06.4** If an operation has no `x-mcp-tool-name`, the proxy shall derive the base name from HTTP method and path using the configured slug rules, with method-based verb prefixes (`list_`, `get_`, `create_`, `update_`, `delete_`, `patch_`).

**AC-06.5** If an operation has an `operationId` and no `x-mcp-tool-name`, the proxy shall use the `operationId` as the base for slugification.

**AC-06.6** The proxy shall prefix every generated tool name with `{upstream.tool_prefix}{naming.separator}`.

**AC-06.7** The proxy shall truncate the un-prefixed base name so the total prefixed length does not exceed `naming.max_length`.

**AC-06.8** The proxy shall truncate operation descriptions to `naming.description_max_length` characters, appending `naming.description_truncation_suffix`. If `description_max_length` is `0`, no truncation is applied.

-----

### AC-07: Automatic Tool Generation — Conflict Detection

**AC-07.1** After generating all prefixed names across all upstreams, the proxy shall detect any duplicate prefixed names.

**AC-07.2** If `naming.conflict_resolution` is `error` and a duplicate exists, the proxy shall exit with a non-zero exit code and log both conflicting operation paths.

**AC-07.3** If `naming.conflict_resolution` is `first_wins` and a duplicate exists, the proxy shall keep the first and skip the second, logging a WARN.

**AC-07.4** If `naming.conflict_resolution` is `skip` and a duplicate exists, the proxy shall skip both and log a WARN.

**AC-07.5** If two upstreams share the same `tool_prefix`, the proxy shall treat this as a fatal configuration error.

-----

### AC-08: Automatic Tool Generation — Request jq

**AC-08.1** For each auto-generated tool, the proxy shall synthesise a jq request expression from the operation’s OpenAPI parameter metadata, mapping path params to the `path` envelope key, query params to `query`, request body properties to `body`, and header params to `headers`.

**AC-08.2** The proxy shall coerce integer and number query parameter values to strings in the generated jq expression using `tostring`.

**AC-08.3** Optional query parameters shall be wrapped in a null-check in the generated jq expression so absent MCP args produce `null` entries rather than jq errors.

**AC-08.4** The proxy shall log the generated jq expression at DEBUG level for each tool, including the tool name and upstream name.

**AC-08.5** If `x-mcp-request-transform` is present on an operation (via overlay), the proxy shall use that expression instead of the generated one.

-----

### AC-09: Automatic Tool Generation — InputSchema

**AC-09.1** The proxy shall derive the MCP `InputSchema` from path parameters (required), query parameters (required if `parameter.Required`), request body `application/json` schema (merged as top-level properties), and header parameters (optional).

**AC-09.2** The proxy shall preserve type, format, minimum, maximum, minLength, maxLength, enum, pattern, and description from each OpenAPI parameter schema in the generated InputSchema.

-----

### AC-10: Multi-Upstream Routing

**AC-10.1** The proxy shall maintain a tool registry mapping each prefixed tool name to its originating upstream.

**AC-10.2** When a `tools/call` request arrives, the proxy shall split the tool name on the first occurrence of `naming.separator` to extract the upstream prefix.

**AC-10.3** If the prefix does not match any registered upstream, the proxy shall return `CallToolResult{IsError: true}` with a descriptive error.

**AC-10.4** When dispatching to an upstream, the proxy shall use the original (un-prefixed) tool name when constructing the upstream HTTP request path.

**AC-10.5** A `tools/list` response shall include only the tools belonging to the group associated with the MCP endpoint being called.

**AC-10.6** If an upstream is `enabled: false`, the proxy shall not generate tools for it and shall not include them in any `tools/list` response.

-----

### AC-11: Tool Groups

**AC-11.1** The proxy shall expose a separate MCP endpoint for each configured group, serving only the tools in that group’s upstream list.

**AC-11.2** If a group specifies a `filter` JSONPath expression, the proxy shall include only tools whose corresponding post-overlay operation node satisfies the expression at tool generation time.

**AC-11.3** The proxy shall use the same RFC 9535 JSONPath library for group filter evaluation as for overlay target expressions.

**AC-11.4** If a tool’s upstream is not in a group’s upstream list, that tool shall not appear in that group’s `tools/list` and shall return a routing error if called via that group’s endpoint.

-----

### AC-12: Per-Operation Auth Bypass

**AC-12.1** If an operation has `x-mcp-auth-required: false`, the proxy shall skip inbound token validation for tool calls to that operation.

**AC-12.2** The proxy shall default `x-mcp-auth-required` to `true` for all operations.

**AC-12.3** The proxy shall log all operations with `x-mcp-auth-required: false` at INFO level during startup, including the tool name and upstream name.

**AC-12.4** The `/.well-known/oauth-protected-resource` endpoint shall always be public regardless of `x-mcp-auth-required` settings.

-----

### AC-13: Inbound Authentication — JWT

**AC-13.1** When `inbound_auth.strategy` is `jwt`, the proxy shall extract the Bearer token from the `Authorization` header of every inbound MCP HTTP request where `AuthRequired` is `true`.

**AC-13.2** The proxy shall verify the JWT signature using JWKS from the issuer discovery endpoint or the explicit `jwks_url`.

**AC-13.3** If the JWT audience does not include the configured `audience`, the proxy shall reject with HTTP 401.

**AC-13.4** If the JWT is expired, the proxy shall reject with HTTP 401 and a `WWW-Authenticate` header per RFC 9728 §5.1.

**AC-13.5** The proxy shall automatically refresh the JWKS when a token is signed by a key ID not present in the current cached set.

-----

### AC-14: Inbound Authentication — Introspection

**AC-14.1** When `inbound_auth.strategy` is `introspection`, the proxy shall call the authorization server’s introspection endpoint for each token where `AuthRequired` is `true`.

**AC-14.2** If introspection returns `active: false`, the proxy shall reject with HTTP 401.

**AC-14.3** If the introspected token’s audience does not include the configured `audience`, the proxy shall reject with HTTP 401.

-----

### AC-15: Inbound Authentication — API Key

**AC-15.1** When `inbound_auth.strategy` is `apikey`, the proxy shall extract the configured header value from every inbound request where `AuthRequired` is `true`.

**AC-15.2** The proxy shall compare the value against the comma-separated key list from the environment variable named by `apikey.keys_env`.

**AC-15.3** If the header is absent or the value does not match, the proxy shall return HTTP 401.

-----

### AC-16: Inbound Authentication — Lua

**AC-16.1** When `inbound_auth.strategy` is `lua`, the proxy shall call `check_auth(token)` for each request where `AuthRequired` is `true`.

**AC-16.2** The proxy shall run Lua scripts in a sandboxed VM with `io`, `os`, `package`, and `debug` stdlib disabled.

**AC-16.3** The proxy shall enforce `lua.timeout` using `context.WithTimeout`.

**AC-16.4** If the Lua function returns `false` as the first return value, the proxy shall return HTTP 401 with the fourth return value as the error message.

**AC-16.5** The proxy shall pre-compile Lua scripts to bytecode at config load time and reuse bytecode across pooled VMs.

-----

### AC-17: Well-Known OAuth Protected Resource

**AC-17.1** The proxy shall serve `GET /.well-known/oauth-protected-resource` with `resource`, `authorization_servers`, `scopes_supported`, and `bearer_methods_supported` fields.

**AC-17.2** When a request is rejected with HTTP 401, the proxy shall include `WWW-Authenticate` with a `resource_metadata` parameter per RFC 9728 §5.1.

-----

### AC-18: Outbound Authentication

**AC-18.1** The proxy shall use each upstream’s `outbound_auth` configuration to obtain credentials for all HTTP requests to that upstream.

**AC-18.2** The proxy shall never forward the inbound MCP client’s Bearer token to any upstream API.

**AC-18.3** If `outbound_auth.strategy` is `oauth2_client_credentials`, the proxy shall obtain and cache a token using the client credentials flow and refresh it before expiry.

**AC-18.4** If `outbound_auth.strategy` is `bearer`, the proxy shall inject `Authorization: Bearer {token}` using the value from the environment variable named by `bearer.token_env`.

**AC-18.5** If `outbound_auth.strategy` is `api_key`, the proxy shall inject the key into the configured header with the configured prefix.

**AC-18.6** If `outbound_auth.strategy` is `lua`, the proxy shall call `get_upstream_token(upstream, cached_token, cached_expiry)` and cache the result until the returned `expiry_unix`. If `expiry_unix` is `0`, the script shall be called on every request without caching. If `raw_headers` is non-empty, those headers shall be injected and take precedence over the `token` value for `Authorization`.

**AC-18.7** If `outbound_auth.strategy` is `none`, the proxy shall make upstream requests with no added authentication headers.

-----

### AC-19: jq Transformation — Config-Time Validation

**AC-19.1** The proxy shall compile every jq expression (request, response, error) using `gojq.Parse` + `gojq.Compile` at config load time; any compile error is a fatal startup error.

**AC-19.2** The proxy shall dry-run each compiled expression against three synthetic inputs and treat runtime errors on all inputs as a fatal startup error.

**AC-19.3** If the response transform output does not satisfy the target schema on any synthetic input, the proxy shall treat it as a fatal startup error.

**AC-19.4** Dry-run execution per upstream shall respect `server.startup_validation_timeout`. On timeout during hot-reload, the reload shall be abandoned and the previous config kept live.

**AC-19.5** Tools with no explicit transform expressions shall use the auto-generated request jq and the identity (`.`) response jq; only the auto-generated request jq is dry-run validated.

-----

### AC-20: jq Transformation — Runtime

**AC-20.1** When a tool call is received, the proxy shall execute the compiled request jq on the MCP tool arguments and use the resulting envelope to construct the upstream HTTP request.

**AC-20.2** The proxy shall apply the compiled response jq to success-status responses and return the result as MCP tool result content.

**AC-20.3** The proxy shall apply the compiled error jq to error-status responses and return `CallToolResult{IsError: true}`.

**AC-20.4** If any jq expression produces a runtime error, the proxy shall return `CallToolResult{IsError: true}` with the jq error message.

**AC-20.5** The proxy shall execute all jq expressions with the request’s `context.Context`, cancelling execution when the request deadline is reached.

-----

### AC-21: Non-JSON Response Handling

**AC-21.1** The proxy shall map the upstream response to an MCP content type based on `x-mcp-response-format`: `json`→`TextContent`, `text`→`TextContent`, `image`→`ImageContent`, `audio`→`AudioContent`, `binary`→`ResourceContent`.

**AC-21.2** If `x-mcp-response-format` is `auto`, the proxy shall inspect the upstream `Content-Type` header and apply the auto-detection rules to select the MCP content type.

**AC-21.3** The proxy shall skip the response jq transform for `image`, `audio`, and `binary` response formats.

**AC-21.4** For `image`, `audio`, and `binary` formats, the proxy shall base64-encode the raw response body and set `mimeType` from the upstream `Content-Type` header.

-----

### AC-22: Error Response Handling

**AC-22.1** The proxy shall route upstream responses to the success or error path based on `validation.success_status` and `validation.error_status` lists.

**AC-22.2** If the upstream response status is in neither list, the proxy shall return `CallToolResult{IsError: true}` with the raw body excerpt without running any transform.

**AC-22.3** If the error-path response body has `Content-Type: application/problem+json`, the proxy shall parse it as RFC 9457 `ProblemDetail` before passing it to the error transform.

**AC-22.4** The proxy shall apply the compiled error jq to error-path responses and return `CallToolResult{IsError: true}` with the transformed result.

**AC-22.5** The default error transform shall extract `title`, `detail`, and `status` from problem+json responses; for other formats it shall return a generic `{error, body}` structure.

**AC-22.6** The error jq expression shall be compiled and dry-run at config load time using the operation’s non-2xx response schemas as synthetic input.

-----

### AC-23: Synthetic Data Generation

**AC-23.1** The proxy shall generate synthetic JSON from OpenAPI schemas by checking in order: `example`, `x-example`, `default`, `enum[0]`, first `oneOf`/`anyOf` branch, type-based fallback.

**AC-23.2** For `string` schemas with a `pattern`, the proxy shall use `reggen.Generate` to produce a conforming value.

**AC-23.3** For `string` schemas with format `email`, `uuid`, `date-time`, or `uri`, the proxy shall generate a format-valid value.

**AC-23.4** For `object` schemas, the proxy shall recurse all properties and populate all required fields.

**AC-23.5** For `array` schemas, the proxy shall generate at least `minItems` elements (default one).

**AC-23.6** When a schema contains a circular `$ref`, the proxy shall detect it, break the cycle with a sentinel, log a WARN, and continue.

**AC-23.7** The proxy shall generate three synthetic instances per schema: all-properties, required-only, and one per `oneOf`/`anyOf` variant.

-----

### AC-24: Runtime Request Validation

**AC-24.1** When `validation.validate_request` is `true`, the proxy shall validate the upstream HTTP request using `openapi3filter.ValidateRequest()` before sending it.

**AC-24.2** If request validation fails, the proxy shall return `CallToolResult{IsError: true}` without calling the upstream.

**AC-24.3** The proxy shall use `NoopAuthenticationFunc` in openapi3filter options to avoid re-validating upstream auth schemes.

-----

### AC-25: Runtime Response Validation

**AC-25.1** When `validation.validate_response` is `true`, the proxy shall validate success-path responses using `openapi3filter.ValidateResponse()`.

**AC-25.2** If validation fails and `response_validation_failure` is `fail`, the proxy shall return `CallToolResult{IsError: true}`.

**AC-25.3** If validation fails and `response_validation_failure` is `warn`, the proxy shall log a structured WARN and continue to the transform stage.

-----

### AC-26: Config Hot-Reload

**AC-26.1** While running, the proxy shall watch the parent directory of `config.yaml` using fsnotify.

**AC-26.2** When a `CREATE` event is detected on the config directory, the proxy shall debounce 500ms before attempting a reload.

**AC-26.3** When a reload succeeds, the proxy shall atomically swap the active config and the MCP SDK shall emit `notifications/tools/list_changed` to all connected clients.

**AC-26.4** When a reload succeeds, the proxy shall log an INFO message listing added, removed, and modified tools by prefixed name.

**AC-26.5** When a reload fails at any stage, the proxy shall retain the previous configuration unchanged and log a structured ERROR with the upstream name and failure reason.

**AC-26.6** When a reload fails, the proxy shall increment `mcp_anything_config_reload_errors_total`.

**AC-26.7** While a reload is in progress, the proxy shall continue serving requests using the previous configuration.

-----

### AC-27: Transports

**AC-27.1** The proxy shall expose a Streamable HTTP transport (POST + GET on the same path) for each group endpoint listed in `server.transport: [streamable-http]`.

**AC-27.2** The proxy shall expose an SSE legacy transport (GET on `{endpoint}/sse`) for each group endpoint when `server.transport` includes `sse`.

**AC-27.3** Both transports shall serve the same tool registry for a given group endpoint.

-----

### AC-28: Observability — Traces

**AC-28.1** The proxy shall create an OTel span for every inbound MCP HTTP request named by HTTP route pattern.

**AC-28.2** The proxy shall create a child span for every upstream HTTP call with `server.address`, `http.request.method`, and `http.response.status_code` attributes.

**AC-28.3** For every `tools/call`, the proxy shall add `mcp.tool.name`, `mcp.method`, and `mcp.session.id` to the active span.

**AC-28.4** The proxy shall propagate W3C Trace Context from `traceparent` headers and MCP `params._meta.traceparent`.

**AC-28.5** The proxy shall export traces to the configured OTLP gRPC endpoint.

-----

### AC-29: Observability — Metrics

**AC-29.1** The proxy shall emit `http.server.request.duration` for every inbound HTTP request.

**AC-29.2** The proxy shall emit `http.client.request.duration` for every upstream HTTP call.

**AC-29.3** The proxy shall emit `mcp.tool.call.duration` for every `tools/call`, attributed by `mcp.tool.name`.

**AC-29.4** The proxy shall increment `mcp.tool.call.errors.total` on every `IsError: true` result, attributed by `mcp.tool.name` and `error.type`.

**AC-29.5** The proxy shall emit `mcp_anything.config_reload.total` for every reload attempt with `status=success` or `status=failure`.

**AC-29.6** The proxy shall emit `mcp_anything.spec_refresh.total` for every background spec fetch with `upstream` and `status` attributes.

-----

### AC-30: Health Endpoints

**AC-30.1** The proxy shall serve `GET /healthz` returning HTTP 200 while the process is alive.

**AC-30.2** The proxy shall serve `GET /readyz` returning HTTP 200 only when all upstreams have loaded, compiled, and dry-run passed.

**AC-30.3** While startup validation is in progress, `GET /readyz` shall return HTTP 503.

**AC-30.4** If a reload fails, `GET /readyz` shall return HTTP 200 (previous config is still serving).

**AC-30.5** If an upstream exceeds `max_refresh_failures`, `GET /readyz` shall return HTTP 503 with the affected upstream name in the response body.

-----

### AC-31: Kubernetes — ConfigMap Loading

**AC-31.1** The proxy shall support ConfigMap volume directory mounts for spec and overlay files.

**AC-31.2** The proxy shall watch the parent directory of mounted files (not individual files) to detect Kubernetes atomic symlink swaps.

**AC-31.3** The proxy shall not require `subPath` mounts.

-----

### AC-32: Error Response Format

**AC-32.1** The proxy shall recover from all Go panics in request handlers, log the stack trace at ERROR, and return `CallToolResult{IsError: true}` with a generic message.

**AC-32.2** All tool-level errors shall use `CallToolResult{IsError: true}` rather than JSON-RPC error objects.

**AC-32.3** Error messages returned to MCP clients shall not include stack traces, file paths, or environment variable names.