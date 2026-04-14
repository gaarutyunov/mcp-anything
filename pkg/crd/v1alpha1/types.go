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

	Spec   MCPProxySpec   `json:"spec,omitempty"`
	Status MCPProxyStatus `json:"status,omitempty"`
}

// MCPProxySpec defines the desired state of MCPProxy.
type MCPProxySpec struct {
	// UpstreamSelector selects MCPUpstream resources by label.
	// +optional
	UpstreamSelector metav1.LabelSelector `json:"upstreamSelector,omitempty"`

	// NamespaceSelector restricts which namespaces are searched for matching MCPUpstream resources.
	// If empty, only the same namespace as the MCPProxy is searched.
	// +optional
	NamespaceSelector NamespaceSelectorSpec `json:"namespaceSelector,omitempty"`

	// ServiceDiscovery configures annotation-based upstream discovery from Kubernetes Services.
	// +optional
	ServiceDiscovery *ServiceDiscoverySpec `json:"serviceDiscovery,omitempty"`

	// Replicas is the number of proxy pod replicas. Defaults to 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// Image is the proxy container image. Defaults to ghcr.io/gaarutyunov/mcp-anything:latest.
	// +optional
	Image string `json:"image,omitempty"`

	// Resources defines CPU/memory requirements for the proxy container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Server configures the MCP server endpoint.
	// +optional
	Server ProxyServerSpec `json:"server,omitempty"`

	// Naming configures how MCP tool names are generated.
	// +optional
	Naming ProxyNamingSpec `json:"naming,omitempty"`

	// InboundAuth configures authentication for inbound MCP clients.
	// +optional
	InboundAuth *ProxyInboundAuthSpec `json:"inboundAuth,omitempty"`

	// Telemetry configures observability settings.
	// +optional
	Telemetry *ProxyTelemetrySpec `json:"telemetry,omitempty"`
}

// NamespaceSelectorSpec selects namespaces by name.
type NamespaceSelectorSpec struct {
	// MatchNames is a list of namespace names to search for MCPUpstream resources.
	// +optional
	MatchNames []string `json:"matchNames,omitempty"`
}

// ServiceDiscoverySpec configures annotation-based upstream discovery from Services.
type ServiceDiscoverySpec struct {
	// Enabled enables scanning for Services annotated with mcp-anything.ai/enabled=true.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// NamespaceSelector restricts which namespaces are scanned for annotated Services.
	// If not set, the same namespaces as NamespaceSelector are used (defaulting to the proxy's namespace).
	// +optional
	NamespaceSelector *ServiceDiscoveryNamespaceSelector `json:"namespaceSelector,omitempty"`
}

