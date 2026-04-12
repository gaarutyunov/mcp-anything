// Package mcp bootstraps the MCP server using the official go-sdk, registers
// all generated tools from the upstream registry, and implements the tool call
// pipeline: prefix-based routing, transform, OpenAPI validation, outbound auth,
// upstream HTTP dispatch, and response conversion.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/yaml.v3"

	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	pkgcache "github.com/gaarutyunov/mcp-anything/pkg/cache"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgembedding "github.com/gaarutyunov/mcp-anything/pkg/embedding"
	"github.com/gaarutyunov/mcp-anything/pkg/ratelimit"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
	pkgsearch "github.com/gaarutyunov/mcp-anything/pkg/search"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	"github.com/gaarutyunov/mcp-anything/pkg/tokencounter"
	pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"
	pkgcb "github.com/gaarutyunov/mcp-anything/pkg/upstream/circuitbreaker"
)

// searchToolName is the name of the semantic search MCP tool.
const searchToolName = "search_tools"

// upstreamState holds the current per-upstream entries and spec YAML root,
// used for incremental registry rebuilds triggered by background refresh.
type upstreamState struct {
	entries      []*pkgupstream.RegistryEntry
	specYAMLRoot *yaml.Node
}

// Manager owns the MCP servers and live tool registry.
// It rebuilds and updates them on config reload.
type Manager struct {
	servers  map[string]*sdkmcp.Server // keyed by group endpoint
	registry *pkgupstream.Registry
	impl     *sdkmcp.Implementation // set on first Rebuild
	mu       sync.RWMutex

	pools    *runtime.Registry     // for injecting runtime pools into upstream configs
	counter  *tokencounter.Counter // nil when token counting is disabled
	enforcer *ratelimit.Enforcer   // nil when no rate limits are configured

	// rateLimitCfgs holds the named rate limit configs for source key resolution.
	// Updated in Rebuild; read under mu.
	rateLimitCfgs map[string]config.RateLimitConfig

	// circuitBreakerSet holds the active circuit breakers for readiness checking.
	// nil when no circuit breakers are configured. Updated in Rebuild under mu.
	circuitBreakerSet *pkgcb.Set
	// oauthStore and oauthCallbackReg are injected for oauth2_user_session strategy.
	// Nil when no session_store is configured.
	oauthStore       config.OAuthTokenStore
	oauthCallbackReg config.OAuthCallbackRegistrar

	// State needed for per-upstream incremental updates (background refresh).
	groups         []config.GroupConfig
	namingCfg      *config.NamingConfig
	upstreamByName map[string]*upstreamState // latest state per upstream name

	// Semantic tool search state, keyed by group endpoint.
	searchIndexes map[string]*pkgsearch.Index
	searchTools   map[string]*sdkmcp.Tool
	searchLimit   int

	// Cache state — populated by Rebuild when caches are configured.
	store        pkgcache.Store
	cacheConfigs map[string]config.CacheConfig
}

// NewManager creates a Manager with no active servers.
// pools is used to bound the number of concurrent script runtime instances
// across all upstream builders and auth factories.
func NewManager(pools *runtime.Registry) *Manager {
	return &Manager{
		servers:        make(map[string]*sdkmcp.Server),
		upstreamByName: make(map[string]*upstreamState),
		pools:          pools,
		searchIndexes:  make(map[string]*pkgsearch.Index),
		searchTools:    make(map[string]*sdkmcp.Tool),
	}
}

// SetOAuthConfig configures per-user OAuth session storage for the oauth2_user_session strategy.
// Must be called before the first Rebuild when any upstream uses oauth2_user_session.
func (m *Manager) SetOAuthConfig(store config.OAuthTokenStore, reg config.OAuthCallbackRegistrar) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.oauthStore = store
	m.oauthCallbackReg = reg
}

