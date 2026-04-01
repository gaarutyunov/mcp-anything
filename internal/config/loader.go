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
}
