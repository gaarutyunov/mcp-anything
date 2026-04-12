// Package all imports all built-in session store sub-packages,
// registering them via their init() functions.
// Import this package with a blank import to make all session store providers available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/session/all"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/postgres"
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/redis"
)
