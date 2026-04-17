package transform

import (
	"net/http"
)

// Handler is an http.Handler that runs the request transform and chains to Next.
// It reads MCP tool arguments from context (stored via WithMCPArgs), executes
// the request jq expression, and stores the resulting RequestEnvelope in context.
//
// On failure the error is stored in context via withTransformError and Next is
// still called so that the terminal handler can write the error to the pipeline state.
// The terminal handler must check TransformErrorFromContext before proceeding.
type Handler struct {
	Transforms *CompiledTransforms
	Next       http.Handler
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	args := MCPArgsFromContext(ctx)

	envelope, err := h.Transforms.RunRequest(ctx, args)
	if err != nil {
		h.Next.ServeHTTP(w, r.WithContext(withTransformError(ctx, err)))
		return
	}

	h.Next.ServeHTTP(w, r.WithContext(withEnvelope(ctx, envelope)))
}
