// Package script re-exports from pkg/upstream/script. See pkg/upstream/script for documentation.
package script

import (
	pkgscript "github.com/gaarutyunov/mcp-anything/pkg/upstream/script"
)

// Def holds the runtime definition for a JavaScript-backed MCP tool.
// See pkg/upstream/script.Def.
type Def = pkgscript.Def

// Tool holds a script tool's MCP metadata and execution definition.
// See pkg/upstream/script.Tool.
type Tool = pkgscript.Tool

// CompileScript pre-processes and compiles a JavaScript script source into a sobek.Program.
// See pkg/upstream/script.CompileScript.
var CompileScript = pkgscript.CompileScript

// BuildTools converts a slice of ScriptConfig entries into Tool descriptors.
// See pkg/upstream/script.BuildTools.
var BuildTools = pkgscript.BuildTools

// ToTextResult converts script output bytes into a success CallToolResult.
// See pkg/upstream/script.ToTextResult.
var ToTextResult = pkgscript.ToTextResult

// ToErrorResult converts a script failure into an error CallToolResult.
// See pkg/upstream/script.ToErrorResult.
var ToErrorResult = pkgscript.ToErrorResult