// HTTPHandlers returns HTTP handlers for each group endpoint.
// Must be called after the initial Rebuild so servers are populated.
func (m *Manager) HTTPHandlers() map[string]http.Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()

	handlers := make(map[string]http.Handler, len(m.servers))
	for endpoint, srv := range m.servers {
		s := srv // capture for closure
		handlers[endpoint] = sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server { return s }, nil)
	}
	return handlers
}

// AuthRequired implements inbound.RegistryReader. Returns true (conservative
// default) when no registry is loaded or the tool is unknown.
func (m *Manager) AuthRequired(toolName string) bool {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	if reg == nil {
		return true
	}
	return reg.AuthRequired(toolName)
}

// ToolUpstreamName returns the upstream name for the given tool, or an empty
// string if unknown. Used by inbound auth middleware to select per-upstream validators.
func (m *Manager) ToolUpstreamName(toolName string) string {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.ToolUpstreamName(toolName)
}

// DispatchForGroup dispatches a tool call for the named group using the current registry.
// When caching is configured for the tool, the result is served from cache on a hit
// and stored on a miss. Error results (IsError: true) are never cached.
func (m *Manager) DispatchForGroup(ctx context.Context, groupName, toolName string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	m.mu.RLock()
	reg := m.registry
	store := m.store
	cacheConfigs := m.cacheConfigs
	m.mu.RUnlock()

	if reg == nil {
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "no config loaded"}},
		}, nil
	}

	// Cache check: skip if no store is configured or the tool has no cache config.
	if store != nil {
		entry := reg.EntryForTool(toolName)
		if entry != nil && entry.CacheName != "" {
			if cacheCfg, ok := cacheConfigs[entry.CacheName]; ok {
				// Determine authenticated subject for per-user keys.
				subject := ""
				if cacheCfg.PerUser {
					if ti := pkginbound.TokenInfoFromContext(ctx); ti != nil {
						subject = ti.Subject
					}
				}
				key, keyErr := pkgcache.ComputeKey(toolName, args, cacheCfg.PerUser, subject)
				if keyErr != nil {
					slog.Warn("cache key computation failed; skipping cache", "tool", toolName, "error", keyErr)
				} else {
					if cached, hit := store.Get(ctx, key); hit {
						return cached, nil
					}
					result, dispErr := reg.DispatchForGroup(ctx, groupName, toolName, args)
					if dispErr == nil && result != nil && !result.IsError {
						if setErr := store.Set(ctx, key, result, cacheCfg.TTL); setErr != nil {
							slog.Warn("cache store failed; result not cached", "tool", toolName, "error", setErr)
						}
					}
					return result, dispErr
				}
			}
		}
	}

	return reg.DispatchForGroup(ctx, groupName, toolName, args)
}

