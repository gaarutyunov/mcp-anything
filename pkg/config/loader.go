package config

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
)

// Loader watches a config file and atomically updates the live configuration on change.
type Loader struct {
	path    string
	current atomic.Pointer[ProxyConfig]
	onLoad  func(*ProxyConfig) error
}

// NewLoader creates a Loader, performs the initial load and validation, and returns.
// If the initial load or validation fails, it returns an error (callers should treat this as fatal).
func NewLoader(path string, onLoad func(*ProxyConfig) error) (*Loader, error) {
	l := &Loader{
		path:   path,
		onLoad: onLoad,
	}
	cfg, err := Load(path)
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
func (l *Loader) tryReload(ctx context.Context) {
	pkgtelemetry.IncrConfigReloadTotal()

	cfg, err := Load(l.path)
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

// Load reads the YAML config file at path and returns a ProxyConfig with
// defaults applied for any missing fields.
func Load(path string) (*ProxyConfig, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("loading config file %q: %w", path, err)
	}

	applyDefaults(k)

	var cfg ProxyConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	// rawUpstreams holds the parsed YAML array for per-key existence checks.
	// koanf does not expand array-element keys (e.g. "upstreams.0.enabled" never exists),
	// so we inspect the raw Go representation instead.
	rawUpstreams, _ := k.Get("upstreams").([]interface{})

	// Apply per-upstream defaults that mapstructure won't handle for slice elements.
	for i := range cfg.Upstreams {
		up := &cfg.Upstreams[i]
		if up.Timeout == 0 {
			up.Timeout = 10 * time.Second
		}

		// Retrieve the raw map for this upstream entry (may be nil for out-of-range index).
		var rawUp map[string]interface{}
		if i < len(rawUpstreams) {
			rawUp, _ = rawUpstreams[i].(map[string]interface{})
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

	return &cfg, nil
}

// applyValidationDefaults sets defaults for a single upstream's ValidationConfig.
// rawUp is the raw map for the upstream entry (may be nil). Bool fields require
// raw-key existence checks since absent YAML booleans unmarshal to false.
func applyValidationDefaults(rawUp map[string]interface{}, v *ValidationConfig) {
	var rawVal map[string]interface{}
	if rawUp != nil {
		rawVal, _ = rawUp["validation"].(map[string]interface{})
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

// applyDefaults sets scalar defaults on the koanf instance before unmarshalling.
func applyDefaults(k *koanf.Koanf) {
	if !k.Exists("server.port") {
		_ = k.Set("server.port", 8080)
	}
	if !k.Exists("naming.separator") {
		_ = k.Set("naming.separator", "__")
	}
	if !k.Exists("naming.max_length") {
		_ = k.Set("naming.max_length", 128)
	}
	if !k.Exists("naming.conflict_resolution") {
		_ = k.Set("naming.conflict_resolution", "error")
	}
	if !k.Exists("naming.description_max_length") {
		_ = k.Set("naming.description_max_length", 1024)
	}
	if !k.Exists("naming.description_truncation_suffix") {
		_ = k.Set("naming.description_truncation_suffix", "...")
	}
	if !k.Exists("naming.default_slug_rules.replace_slashes") {
		_ = k.Set("naming.default_slug_rules.replace_slashes", true)
	}
	if !k.Exists("naming.default_slug_rules.replace_braces") {
		_ = k.Set("naming.default_slug_rules.replace_braces", true)
	}
	if !k.Exists("naming.default_slug_rules.lowercase") {
		_ = k.Set("naming.default_slug_rules.lowercase", true)
	}
	if !k.Exists("naming.default_slug_rules.collapse_separators") {
		_ = k.Set("naming.default_slug_rules.collapse_separators", true)
	}
	if !k.Exists("server.startup_validation_timeout") {
		_ = k.Set("server.startup_validation_timeout", 30*time.Second)
	}
}
