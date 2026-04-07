package webhook

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// MCPUpstreamValidator validates MCPUpstream create/update requests.
// It implements admission.Validator[*v1alpha1.MCPUpstream].
type MCPUpstreamValidator struct{}

// ValidateCreate validates a newly created MCPUpstream.
func (v *MCPUpstreamValidator) ValidateCreate(_ context.Context, upstream *v1alpha1.MCPUpstream) (admission.Warnings, error) {
	return nil, validateMCPUpstream(upstream)
}

// ValidateUpdate validates an updated MCPUpstream.
func (v *MCPUpstreamValidator) ValidateUpdate(_ context.Context, _, newUpstream *v1alpha1.MCPUpstream) (admission.Warnings, error) {
	return nil, validateMCPUpstream(newUpstream)
}

// ValidateDelete validates a deleted MCPUpstream (always permitted).
func (v *MCPUpstreamValidator) ValidateDelete(_ context.Context, _ *v1alpha1.MCPUpstream) (admission.Warnings, error) {
	return nil, nil
}

func validateMCPUpstream(upstream *v1alpha1.MCPUpstream) error {
	// Exactly one of serviceRef or baseURL must be set.
	hasServiceRef := upstream.Spec.ServiceRef != nil
	hasBaseURL := upstream.Spec.BaseURL != ""

	switch {
	case hasServiceRef && hasBaseURL:
		return fmt.Errorf("spec.serviceRef and spec.baseURL are mutually exclusive; set exactly one")
	case !hasServiceRef && !hasBaseURL:
		return fmt.Errorf("one of spec.serviceRef or spec.baseURL must be set")
	}

	// Exactly one OpenAPI source must be set.
	openAPI := upstream.Spec.OpenAPI
	sourceCount := 0
	if openAPI.ConfigMapRef != nil {
		sourceCount++
	}
	if openAPI.URL != "" {
		sourceCount++
	}
	if openAPI.AutoDiscover != nil {
		sourceCount++
	}

	if sourceCount == 0 {
		return fmt.Errorf("spec.openapi: one of configMapRef, url, or autoDiscover must be set")
	}
	if sourceCount > 1 {
		return fmt.Errorf("spec.openapi: configMapRef, url, and autoDiscover are mutually exclusive; set exactly one")
	}

	return nil
}
