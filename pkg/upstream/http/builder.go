// Package http provides the HTTP upstream builder and executor for mcp-anything.
// It implements the upstream.Builder interface for type "http" (or empty type) upstreams
// and registers itself via init() so that importing this package is sufficient to enable
// HTTP upstream support.
package http

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
	"github.com/gaarutyunov/mcp-anything/pkg/openapi"
	"github.com/gaarutyunov/mcp-anything/pkg/transform"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
)

func init() {
	pkgupstream.RegisterBuilder("http", &Builder{})
	pkgupstream.RegisterBuilder("", &Builder{})
}

// Builder implements upstream.Builder for type: http (or "") upstreams.
// It fetches and validates the OpenAPI spec and constructs RegistryEntry objects
// for all exported operations.
//
// The caller must set cfg.OutboundAuth.JSAuthPool and cfg.OutboundAuth.LuaAuthPool
// before calling Build if script-based outbound auth strategies are used.
type Builder struct{}

// Build validates the OpenAPI spec and returns a ValidatedUpstream with RegistryEntry
// objects ready for registration.
func (b *Builder) Build(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig) (*pkgupstream.ValidatedUpstream, error) {
	valCtx, valCancel := context.WithTimeout(ctx, cfg.StartupValidationTimeout)
	tools, specYAMLRoot, err := openapi.ValidateUpstream(valCtx, cfg, naming)
	valCancel()
	if err != nil {
		return nil, fmt.Errorf("upstream %q validation failed: %w", cfg.Name, err)
	}
	slog.Info("validated tools", "upstream", cfg.Name, "count", len(tools))

	outboundCfg := cfg.OutboundAuth
	outboundCfg.Upstream = cfg.Name
	outboundStrategy := outboundCfg.Strategy
	if outboundStrategy == "" {
		outboundStrategy = "none"
	}
	outboundMW, err := pkgmiddleware.New(ctx, "outbound/"+outboundStrategy, &outboundCfg)
	if err != nil {
		return nil, fmt.Errorf("build outbound auth for upstream %q: %w", cfg.Name, err)
	}

	client, err := NewHTTPClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("building HTTP client for upstream %q: %w", cfg.Name, err)
	}

	up := &pkgupstream.Upstream{
		Name:       cfg.Name,
		ToolPrefix: cfg.ToolPrefix,
		BaseURL:    cfg.BaseURL,
		Client:     client,
	}

	// Build a fetch HTTP client for UI render scripts.
	fetchTimeout := cfg.Timeout
	if fetchTimeout <= 0 {
		fetchTimeout = 30 * time.Second
	}
	uiFetchClient := &nethttp.Client{Timeout: fetchTimeout}

	entries := make([]*pkgupstream.RegistryEntry, 0, len(tools))
	for _, vt := range tools {
		authRequired := extractAuthRequired(vt.Operation)
		if !authRequired {
			slog.Info("public operation (auth not required)", "tool", vt.PrefixedName)
		}
		// Per-tool x-mcp-rate-limit overrides the upstream default; empty falls back to cfg.RateLimit.
		rateLimit := extractRateLimit(vt.Operation)
		if rateLimit == "" {
			rateLimit = cfg.RateLimit
		}
		entry := &pkgupstream.RegistryEntry{
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
			RateLimit:      rateLimit,
			CacheName:      resolveCacheName(vt.Operation, cfg.Cache),
		}
		executor := &Executor{entry: entry}
		entry.Executor = executor

		// Compose the per-tool handler chain:
		//   TransformHandler → outbound auth → Executor (terminal handler)
		var h nethttp.Handler = executor
		h = outboundMW(h)
		if vt.Transforms != nil {
			h = &transform.Handler{Transforms: vt.Transforms, Next: h}
		}
		entry.Handler = h

		// Load and attach a UI handler when a UI source is configured for this tool.
		// buildUIHandler returns nil, nil when no UI factory is registered (pkg/upstream/http/withui not imported).
		if vt.UIConfig != nil {
			resourceURI := "ui://" + vt.PrefixedName + "/app"
			handler, uiErr := buildUIHandler(vt.UIConfig, uiFetchClient, cfg.JSScriptPool, vt.PrefixedName, vt.MCPTool.Description, vt.MCPTool.InputSchema, resourceURI)
			if uiErr != nil {
				return nil, fmt.Errorf("loading UI for tool %q: %w", vt.PrefixedName, uiErr)
			}
			entry.UIHandler = handler
		}

		entries = append(entries, entry)
	}

	return &pkgupstream.ValidatedUpstream{
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

// extractRateLimit reads x-mcp-rate-limit from an operation extension.
// Returns empty string when the extension is absent or not a non-empty string.
func extractRateLimit(op *openapi3.Operation) string {
	if op == nil {
		return ""
	}
	val, ok := op.Extensions["x-mcp-rate-limit"]
	if !ok {
		return ""
	}
	if s, ok := val.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// resolveCacheName determines the cache name for a tool by reading the x-mcp-cache
// operation extension and falling back to the upstream-level default.
// A per-operation extension of "" explicitly disables caching even when the upstream
// has a default cache configured.
func resolveCacheName(op *openapi3.Operation, upstreamDefault string) string {
	if op == nil {
		return upstreamDefault
	}
	val, ok := op.Extensions["x-mcp-cache"]
	if !ok {
		// No per-operation override; use upstream default.
		return upstreamDefault
	}
	// Extension is present — use its value (empty string explicitly disables caching).
	if s, ok := val.(string); ok {
		return s
	}
	return upstreamDefault
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
