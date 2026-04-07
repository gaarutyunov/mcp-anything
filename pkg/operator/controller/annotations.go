// Package controller contains controller-runtime reconcilers for mcp-anything CRDs.
package controller

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// Annotation keys for Service-based upstream auto-discovery.
const (
	// AnnotationEnabled opts a Service in to MCP proxying. Value must be "true".
	AnnotationEnabled = "mcp-anything.ai/enabled"

	// AnnotationToolPrefix sets the tool prefix for tools from this upstream.
	AnnotationToolPrefix = "mcp-anything.ai/tool-prefix"

	// AnnotationOpenAPIPath configures auto-discovery of the OpenAPI spec from the service itself.
	// The proxy will fetch the spec from <baseURL><path> at startup.
	// Mutually exclusive with AnnotationOpenAPIURL and AnnotationOpenAPIConfigMap.
	AnnotationOpenAPIPath = "mcp-anything.ai/openapi-path"

	// AnnotationOpenAPIURL sets a direct URL for the OpenAPI spec.
	// Mutually exclusive with AnnotationOpenAPIPath and AnnotationOpenAPIConfigMap.
	AnnotationOpenAPIURL = "mcp-anything.ai/openapi-url"

	// AnnotationOpenAPIConfigMap references an existing ConfigMap containing the OpenAPI spec.
	// Value format: "name:key" (ConfigMap name and key in the Service's namespace).
	// Mutually exclusive with AnnotationOpenAPIPath and AnnotationOpenAPIURL.
	AnnotationOpenAPIConfigMap = "mcp-anything.ai/openapi-configmap"

	// AnnotationOverlayConfigMap references an existing ConfigMap containing an OpenAPI overlay document.
	// Value format: "name:key" (ConfigMap name and key in the Service's namespace).
	AnnotationOverlayConfigMap = "mcp-anything.ai/overlay-configmap"

	// AnnotationAuthStrategy sets the outbound authentication strategy (e.g. bearer, none).
	AnnotationAuthStrategy = "mcp-anything.ai/auth-strategy"

	// AnnotationAuthSecret references a Kubernetes Secret containing auth credentials.
	// For bearer auth, the secret must have a "token" key.
	AnnotationAuthSecret = "mcp-anything.ai/auth-secret"

	// AnnotationProxy selects which MCPProxy should discover this Service.
	// If not set, all MCPProxy instances with serviceDiscovery.enabled=true will pick up this Service.
	AnnotationProxy = "mcp-anything.ai/proxy"

	// AnnotationPort overrides the port used for the upstream base URL.
	// If not set, the first port in the Service spec is used.
	AnnotationPort = "mcp-anything.ai/port"

	// AnnotationBasePath sets an optional base path prefix appended to the upstream base URL.
	AnnotationBasePath = "mcp-anything.ai/base-path"
)

// serviceToMCPUpstream converts an annotated Kubernetes Service into a synthetic MCPUpstream.
// The returned MCPUpstream is not persisted to Kubernetes; it is used only as an in-memory
// representation for config generation.
//
// Returns an error if required annotations are missing or malformed. The caller should log the
// error and skip the Service rather than halting reconciliation.
func serviceToMCPUpstream(svc *corev1.Service) (*v1alpha1.MCPUpstream, error) {
	ann := svc.Annotations

	toolPrefix := ann[AnnotationToolPrefix]
	if toolPrefix == "" {
		return nil, fmt.Errorf("missing required annotation %s", AnnotationToolPrefix)
	}

	port, err := resolveServicePort(svc)
	if err != nil {
		return nil, err
	}

	basePath := ann[AnnotationBasePath]
	baseURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", svc.Name, svc.Namespace, port, basePath)

	up := &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: toolPrefix,
			BaseURL:    baseURL,
		},
	}

	// OpenAPI source — exactly one of the three annotations must be set.
	switch {
	case ann[AnnotationOpenAPIURL] != "":
		up.Spec.OpenAPI = v1alpha1.MCPUpstreamOpenAPISpec{
			URL: ann[AnnotationOpenAPIURL],
		}
	case ann[AnnotationOpenAPIConfigMap] != "":
		ref, err := parseConfigMapRef(ann[AnnotationOpenAPIConfigMap])
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", AnnotationOpenAPIConfigMap, err)
		}
		up.Spec.OpenAPI = v1alpha1.MCPUpstreamOpenAPISpec{
			ConfigMapRef: ref,
		}
	case ann[AnnotationOpenAPIPath] != "":
		// The proxy fetches the spec from <baseURL><path> at startup (auto-discovery).
		up.Spec.OpenAPI = v1alpha1.MCPUpstreamOpenAPISpec{
			AutoDiscover: &v1alpha1.AutoDiscoverSpec{
				Path: ann[AnnotationOpenAPIPath],
			},
		}
	default:
		return nil, fmt.Errorf("missing OpenAPI source annotation (one of: %s, %s, %s)",
			AnnotationOpenAPIURL, AnnotationOpenAPIConfigMap, AnnotationOpenAPIPath)
	}

	// Optional: overlay ConfigMap.
	if cmRef := ann[AnnotationOverlayConfigMap]; cmRef != "" {
		ref, err := parseConfigMapRef(cmRef)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", AnnotationOverlayConfigMap, err)
		}
		up.Spec.Overlay = &v1alpha1.MCPUpstreamOverlaySpec{
			ConfigMapRef: ref,
		}
	}

	// Optional: outbound auth.
	if strategy := ann[AnnotationAuthStrategy]; strategy != "" && strategy != "none" {
		up.Spec.OutboundAuth = &v1alpha1.MCPUpstreamOutboundAuthSpec{
			Strategy: strategy,
		}
	}

	return up, nil
}

// resolveServicePort returns the port for the upstream base URL.
// It uses the AnnotationPort override, or falls back to the first port in the Service spec.
func resolveServicePort(svc *corev1.Service) (int32, error) {
	if override := svc.Annotations[AnnotationPort]; override != "" {
		p, err := strconv.ParseInt(override, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid %s annotation value %q: %w", AnnotationPort, override, err)
		}
		return int32(p), nil
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service has no ports and %s annotation is not set", AnnotationPort)
	}
	return svc.Spec.Ports[0].Port, nil
}

// parseConfigMapRef parses a "name:key" ConfigMap reference annotation value.
func parseConfigMapRef(value string) (*v1alpha1.ConfigMapKeyRef, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("expected format \"name:key\", got %q", value)
	}
	return &v1alpha1.ConfigMapKeyRef{
		Name: parts[0],
		Key:  parts[1],
	}, nil
}
