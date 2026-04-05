package http

import (
	"context"
	"fmt"
	"log/slog"

	outboundauth "github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/upstream"
)

func init() {
	factory := func(pools *runtime.Registry) upstream.Builder {
		return &Builder{pools: pools}
	}
	upstream.RegisterBuilder("", factory)
	upstream.RegisterBuilder("http", factory)
}

// Builder implements upstream.Builder for type: http (or "") upstreams.
// It fetches and validates the OpenAPI spec and constructs RegistryEntry objects
// for all exported operations.
type Builder struct {
	pools *runtime.Registry
}

// Build validates the OpenAPI spec and returns a ValidatedUpstream with RegistryEntry
// objects ready for registration.
func (b *Builder) Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*upstream.ValidatedUpstream, error) {
	valCtx, valCancel := context.WithTimeout(ctx, cfg.StartupValidationTimeout)
	tools, specYAMLRoot, err := openapi.ValidateUpstream(valCtx, cfg, naming)
	valCancel()
	if err != nil {
		return nil, fmt.Errorf("upstream %q validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated tools", "upstream", cfg.Name, "count", len(tools))

	outboundCfg := cfg.OutboundAuth
	outboundCfg.Upstream = cfg.Name
	provider, err := outboundauth.NewRegistry(b.pools).New(ctx, &outboundCfg)
	if err != nil {
		return nil, fmt.Errorf("build outbound auth for upstream %q: %w", cfg.Name, err)
	}

	up := &upstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
		BaseURL:    cfg.BaseURL,
	}

	client := NewHTTPClient(cfg, provider)

	entries := make([]*upstream.RegistryEntry, 0, len(tools))
	for _, vt := range tools {
		authRequired := extractAuthRequired(vt.Operation)
		if !authRequired {
			slog.Info("public operation (auth not required)", "tool", vt.PrefixedName)
		}
		entries = append(entries, &upstream.RegistryEntry{
			PrefixedName:  vt.PrefixedName,
			OriginalName:  vt.OriginalName,
			Upstream:      up,
			MCPTool:       vt.MCPTool,
			AuthRequired:  authRequired,
			OperationNode: vt.OperationNode,
			Executor: &Executor{
				PrefixedName:   vt.PrefixedName,
				Client:         client,
				BaseURL:        cfg.BaseURL,
				Method:         vt.Method,
				PathTemplate:   vt.PathTemplate,
				Transforms:     vt.Transforms,
				ResponseFormat: extractResponseFormat(vt.Operation),
				Validator:      vt.Validator,
				ValidationCfg:  cfg.Validation,
			},
		})
	}

	return &upstream.ValidatedUpstream{
		Config:       cfg,
		Entries:      entries,
		SpecYAMLRoot: specYAMLRoot,
	}, nil
}
