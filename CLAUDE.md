# CLAUDE.md — AI Agent Instructions for mcp-anything

## Project
mcp-anything is a stateless Go proxy that converts HTTP REST APIs into MCP (Model Context Protocol) tools.
Full design: SPEC.md

## Module
github.com/gaarutyunov/mcp-anything

## Commands

### Build
make build        # go build ./cmd/proxy

### Lint (must pass before every commit)
make lint         # golangci-lint run ./...

### Vet (must pass before every commit)
make vet          # go vet ./...

### Unit tests (must pass before every commit)
make test         # go test -race -count=1 ./...

### Integration tests (require Docker)
make integration  # go test -tags integration ... ./tests/integration/...

Without `PROXY_IMAGE`, tests build the proxy from source using the Dockerfile. Set `PROXY_IMAGE` to test against a pre-built image (used in CI).

### All checks (run before every commit)
make check        # runs lint + vet + test + build in sequence

## Pre-commit checklist
1. Run `make check` and fix all failures before committing.
2. Run `make integration` and fix all failures before committing.

Never commit with failing lint, vet, unit tests, or integration tests.

## Integration tests
Integration tests live in `tests/integration/` with build tag `//go:build integration`. They run the proxy as a Docker container alongside test fixtures (WireMock, etc.) using Testcontainers. They do NOT import internal packages — they test the built image end-to-end via HTTP and MCP protocol.

**You MUST run `make integration` after implementing any feature or fix and ensure all integration tests pass.** If an integration test fails, diagnose and fix the issue before committing. Do not skip or ignore integration test failures.

## Code conventions
- All errors must be wrapped with context: `fmt.Errorf("loading spec: %w", err)`
- No `panic()` in library code; only in `main()` for unrecoverable startup failures
- Structured logging via `log/slog` with `slog.Default()`; keys are snake_case
- No global mutable state; pass dependencies explicitly
- Interfaces are defined in the package that uses them, not the package that implements them
- Use `context.Context` as the first argument in all functions that do I/O or call other services
- All config fields that reference secrets use `${ENV_VAR}` syntax; never log expanded values
- Unit tests live in files named `*_test.go` with no build tag

## Additional rules
See `.claude/rules/` for scoped rules on integration tests, OpenAPI package patterns, and common review issues.

## Testcontainers
Set `TC_CLOUD_TOKEN` environment variable to use Testcontainers Cloud. Without it, tests use the local Docker daemon. The library auto-detects.

## Active development
This project has no public users. There is no backward compatibility requirement. Interfaces, config schemas, and APIs may change freely between tasks.

## Git workflow
Never commit or push directly to `main`. Always create a feature branch (e.g., `claude/issue-<N>-<description>`) and open a pull request via `gh pr create`.

## No stubs
Implementation tasks must produce complete, working code. Do not write placeholder functions that return `nil, nil` or `errors.New("not implemented")`. If a feature is not yet needed, do not create the function at all.

