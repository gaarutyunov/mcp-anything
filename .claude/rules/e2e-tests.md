# E2E test rules

E2E tests live in `tests/e2e/` with build tag `//go:build e2e`. They use a shared k3s cluster (created once in `TestMain`) and test full-stack behaviour — real containers, real Kubernetes, real network traffic.

## CI update required

**Every time you add a new E2E test function, you MUST update `.github/workflows/ci.yml`** to add the test to the `e2e-tests` job matrix. Without this the test never runs in CI.

The matrix entry lives in the `e2e-tests` job under `strategy.matrix.include`:

```yaml
- name: My New Test Name
  test: TestMyNewE2EFunction
```

Because CI workflow files cannot be modified by Claude Code automatically, tell the user what entry to add and in which job. Include the exact YAML snippet.

## Structure

- `tests/e2e/testmain_test.go` — `TestMain`, shared k3s cluster lifecycle
- `tests/e2e/k3s_test.go` — k3s helpers (deploy, wait, port-forward, …)
- `tests/e2e/helpers_test.go` — shared test utilities
- `tests/e2e/<feature>_e2e_test.go` — one file per test scenario

## Running locally

```
make e2e                    # run all E2E tests
go test -tags e2e -run TestFoo ./tests/e2e/...   # run a specific test
```

## Rules

- Each E2E test gets its own Kubernetes namespace — never share namespaces between tests.
- Use the shared k3s cluster from `TestMain`; do not spin up a separate cluster per test.
- E2E tests must NOT import internal packages — they test the built image end-to-end.
- After adding a new E2E test, run `go build -tags e2e ./tests/e2e/...` to verify it compiles.
- The `PROXY_IMAGE` and `OPERATOR_IMAGE` environment variables point to the pre-built images; fall back to building from source when they are absent (same pattern as integration tests).
