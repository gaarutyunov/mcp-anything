// Package v1alpha1 contains the v1alpha1 API types for mcp-anything CRDs.
// This file contains Kubernetes-specific types that cannot be derived automatically
// from the proxy/upstream configuration types in pkg/config.
// Generated spec types (derived from pkg/config) live in spec_gen.go.
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Upstreams",type=integer,JSONPath=".status.upstreamCount"
// +kubebuilder:printcolumn:name="Tools",type=integer,JSONPath=".status.toolCount"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// MCPProxy is the Schema for the mcpproxies API.
type MCPProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec MCPProxySpec `json:"spec,omitempty"`
	// +optional
	Status MCPProxyStatus `json:"status,omitempty"`
}

// MCPProxySpec defines the desired state of MCPProxy.
type MCPProxySpec struct {
	// +optional
	// UpstreamSelector selects MCPUpstream resources by label.
	UpstreamSelector metav1.LabelSelector `json:"upstreamSelector,omitempty"`
	// +optional
	// NamespaceSelector restricts which namespaces are searched for matching
	// MCPUpstream resources. If empty, only the same namespace as the MCPProxy is searched.
	NamespaceSelector NamespaceSelectorSpec `json:"namespaceSelector,omitempty"`
	// +optional
	// ServiceDiscovery configures annotation-based upstream discovery from Kubernetes Services.
	ServiceDiscovery *ServiceDiscoverySpec `json:"serviceDiscovery,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +optional
	// Replicas is the number of proxy pod replicas. Defaults to 1.
	Replicas *int32 `json:"replicas,omitempty"`
	// +optional
	// Image is the proxy container image. Defaults to ghcr.io/gaarutyunov/mcp-anything:latest.
	Image string `json:"image,omitempty"`
	// +optional
	// Resources defines CPU/memory requirements for the proxy container.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// +optional
	// Server configures the MCP server endpoint.
	Server ProxyServerSpec `json:"server,omitempty"`
	// +optional
	// Naming configures how MCP tool names are generated.
	// Generated from pkg/config.NamingSpec.
	Naming NamingSpec `json:"naming,omitempty"`
	// +optional
	// InboundAuth configures authentication for inbound MCP clients.
	InboundAuth *ProxyInboundAuthSpec `json:"inboundAuth,omitempty"`
	// +optional
	// Telemetry configures observability settings.
	// Generated from pkg/config.TelemetrySpec.
	Telemetry *TelemetrySpec `json:"telemetry,omitempty"`
	// +optional
	// GatewayRef configures Kubernetes Gateway API HTTPRoute creation.
	// When set, the operator creates an HTTPRoute that routes traffic from the
	// referenced Gateway to the proxy Service.
	GatewayRef *GatewayRefSpec `json:"gatewayRef,omitempty"`
	// +optional
	// RateLimits configures named rate limit policies for tool calls.
	// Policies are referenced by MCPUpstream resources via the rateLimit field.
	RateLimits *MCPProxyRateLimitsSpec `json:"rateLimits,omitempty"`
}

// GatewayRefSpec references a Kubernetes Gateway resource for HTTPRoute creation.
type GatewayRefSpec struct {
	// Name is the name of the Gateway resource.
	Name string `json:"name"`
	// +optional
	// Namespace is the namespace of the Gateway resource.
	// Defaults to the same namespace as the MCPProxy.
	Namespace string `json:"namespace,omitempty"`
	// +optional
	// Hostname is the hostname to match in the HTTPRoute rules.
	// If empty, all hostnames are matched.
	Hostname string `json:"hostname,omitempty"`
}

// MCPProxyRateLimitsSpec configures named rate limit policies for the proxy.
type MCPProxyRateLimitsSpec struct {
	// +optional
	// Policies maps rate limit policy names to their configurations.
	// MCPUpstream resources reference these policies by name via the rateLimit field.
	Policies map[string]MCPProxyRateLimitPolicySpec `json:"policies,omitempty"`
}

// MCPProxyRateLimitPolicySpec defines a named rate limit policy.
type MCPProxyRateLimitPolicySpec struct {
	// Average is the number of requests allowed per Period.
	Average int64 `json:"average"`
	// Period is the time window for rate limiting (e.g. "1m", "1h").
	Period string `json:"period"`
	// +optional
	// Burst is the number of additional requests allowed above Average in a single burst.
	Burst int64 `json:"burst,omitempty"`
	// +optional
	// Source determines the counter key: "user" (authenticated subject),
	// "ip" (remote address), or "session" (MCP session ID). Defaults to "ip".
	Source string `json:"source,omitempty"`
}

// NamespaceSelectorSpec selects namespaces by name.
type NamespaceSelectorSpec struct {
	// +optional
	// MatchNames is a list of namespace names to search for MCPUpstream resources.
	MatchNames []string `json:"matchNames,omitempty"`
}

// ServiceDiscoverySpec configures annotation-based upstream discovery from Services.
type ServiceDiscoverySpec struct {
	// +optional
	// Enabled enables scanning for Services annotated with mcp-anything.ai/enabled=true.
	Enabled bool `json:"enabled,omitempty"`
	// +optional
	// NamespaceSelector restricts which namespaces are scanned for annotated Services.
	// If not set, the same namespaces as NamespaceSelector are used.
	NamespaceSelector *ServiceDiscoveryNamespaceSelector `json:"namespaceSelector,omitempty"`
}

// ServiceDiscoveryNamespaceSelector restricts which namespaces are scanned for annotated Services.
type ServiceDiscoveryNamespaceSelector struct {
	// +optional
	// MatchNames is a list of specific namespace names to scan.
	MatchNames []string `json:"matchNames,omitempty"`
	// +optional
	// MatchLabels scans all namespaces whose labels match these key-value pairs.
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// MCPProxyStatus defines the observed state of MCPProxy.
type MCPProxyStatus struct {
	// +optional
	// Conditions represents the latest available observations of the MCPProxy state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// UpstreamCount is the number of MCPUpstream resources currently selected.
	UpstreamCount int `json:"upstreamCount,omitempty"`
	// AnnotatedServiceCount is the number of annotated Services currently discovered.
	AnnotatedServiceCount int `json:"annotatedServiceCount,omitempty"`
	// ToolCount is the total number of MCP tools exposed.
	ToolCount int `json:"toolCount,omitempty"`
	// ObservedGeneration is the generation of the spec last processed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true

// MCPProxyList contains a list of MCPProxy.
type MCPProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items is the list of MCPProxy resources.
	Items []MCPProxy `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Proxy",type=string,JSONPath=".status.assignedProxy"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// MCPUpstream is the Schema for the mcpupstreams API.
type MCPUpstream struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec MCPUpstreamSpec `json:"spec,omitempty"`
	// +optional
	Status MCPUpstreamStatus `json:"status,omitempty"`
}

