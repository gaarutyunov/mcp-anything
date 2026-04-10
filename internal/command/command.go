// Package command re-exports from pkg/upstream/command. See pkg/upstream/command for documentation.
package command

import pkgcmd "github.com/gaarutyunov/mcp-anything/pkg/upstream/command"

// DefaultMaxOutputBytes is the maximum bytes captured from stdout or stderr when
// MaxOutput is not configured.
// See pkg/upstream/command.DefaultMaxOutputBytes.
const DefaultMaxOutputBytes = pkgcmd.DefaultMaxOutputBytes

// Def holds the runtime definition for a command-backed MCP tool.
// See pkg/upstream/command.Def.
type Def = pkgcmd.Def

// Tool holds a command tool's MCP metadata and execution definition.
// See pkg/upstream/command.Tool.
type Tool = pkgcmd.Tool

// BuildTools converts a slice of CommandConfig entries into Tool descriptors.
// See pkg/upstream/command.BuildTools.
var BuildTools = pkgcmd.BuildTools

// ToTextResult converts command stdout into a success CallToolResult.
// See pkg/upstream/command.ToTextResult.
var ToTextResult = pkgcmd.ToTextResult

// ToErrorResult converts a command failure into an error CallToolResult.
// See pkg/upstream/command.ToErrorResult.
var ToErrorResult = pkgcmd.ToErrorResult