// ServiceDiscoveryNamespaceSelector restricts which namespaces are scanned for annotated Services.
type ServiceDiscoveryNamespaceSelector struct {
	// MatchNames is a list of specific namespace names to scan.
	// +optional
	MatchNames []string `json:"matchNames,omitempty"`

	// MatchLabels scans all namespaces whose labels match these key-value pairs.
	// +optional
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// MCPProxyStatus defines the observed state of MCPProxy.
type MCPProxyStatus struct {
	// Conditions represents the latest available observations of the MCPProxy state.
	// +optional
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
	Items           []MCPProxy `json:"items"`
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

	Spec   MCPUpstreamSpec   `json:"spec,omitempty"`
	Status MCPUpstreamStatus `json:"status,omitempty"`
}

// MCPUpstreamSpec defines the desired state of MCPUpstream.
type MCPUpstreamSpec struct {
	// Type is the upstream type: "http" (default) or "command".
	// HTTP upstreams require baseURL/serviceRef and openapi.
	// Command upstreams require commands and must not set baseURL/serviceRef/openapi.
	// +optional
	// +kubebuilder:default=http
	// +kubebuilder:validation:Enum=http;command
	Type string `json:"type,omitempty"`

	// ToolPrefix is prepended to all tool names from this upstream.
	// +optional
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// ServiceRef references an in-cluster Kubernetes Service.
	// Mutually exclusive with BaseURL. Only used when Type is "http".
	// +optional
	ServiceRef *ServiceRefSpec `json:"serviceRef,omitempty"`

	// BaseURL is the base URL for the upstream HTTP API.
	// Mutually exclusive with ServiceRef. Only used when Type is "http".
	// +optional
	BaseURL string `json:"baseURL,omitempty"`

	// OpenAPI configures the OpenAPI spec source. Required when Type is "http".
	// +optional
	OpenAPI MCPUpstreamOpenAPISpec `json:"openapi,omitempty"`

	// Overlay configures an optional OpenAPI Overlay document.
	// +optional
	Overlay *MCPUpstreamOverlaySpec `json:"overlay,omitempty"`

	// OutboundAuth configures authentication for outbound requests to the upstream.
	// +optional
	OutboundAuth *MCPUpstreamOutboundAuthSpec `json:"outboundAuth,omitempty"`

	// Transport configures HTTP transport settings for the upstream.
	// +optional
	Transport *MCPUpstreamTransportSpec `json:"transport,omitempty"`

	// Validation configures request/response validation against the OpenAPI schema.
	// +optional
	Validation *MCPUpstreamValidationSpec `json:"validation,omitempty"`

	// Commands defines command-backed MCP tools. Required when Type is "command".
	// +optional
	Commands []MCPUpstreamCommandSpec `json:"commands,omitempty"`
}

// ServiceRefSpec references a Kubernetes Service.
type ServiceRefSpec struct {
	// Name is the name of the Service.
	Name string `json:"name"`

	// Port is the port the service exposes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// MCPUpstreamOpenAPISpec configures the OpenAPI spec source.
type MCPUpstreamOpenAPISpec struct {
	// ConfigMapRef references a ConfigMap containing the OpenAPI spec.
	// Mutually exclusive with URL and AutoDiscover.
	// +optional
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`

	// URL is the URL from which the OpenAPI spec is fetched.
	// Mutually exclusive with ConfigMapRef and AutoDiscover.
	// +optional
	URL string `json:"url,omitempty"`

	// AutoDiscover configures automatic OpenAPI spec discovery from the upstream.
	// Mutually exclusive with ConfigMapRef and URL.
	// +optional
	AutoDiscover *AutoDiscoverSpec `json:"autoDiscover,omitempty"`
}

// ConfigMapKeyRef references a specific key within a ConfigMap.
type ConfigMapKeyRef struct {
	// Name is the name of the ConfigMap.
	Name string `json:"name"`

	// Key is the key in the ConfigMap data.
	Key string `json:"key"`
}

// AutoDiscoverSpec configures automatic OpenAPI discovery.
type AutoDiscoverSpec struct {
	// Path is the URL path at which the upstream serves its OpenAPI spec.
	// +optional
	Path string `json:"path,omitempty"`
}

// MCPUpstreamOverlaySpec configures an OpenAPI Overlay document.
type MCPUpstreamOverlaySpec struct {
	// ConfigMapRef references a ConfigMap containing the overlay document.
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// MCPUpstreamStatus defines the observed state of MCPUpstream.
type MCPUpstreamStatus struct {
	// Conditions represents the latest available observations of the MCPUpstream state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AssignedProxy is the name of the MCPProxy this upstream is currently assigned to.
	// +optional
	AssignedProxy string `json:"assignedProxy,omitempty"`

	// ToolCount is the number of MCP tools this upstream contributes.
	ToolCount int `json:"toolCount,omitempty"`
}

// +kubebuilder:object:root=true

// MCPUpstreamList contains a list of MCPUpstream.
type MCPUpstreamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCPUpstream `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MCPProxy{}, &MCPProxyList{})
	SchemeBuilder.Register(&MCPUpstream{}, &MCPUpstreamList{})
}
