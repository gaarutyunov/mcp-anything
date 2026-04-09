package upstream

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gaarutyunov/mcp-anything/internal/command"
	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// CommandBuilder implements Builder for type: command upstreams.
// It compiles command tool definitions and constructs RegistryEntry objects.
type CommandBuilder struct{}

// Build compiles command tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
func (b *CommandBuilder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error) {
	cmdTools, err := command.BuildTools(cfg.Commands, cfg, naming)
	if err != nil {
		return nil, fmt.Errorf("upstream %q command validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated command tools", "upstream", cfg.Name, "count", len(cmdTools))

	up := &Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*RegistryEntry, 0, len(cmdTools))
	for _, ct := range cmdTools {
		entries = append(entries, &RegistryEntry{
			PrefixedName: ct.PrefixedName,
			OriginalName: ct.OriginalName,
			Upstream:     up,
			MCPTool:      ct.MCPTool,
			Transforms:   ct.Transforms,
			AuthRequired: true,
			Executor:     &CommandExecutor{toolName: ct.PrefixedName, commandDef: ct.Def},
		})
	}

	return &ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}
