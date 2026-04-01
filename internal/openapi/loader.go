package openapi

import (
	"context"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// Load loads and validates an OpenAPI 3.0 spec from a local file path.
// Returns the parsed document and a gorillamux router for request matching.
func Load(ctx context.Context, cfg config.OpenAPISourceConfig) (*openapi3.T, routers.Router, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(cfg.Source)
	if err != nil {
		return nil, nil, fmt.Errorf("loading OpenAPI spec from %q: %w", cfg.Source, err)
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
