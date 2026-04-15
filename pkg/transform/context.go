package transform

import "context"

// mcpArgsKey is the unexported context key for MCP tool call arguments.
type mcpArgsKey struct{}

// envelopeKey is the unexported context key for the compiled RequestEnvelope.
type envelopeKey struct{}

// transformErrKey is the unexported context key for a request-transform error.
type transformErrKey struct{}

// WithMCPArgs stores MCP tool call arguments in ctx for the request-transform middleware.
func WithMCPArgs(ctx context.Context, args map[string]any) context.Context {
	return context.WithValue(ctx, mcpArgsKey{}, args)
}

// MCPArgsFromContext retrieves MCP tool call arguments stored by WithMCPArgs.
// Returns nil if no arguments are present.
func MCPArgsFromContext(ctx context.Context) map[string]any {
	args, _ := ctx.Value(mcpArgsKey{}).(map[string]any)
	return args
}

// RequestEnvelopeFromContext retrieves the RequestEnvelope stored by the
// request-transform middleware. Returns nil when no envelope is present.
func RequestEnvelopeFromContext(ctx context.Context) *RequestEnvelope {
	env, _ := ctx.Value(envelopeKey{}).(*RequestEnvelope)
	return env
}

// withEnvelope stores env in ctx under the package-private envelopeKey.
func withEnvelope(ctx context.Context, env *RequestEnvelope) context.Context {
	return context.WithValue(ctx, envelopeKey{}, env)
}

// ErrorFromContext retrieves a transform error set by the request-transform middleware.
// Returns nil when no error is present.
func ErrorFromContext(ctx context.Context) error {
	err, _ := ctx.Value(transformErrKey{}).(error)
	return err
}

// withTransformError stores err in ctx under the package-private transformErrKey.
func withTransformError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, transformErrKey{}, err)
}
