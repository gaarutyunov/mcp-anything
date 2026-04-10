package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

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
	outboundCfg.JSAuthPool = b.pools.JSAuth
	outboundCfg.LuaAuthPool = b.pools.LuaAuth
	provider, err := outboundauth.New(ctx, &outboundCfg)
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
		entry := &RegistryEntry{
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
		}
		entry.Executor = &HTTPExecutor{entry: entry}
		entries = append(entries, entry)
	}

	return &ValidatedUpstream{
		Config:       cfg,
		Entries:      entries,
		SpecYAMLRoot: specYAMLRoot,
	}, nil
}

// extractResponseFormat reads x-mcp-response-format from an operation extension.
func extractResponseFormat(op *openapi3.Operation) string {
	if op == nil {
		return "json"
	}
	val, ok := op.Extensions["x-mcp-response-format"]
	if !ok {
		return "json"
	}
	if s, ok := val.(string); ok && s != "" {
		return s
	}
	return "json"
}

// extractAuthRequired reads x-mcp-auth-required from an operation extension (default true).
func extractAuthRequired(op *openapi3.Operation) bool {
	if op == nil {
		return true
	}
	val, ok := op.Extensions["x-mcp-auth-required"]
	if !ok {
		return true
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return strings.ToLower(v) != "false"
	}
	return true
}
