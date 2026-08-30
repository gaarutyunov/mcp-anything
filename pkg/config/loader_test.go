package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// minimalConfig is a config file with one upstream and nothing else set, so
// that every default below is a default rather than a value.
const minimalConfig = `server:
  port: 9001
upstreams:
  - name: pets
    base_url: http://example.invalid
    openapi:
      source: /dev/null
`

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadAppliesDefaults pins the defaults that used to be applied by a
// hand-rolled "set it when koanf says the key is missing" pass and are now the
// lowest-precedence goga/config source.
func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, t.TempDir(), minimalConfig)

	cfg, err := config.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != 9001 {
		t.Errorf("server.port: the file must win over the default: got %d", cfg.Server.Port)
	}
	if cfg.Server.StartupValidationTimeout != 30*time.Second {
		t.Errorf("server.startup_validation_timeout: got %v, want 30s", cfg.Server.StartupValidationTimeout)
	}
	if cfg.Naming.Separator != "__" {
		t.Errorf("naming.separator: got %q, want %q", cfg.Naming.Separator, "__")
	}
	if cfg.Naming.MaxLength != 128 {
		t.Errorf("naming.max_length: got %d, want 128", cfg.Naming.MaxLength)
	}
	if cfg.Naming.ConflictResolution != "error" {
		t.Errorf("naming.conflict_resolution: got %q, want %q", cfg.Naming.ConflictResolution, "error")
	}
	if cfg.Naming.DescriptionMaxLength != 1024 {
		t.Errorf("naming.description_max_length: got %d, want 1024", cfg.Naming.DescriptionMaxLength)
	}
	if cfg.Naming.DescriptionTruncationSuffix != "..." {
		t.Errorf("naming.description_truncation_suffix: got %q", cfg.Naming.DescriptionTruncationSuffix)
	}
	rules := cfg.Naming.DefaultSlugRules
	if !rules.ReplaceSlashes || !rules.ReplaceBraces || !rules.ExpandCamelCase ||
		!rules.Lowercase || !rules.CollapseSeparators {
		t.Errorf("naming.default_slug_rules: every rule defaults to true: got %+v", rules)
	}

	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams: got %d, want 1", len(cfg.Upstreams))
	}
	up := cfg.Upstreams[0]
	if !up.Enabled {
		t.Error("upstreams[0].enabled: an absent key defaults to true")
	}
	if up.Timeout != 10*time.Second {
		t.Errorf("upstreams[0].timeout: got %v, want 10s", up.Timeout)
	}
	if up.StartupValidationTimeout != 30*time.Second {
		t.Errorf("upstreams[0].startup_validation_timeout: inherits the server value: got %v", up.StartupValidationTimeout)
	}
	if !up.Validation.ValidateRequest || !up.Validation.ValidateResponse {
		t.Errorf("upstreams[0].validation: both checks default to true: got %+v", up.Validation)
	}
	if up.Validation.ResponseValidationFailure != "warn" {
		t.Errorf("upstreams[0].validation.response_validation_failure: got %q, want %q",
			up.Validation.ResponseValidationFailure, "warn")
	}
	if len(up.Validation.SuccessStatus) == 0 || len(up.Validation.ErrorStatus) == 0 {
		t.Errorf("upstreams[0].validation status lists default to non-empty: got %+v", up.Validation)
	}
}

// TestLoadEnvBeatsFile pins the precedence goga/config fixes: the environment
// beats the file, whatever order the sources were declared in.
func TestLoadEnvBeatsFile(t *testing.T) {
	path := writeConfig(t, t.TempDir(), minimalConfig)
	t.Setenv(config.EnvPrefix+"__SERVER__PORT", "9999")
	t.Setenv(config.EnvPrefix+"__NAMING__MAX_LENGTH", "64")

	cfg, err := config.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("server.port: the environment must beat the file: got %d, want 9999", cfg.Server.Port)
	}
	if cfg.Naming.MaxLength != 64 {
		t.Errorf("naming.max_length: the environment must beat the default: got %d, want 64", cfg.Naming.MaxLength)
	}
}

// TestLoadMissingFile pins that an absent config file is fatal. mcp-anything has
// no usable configuration without one, so goga/config's WithRequiredFile is the
// right option rather than WithFile.
func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := config.Load(context.Background(), path); err == nil {
		t.Fatal("Load: want an error for a config file that does not exist")
	}
}

// TestWatchReloadsOnConfigMapSwap is the regression test for the one reload that
// matters in production. A Kubernetes ConfigMap volume is a directory of
// timestamped versions behind a "..data" symlink, with the mounted file a
// symlink through it; an update writes a new version directory and renames a new
// "..data" over the old one, so no filesystem event ever names config.yaml.
// A watcher that filters events by filename — goga/config's WithWatch does —
// sees nothing at all, which is why Loader keeps its own directory watch.
func TestWatchReloadsOnConfigMapSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	mountVersion := func(name, content string) {
		t.Helper()
		versionDir := filepath.Join(dir, name)
		if err := os.Mkdir(versionDir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(versionDir, "config.yaml"), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s/config.yaml: %v", name, err)
		}
		tmpLink := filepath.Join(dir, "..data_tmp")
		if err := os.Symlink(name, tmpLink); err != nil {
			t.Fatalf("symlink ..data_tmp: %v", err)
		}
		if err := os.Rename(tmpLink, filepath.Join(dir, "..data")); err != nil {
			t.Fatalf("rename ..data_tmp over ..data: %v", err)
		}
	}

	mountVersion("..2026_01_01_00_00_00.0001", minimalConfig)
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), path); err != nil {
		t.Fatalf("symlink config.yaml: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loader, err := config.NewLoader(ctx, path, func(*config.ProxyConfig) error { return nil })
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := loader.Current().Server.Port; got != 9001 {
		t.Fatalf("initial server.port: got %d, want 9001", got)
	}

	watching := make(chan struct{})
	go func() {
		close(watching)
		loader.Watch(ctx)
	}()
	<-watching
	// Give the watcher time to register the directory before swapping under it.
	time.Sleep(300 * time.Millisecond)

	const rotated = `server:
  port: 9002
upstreams:
  - name: pets
    base_url: http://example.invalid
    openapi:
      source: /dev/null
`
	mountVersion("..2026_01_01_00_00_01.0002", rotated)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if loader.Current().Server.Port == 9002 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("config was not reloaded after a ConfigMap rotation: server.port is still %d",
		loader.Current().Server.Port)
}

// TestLoadHonoursExplicitFalse pins the one thing a defaults map cannot do. An
// absent YAML boolean and an explicit `false` both decode to false, so
// upstreams[].enabled and the validation switches are defaulted by asking the
// raw merged tree whether the key was there at all. If that raw lookup ever
// stops returning the upstreams array, every explicit `false` here silently
// flips to true.
func TestLoadHonoursExplicitFalse(t *testing.T) {
	const explicitFalse = `upstreams:
  - name: pets
    enabled: false
    base_url: http://example.invalid
    openapi:
      source: /dev/null
    validation:
      validate_request: false
      validate_response: false
`
	path := writeConfig(t, t.TempDir(), explicitFalse)

	cfg, err := config.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams: got %d, want 1", len(cfg.Upstreams))
	}
	up := cfg.Upstreams[0]
	if up.Enabled {
		t.Error("upstreams[0].enabled: an explicit false must not be defaulted to true")
	}
	if up.Validation.ValidateRequest {
		t.Error("upstreams[0].validation.validate_request: an explicit false must survive")
	}
	if up.Validation.ValidateResponse {
		t.Error("upstreams[0].validation.validate_response: an explicit false must survive")
	}
}
