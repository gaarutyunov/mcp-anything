// Package all imports every built-in scripting runtime sub-package so that their
// init() functions register both the runtime pools and the middleware strategies.
//
// The proxy binary imports this package (via cmd/proxy/deps) to get all runtimes.
// SDK consumers who want only specific runtimes should import individual sub-packages:
//
//	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/js"
//	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/lua"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/js"
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/lua"
)