// MCPUpstreamSpec defines the desired state of MCPUpstream.
type MCPUpstreamSpec struct {
	// +kubebuilder:default=http
	// +kubebuilder:validation:Enum=http;command
	// +optional
	// Type is the upstream type: "http" (default) or "command".
	// HTTP upstreams require baseURL/serviceRef and openapi.
	// Command upstreams require commands and must not set baseURL/serviceRef/openapi.
	Type string `json:"type,omitempty"`
	// +optional
	// ToolPrefix is prepended to all tool names from this upstream.
	ToolPrefix string `json:"toolPrefix,omitempty"`
	// +optional
	// ServiceRef references an in-cluster Kubernetes Service.
	// Mutually exclusive with BaseURL. Only used when Type is "http".
	ServiceRef *ServiceRefSpec `json:"serviceRef,omitempty"`
	// +optional
	// BaseURL is the base URL for the upstream HTTP API.
	// Mutually exclusive with ServiceRef. Only used when Type is "http".
	BaseURL string `json:"baseURL,omitempty"`
	// +optional
	// OpenAPI configures the OpenAPI spec source. Required when Type is "http".
	OpenAPI MCPUpstreamOpenAPISpec `json:"openapi,omitempty"`
	// +optional
	// Overlay configures an optional OpenAPI Overlay document.
	Overlay *MCPUpstreamOverlaySpec `json:"overlay,omitempty"`
	// +optional
	// OutboundAuth configures authentication for outbound requests to the upstream.
	OutboundAuth *MCPUpstreamOutboundAuthSpec `json:"outboundAuth,omitempty"`
	// +optional
	// Transport configures HTTP transport settings for the upstream.
	Transport *MCPUpstreamTransportSpec `json:"transport,omitempty"`
	// +optional
	// Validation configures request/response validation against the OpenAPI schema.
	// Generated from pkg/config.ValidationSpec.
	Validation *ValidationSpec `json:"validation,omitempty"`
	// +optional
	// Commands defines command-backed MCP tools. Required when Type is "command".
	// Generated from pkg/config.CommandSpec.
	Commands []CommandSpec `json:"commands,omitempty"`
	// +optional
	// RateLimit is the name of a rate limit policy defined in the owning MCPProxy's
	// rateLimits.policies map. When set, the named policy is applied to all tool calls
	// from this upstream.
	RateLimit string `json:"rateLimit,omitempty"`
}

