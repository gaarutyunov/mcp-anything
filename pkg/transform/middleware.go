package transform

import (
	"net/http"
)

// RequestMiddleware returns an HTTP middleware that runs the request transform.
// It reads MCP tool arguments from context (stored via WithMCPArgs), executes
// the request jq expression, and stores the resulting RequestEnvelope in context.
//
// On failure the error is stored in context via withTransformError and next is
// still called so that the terminal handler can write the error to the pipeline state.
// The terminal handler must check TransformErrorFromContext before proceeding.
func (c *CompiledTransforms) RequestMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			args := MCPArgsFromContext(ctx)

			envelope, err := c.RunRequest(ctx, args)
			if err != nil {
				next.ServeHTTP(w, r.WithContext(withTransformError(ctx, err)))
				return
			}

			next.ServeHTTP(w, r.WithContext(withEnvelope(ctx, envelope)))
		})
	}
}
