package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"gopkg.in/yaml.v3"

	outboundauth "github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/openapi"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/telemetry"
)

// Snapshot is the compiled state for one upstream at a point in time.
// It is immutable after construction. The active snapshot is held in an atomic pointer.
type Snapshot struct {
	Doc           *openapi3.T
	Router        routers.Router
	CompiledTools []*RegistryEntry
	SpecYAMLRoot  *yaml.Node
	SpecETag      string
	OverlayETag   string
	FetchedAt     time.Time
	FailureCount  int // consecutive refresh failures (0 = healthy)

	// cachedSpecBytes and cachedOverlayBytes store raw bytes for reuse when
	// the respective resource hasn't changed (304 Not Modified).
	cachedSpecBytes    []byte
	cachedOverlayBytes []byte
}

// RegistryManager is implemented by the MCP Manager to receive upstream updates
// from background refresh goroutines.
type RegistryManager interface {
	// UpdateUpstream atomically replaces the tools for one upstream in the registry.
	UpdateUpstream(upstreamName string, entries []*RegistryEntry, specYAMLRoot *yaml.Node) error
	// RemoveUpstream removes all tools for one upstream from the registry.
	RemoveUpstream(upstreamName string)
}

// Refresher manages the lifecycle of background spec refresh for one upstream.
type Refresher struct {
	cfg      *config.UpstreamConfig
	naming   *config.NamingConfig
	current  atomic.Pointer[Snapshot]
	manager  RegistryManager
	failures atomic.Int32
	pools    *runtime.Registry

	lastOverlayFetch time.Time
}

// NewRefresher creates a Refresher with an initial snapshot loaded synchronously.
// Returns an error if the initial load fails.
func NewRefresher(ctx context.Context, cfg *config.UpstreamConfig, naming *config.NamingConfig, manager RegistryManager, pools *runtime.Registry) (*Refresher, error) {
	r := &Refresher{
		cfg:     cfg,
		naming:  naming,
		manager: manager,
		pools:   pools,
	}

	snap, err := r.buildSnapshot(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("initial snapshot for upstream %q: %w", cfg.Name, err)
	}
	r.current.Store(snap)
	return r, nil
}

// Start launches the background refresh goroutine.
// It exits when ctx is cancelled. A non-positive RefreshInterval disables background refresh.
func (r *Refresher) Start(ctx context.Context) {
	interval := r.cfg.OpenAPI.RefreshInterval
	if interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.refresh(ctx); err != nil {
					slog.Warn("spec refresh failed", "upstream", r.cfg.Name, "error", err)
					telemetry.RecordSpecRefresh(ctx, r.cfg.Name, false)
					count := r.failures.Add(1)
					if r.cfg.OpenAPI.MaxRefreshFailures > 0 && int(count) >= r.cfg.OpenAPI.MaxRefreshFailures {
						slog.Error("upstream marked degraded",
							"upstream", r.cfg.Name,
							"consecutive_failures", count)
						r.manager.RemoveUpstream(r.cfg.Name)
					}
				} else {
					telemetry.RecordSpecRefresh(ctx, r.cfg.Name, true)
					r.failures.Store(0)
				}
			}
		}
	}()
}

// Current returns the active snapshot. Safe for concurrent reads.
func (r *Refresher) Current() *Snapshot {
	return r.current.Load()
}

// IsHealthy returns true if the upstream is below max_refresh_failures.
func (r *Refresher) IsHealthy() bool {
	maxFail := r.cfg.OpenAPI.MaxRefreshFailures
	if maxFail <= 0 {
		return true
	}
	return int(r.failures.Load()) < maxFail
}

// UpstreamName returns the upstream name this Refresher manages.
func (r *Refresher) UpstreamName() string {
	return r.cfg.Name
}