// Rebuild revalidates all upstreams from the new config, builds a new registry,
// and diffs the tool sets to call AddTool/RemoveTools on each MCP server.
// Connected clients receive notifications/tools/list_changed automatically.
// If any upstream validation fails, the existing registry and servers are unchanged.
func (m *Manager) Rebuild(ctx context.Context, cfg *config.ProxyConfig) error {
	// Set Implementation on first Rebuild.
	if m.impl == nil {
		m.impl = &sdkmcp.Implementation{
			Name:    "mcp-anything",
			Version: cfg.Telemetry.ServiceVersion,
		}
	}

	// Validate that no two enabled upstreams share the same tool_prefix.
	if err := validateUpstreamPrefixes(cfg.Upstreams); err != nil {
		return fmt.Errorf("invalid upstream configuration: %w", err)
	}

	// Validate that rate_limit references on upstreams exist in rate_limits map.
	for i := range cfg.Upstreams {
		up := &cfg.Upstreams[i]
		if !up.Enabled || up.RateLimit == "" {
			continue
		}
		if _, ok := cfg.RateLimits[up.RateLimit]; !ok {
			return fmt.Errorf("upstream %q references unknown rate limit %q", up.Name, up.RateLimit)
		}
	}

	// Validate that circuit_breaker references on upstreams exist in circuit_breakers map.
	for i := range cfg.Upstreams {
		up := &cfg.Upstreams[i]
		if !up.Enabled || up.CircuitBreaker == "" {
			continue
		}
		if _, ok := cfg.CircuitBreakers[up.CircuitBreaker]; !ok {
			return fmt.Errorf("upstream %q references unknown circuit_breaker %q", up.Name, up.CircuitBreaker)
		}
	}

	// Validate each enabled upstream with its configured timeout.
	var validatedUpstreams []*pkgupstream.ValidatedUpstream
	for i := range cfg.Upstreams {
		upCfg := &cfg.Upstreams[i]
		if !upCfg.Enabled {
			continue
		}

		// Inject runtime pools into a copy of the config so that pkg-level builders
		// can use bounded pools without holding a direct reference to the runtime registry.
		cfgCopy := *upCfg
		if m.pools != nil {
			cfgCopy.OutboundAuth.JSAuthPool = m.pools.JSAuth
			cfgCopy.OutboundAuth.LuaAuthPool = m.pools.LuaAuth
			cfgCopy.JSScriptPool = m.pools.JSScript
		}
		// Inject OAuth session config for oauth2_user_session strategy.
		cfgCopy.OutboundAuth.OAuthTokenStore = m.oauthStore
		cfgCopy.OutboundAuth.OAuthCallbackReg = m.oauthCallbackReg

		vu, vuErr := pkgupstream.Build(ctx, &cfgCopy, &cfg.Naming)
		if vuErr != nil {
			return fmt.Errorf("upstream %q: %w", upCfg.Name, vuErr)
		}
		slog.Info("upstream ready", "upstream", upCfg.Name, "tools", len(vu.Entries))

		validatedUpstreams = append(validatedUpstreams, vu)
	}

	// Build circuit breakers for upstreams that reference a named policy.
	// We set the breaker directly on the shared *Upstream pointer so the executor
	// finds it at dispatch time without any extra lookup.
	var cbBreakers []*pkgcb.Breaker
	for _, vu := range validatedUpstreams {
		upCfg := vu.Config
		if upCfg.CircuitBreaker == "" {
			continue
		}
		cbCfg := cfg.CircuitBreakers[upCfg.CircuitBreaker] // validated to exist above
		b := pkgcb.New(upCfg.Name, cbCfg)
		// All entries in this upstream share the same *Upstream pointer.
		for _, entry := range vu.Entries {
			if entry.Upstream != nil {
				entry.Upstream.CircuitBreaker = b
				break // set once; all entries share the pointer
			}
		}
		cbBreakers = append(cbBreakers, b)
	}

	// Build circuit breaker set for readiness checking.
	var newCBSet *pkgcb.Set
	if len(cbBreakers) > 0 {
		newCBSet = pkgcb.NewSet(cbBreakers)
	}

	// Build group config — synthesise a default group if none are configured.
	groups := cfg.Groups
	if len(groups) == 0 {
		allNames := make([]string, 0, len(validatedUpstreams))
		for _, vu := range validatedUpstreams {
			allNames = append(allNames, vu.Config.Name)
		}
		groups = []config.GroupConfig{{
			Name:      "default",
			Endpoint:  "/mcp",
			Upstreams: allNames,
		}}
	}

	// Build new registry (full validation; no partial updates on error).
	newRegistry, err := pkgupstream.New(validatedUpstreams, &cfg.Naming, groups)
	if err != nil {
		return fmt.Errorf("building tool registry: %w", err)
	}

	// Validate that x-mcp-rate-limit overlay entries on tools reference known rate limits.
	for _, vu := range validatedUpstreams {
		for _, entry := range vu.Entries {
			if entry.RateLimit == "" {
				continue
			}
			if _, ok := cfg.RateLimits[entry.RateLimit]; !ok {
				return fmt.Errorf("tool %q references unknown rate limit %q", entry.PrefixedName, entry.RateLimit)
			}
		}
	}

	// Validate that all cache names referenced by tools exist in the top-level caches map.
	for _, vu := range validatedUpstreams {
		for _, entry := range vu.Entries {
			if entry.CacheName == "" {
				continue
			}
			if _, ok := cfg.Caches[entry.CacheName]; !ok {
				return fmt.Errorf("upstream %q tool %q references unknown cache %q — define it under the top-level \"caches\" key",
					vu.Config.Name, entry.PrefixedName, entry.CacheName)
			}
		}
	}

	// Build the rate limit enforcer.
	newEnforcer, err := ratelimit.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("building rate limit enforcer: %w", err)
	}

	// Build token counter when enabled in config.
	var newCounter *tokencounter.Counter
	if cfg.TokenCounting.Enabled {
		newCounter, err = tokencounter.New(cfg.TokenCounting.Encoding)
		if err != nil {
			slog.Warn("token counting disabled: could not initialise tokenizer", "error", err)
		}
	}

	// Initialise the cache store when caches are configured.
	var newStore pkgcache.Store
	if len(cfg.Caches) > 0 {
		storeCfg := cfg.CacheStore
		if storeCfg.Provider == "" {
			storeCfg.Provider = "memory"
		}
		newStore, err = pkgcache.New(ctx, &storeCfg)
		if err != nil {
			return fmt.Errorf("initialising cache store: %w", err)
		}
		slog.Info("cache store initialised", "provider", storeCfg.Provider, "named_caches", len(cfg.Caches))
	}

	// Build per-upstream state map from the new registry.
	newUpstreamByName := make(map[string]*upstreamState, len(validatedUpstreams))
	for _, vu := range validatedUpstreams {
		newUpstreamByName[vu.Config.Name] = &upstreamState{
			entries:      newRegistry.EntriesForUpstream(vu.Config.Name),
			specYAMLRoot: newRegistry.SpecRootForUpstream(vu.Config.Name),
		}
	}

	// Build per-group search indexes outside the lock (embedding calls may be slow).
	newSearchIndexes, newSearchTools, newSearchLimit, searchErr := m.buildSearchState(ctx, cfg.ToolSearch, newRegistry, groups)
	if searchErr != nil {
		// Search index build failure is non-fatal: degrade gracefully.
		slog.Error("failed to build search indexes — tool search will be unavailable", "error", searchErr)
		newSearchIndexes = make(map[string]*pkgsearch.Index)
		newSearchTools = make(map[string]*sdkmcp.Tool)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.counter = newCounter
	m.enforcer = newEnforcer
	m.rateLimitCfgs = cfg.RateLimits
	m.circuitBreakerSet = newCBSet
	m.applyRegistryLocked(newRegistry, groups, newSearchTools)

	m.groups = groups
	m.namingCfg = &cfg.Naming
	m.upstreamByName = newUpstreamByName
	m.searchIndexes = newSearchIndexes
	m.searchTools = newSearchTools
	m.searchLimit = newSearchLimit
	m.store = newStore
	m.cacheConfigs = cfg.Caches
	return nil
}

