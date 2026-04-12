// Package all imports all built-in cache store sub-packages,
// registering them via their init() functions.
// Import this package with a blank import to make all providers available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/cache/all"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/cache/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/cache/redis"
)
