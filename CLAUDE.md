# CLAUDE.md — AI Agent Instructions for mcp-anything

## Project
mcp-anything is a stateless Go proxy that converts HTTP REST APIs into MCP (Model Context Protocol) tools.
Full design: SPEC.md

## Module
github.com/your-org/mcp-anything

## Commands

### Build
make build        # go build ./cmd/proxy

### Lint (must pass before every commit)
make lint         # golangci-lint run ./...

### Vet (must pass before every commit)
make vet          # go vet ./...

### Unit tests (must pass before every commit)
make test         # go test -race -count=1 ./...

### Integration tests (require Docker + TC_CLOUD_TOKEN env var)
make integration  # go test -tags integration -race -count=1 -timeout 300s ./...

### All checks (run before every commit)
make check        # runs lint + vet + test + build in sequence

## Pre-commit checklist
Run `make check` and fix all failures before committing. Never commit with failing lint, vet, or unit tests. Integration tests are run in CI but should also pass locally if Docker is available.

## Code conventions
- All errors must be wrapped with context: `fmt.Errorf("loading spec: %w", err)`
- No `panic()` in library code; only in `main()` for unrecoverable startup failures
- Structured logging via `log/slog` with `slog.Default()`; keys are snake_case
- No global mutable state; pass dependencies explicitly
- Interfaces are defined in the package that uses them, not the package that implements them
- Use `context.Context` as the first argument in all functions that do I/O or call other services
- All config fields that reference secrets use `${ENV_VAR}` syntax; never log expanded values
- Integration tests live in files named `*_integration_test.go` with build tag `//go:build integration`
- Unit tests live in files named `*_test.go` with no build tag

## Testcontainers
Set `TC_CLOUD_TOKEN` environment variable to use Testcontainers Cloud. Without it, tests use the local Docker daemon. The library auto-detects.

## Active development
This project has no public users. There is no backward compatibility requirement. Interfaces, config schemas, and APIs may change freely between tasks.

## No stubs
Implementation tasks must produce complete, working code. Do not write placeholder functions that return `nil, nil` or `errors.New("not implemented")`. If a feature is not yet needed, do not create the function at all.
