// Package all imports all built-in inbound auth strategy sub-packages,
// registering them via their init() functions.
// Import this package with a blank import to make all strategies available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/all"
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/ext_authz"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/introspection"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/js"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/jwt"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/lua"
)