// buildSearchState builds per-group search indexes when tool_search is enabled.
// Returns empty maps when disabled or on error; the caller handles the error.
func (m *Manager) buildSearchState(
	ctx context.Context,
	cfg *config.ToolSearchConfig,
	reg *pkgupstream.Registry,
	groups []config.GroupConfig,
) (map[string]*pkgsearch.Index, map[string]*sdkmcp.Tool, int, error) {
	indexes := make(map[string]*pkgsearch.Index)
	tools := make(map[string]*sdkmcp.Tool)

	if cfg == nil || !cfg.Enabled {
		return indexes, tools, 0, nil
	}

	embeddingFunc, err := pkgembedding.New(ctx, &cfg.Embedding)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("building embedding func: %w", err)
	}

	limit := cfg.Limit
	if limit <= 0 {
		limit = 5
	}

	searchTool := buildSearchTool(limit)

	for _, g := range groups {
		groupTools := reg.ToolsForGroup(g.Name)
		idx, idxErr := pkgsearch.Build(ctx, groupTools, embeddingFunc)
		if idxErr != nil {
			return nil, nil, 0, fmt.Errorf("building search index for group %q: %w", g.Name, idxErr)
		}
		indexes[g.Endpoint] = idx
		tools[g.Endpoint] = searchTool
		slog.Info("search index built", "group", g.Name, "tools", len(groupTools))
	}

	return indexes, tools, limit, nil
}

