package openapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	speakovl "github.com/speakeasy-api/openapi-overlay/pkg/overlay"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// ApplyOverlay loads an overlay from the given config and applies it to the spec bytes.
// Returns the modified spec bytes, any warnings from unmatched targets, and an error.
// If cfg is nil, the original specBytes are returned unchanged.
func ApplyOverlay(ctx context.Context, specBytes []byte, cfg *config.OverlayConfig) ([]byte, []string, error) {
	if cfg == nil {
		return specBytes, nil, nil
	}

	overlayBytes, err := loadOverlayBytes(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("loading overlay: %w", err)
	}

	var ol speakovl.Overlay
	dec := yaml.NewDecoder(strings.NewReader(string(overlayBytes)))
	if err := dec.Decode(&ol); err != nil {
		return nil, nil, fmt.Errorf("parsing overlay: %w", err)
	}

	if err := ol.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validating overlay: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(specBytes, &root); err != nil {
		return nil, nil, fmt.Errorf("unmarshalling spec for overlay: %w", err)
	}

	applyErr, warnings := ol.ApplyToStrict(&root)
	if applyErr != nil {
		return nil, nil, fmt.Errorf("applying overlay to spec: %w", applyErr)
	}

	modified, err := yaml.Marshal(&root)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling spec after overlay: %w", err)
	}

	return modified, warnings, nil
}

// loadOverlayBytes fetches overlay content from file, URL, or inline string.
func loadOverlayBytes(ctx context.Context, cfg *config.OverlayConfig) ([]byte, error) {
	if cfg.Inline != "" {
		return []byte(cfg.Inline), nil
	}

	if strings.HasPrefix(cfg.Source, "http") {
		authHeader := os.ExpandEnv(cfg.AuthHeader)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Source, nil)
		if err != nil {
			return nil, fmt.Errorf("creating overlay request for %q: %w", cfg.Source, err)
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching overlay from %q: %w", cfg.Source, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetching overlay from %q: unexpected status %d", cfg.Source, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading overlay response from %q: %w", cfg.Source, err)
		}
		return data, nil
	}

	data, err := os.ReadFile(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("reading overlay file %q: %w", cfg.Source, err)
	}
	return data, nil
}
