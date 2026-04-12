// Package all imports all built-in rate limit store sub-packages so that each
// registers itself with the global store registry via its init() function.
//
// Import this package with a blank identifier to enable all rate limit stores:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/all"
//
// SDK users who only need a subset can import individual sub-packages instead.
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/redis"
)
