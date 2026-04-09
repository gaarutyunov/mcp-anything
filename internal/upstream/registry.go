// Package upstream re-exports core types from pkg/upstream. See pkg/upstream for documentation.
package upstream

import (
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

// Type aliases — transparent to all callers; no behavior change.
// See pkg/upstream for full documentation of each type.

// Upstream holds the per-upstream HTTP routing state.
// See pkg/upstream.Upstream.
type Upstream = pkgupstream.Upstream

// RegistryEntry associates a prefixed tool name with its upstream and runtime state.
// See pkg/upstream.RegistryEntry.
type RegistryEntry = pkgupstream.RegistryEntry

// ValidatedUpstream is the result of validating a single upstream configuration.
// See pkg/upstream.ValidatedUpstream.
type ValidatedUpstream = pkgupstream.ValidatedUpstream

// Registry maps prefixed tool names to their upstream and compiled tool state.
// See pkg/upstream.Registry.
type Registry = pkgupstream.Registry

// New builds a Registry from all validated upstreams and group configurations.
// See pkg/upstream.New.
var New = pkgupstream.New

// NewFromEntries builds a Registry from pre-compiled RegistryEntry objects.
// See pkg/upstream.NewFromEntries.
var NewFromEntries = pkgupstream.NewFromEntries