// refresh performs one refresh cycle: fetches spec (and overlay if needed),
// re-runs the pipeline, atomically swaps the snapshot, and notifies the manager.
func (r *Refresher) refresh(ctx context.Context) error {
	prev := r.current.Load()

	// 1. Fetch spec bytes with conditional GET (no retry — a single failure increments the counter).
	specData, newSpecETag, notModified, err := openapi.FetchSpecConditional(ctx, r.cfg.OpenAPI, prev.SpecETag, 1)
	if err != nil {
		return fmt.Errorf("fetching spec: %w", err)
	}
	if notModified && !r.shouldRefreshOverlay() {
		// Neither spec nor overlay has changed.
		return nil
	}
	if notModified {
		// Spec unchanged — reuse cached bytes from the previous snapshot.
		specData = prev.cachedSpecBytes
		newSpecETag = prev.SpecETag
	}
	if newSpecETag == "" {
		newSpecETag = prev.SpecETag
	}

	// 2. Fetch overlay bytes (conditional if URL-based).
	overlayBytes, newOverlayETag, overlayFetched, err := r.fetchOverlay(ctx, prev)
	if err != nil {
		return fmt.Errorf("fetching overlay: %w", err)
	}

	// 3. Apply overlay (if any).
	var mergedBytes []byte
	if overlayBytes != nil {
		merged, warnings, applyErr := openapi.ApplyOverlayBytes(specData, overlayBytes)
		if applyErr != nil {
			return fmt.Errorf("applying overlay: %w", applyErr)
		}
		for _, w := range warnings {
			slog.Warn("overlay unmatched target", "upstream", r.cfg.Name, "warning", w)
		}
		mergedBytes = merged
	} else {
		mergedBytes = specData
	}

	// 4. Run full pipeline from merged bytes.
	doc, router, specYAMLRoot, err := openapi.LoadPipelineFromBytes(ctx, mergedBytes, r.cfg.OpenAPI)
	if err != nil {
		return fmt.Errorf("loading pipeline: %w", err)
	}

	// 5. Generate and validate tools.
	tools, err := openapi.GenerateTools(doc, r.cfg, r.naming)
	if err != nil {
		return fmt.Errorf("generating tools: %w", err)
	}
	for _, gt := range tools {
		gt.OperationNode = openapi.FindOperationYAMLNode(specYAMLRoot, gt.PathTemplate, strings.ToLower(gt.Method))
	}

	outboundCfg := r.cfg.OutboundAuth
	outboundCfg.Upstream = r.cfg.Name
	outboundCfg.JSAuthPool = r.pools.JSAuth
	outboundCfg.LuaAuthPool = r.pools.LuaAuth
	provider, err := outboundauth.New(ctx, &outboundCfg)
	if err != nil {
		return fmt.Errorf("building outbound auth: %w", err)
	}

	client, err := NewHTTPClient(r.cfg, provider)
	if err != nil {
		return fmt.Errorf("building HTTP client: %w", err)
	}

	up := &Upstream{
		Name:       r.cfg.Name,
		ToolPrefix: r.cfg.ToolPrefix,
		BaseURL:    r.cfg.BaseURL,
		Client:     client,
	}
	validator := openapi.NewValidator(doc, router)

	var entries []*RegistryEntry
	for _, gt := range tools {
		vt, valErr := openapi.ValidateTool(ctx, gt, doc)
		if valErr != nil {
			return fmt.Errorf("validating tool %q: %w", gt.PrefixedName, valErr)
		}
		vt.Validator = validator
		entry := &RegistryEntry{
			PrefixedName:   gt.PrefixedName,
			OriginalName:   gt.OriginalName,
			Upstream:       up,
			MCPTool:        gt.MCPTool,
			Transforms:     vt.Transforms,
			ResponseFormat: extractResponseFormat(gt.Operation),
			AuthRequired:   extractAuthRequired(gt.Operation),
			Method:         gt.Method,
			PathTemplate:   gt.PathTemplate,
			Validator:      vt.Validator,
			ValidationCfg:  r.cfg.Validation,
			OperationNode:  gt.OperationNode,
		}
		entry.Executor = &HTTPExecutor{entry: entry}
		entries = append(entries, entry)
	}

	// 6. Build new snapshot.
	snap := &Snapshot{
		Doc:                doc,
		Router:             router,
		CompiledTools:      entries,
		SpecYAMLRoot:       specYAMLRoot,
		SpecETag:           newSpecETag,
		OverlayETag:        newOverlayETag,
		FetchedAt:          time.Now(),
		cachedSpecBytes:    specData,
		cachedOverlayBytes: overlayBytes,
	}

	// 7. Notify registry manager before advancing snapshot state.
	if err := r.manager.UpdateUpstream(r.cfg.Name, entries, specYAMLRoot); err != nil {
		return fmt.Errorf("updating upstream registry: %w", err)
	}

	// 8. Atomically swap snapshot only after registry publish succeeds.
	r.current.Store(snap)
	if overlayFetched {
		r.lastOverlayFetch = time.Now()
	}

	slog.Info("spec refreshed", "upstream", r.cfg.Name, "spec_etag", newSpecETag, "tools", len(entries))
	return nil
}

// shouldRefreshOverlay reports whether the overlay should be re-fetched on this cycle.
func (r *Refresher) shouldRefreshOverlay() bool {
	if r.cfg.Overlay == nil {
		return false
	}
	overlayInterval := r.cfg.Overlay.RefreshInterval
	if overlayInterval <= 0 {
		// Overlay is refreshed together with the spec every cycle.
		return true
	}
	return time.Since(r.lastOverlayFetch) >= overlayInterval
}

