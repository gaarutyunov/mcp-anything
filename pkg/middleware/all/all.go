// Package all imports all built-in middleware strategy sub-packages,
// registering them with the unified middleware registry via their init() functions.
// Import this package with a blank import to make all strategies available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/middleware/all"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/all"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/all"
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/all"
)
