package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/script"
)

// ScriptBuilder implements Builder for type: script upstreams.
// It compiles JavaScript tool definitions at startup and constructs RegistryEntry objects.
type ScriptBuilder struct{}

// Build compiles all script tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
// Parse errors in any script are fatal at startup.
func (b *ScriptBuilder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error) {
	// Build an HTTP client for ctx.fetch() — use the upstream timeout or a default.
	fetchTimeout := cfg.Timeout
	if fetchTimeout <= 0 {
		fetchTimeout = 30 * time.Second
	}
	httpClient := &http.Client{
		Timeout: fetchTimeout,
	}

	scriptTools, err := script.BuildTools(cfg.Scripts, cfg, naming, httpClient)
	if err != nil {
		return nil, fmt.Errorf("upstream %q script validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated script tools", "upstream", cfg.Name, "count", len(scriptTools))

	up := &Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*RegistryEntry, 0, len(scriptTools))
	for _, st := range scriptTools {
		entries = append(entries, &RegistryEntry{
			PrefixedName: st.PrefixedName,
			OriginalName: st.OriginalName,
			Upstream:     up,
			MCPTool:      st.MCPTool,
			Transforms:   st.Transforms,
			AuthRequired: true,
			ScriptDef:    st.Def,
		})
	}

	return &ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}
