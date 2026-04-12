package script

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

func init() {
	pkgupstream.RegisterBuilder("script", &Builder{})
}

// Register registers the script Builder with the pkg/upstream global builder registry.
// This is called automatically via init() when the package is imported.
// Call this explicitly only if you need to re-register (e.g. in tests).
func Register() {
	pkgupstream.RegisterBuilder("script", &Builder{})
}

// Builder implements upstream.Builder for type: script upstreams.
// It compiles JavaScript tool definitions at startup and constructs RegistryEntry objects.
type Builder struct{}

// Build compiles all script tool definitions and returns a ValidatedUpstream
// with RegistryEntry objects ready for registration.
// Parse errors in any script are fatal at startup.
// cfg.JSScriptPool must be set before calling Build; it bounds concurrent JS runtimes.
func (b *Builder) Build(_ context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*pkgupstream.ValidatedUpstream, error) {
	if cfg.JSScriptPool == nil {
		return nil, fmt.Errorf("upstream %q: JSScriptPool must be set before building a script upstream", cfg.Name)
	}

	// Build an HTTP client for ctx.fetch() — use the upstream timeout or a default.
	fetchTimeout := cfg.Timeout
	if fetchTimeout <= 0 {
		fetchTimeout = 30 * time.Second
	}
	httpClient := &http.Client{
		Timeout: fetchTimeout,
	}

	scriptTools, err := BuildTools(cfg.Scripts, cfg, naming, httpClient, cfg.JSScriptPool)
	if err != nil {
		return nil, fmt.Errorf("upstream %q script validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated script tools", "upstream", cfg.Name, "count", len(scriptTools))

	up := &pkgupstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
	}

	entries := make([]*pkgupstream.RegistryEntry, 0, len(scriptTools))
	for _, st := range scriptTools {
		entries = append(entries, &pkgupstream.RegistryEntry{
			PrefixedName: st.PrefixedName,
			OriginalName: st.OriginalName,
			Upstream:     up,
			MCPTool:      st.MCPTool,
			Transforms:   st.Transforms,
			AuthRequired: true,
			RateLimit:    cfg.RateLimit,
			Executor:     &Executor{toolName: st.PrefixedName, scriptDef: st.Def},
		})
	}

	return &pkgupstream.ValidatedUpstream{
		Config:  cfg,
		Entries: entries,
	}, nil
}
