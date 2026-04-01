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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// LoadPipeline executes the full OpenAPI loading pipeline for a single upstream:
// load spec bytes → apply overlay → parse with kin-openapi → validate → build router.
// It is safe to call from multiple goroutines if cfg is read-only.
func LoadPipeline(ctx context.Context, specCfg config.OpenAPISourceConfig, overlayCfg *config.OverlayConfig) (*openapi3.T, routers.Router, error) {
	specBytes, etag, err := loadSpecBytes(ctx, specCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("loading spec bytes: %w", err)
	}
	_ = etag

	modifiedBytes, warnings, err := ApplyOverlay(ctx, specBytes, overlayCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("applying overlay: %w", err)
	}
	for _, w := range warnings {
		slog.Warn("overlay unmatched target", "warning", w)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = specCfg.AllowExternalRefs
	loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(
		buildAuthHTTPReader(ctx, os.ExpandEnv(specCfg.AuthHeader)),
		openapi3.ReadFromFile,
	))

	parsedBaseURI, err := specBaseURI(specCfg.Source)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving spec base URI: %w", err)
	}

	doc, err := loader.LoadFromDataWithPath(modifiedBytes, parsedBaseURI)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing OpenAPI spec: %w", err)
	}

	if err := doc.Validate(ctx); err != nil {
		return nil, nil, fmt.Errorf("validating OpenAPI spec: %w", err)
	}

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("building router from OpenAPI spec: %w", err)
	}

	return doc, router, nil
}

// loadSpecBytes loads the raw spec bytes and returns the ETag (empty for file-based).
func loadSpecBytes(ctx context.Context, cfg config.OpenAPISourceConfig) ([]byte, string, error) {
	if !strings.HasPrefix(cfg.Source, "http") {
		data, err := os.ReadFile(cfg.Source)
		if err != nil {
			return nil, "", fmt.Errorf("reading spec file %q: %w", cfg.Source, err)
		}
		return data, "", nil
	}

	authHeader := os.ExpandEnv(cfg.AuthHeader)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Source, nil)
	if err != nil {
		return nil, "", fmt.Errorf("creating request for %q: %w", cfg.Source, err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching spec from %q: %w", cfg.Source, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetching spec from %q: unexpected status %d", cfg.Source, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading spec response from %q: %w", cfg.Source, err)
	}

	return data, resp.Header.Get("ETag"), nil
}

// buildAuthHTTPReader returns a ReadFromURIFunc that adds an Authorization header on all HTTP requests.
// ctx is captured from the loader startup call and used for all $ref fetches.
func buildAuthHTTPReader(ctx context.Context, authHeader string) openapi3.ReadFromURIFunc {
	return func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location.Scheme == "" || location.Host == "" {
			return nil, openapi3.ErrURINotSupported
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
		if err != nil {
			return nil, err
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("error loading %q: request returned status code %d", location.String(), resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
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
	return url.Parse("file://" + filepath.ToSlash(abs))
}
