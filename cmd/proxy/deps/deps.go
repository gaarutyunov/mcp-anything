// Package deps registers all built-in components for the mcp-anything proxy binary.
// It imports every sub-package that self-registers via init(), so that the proxy
// supports all cache backends, upstream types, auth strategies, rate-limit stores,
// embedding providers, and session stores out of the box.
//
// SDK users who embed only specific components should import individual sub-packages
// instead of this package.
package deps

import (
	// Cache backends.
	_ "github.com/gaarutyunov/mcp-anything/pkg/cache/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/cache/redis"

	// Upstream builders.
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/command"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http/withui"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/script"

	// Inbound auth strategies.
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/introspection"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/jwt"

	// Outbound auth strategies.
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/apikey"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/bearer"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/none"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/oauth2usersession"

	// Scripting runtimes (register both inbound and outbound JS/Lua strategies).
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/js"
	_ "github.com/gaarutyunov/mcp-anything/pkg/runtime/lua"

	// Rate-limit stores.
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/redis"

	// Embedding providers.
	_ "github.com/gaarutyunov/mcp-anything/pkg/embedding/hugot"

	// Session store backends.
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/memory"
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/postgres"
	_ "github.com/gaarutyunov/mcp-anything/pkg/session/redis"
)
