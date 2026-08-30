package config

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	gogaconfig "github.com/gaarutyunov/goga/config"
	"github.com/knadh/koanf/v2"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// EnvPrefix is the prefix of the environment variables that override
// configuration keys, per the goga/config convention: the prefix is upper
// case, "__" separates key-path segments and "_" is a literal underscore
// inside a segment. So MCP_ANYTHING__SERVER__PORT sets server.port and
// MCP_ANYTHING__NAMING__MAX_LENGTH sets naming.max_length.
//
// Environment variables beat the YAML file and are beaten by nothing, since
// mcp-anything configures no flags. See [Load].
const EnvPrefix = "MCP_ANYTHING"

// Loader watches a config file and atomically updates the live configuration on change.
type Loader struct {
	path    string
	current atomic.Pointer[ProxyConfig]
	onLoad  func(*ProxyConfig) error
}

// NewLoader creates a Loader, performs the initial load and validation, and returns.
// If the initial load or validation fails, it returns an error (callers should treat this as fatal).
func NewLoader(ctx context.Context, path string, onLoad func(*ProxyConfig) error) (*Loader, error) {
	l := &Loader{
		path:   path,
		onLoad: onLoad,
	}
	cfg, err := Load(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("initial config load: %w", err)
	}
	if err := onLoad(cfg); err != nil {
		return nil, fmt.Errorf("initial validation: %w", err)
	}
	l.current.Store(cfg)
	return l, nil
}

// Current returns the currently active configuration. Safe for concurrent reads.
func (l *Loader) Current() *ProxyConfig {
	return l.current.Load()
}

// Watch starts the fsnotify watcher for the parent directory of the config file.
// It debounces CREATE events (500 ms) before triggering a reload.
// Blocks until ctx is cancelled.
//
// This is deliberately mcp-anything's own watcher rather than goga/config's
// [gogaconfig.WithWatch]. goga's watcher ignores every event whose name is not
// one of the configured files, and a Kubernetes ConfigMap update never names
// the configured file: kubelet writes a new timestamped directory, points a
// "..data" symlink at it and renames that symlink over the old one, so the
// inotify events carry "..data" and "..2026_…" while the mounted
// config.yaml symlink is never touched. The operator mounts the proxy's
// configuration exactly that way (see buildDeployment in
// pkg/operator/controller), so a filename filter silently ends hot reload in
// the only deployment that has one. Watching the whole directory, as below, is
// what makes a ConfigMap rotation visible.
func (l *Loader) Watch(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("creating config watcher", "error", err)
		return
	}
	defer func() {
		if closeErr := watcher.Close(); closeErr != nil {
			slog.Warn("closing config watcher", "error", closeErr)
		}
	}()

	dir := filepath.Dir(l.path)
	if err := watcher.Add(dir); err != nil {
		slog.Error("watching config directory", "path", dir, "error", err)
		return
	}
	slog.Info("config watcher started", "path", l.path)

	var debounceTimer *time.Timer
	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
				l.tryReload(ctx)
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("config watcher error", "error", err)
		}
	}
}

// tryReload attempts to load and validate the config file. On success it atomically swaps
// the active config. On failure it retains the previous config and logs the error.
//
// The whole pipeline is re-run, not just the file: an environment variable that
// beat the file at startup must go on beating it at run time.
func (l *Loader) tryReload(ctx context.Context) {
	pkgtelemetry.IncrConfigReloadTotal()

	cfg, err := Load(ctx, l.path)
	if err != nil {
		slog.Error("config reload failed", "error", err)
		pkgtelemetry.IncrConfigReloadErrors(ctx)
		return
	}
	if err := l.onLoad(cfg); err != nil {
		slog.Error("config reload failed", "error", err)
		pkgtelemetry.IncrConfigReloadErrors(ctx)
		return
	}
	l.current.Store(cfg)
	pkgtelemetry.IncrConfigReloadSuccess(ctx)
	slog.Info("config reloaded", "upstreams", len(cfg.Upstreams))
}