// ServiceRefSpec references a Kubernetes Service by name and port.
type ServiceRefSpec struct {
	// Name is the name of the Service.
	Name string `json:"name"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// Port is the port the Service exposes.
	Port int32 `json:"port"`
}

// MCPUpstreamOpenAPISpec configures the OpenAPI spec source for an upstream.
type MCPUpstreamOpenAPISpec struct {
	// +optional
	// ConfigMapRef references a ConfigMap containing the OpenAPI spec.
	// Mutually exclusive with URL and AutoDiscover.
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
	// +optional
	// URL is the URL from which the OpenAPI spec is fetched.
	// Mutually exclusive with ConfigMapRef and AutoDiscover.
	URL string `json:"url,omitempty"`
	// +optional
	// AutoDiscover configures automatic OpenAPI spec discovery from the upstream.
	// Mutually exclusive with ConfigMapRef and URL.
	AutoDiscover *AutoDiscoverSpec `json:"autoDiscover,omitempty"`
}

// ConfigMapKeyRef references a specific key within a ConfigMap.
type ConfigMapKeyRef struct {
	// Name is the name of the ConfigMap.
	Name string `json:"name"`
	// Key is the key in the ConfigMap data.
	Key string `json:"key"`
}

// AutoDiscoverSpec configures automatic OpenAPI spec discovery.
type AutoDiscoverSpec struct {
	// +optional
	// Path is the URL path at which the upstream serves its OpenAPI spec.
	Path string `json:"path,omitempty"`
}

// MCPUpstreamOverlaySpec configures an OpenAPI Overlay document.
type MCPUpstreamOverlaySpec struct {
	// +optional
	// ConfigMapRef references a ConfigMap containing the overlay document.
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// MCPUpstreamStatus defines the observed state of MCPUpstream.
type MCPUpstreamStatus struct {
	// +optional
	// Conditions represents the latest available observations of the MCPUpstream state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	// AssignedProxy is the name of the MCPProxy this upstream is currently assigned to.
	AssignedProxy string `json:"assignedProxy,omitempty"`
	// ToolCount is the number of MCP tools this upstream contributes.
	ToolCount int `json:"toolCount,omitempty"`
}

// +kubebuilder:object:root=true

// MCPUpstreamList contains a list of MCPUpstream.
type MCPUpstreamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items is the list of MCPUpstream resources.
	Items []MCPUpstream `json:"items"`
}

// ── Kubernetes-specific spec types ────────────────────────────────────────────
// These types use Kubernetes primitives (SecretRef, ServiceRef, etc.) and cannot
// be derived automatically from pkg/config types.

// ProxyServerSpec configures the MCP HTTP server endpoint.
// TLS uses a Kubernetes Secret reference instead of file paths.
type ProxyServerSpec struct {
	// +optional
	// Port is the port the proxy server listens on. Defaults to 8080.
	Port int32 `json:"port,omitempty"`
	// +optional
	// Transport is the list of MCP transport protocols to enable (e.g. sse, streamable-http).
	Transport []string `json:"transport,omitempty"`
	// +optional
	// TLS configures TLS termination for the proxy server using a Kubernetes Secret.
	TLS *ProxyTLSSpec `json:"tls,omitempty"`
}

// ProxyTLSSpec references a Kubernetes Secret containing TLS credentials for the proxy server.
type ProxyTLSSpec struct {
	// SecretName is the name of the Secret containing tls.crt and tls.key.
	SecretName string `json:"secretName"`
}

// ProxyInboundAuthSpec configures inbound authentication for MCP clients.
type ProxyInboundAuthSpec struct {
	// +kubebuilder:validation:Enum=jwt;none
	// Strategy is the auth strategy: jwt|none.
	Strategy string `json:"strategy"`
	// +optional
	// JWT configures JWT Bearer token validation.
	// Generated from pkg/config.JWTAuthSpec.
	JWT *JWTAuthSpec `json:"jwt,omitempty"`
}

// MCPUpstreamTransportSpec configures the HTTP transport for outbound upstream connections.
// TLS uses a Kubernetes Secret reference instead of file paths.
type MCPUpstreamTransportSpec struct {
	// +optional
	// MaxIdleConns is the maximum number of idle (keep-alive) connections. Default: 100.
	MaxIdleConns int `json:"maxIdleConns,omitempty"`
	// +optional
	// MaxIdleConnsPerHost is the maximum number of idle connections per host. Default: 10.
	MaxIdleConnsPerHost int `json:"maxIdleConnsPerHost,omitempty"`
	// +optional
	// TLS configures TLS for outbound connections using a Kubernetes Secret.
	TLS *UpstreamTLSSpec `json:"tls,omitempty"`
}

// UpstreamTLSSpec references a Kubernetes Secret containing TLS credentials for upstream connections.
type UpstreamTLSSpec struct {
	// SecretName is the name of the Secret containing CA/client TLS credentials.
	SecretName string `json:"secretName"`
}

// MCPUpstreamOutboundAuthSpec configures outbound authentication for upstream requests.
type MCPUpstreamOutboundAuthSpec struct {
	// +kubebuilder:validation:Enum=bearer;oauth2_client_credentials;none
	// Strategy is the outbound auth strategy: bearer|oauth2_client_credentials|none.
	Strategy string `json:"strategy"`
	// +optional
	// Bearer configures static Bearer token injection from a Kubernetes Secret.
	Bearer *BearerSpec `json:"bearer,omitempty"`
	// +optional
	// OAuth2 configures OAuth2 client credentials flow with credentials from a Kubernetes Secret.
	OAuth2 *OAuth2Spec `json:"oauth2,omitempty"`
}

// BearerSpec configures Bearer token authentication using a Kubernetes Secret.
type BearerSpec struct {
	// +optional
	// SecretRef references a Secret containing the bearer token under key "token".
	SecretRef *SecretRef `json:"secretRef,omitempty"`
}

// OAuth2Spec configures OAuth2 client credentials flow using a Kubernetes Secret.
type OAuth2Spec struct {
	// TokenURL is the OAuth2 token endpoint.
	TokenURL string `json:"tokenURL"`
	// +optional
	// SecretRef references a Secret containing client_id and client_secret.
	SecretRef *SecretRef `json:"secretRef,omitempty"`
}

// SecretRef references a Kubernetes Secret by name.
type SecretRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`
}

func init() {
	SchemeBuilder.Register(&MCPProxy{}, &MCPProxyList{})
	SchemeBuilder.Register(&MCPUpstream{}, &MCPUpstreamList{})
}