// fetchOverlay fetches overlay bytes, using conditional GET if the overlay is a URL.
// Returns nil bytes if there is no overlay or if the overlay hasn't changed (304).
// When returning nil due to 304, the caller should reuse the previous snapshot's cached bytes.
// The fetched bool reports whether an HTTP request was actually made (true = polled the URL).
func (r *Refresher) fetchOverlay(ctx context.Context, prev *Snapshot) (data []byte, etag string, fetched bool, err error) {
	if r.cfg.Overlay == nil {
		return nil, "", false, nil
	}

	if !r.shouldRefreshOverlay() {
		// Reuse cached overlay from previous snapshot — no network request made.
		return prev.cachedOverlayBytes, prev.OverlayETag, false, nil
	}

	ifNoneMatch := prev.OverlayETag
	respData, respETag, notModified, fetchErr := openapi.FetchOverlayConditional(ctx, r.cfg.Overlay, ifNoneMatch)
	if fetchErr != nil {
		return nil, "", true, fetchErr
	}
	if notModified {
		return prev.cachedOverlayBytes, prev.OverlayETag, true, nil
	}
	return respData, respETag, true, nil
}

// buildSnapshot performs the initial load: fetches spec + overlay, runs the full pipeline,
// and returns the first Snapshot (without notifying the manager).
func (r *Refresher) buildSnapshot(ctx context.Context, prev *Snapshot) (*Snapshot, error) {
	specData, specETag, _, err := openapi.FetchSpecConditional(ctx, r.cfg.OpenAPI, "", 5)
	if err != nil {
		return nil, fmt.Errorf("fetching spec: %w", err)
	}

	overlayBytes, overlayETag, overlayFetched, err := r.fetchOverlay(ctx, &Snapshot{})
	if err != nil {
		return nil, fmt.Errorf("fetching overlay: %w", err)
	}

	var mergedBytes []byte
	if overlayBytes != nil {
		merged, warnings, applyErr := openapi.ApplyOverlayBytes(specData, overlayBytes)
		if applyErr != nil {
			return nil, fmt.Errorf("applying overlay: %w", applyErr)
		}
		for _, w := range warnings {
			slog.Warn("overlay unmatched target", "upstream", r.cfg.Name, "warning", w)
		}
		mergedBytes = merged
	} else {
		mergedBytes = specData
	}

	doc, router, specYAMLRoot, err := openapi.LoadPipelineFromBytes(ctx, mergedBytes, r.cfg.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("loading pipeline: %w", err)
	}

	tools, err := openapi.GenerateTools(doc, r.cfg, r.naming)
	if err != nil {
		return nil, fmt.Errorf("generating tools: %w", err)
	}
	for _, gt := range tools {
		gt.OperationNode = openapi.FindOperationYAMLNode(specYAMLRoot, gt.PathTemplate, strings.ToLower(gt.Method))
	}

	outboundCfg := r.cfg.OutboundAuth
	outboundCfg.Upstream = r.cfg.Name
	outboundCfg.JSAuthPool = r.pools.JSAuth
	outboundCfg.LuaAuthPool = r.pools.LuaAuth
	provider, err := outboundauth.New(ctx, &outboundCfg)
	if err != nil {
		return nil, fmt.Errorf("building outbound auth: %w", err)
	}

	httpClient, err := NewHTTPClient(r.cfg, provider)
	if err != nil {
		return nil, fmt.Errorf("building HTTP client: %w", err)
	}

	up := &Upstream{
		Name:       r.cfg.Name,
		ToolPrefix: r.cfg.ToolPrefix,
		BaseURL:    r.cfg.BaseURL,
		Client:     httpClient,
	}
	validator := openapi.NewValidator(doc, router)

	var entries []*RegistryEntry
	for _, gt := range tools {
		vt, valErr := openapi.ValidateTool(ctx, gt, doc)
		if valErr != nil {
			return nil, fmt.Errorf("validating tool %q: %w", gt.PrefixedName, valErr)
		}
		vt.Validator = validator
		entry := &RegistryEntry{
			PrefixedName:   gt.PrefixedName,
			OriginalName:   gt.OriginalName,
			Upstream:       up,
			MCPTool:        gt.MCPTool,
			Transforms:     vt.Transforms,
			ResponseFormat: extractResponseFormat(gt.Operation),
			AuthRequired:   extractAuthRequired(gt.Operation),
			Method:         gt.Method,
			PathTemplate:   gt.PathTemplate,
			Validator:      vt.Validator,
			ValidationCfg:  r.cfg.Validation,
			OperationNode:  gt.OperationNode,
		}
		entry.Executor = &HTTPExecutor{entry: entry}
		entries = append(entries, entry)
	}

	_ = prev

	if overlayFetched {
		r.lastOverlayFetch = time.Now()
	}
	return &Snapshot{
		Doc:                doc,
		Router:             router,
		CompiledTools:      entries,
		SpecYAMLRoot:       specYAMLRoot,
		SpecETag:           specETag,
		OverlayETag:        overlayETag,
		FetchedAt:          time.Now(),
		cachedSpecBytes:    specData,
		cachedOverlayBytes: overlayBytes,
	}, nil
}
