// Package webhook contains validating webhooks for mcp-anything CRDs.
package webhook

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// MCPProxyValidator validates MCPProxy create/update requests.
// It implements admission.Validator[*v1alpha1.MCPProxy].
type MCPProxyValidator struct{}

// ValidateCreate validates a newly created MCPProxy.
func (v *MCPProxyValidator) ValidateCreate(_ context.Context, proxy *v1alpha1.MCPProxy) (admission.Warnings, error) {
	return nil, validateMCPProxy(proxy)
}

// ValidateUpdate validates an updated MCPProxy.
func (v *MCPProxyValidator) ValidateUpdate(_ context.Context, _, newProxy *v1alpha1.MCPProxy) (admission.Warnings, error) {
	return nil, validateMCPProxy(newProxy)
}

// ValidateDelete validates a deleted MCPProxy (always permitted).
func (v *MCPProxyValidator) ValidateDelete(_ context.Context, _ *v1alpha1.MCPProxy) (admission.Warnings, error) {
	return nil, nil
}

func validateMCPProxy(proxy *v1alpha1.MCPProxy) error {
	// Validate replicas > 0 when explicitly set.
	if proxy.Spec.Replicas != nil && *proxy.Spec.Replicas <= 0 {
		return fmt.Errorf("spec.replicas must be greater than 0, got %d", *proxy.Spec.Replicas)
	}

	// Validate server port is in valid range (1–65535) when explicitly set.
	if proxy.Spec.Server.Port != 0 {
		if proxy.Spec.Server.Port < 1 || proxy.Spec.Server.Port > 65535 {
			return fmt.Errorf("spec.server.port must be between 1 and 65535, got %d", proxy.Spec.Server.Port)
		}
	}

	return nil
}