// buildSearchTool creates the search_tools MCP tool definition.
func buildSearchTool(defaultLimit int) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:        searchToolName,
		Description: fmt.Sprintf("Search available tools by natural language query. Returns up to %d matching tool definitions including their input schemas.", defaultLimit),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language description of what you want to do",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum number of tools to return (default: %d)", defaultLimit),
				},
			},
			"required": []string{"query"},
		},
	}
}

// applyRegistryLocked diffs the new registry against the current one and updates
// MCP servers accordingly. Must be called with m.mu held for writing.
// newSearchTools maps group endpoint → search_tools tool definition; pass nil
// to leave existing search tool state unchanged (used by incremental updates).
func (m *Manager) applyRegistryLocked(newRegistry *pkgupstream.Registry, groups []config.GroupConfig, newSearchTools map[string]*sdkmcp.Tool) {
	for _, g := range groups {
		groupName := g.Name    // capture for closure
		endpoint := g.Endpoint // capture for closure

		srv, exists := m.servers[g.Endpoint]
		if !exists {
			srv = sdkmcp.NewServer(m.impl, nil)
			// Add the tools/list intercept middleware once on server creation.
			// It is dynamic: reads m.searchTools[endpoint] at request time.
			srv.AddReceivingMiddleware(m.listToolsMiddleware(endpoint))
			m.servers[g.Endpoint] = srv
		}

		newTools := newRegistry.ToolsForGroup(g.Name)

		// Build set of new tool names for O(1) lookups.
		newToolSet := make(map[string]bool, len(newTools))
		for _, t := range newTools {
			newToolSet[t.Name] = true
		}

		// Build set of existing tool names for diffing.
		oldToolSet := make(map[string]bool)
		if m.registry != nil {
			for _, t := range m.registry.ToolsForGroup(g.Name) {
				oldToolSet[t.Name] = true
			}
		}

		// Remove tools no longer present in the new registry.
		var removedNames []string
		for name := range oldToolSet {
			if !newToolSet[name] {
				removedNames = append(removedNames, name)
			}
		}
		if len(removedNames) > 0 {
			srv.RemoveTools(removedNames...)
		}

		// Diff and update resources (MCP Apps UI handlers).
		m.applyResourcesLocked(srv, g.Name, newRegistry)

		// Add or replace tools (AddTool replaces existing tools with the same name).
		var addedNames []string
		for _, tool := range newTools {
			if !oldToolSet[tool.Name] {
				addedNames = append(addedNames, tool.Name)
			}
			t := tool       // capture
			gn := groupName // capture

			// Capture rate limit name from the registry entry at build time.
			var rlName string
			if entry := newRegistry.EntryForTool(t.Name); entry != nil {
				rlName = entry.RateLimit
			}

			srv.AddTool(t, func(callCtx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				// Extract W3C trace context from MCP _meta.traceparent (AC-28.4).
				if tp, ok := req.Params.Meta["traceparent"].(string); ok && tp != "" {
					carrier := propagation.MapCarrier{"traceparent": tp}
					if ts, ok2 := req.Params.Meta["tracestate"].(string); ok2 && ts != "" {
						carrier["tracestate"] = ts
					}
					callCtx = otel.GetTextMapPropagator().Extract(callCtx, carrier)
				}

				// Get session ID for span attributes.
				sessionID := ""
				if req.Session != nil {
					sessionID = req.Session.ID()
				}

				spanName := "tools/call " + t.Name
				callCtx, span := otel.Tracer("mcp-anything").Start(callCtx, spanName,
					trace.WithAttributes(pkgtelemetry.ToolCallAttributes(t.Name, "tools/call", sessionID)...),
					trace.WithSpanKind(trace.SpanKindServer),
				)
				defer span.End()

				// Rate limit check — before upstream dispatch.
				if rlName != "" {
					if result := m.checkRateLimit(callCtx, rlName, sessionID); result != nil {
						return result, nil
					}
				}

				start := time.Now()

				args, parseErr := managerParseArguments(req.Params.Arguments)
				if parseErr != nil {
					span.RecordError(parseErr)
					return nil, fmt.Errorf("parsing tool arguments: %w", parseErr)
				}

				result, dispErr := m.DispatchForGroup(callCtx, gn, req.Params.Name, args)

				elapsed := time.Since(start).Seconds()
				toolAttrs := metric.WithAttributes(
					attribute.String("mcp.tool.name", t.Name),
					attribute.String("mcp.method", "tools/call"),
				)
				if pkgtelemetry.ToolCallDuration != nil {
					pkgtelemetry.ToolCallDuration.Record(callCtx, elapsed, toolAttrs)
				}
				if dispErr != nil {
					span.RecordError(dispErr)
				}

				// Record token count for successful results (no error, non-nil counter).
				if dispErr == nil && result != nil {
					m.mu.RLock()
					ctr := m.counter
					reg := m.registry
					m.mu.RUnlock()
					upstreamName := ""
					if reg != nil {
						upstreamName = reg.ToolUpstreamName(t.Name)
					}
					ctr.Record(callCtx, t.Name, upstreamName, result)
				}

				return result, dispErr
			})
		}

		if len(addedNames) > 0 || len(removedNames) > 0 {
			slog.Info("tools updated", "group", g.Name, "added", addedNames, "removed", removedNames)
		}

		// Handle search_tools: add when enabled, remove when disabled.
		// Only act when newSearchTools is provided (nil = incremental update, leave as-is).
		if newSearchTools != nil {
			if searchTool, ok := newSearchTools[endpoint]; ok {
				srv.AddTool(searchTool, m.makeSearchHandler(endpoint))
			} else {
				// Search disabled for this group; remove if it was present before.
				if m.searchTools != nil {
					if _, wasPresentBefore := m.searchTools[endpoint]; wasPresentBefore {
						srv.RemoveTools(searchToolName)
					}
				}
			}
		}
	}

	m.registry = newRegistry
}

