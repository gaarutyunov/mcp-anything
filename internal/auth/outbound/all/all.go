// Package all imports all outbound auth provider sub-packages for side effects,
// registering all built-in strategies (bearer, api_key, oauth2, lua, js, none).
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/apikeyprovider"
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/bearerprovider"
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/jsprovider"
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/luaprovider"
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/noneprovider"
	_ "github.com/gaarutyunov/mcp-anything/internal/auth/outbound/oauth2provider"
)
