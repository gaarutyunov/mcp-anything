# mcp-anything

A stateless Kubernetes-native proxy that converts any HTTP REST API into an MCP (Model Context Protocol) server. Define your upstream API via an OpenAPI 3.0 spec, optionally apply an OpenAPI Overlay to filter and rename operations, and the proxy automatically generates MCP tools — no code required.

## Status
Active development. No stability guarantees.

## Design
See [SPEC.md](SPEC.md) for the full architecture and design decisions.

## Quick start
(To be filled in when the MVP proxy is working — see TASK-03)

## Development
Requirements: Go 1.25+, Docker (for integration tests)

    make check        # lint + vet + unit tests + build
    make integration  # integration tests (builds from Dockerfile, or set PROXY_IMAGE)

Set `TC_CLOUD_TOKEN` to run containers via Testcontainers Cloud.
