package command

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

func init() {
	pkgupstream.RegisterBuilder("command", &Builder{})
}

// Register registers the command Builder with the pkg/upstream global builder registry.
// This is called automatically via init() when the package is imported.
// Call this explicitly only if you need to re-register (e.g. in tests).
func Register() {
	pkgupstream.RegisterBuilder("command", &Builder{})
}

// Builder implements upstream.Builder for type: command upstreams.
// It compiles command tool definitions and constructs RegistryEntry objects.
type Builder struct{}

// Build compiles command tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
func (b *Builder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*pkgupstream.ValidatedUpstream, error) {
	cmdTools, err := BuildTools(cfg.Commands, cfg, naming)
	if err != nil {
		return nil, fmt.Errorf("upstream %q command validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated command tools", "upstream", cfg.Name, "count", len(cmdTools))

	up := &pkgupstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*pkgupstream.RegistryEntry, 0, len(cmdTools))
	for _, ct := range cmdTools {
		entries = append(entries, &pkgupstream.RegistryEntry{
			PrefixedName: ct.PrefixedName,
			OriginalName: ct.OriginalName,
			Upstream:     up,
			MCPTool:      ct.MCPTool,
			Transforms:   ct.Transforms,
			AuthRequired: true,
			RateLimit:    cfg.RateLimit,
			Executor:     &Executor{toolName: ct.PrefixedName, commandDef: ct.Def},
		})
	}

	return &pkgupstream.ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}