// listToolsMiddleware returns a Middleware that intercepts tools/list and
// returns only search_tools when semantic search is enabled for this endpoint.
// The middleware is dynamic: it reads the current search state under a read lock
// so changes take effect on the next tools/list call after a config reload.
func (m *Manager) listToolsMiddleware(endpoint string) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			m.mu.RLock()
			searchTool := m.searchTools[endpoint]
			m.mu.RUnlock()
			if searchTool == nil {
				// Search not enabled — return the full tool list.
				return next(ctx, method, req)
			}
			// Search enabled — return only search_tools.
			return &sdkmcp.ListToolsResult{
				Tools: []*sdkmcp.Tool{searchTool},
			}, nil
		}
	}
}

// makeSearchHandler returns a ToolHandler for the search_tools tool.
// The handler reads from the live search index for the given endpoint.
func (m *Manager) makeSearchHandler(endpoint string) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		args, parseErr := managerParseArguments(req.Params.Arguments)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing search_tools arguments: %w", parseErr)
		}

		query, _ := args["query"].(string)
		if query == "" {
			return &sdkmcp.CallToolResult{
				IsError: true,
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "query is required"}},
			}, nil
		}

		m.mu.RLock()
		idx := m.searchIndexes[endpoint]
		limit := m.searchLimit
		m.mu.RUnlock()

		if lf, ok := args["limit"].(float64); ok && lf > 0 {
			limit = int(lf)
		}
		if limit <= 0 {
			limit = 5
		}

		if idx == nil {
			return &sdkmcp.CallToolResult{
				IsError: true,
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "search index not available"}},
			}, nil
		}

		results, searchErr := idx.Search(ctx, query, limit)
		if searchErr != nil {
			return &sdkmcp.CallToolResult{
				IsError: true,
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "search failed: " + searchErr.Error()}},
			}, nil
		}

		resultJSON, marshalErr := json.Marshal(results)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshalling search results: %w", marshalErr)
		}

		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(resultJSON)}},
		}, nil
	}
}

