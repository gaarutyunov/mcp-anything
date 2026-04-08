// Package all imports all built-in outbound auth strategy sub-packages,
// registering them via their init() functions.
// Import this package with a blank import to make all strategies available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/all"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/bearer"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/js"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/lua"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/none"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2"
)
