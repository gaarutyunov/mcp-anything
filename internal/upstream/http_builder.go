package upstream

import (
	"context"
	"fmt"
	"log/slog"

	outboundauth "github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

// HTTPBuilder implements Builder for type: http (or "") upstreams.
// It fetches and validates the OpenAPI spec and constructs RegistryEntry objects
// for all exported operations.
type HTTPBuilder struct {
	pools *runtime.Registry
}

// Build validates the OpenAPI spec and returns a ValidatedUpstream with RegistryEntry
// objects ready for registration.
func (b *HTTPBuilder) Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*ValidatedUpstream, error) {
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

	client, err := NewHTTPClient(cfg, provider)
	if err != nil {
		return nil, fmt.Errorf("building HTTP client for upstream %q: %w", cfg.Name, err)
	}

	up := &Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
		BaseURL:    cfg.BaseURL,
		Client:     client,
	}

	entries := make([]*RegistryEntry, 0, len(tools))
	for _, vt := range tools {
		authRequired := extractAuthRequired(vt.Operation)
		if !authRequired {
			slog.Info("public operation (auth not required)", "tool", vt.PrefixedName)
		}
		entries = append(entries, &RegistryEntry{
			PrefixedName:   vt.PrefixedName,
			OriginalName:   vt.OriginalName,
			Upstream:       up,
			MCPTool:        vt.MCPTool,
			Transforms:     vt.Transforms,
			ResponseFormat: extractResponseFormat(vt.Operation),
			AuthRequired:   authRequired,
			Method:         vt.Method,
			PathTemplate:   vt.PathTemplate,
			Validator:      vt.Validator,
			ValidationCfg:  cfg.Validation,
			OperationNode:  vt.OperationNode,
		})
	}

	return &ValidatedUpstream{
		Config:       cfg,
		Entries:      entries,
		SpecYAMLRoot: specYAMLRoot,
	}, nil
}
