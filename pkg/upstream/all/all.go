// Package all imports all built-in upstream builder sub-packages so that each
// registers itself with the global builder registry via its init() function.
//
// Import this package with a blank identifier to enable all upstream types:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/upstream/all"
//
// SDK users who only need a subset can import individual sub-packages instead.
package all

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/command"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http/withui"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/script"
)
