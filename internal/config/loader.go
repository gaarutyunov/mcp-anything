package config

import (
	"fmt"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

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

	// Apply per-upstream defaults that mapstructure won't handle for slice elements.
	for i := range cfg.Upstreams {
		up := &cfg.Upstreams[i]
		if up.Timeout == 0 {
			up.Timeout = 10 * time.Second
		}
		// enabled defaults to true; koanf unmarshals false for missing bool keys.
		// We check the raw key to decide whether to set the default.
		key := fmt.Sprintf("upstreams.%d.enabled", i)
		if !k.Exists(key) {
			up.Enabled = true
		}
	}

	return &cfg, nil
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
}
