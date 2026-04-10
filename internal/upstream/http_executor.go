package upstream

import pkghttp "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"

// HTTPExecutor executes an HTTP-backed tool by running the full request pipeline.
// See pkg/upstream/http.Executor.
type HTTPExecutor = pkghttp.Executor