// applyResourcesLocked diffs the MCP Apps UI resources for a single group and
// calls AddResource / RemoveResources on the server to keep them in sync with the
// new registry. Must be called with m.mu held for writing.
func (m *Manager) applyResourcesLocked(srv *sdkmcp.Server, groupName string, newRegistry *pkgupstream.Registry) {
	newTools := newRegistry.ToolsForGroup(groupName)

	// Build the new resource set: tool name → handler (nil entry = no UI).
	type resourceEntry struct {
		handler sdkmcp.ResourceHandler
		name    string
	}
	newResources := make(map[string]resourceEntry, len(newTools))
	for _, tool := range newTools {
		h := newRegistry.UIHandlerForTool(tool.Name)
		if h != nil {
			uri := "ui://" + tool.Name + "/app"
			newResources[uri] = resourceEntry{handler: h, name: tool.Name}
		}
	}

	// Build the old resource set for diffing.
	oldResourceURIs := make(map[string]bool)
	if m.registry != nil {
		for _, tool := range m.registry.ToolsForGroup(groupName) {
			if m.registry.UIHandlerForTool(tool.Name) != nil {
				oldResourceURIs["ui://"+tool.Name+"/app"] = true
			}
		}
	}

	// Remove resources whose tools are no longer present or no longer have a UI.
	var removedURIs []string
	for uri := range oldResourceURIs {
		if _, ok := newResources[uri]; !ok {
			removedURIs = append(removedURIs, uri)
		}
	}
	if len(removedURIs) > 0 {
		srv.RemoveResources(removedURIs...)
	}

	// Register (add or replace) resources for all tools that have a UI handler.
	for uri, re := range newResources {
		srv.AddResource(&sdkmcp.Resource{
			URI:      uri,
			Name:     re.name,
			MIMEType: "text/html",
		}, re.handler)
	}
}

// UpdateUpstream atomically replaces the tools for one upstream in the registry.
// It is called by the background Refresher after a successful spec re-fetch.
// Implements upstream.RegistryManager.
func (m *Manager) UpdateUpstream(upstreamName string, entries []*pkgupstream.RegistryEntry, specYAMLRoot *yaml.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.namingCfg == nil {
		return fmt.Errorf("manager not yet initialised (no Rebuild called)")
	}

	// Update per-upstream state.
	m.upstreamByName[upstreamName] = &upstreamState{
		entries:      entries,
		specYAMLRoot: specYAMLRoot,
	}

	return m.rebuildFromStateLocked()
}

// RemoveUpstream removes all tools for one upstream from the registry.
// It is called by the background Refresher after max_refresh_failures is exceeded.
// Implements upstream.RegistryManager.
func (m *Manager) RemoveUpstream(upstreamName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.namingCfg == nil {
		return
	}

	oldState := m.upstreamByName[upstreamName]
	delete(m.upstreamByName, upstreamName)
	if err := m.rebuildFromStateLocked(); err != nil {
		if oldState != nil {
			m.upstreamByName[upstreamName] = oldState
		}
		slog.Error("failed to remove upstream from registry", "upstream", upstreamName, "error", err)
	}
}

// rebuildFromStateLocked builds a new Registry from m.upstreamByName and applies it.
// Must be called with m.mu held for writing.
func (m *Manager) rebuildFromStateLocked() error {
	entriesByUpstream := make(map[string][]*pkgupstream.RegistryEntry, len(m.upstreamByName))
	specRootByUpstream := make(map[string]*yaml.Node, len(m.upstreamByName))
	for name, state := range m.upstreamByName {
		entriesByUpstream[name] = state.entries
		specRootByUpstream[name] = state.specYAMLRoot
	}

	newRegistry, err := pkgupstream.NewFromEntries(entriesByUpstream, specRootByUpstream, m.namingCfg, m.groups)
	if err != nil {
		return fmt.Errorf("rebuilding registry: %w", err)
	}

	// Pass nil for search tools: incremental updates do not change search state.
	m.applyRegistryLocked(newRegistry, m.groups, nil)
	return nil
}

