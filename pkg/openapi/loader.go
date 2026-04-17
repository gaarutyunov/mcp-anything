package openapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"gopkg.in/yaml.v3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// LoadPipeline executes the full OpenAPI loading pipeline for a single upstream:
// load spec bytes → apply overlay → parse with kin-openapi → validate → build router.
// It also returns the post-overlay YAML root node for JSONPath filter evaluation.
// It is safe to call from multiple goroutines if cfg is read-only.
func LoadPipeline(ctx context.Context, specCfg config.OpenAPISourceSpec, overlayCfg *config.OverlaySpec) (*openapi3.T, routers.Router, *yaml.Node, error) {
	specBytes, etag, err := loadSpecBytes(ctx, specCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading spec bytes: %w", err)
	}
	_ = etag // TODO: use for conditional refresh (If-None-Match) in a future task

	modifiedBytes, warnings, err := ApplyOverlay(ctx, specBytes, overlayCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("applying overlay: %w", err)
	}
	for _, w := range warnings {
		slog.Warn("overlay unmatched target", "warning", w)
	}

	// Parse the post-overlay bytes into a YAML node tree for JSONPath filter evaluation.
	var specYAMLRoot yaml.Node
	if err := yaml.Unmarshal(modifiedBytes, &specYAMLRoot); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing spec YAML root: %w", err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = specCfg.AllowExternalRefs
	loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(
		buildAuthHTTPReader(ctx, os.ExpandEnv(specCfg.AuthHeader)),
		openapi3.ReadFromFile,
	))

	parsedBaseURI, err := specBaseURI(specCfg.Source)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving spec base URI: %w", err)
	}

	doc, err := loader.LoadFromDataWithPath(modifiedBytes, parsedBaseURI)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing OpenAPI spec: %w", err)
	}

	if err := doc.Validate(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("validating OpenAPI spec: %w", err)
	}

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building router from OpenAPI spec: %w", err)
	}

	return doc, router, &specYAMLRoot, nil
}

// httpClient is used for fetching specs and overlays from remote URLs.
// The 30-second timeout prevents indefinite hangs if the server is unresponsive.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// loadSpecBytes loads the raw spec bytes and returns the ETag (empty for file-based).
// For HTTP URLs, it retries up to 5 times on transient connection errors and 5xx responses.
func loadSpecBytes(ctx context.Context, cfg config.OpenAPISourceSpec) ([]byte, string, error) {
	data, etag, _, err := FetchSpecConditional(ctx, cfg, "", 5)
	return data, etag, err
}

// FetchSpecConditional fetches spec bytes, optionally using conditional GET.
// If ifNoneMatch is non-empty and the server returns 304 Not Modified, returns
// notModified=true with nil data and empty etag.
// For file-based sources, ifNoneMatch is ignored and notModified is always false.
// maxAttempts controls the number of attempts for HTTP sources (1 = no retry).
func FetchSpecConditional(ctx context.Context, cfg config.OpenAPISourceSpec, ifNoneMatch string, maxAttempts int) (data []byte, etag string, notModified bool, err error) {
	if !strings.HasPrefix(cfg.Source, "http") {
		d, readErr := os.ReadFile(cfg.Source)
		if readErr != nil {
			return nil, "", false, fmt.Errorf("reading spec file %q: %w", cfg.Source, readErr)
		}
		return d, "", false, nil
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	authHeader := os.ExpandEnv(cfg.AuthHeader)

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Warn("retrying spec fetch", "url", cfg.Source, "attempt", attempt+1)
			select {
			case <-ctx.Done():
				return nil, "", false, fmt.Errorf("processing spec %s: %w", cfg.Source, ctx.Err())
			case <-time.After(2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Source, nil)
		if err != nil {
			return nil, "", false, fmt.Errorf("creating request for %q: %w", cfg.Source, err)
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("fetching spec from %q: %w", cfg.Source, err)
			slog.Warn("spec fetch failed, will retry", "url", cfg.Source, "error", err)
			continue
		}

		if resp.StatusCode == http.StatusNotModified {
			etag := resp.Header.Get("ETag")
			_ = resp.Body.Close()
			return nil, etag, true, nil
		}

		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("fetching spec from %q: unexpected status %d", cfg.Source, resp.StatusCode)
			slog.Warn("spec fetch got server error, will retry", "url", cfg.Source, "status", resp.StatusCode)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, "", false, fmt.Errorf("fetching spec from %q: unexpected status %d", cfg.Source, resp.StatusCode)
		}

		d, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, "", false, fmt.Errorf("reading spec response from %q: %w", cfg.Source, readErr)
		}

		return d, resp.Header.Get("ETag"), false, nil
	}
	return nil, "", false, lastErr
}

// LoadPipelineFromBytes runs the OpenAPI loading pipeline from pre-fetched spec bytes
// (with overlay already applied). Used by background refresh to avoid re-fetching.
func LoadPipelineFromBytes(ctx context.Context, specBytes []byte, specCfg config.OpenAPISourceSpec) (*openapi3.T, routers.Router, *yaml.Node, error) {
	var specYAMLRoot yaml.Node
	if err := yaml.Unmarshal(specBytes, &specYAMLRoot); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing spec YAML root: %w", err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = specCfg.AllowExternalRefs
	loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(
		buildAuthHTTPReader(ctx, os.ExpandEnv(specCfg.AuthHeader)),
		openapi3.ReadFromFile,
	))

	parsedBaseURI, err := specBaseURI(specCfg.Source)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving spec base URI: %w", err)
	}

	doc, err := loader.LoadFromDataWithPath(specBytes, parsedBaseURI)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing OpenAPI spec: %w", err)
	}

	if err := doc.Validate(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("validating OpenAPI spec: %w", err)
	}

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building router from OpenAPI spec: %w", err)
	}

	return doc, router, &specYAMLRoot, nil
}

// buildAuthHTTPReader returns a ReadFromURIFunc that adds an Authorization header on all HTTP requests.
// ctx is captured from the loader startup call and used for all $ref fetches.
func buildAuthHTTPReader(ctx context.Context, authHeader string) openapi3.ReadFromURIFunc {
	return func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location.Scheme != "http" && location.Scheme != "https" {
			return nil, openapi3.ErrURINotSupported
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("processing spec %s: %w", location.String(), err)
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("processing spec %s: %w", location.String(), err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("error loading %q: request returned status code %d", location.String(), resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("processing spec %s: %w", location.String(), err)
		}
		return data, nil
	}
}

// specBaseURI builds the base URI for $ref resolution.
func specBaseURI(source string) (*url.URL, error) {
	if strings.HasPrefix(source, "http") {
		u, err := url.Parse(source)
		if err != nil {
			return nil, fmt.Errorf("parsing spec URL %q: %w", source, err)
		}
		return u, nil
	}

	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute path for %q: %w", source, err)
	}
	u, err := url.Parse("file://" + filepath.ToSlash(abs))
	if err != nil {
		return nil, fmt.Errorf("parsing file URI for %q: %w", source, err)
	}
	return u, nil
}
