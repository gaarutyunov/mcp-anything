# Registry pattern and tree-shaking

mcp-anything is used both as a standalone binary and as a Go SDK. Every pluggable component (auth strategies, upstream builders, embedding providers, etc.) must follow the registry pattern so that unused components are excluded from the binary by the Go linker.

## The pattern

### 1. Define the registry in the parent package

```go
// pkg/auth/inbound/inbound.go
type ValidatorFactory func(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error)

var registry = map[string]ValidatorFactory{}
var mu sync.RWMutex

func Register(strategy string, f ValidatorFactory) {
    mu.Lock()
    defer mu.Unlock()
    registry[strategy] = f
}

func New(ctx context.Context, cfg *config.InboundAuthConfig) (TokenValidator, string, error) {
    mu.RLock()
    f, ok := registry[cfg.Strategy]
    mu.RUnlock()
    if !ok {
        return nil, "", fmt.Errorf("unknown strategy %q — import the strategy package or pkg/auth/inbound/all", cfg.Strategy)
    }
    return f(ctx, cfg)
}
```

### 2. Each strategy sub-package registers itself via init()

```go
// pkg/auth/inbound/jwt/jwt.go
package jwt

func init() {
    inbound.Register("jwt", func(ctx context.Context, cfg *config.InboundAuthConfig) (inbound.TokenValidator, string, error) {
        return NewValidator(ctx, cfg.JWT)
    })
}
```

The `init()` function runs exactly once, when the package is imported. If the package is never imported, neither the `init()` nor any of the package's code is linked into the binary.

### 3. An "all" sub-package bundles all strategies via blank imports

```go
// pkg/auth/inbound/all/all.go
package all

import (
    _ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/apikey"
    _ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/jwt"
    _ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/lua"
    // ...
)
```

`cmd/proxy/main.go` imports `all` to get every strategy. SDK users import only the sub-packages they need.

### 4. Tree-shaking tests verify unused packages are excluded

`tests/treeshake/` contains build-tag-isolated programs that import only specific sub-packages and assert that heavyweight transitive dependencies (e.g., Sobek, gopher-lua, kin-openapi) are absent from the resulting `go.sum` graph.

**Every new registry entry must add a treeshake test** that verifies importing only that sub-package does not pull in unrelated dependencies.

## Rules

- Every pluggable component lives in its own sub-package under its registry parent.
- Sub-packages register via `init()` only — never require the caller to call a `Register()` function manually.
- The error message from `New()` / `Build()` must name the missing package so the operator knows what to import.
- The `all/` bundle package is updated when a new sub-package is added.
- New sub-packages must not import anything from other strategy sub-packages.
- Avoid `init()` side-effects beyond calling `Register()` — no goroutines, no file I/O, no network calls in `init()`.
