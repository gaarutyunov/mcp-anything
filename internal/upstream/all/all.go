// Package all imports all upstream builder sub-packages for side effects,
// registering all built-in upstream types (http, command, script).
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/internal/upstream/commandbuilder"
	_ "github.com/gaarutyunov/mcp-anything/internal/upstream/http"
	_ "github.com/gaarutyunov/mcp-anything/internal/upstream/scriptbuilder"
)
