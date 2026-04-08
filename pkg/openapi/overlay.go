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

	"github.com/gaarutyunov/mcp-anything/pkg/config"
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

	return ApplyOverlayBytes(specBytes, overlayBytes)
}

// ApplyOverlayBytes applies pre-loaded overlay bytes to spec bytes.
// Used by background refresh when overlay bytes are already cached.
func ApplyOverlayBytes(specBytes, overlayBytes []byte) ([]byte, []string, error) {
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

// FetchOverlayConditional fetches overlay bytes using conditional GET (for URL-based overlays).
// If the overlay is inline or file-based, ifNoneMatch is ignored and notModified is always false.
// Returns notModified=true if the server responds with 304 Not Modified.
func FetchOverlayConditional(ctx context.Context, cfg *config.OverlayConfig, ifNoneMatch string) (data []byte, etag string, notModified bool, err error) {
	if cfg == nil {
		return nil, "", false, nil
	}
	if cfg.Inline != "" {
		return []byte(cfg.Inline), "", false, nil
	}
	if !strings.HasPrefix(cfg.Source, "http") {
		d, readErr := os.ReadFile(cfg.Source)
		if readErr != nil {
			return nil, "", false, fmt.Errorf("reading overlay file %q: %w", cfg.Source, readErr)
		}
		return d, "", false, nil
	}

	authHeader := os.ExpandEnv(cfg.AuthHeader)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Source, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("creating overlay request for %q: %w", cfg.Source, err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("fetching overlay from %q: %w", cfg.Source, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.Header.Get("ETag"), true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("fetching overlay from %q: unexpected status %d", cfg.Source, resp.StatusCode)
	}
	d, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, "", false, fmt.Errorf("reading overlay response from %q: %w", cfg.Source, readErr)
	}
	return d, resp.Header.Get("ETag"), false, nil
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