// loadOptions are the goga/config sources mcp-anything is configured from, in
// no significant order: goga merges defaults, then files, then the environment,
// then flags, whatever order the options are passed in.
func loadOptions(path string) []gogaconfig.Option {
	return []gogaconfig.Option{
		gogaconfig.WithDefaults(scalarDefaults()),
		gogaconfig.WithRequiredFile(path),
		gogaconfig.WithEnv(EnvPrefix),
	}
}

// Load reads the YAML config file at path, overlays any MCP_ANYTHING__ environment
// variables on top of it, and returns a ProxyConfig with defaults applied for any
// missing fields.
//
// The file must exist: mcp-anything has no usable configuration without one, so
// its absence is a misconfiguration rather than a deployment style.
func Load(ctx context.Context, path string) (*ProxyConfig, error) {
	loaded, err := gogaconfig.Load[ProxyConfig](ctx, loadOptions(path)...)
	if err != nil {
		return nil, fmt.Errorf("loading config file %q: %w", path, err)
	}

	cfg := loaded.Value
	applyElementDefaults(&cfg, loaded.K)
	return &cfg, nil
}

// applyElementDefaults fills in the defaults that a defaults map cannot express,
// because they belong to the elements of the upstreams array rather than to a
// fixed key path.
//
// k is the merged configuration the value was decoded from. koanf does not
// expand array-element keys (e.g. "upstreams.0.enabled" never exists), so the
// "was this key present?" tests below inspect the raw Go representation.
func applyElementDefaults(cfg *ProxyConfig, k *koanf.Koanf) {
	rawUpstreams, _ := k.Get("upstreams").([]any)

	for i := range cfg.Upstreams {
		up := &cfg.Upstreams[i]
		if up.Timeout == 0 {
			up.Timeout = 10 * time.Second
		}

		// Retrieve the raw map for this upstream entry (may be nil for out-of-range index).
		var rawUp map[string]any
		if i < len(rawUpstreams) {
			rawUp, _ = rawUpstreams[i].(map[string]any)
		}

		// enabled defaults to true when the key is absent from the YAML.
		if _, exists := rawUp["enabled"]; !exists {
			up.Enabled = true
		}
		if up.StartupValidationTimeout == 0 {
			up.StartupValidationTimeout = cfg.Server.StartupValidationTimeout
		}
		applyValidationDefaults(rawUp, &up.Validation)
	}
}

// applyValidationDefaults sets defaults for a single upstream's ValidationConfig.
// rawUp is the raw map for the upstream entry (may be nil). Bool fields require
// raw-key existence checks since absent YAML booleans unmarshal to false.
func applyValidationDefaults(rawUp map[string]any, v *ValidationConfig) {
	var rawVal map[string]any
	if rawUp != nil {
		rawVal, _ = rawUp["validation"].(map[string]any)
	}
	if _, exists := rawVal["validate_request"]; !exists {
		v.ValidateRequest = true
	}
	if _, exists := rawVal["validate_response"]; !exists {
		v.ValidateResponse = true
	}
	if v.ResponseValidationFailure == "" {
		v.ResponseValidationFailure = "warn"
	}
	if len(v.SuccessStatus) == 0 {
		v.SuccessStatus = []int{200, 201, 202, 204}
	}
	if len(v.ErrorStatus) == 0 {
		v.ErrorStatus = []int{400, 401, 403, 404, 422, 429, 500, 502, 503}
	}
}

// scalarDefaults is the lowest-precedence source: the values used when neither
// the config file nor the environment sets them.
//
// It replaces the hand-rolled "set it if k.Exists says it is missing" pass that
// stood here before. That test and goga's precedence order say the same thing,
// but only one of them says it once for every source.
func scalarDefaults() map[string]any {
	return map[string]any{
		"server.port":                                   8080,
		"server.startup_validation_timeout":             "30s",
		"naming.separator":                              "__",
		"naming.max_length":                             128,
		"naming.conflict_resolution":                    "error",
		"naming.description_max_length":                 1024,
		"naming.description_truncation_suffix":          "...",
		"naming.default_slug_rules.replace_slashes":     true,
		"naming.default_slug_rules.replace_braces":      true,
		"naming.default_slug_rules.expand_camel_case":   true,
		"naming.default_slug_rules.lowercase":           true,
		"naming.default_slug_rules.collapse_separators": true,
	}
}