// validateUpstreamPrefixes returns an error if any two enabled upstreams share the same tool_prefix.
func validateUpstreamPrefixes(upstreams []config.UpstreamConfig) error {
	seen := make(map[string]string)
	for _, up := range upstreams {
		if !up.Enabled {
			continue
		}
		if prev, ok := seen[up.ToolPrefix]; ok {
			return fmt.Errorf("upstreams %q and %q share the same tool_prefix %q", prev, up.Name, up.ToolPrefix)
		}
		seen[up.ToolPrefix] = up.Name
	}
	return nil
}

// checkRateLimit evaluates the named rate limit for the current request.
// Returns a non-nil CallToolResult (IsError: true) when the limit is exceeded, nil when allowed.
func (m *Manager) checkRateLimit(ctx context.Context, limitName, sessionID string) *sdkmcp.CallToolResult {
	m.mu.RLock()
	enf := m.enforcer
	rlCfgs := m.rateLimitCfgs
	m.mu.RUnlock()

	if enf == nil {
		return nil
	}

	cfg, ok := rlCfgs[limitName]
	if !ok {
		return nil
	}

	key := rateLimitKey(ctx, cfg.Source, limitName, sessionID)
	remaining, reset, reached, err := enf.Allow(ctx, limitName, key)
	if err != nil {
		slog.Warn("rate limit check failed", "limit", limitName, "error", err)
		return nil // allow on error to avoid false positives
	}
	if !reached {
		return nil
	}

	msg := fmt.Sprintf(
		"rate limit exceeded: limit=%s remaining=%d reset=%s",
		limitName, remaining, reset.UTC().Format(time.RFC3339),
	)
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
	}
}

// IsCircuitBreakerReady reports whether all upstream circuit breakers are in a
// non-open state. Returns true (ready) when no circuit breakers are configured.
// Safe to call concurrently; reads the current set under m.mu.
func (m *Manager) IsCircuitBreakerReady() (bool, string) {
	m.mu.RLock()
	cbs := m.circuitBreakerSet
	m.mu.RUnlock()
	if cbs == nil {
		return true, ""
	}
	return cbs.IsReady()
}

// rateLimitKey builds the counter key for the given source criterion.
// - "user": authenticated subject, falling back to IP, then "anonymous"
// - "ip": client IP from context, falling back to "unknown"
// - "session": MCP session ID, falling back to "unknown"
func rateLimitKey(ctx context.Context, source, limitName, sessionID string) string {
	var part string
	switch source {
	case "user":
		info := pkginbound.TokenInfoFromContext(ctx)
		if info != nil && info.Subject != "" {
			part = info.Subject
		} else {
			part = ratelimit.ClientIPFromContext(ctx)
			if part == "" {
				part = "anonymous"
			}
		}
	case "ip":
		part = ratelimit.ClientIPFromContext(ctx)
		if part == "" {
			part = "unknown"
		}
	case "session":
		part = sessionID
		if part == "" {
			part = "unknown"
		}
	default:
		part = "unknown"
	}
	return limitName + ":" + source + ":" + part
}

// managerParseArguments unmarshals tool call arguments into a map.
func managerParseArguments(raw any) (map[string]any, error) {
	if raw == nil {
		return make(map[string]any), nil
	}
	b, ok := raw.(json.RawMessage)
	if !ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-marshalling arguments: %w", err)
		}
		b = data
	}
	if len(b) == 0 || string(b) == "null" {
		return make(map[string]any), nil
	}
	var args map[string]any
	if err := json.Unmarshal(b, &args); err != nil {
		return nil, fmt.Errorf("unmarshalling arguments: %w", err)
	}
	return args, nil
}
