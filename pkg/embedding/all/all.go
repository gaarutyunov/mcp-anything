// Package all imports all built-in embedding provider sub-packages,
// registering them via their init() functions.
// Import this package with a blank import to make all providers available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/embedding/all"
package all

import (
	// Register the hugot (in-process ONNX) provider.
	// All other providers are registered by pkg/embedding/embedding.go's init().
	_ "github.com/gaarutyunov/mcp-anything/pkg/embedding/hugot"
)
