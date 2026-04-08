// Package transform re-exports from pkg/transform. See pkg/transform for documentation.
package transform

import (
	"github.com/getkin/kin-openapi/openapi3"

	pkgtransform "github.com/gaarutyunov/mcp-anything/pkg/transform"
)

// DefaultResponseExpr is the identity jq expression used when no response transform is specified.
const DefaultResponseExpr = pkgtransform.DefaultResponseExpr

// DefaultErrorExpr is the default error transform that handles problem+json and generic errors.
const DefaultErrorExpr = pkgtransform.DefaultErrorExpr

// CompiledTransforms holds the three compiled jq expressions for one tool.
// See pkg/transform.CompiledTransforms.
type CompiledTransforms = pkgtransform.CompiledTransforms

// RequestEnvelope is the output of the request transform.
// See pkg/transform.RequestEnvelope.
type RequestEnvelope = pkgtransform.RequestEnvelope

// Compile compiles all three jq expressions for a tool.
// See pkg/transform.Compile.
func Compile(reqExpr, respExpr, errExpr string) (*CompiledTransforms, error) {
	return pkgtransform.Compile(reqExpr, respExpr, errExpr)
}

// GenerateRequestJq generates a jq expression string from OpenAPI operation metadata.
// See pkg/transform.GenerateRequestJq.
func GenerateRequestJq(op *openapi3.Operation, toolName string, argMap map[string]string) string {
	return pkgtransform.GenerateRequestJq(op, toolName, argMap)
}
