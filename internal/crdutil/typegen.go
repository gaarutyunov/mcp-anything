// Package crdutil generates CRD spec types from proxy/upstream config type definitions.
//
// The type mapping in this file defines how pkg/config/config.go types map to
// CRD spec types. When a config type changes, update the relevant typeSpec entry
// here and re-run "make generate-crds".
package crdutil

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// SpecGenPath is the relative path (from repo root) of the generated spec types file.
const SpecGenPath = "pkg/crd/v1alpha1/spec_gen.go"

// TypesGenPath is the relative path (from repo root) of the generated Kubernetes wrapper types file.
const TypesGenPath = "pkg/crd/v1alpha1/types_gen.go"

// typeSpec defines a CRD spec type to generate from a config type.
type typeSpec struct {
	Name        string      // generated Go type name
	Doc         string      // type-level doc comment
	Src         string      // source config type name (for doc comment lookup)
	TypeMarkers []string    // kubebuilder markers written before the type doc comment
	Fields      []fieldSpec // fields to include in the generated type
}

// fieldSpec defines a single field in a generated CRD type.
type fieldSpec struct {
	Name        string   // Go field name in the generated struct
	Embedded    bool     // if true, output as embedded field (Type JSONTag only, no Name or Doc)
	ConfigField string   // config struct field name (used to inherit doc comments)
	Type        string   // Go type string
	JSONTag     string   // full JSON struct tag, e.g. `json:"port,omitempty"`
	Markers     []string // kubebuilder markers (written as // +kubebuilder:... comments)
	Optional    bool     // if true, add // +optional marker
	Doc         string   // explicit doc comment (overrides the config field comment)
}

// crdRootTypeSpecs defines all Kubernetes wrapper types for pkg/crd/v1alpha1/types_gen.go.
// These are the root CRD objects, their specs, statuses, list types, and K8s-specific helpers.
var crdRootTypeSpecs = []typeSpec{
	// ── MCPProxy ─────────────────────────────────────────────────────────────────
	{
		Name: "MCPProxy",
		Doc:  "MCPProxy is the Schema for the mcpproxies API.",
		TypeMarkers: []string{
			"+kubebuilder:object:root=true",
			"+kubebuilder:subresource:status",
			"+kubebuilder:resource:scope=Namespaced",
			`+kubebuilder:printcolumn:name="Upstreams",type=integer,JSONPath=".status.upstreamCount"`,
			`+kubebuilder:printcolumn:name="Tools",type=integer,JSONPath=".status.toolCount"`,
			`+kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"`,
		},
		Fields: []fieldSpec{
			{Embedded: true, Type: "metav1.TypeMeta", JSONTag: "`json:\",inline\"`"},
			{Embedded: true, Type: "metav1.ObjectMeta", JSONTag: "`json:\"metadata,omitempty\"`"},
			{Name: "Spec", Type: "MCPProxySpec", JSONTag: "`json:\"spec,omitempty\"`", Doc: "Spec is the desired state of MCPProxy.", Optional: true},
			{Name: "Status", Type: "MCPProxyStatus", JSONTag: "`json:\"status,omitempty\"`", Doc: "Status is the observed state of MCPProxy.", Optional: true},
		},
	},
	// ── MCPProxySpec ──────────────────────────────────────────────────────────────
	{
		Name: "MCPProxySpec",
		Doc:  "MCPProxySpec defines the desired state of MCPProxy.",
		Fields: []fieldSpec{
			{
				Name:     "UpstreamSelector",
				Type:     "metav1.LabelSelector",
				JSONTag:  "`json:\"upstreamSelector,omitempty\"`",
				Doc:      "UpstreamSelector selects MCPUpstream resources by label.",
				Optional: true,
			},
			{
				Name:     "NamespaceSelector",
				Type:     "NamespaceSelectorSpec",
				JSONTag:  "`json:\"namespaceSelector,omitempty\"`",
				Doc:      "NamespaceSelector restricts which namespaces are searched for matching MCPUpstream resources. If empty, only the same namespace as the MCPProxy is searched.",
				Optional: true,
			},
			{
				Name:     "ServiceDiscovery",
				Type:     "*ServiceDiscoverySpec",
				JSONTag:  "`json:\"serviceDiscovery,omitempty\"`",
				Doc:      "ServiceDiscovery configures annotation-based upstream discovery from Kubernetes Services.",
				Optional: true,
			},
			{
				Name:     "Replicas",
				Type:     "*int32",
				JSONTag:  "`json:\"replicas,omitempty\"`",
				Markers:  []string{"+kubebuilder:validation:Minimum=1"},
				Doc:      "Replicas is the number of proxy pod replicas. Defaults to 1.",
				Optional: true,
			},
			{
				Name:     "Image",
				Type:     "string",
				JSONTag:  "`json:\"image,omitempty\"`",
				Doc:      "Image is the proxy container image. Defaults to ghcr.io/gaarutyunov/mcp-anything:latest.",
				Optional: true,
			},
			{
				Name:     "Resources",
				Type:     "corev1.ResourceRequirements",
				JSONTag:  "`json:\"resources,omitempty\"`",
				Doc:      "Resources defines CPU/memory requirements for the proxy container.",
				Optional: true,
			},
			{
				Name:     "Server",
				Type:     "ProxyServerSpec",
				JSONTag:  "`json:\"server,omitempty\"`",
				Doc:      "Server configures the MCP server endpoint.",
				Optional: true,
			},
			{
				Name:     "Naming",
				Type:     "ProxyNamingSpec",
				JSONTag:  "`json:\"naming,omitempty\"`",
				Doc:      "Naming configures how MCP tool names are generated.",
				Optional: true,
			},
			{
				Name:     "InboundAuth",
				Type:     "*ProxyInboundAuthSpec",
				JSONTag:  "`json:\"inboundAuth,omitempty\"`",
				Doc:      "InboundAuth configures authentication for inbound MCP clients.",
				Optional: true,
			},
			{
				Name:     "Telemetry",
				Type:     "*ProxyTelemetrySpec",
				JSONTag:  "`json:\"telemetry,omitempty\"`",
				Doc:      "Telemetry configures observability settings.",
				Optional: true,
			},
		},
	},
	// ── NamespaceSelectorSpec ─────────────────────────────────────────────────────
	{
		Name: "NamespaceSelectorSpec",
		Doc:  "NamespaceSelectorSpec selects namespaces by name.",
		Fields: []fieldSpec{
			{
				Name:     "MatchNames",
				Type:     "[]string",
				JSONTag:  "`json:\"matchNames,omitempty\"`",
				Doc:      "MatchNames is a list of namespace names to search for MCPUpstream resources.",
				Optional: true,
			},
		},
	},
	// ── ServiceDiscoverySpec ──────────────────────────────────────────────────────
	{
		Name: "ServiceDiscoverySpec",
		Doc:  "ServiceDiscoverySpec configures annotation-based upstream discovery from Services.",
		Fields: []fieldSpec{
			{
				Name:     "Enabled",
				Type:     "bool",
				JSONTag:  "`json:\"enabled,omitempty\"`",
				Doc:      "Enabled enables scanning for Services annotated with mcp-anything.ai/enabled=true.",
				Optional: true,
			},
			{
				Name:     "NamespaceSelector",
				Type:     "*ServiceDiscoveryNamespaceSelector",
				JSONTag:  "`json:\"namespaceSelector,omitempty\"`",
				Doc:      "NamespaceSelector restricts which namespaces are scanned for annotated Services. If not set, the same namespaces as NamespaceSelector are used.",
				Optional: true,
			},
		},
	},
	// ── ServiceDiscoveryNamespaceSelector ────────────────────────────────────────
	{
		Name: "ServiceDiscoveryNamespaceSelector",
		Doc:  "ServiceDiscoveryNamespaceSelector restricts which namespaces are scanned for annotated Services.",
		Fields: []fieldSpec{
			{
				Name:     "MatchNames",
				Type:     "[]string",
				JSONTag:  "`json:\"matchNames,omitempty\"`",
				Doc:      "MatchNames is a list of specific namespace names to scan.",
				Optional: true,
			},
			{
				Name:     "MatchLabels",
				Type:     "map[string]string",
				JSONTag:  "`json:\"matchLabels,omitempty\"`",
				Doc:      "MatchLabels scans all namespaces whose labels match these key-value pairs.",
				Optional: true,
			},
		},
	},
	// ── MCPProxyStatus ────────────────────────────────────────────────────────────
	{
		Name: "MCPProxyStatus",
		Doc:  "MCPProxyStatus defines the observed state of MCPProxy.",
		Fields: []fieldSpec{
			{
				Name:     "Conditions",
				Type:     "[]metav1.Condition",
				JSONTag:  "`json:\"conditions,omitempty\"`",
				Doc:      "Conditions represents the latest available observations of the MCPProxy state.",
				Optional: true,
			},
			{
				Name:    "UpstreamCount",
				Type:    "int",
				JSONTag: "`json:\"upstreamCount,omitempty\"`",
				Doc:     "UpstreamCount is the number of MCPUpstream resources currently selected.",
			},
			{
				Name:    "AnnotatedServiceCount",
				Type:    "int",
				JSONTag: "`json:\"annotatedServiceCount,omitempty\"`",
				Doc:     "AnnotatedServiceCount is the number of annotated Services currently discovered.",
			},
			{
				Name:    "ToolCount",
				Type:    "int",
				JSONTag: "`json:\"toolCount,omitempty\"`",
				Doc:     "ToolCount is the total number of MCP tools exposed.",
			},
			{
				Name:    "ObservedGeneration",
				Type:    "int64",
				JSONTag: "`json:\"observedGeneration,omitempty\"`",
				Doc:     "ObservedGeneration is the generation of the spec last processed by the controller.",
			},
		},
	},
	// ── MCPProxyList ──────────────────────────────────────────────────────────────
	{
		Name:        "MCPProxyList",
		Doc:         "MCPProxyList contains a list of MCPProxy.",
		TypeMarkers: []string{"+kubebuilder:object:root=true"},
		Fields: []fieldSpec{
			{Embedded: true, Type: "metav1.TypeMeta", JSONTag: "`json:\",inline\"`"},
			{Embedded: true, Type: "metav1.ListMeta", JSONTag: "`json:\"metadata,omitempty\"`"},
			{Name: "Items", Type: "[]MCPProxy", JSONTag: "`json:\"items\"`", Doc: "Items is the list of MCPProxy resources."},
		},
	},
	// ── MCPUpstream ───────────────────────────────────────────────────────────────
	{
		Name: "MCPUpstream",
		Doc:  "MCPUpstream is the Schema for the mcpupstreams API.",
		TypeMarkers: []string{
			"+kubebuilder:object:root=true",
			"+kubebuilder:subresource:status",
			"+kubebuilder:resource:scope=Namespaced",
			`+kubebuilder:printcolumn:name="Proxy",type=string,JSONPath=".status.assignedProxy"`,
			`+kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"`,
		},
		Fields: []fieldSpec{
			{Embedded: true, Type: "metav1.TypeMeta", JSONTag: "`json:\",inline\"`"},
			{Embedded: true, Type: "metav1.ObjectMeta", JSONTag: "`json:\"metadata,omitempty\"`"},
			{Name: "Spec", Type: "MCPUpstreamSpec", JSONTag: "`json:\"spec,omitempty\"`", Doc: "Spec is the desired state of MCPUpstream.", Optional: true},
			{Name: "Status", Type: "MCPUpstreamStatus", JSONTag: "`json:\"status,omitempty\"`", Doc: "Status is the observed state of MCPUpstream.", Optional: true},
		},
	},
	// ── MCPUpstreamSpec ───────────────────────────────────────────────────────────
	{
		Name: "MCPUpstreamSpec",
		Doc:  "MCPUpstreamSpec defines the desired state of MCPUpstream.",
		Fields: []fieldSpec{
			{
				Name:     "Type",
				Type:     "string",
				JSONTag:  "`json:\"type,omitempty\"`",
				Markers:  []string{"+kubebuilder:default=http", "+kubebuilder:validation:Enum=http;command"},
				Doc:      "Type is the upstream type: \"http\" (default) or \"command\". HTTP upstreams require baseURL/serviceRef and openapi. Command upstreams require commands and must not set baseURL/serviceRef/openapi.",
				Optional: true,
			},
			{
				Name:     "ToolPrefix",
				Type:     "string",
				JSONTag:  "`json:\"toolPrefix,omitempty\"`",
				Doc:      "ToolPrefix is prepended to all tool names from this upstream.",
				Optional: true,
			},
			{
				Name:     "ServiceRef",
				Type:     "*ServiceRefSpec",
				JSONTag:  "`json:\"serviceRef,omitempty\"`",
				Doc:      "ServiceRef references an in-cluster Kubernetes Service. Mutually exclusive with BaseURL. Only used when Type is \"http\".",
				Optional: true,
			},
			{
				Name:     "BaseURL",
				Type:     "string",
				JSONTag:  "`json:\"baseURL,omitempty\"`",
				Doc:      "BaseURL is the base URL for the upstream HTTP API. Mutually exclusive with ServiceRef. Only used when Type is \"http\".",
				Optional: true,
			},
			{
				Name:     "OpenAPI",
				Type:     "MCPUpstreamOpenAPISpec",
				JSONTag:  "`json:\"openapi,omitempty\"`",
				Doc:      "OpenAPI configures the OpenAPI spec source. Required when Type is \"http\".",
				Optional: true,
			},
			{
				Name:     "Overlay",
				Type:     "*MCPUpstreamOverlaySpec",
				JSONTag:  "`json:\"overlay,omitempty\"`",
				Doc:      "Overlay configures an optional OpenAPI Overlay document.",
				Optional: true,
			},
			{
				Name:     "OutboundAuth",
				Type:     "*MCPUpstreamOutboundAuthSpec",
				JSONTag:  "`json:\"outboundAuth,omitempty\"`",
				Doc:      "OutboundAuth configures authentication for outbound requests to the upstream.",
				Optional: true,
			},
			{
				Name:     "Transport",
				Type:     "*MCPUpstreamTransportSpec",
				JSONTag:  "`json:\"transport,omitempty\"`",
				Doc:      "Transport configures HTTP transport settings for the upstream.",
				Optional: true,
			},
			{
				Name:     "Validation",
				Type:     "*MCPUpstreamValidationSpec",
				JSONTag:  "`json:\"validation,omitempty\"`",
				Doc:      "Validation configures request/response validation against the OpenAPI schema.",
				Optional: true,
			},
			{
				Name:     "Commands",
				Type:     "[]MCPUpstreamCommandSpec",
				JSONTag:  "`json:\"commands,omitempty\"`",
				Doc:      "Commands defines command-backed MCP tools. Required when Type is \"command\".",
				Optional: true,
			},
		},
	},
	// ── ServiceRefSpec ────────────────────────────────────────────────────────────
	{
		Name: "ServiceRefSpec",
		Doc:  "ServiceRefSpec references a Kubernetes Service.",
		Fields: []fieldSpec{
			{
				Name:    "Name",
				Type:    "string",
				JSONTag: "`json:\"name\"`",
				Doc:     "Name is the name of the Service.",
			},
			{
				Name:    "Port",
				Type:    "int32",
				JSONTag: "`json:\"port\"`",
				Markers: []string{"+kubebuilder:validation:Minimum=1", "+kubebuilder:validation:Maximum=65535"},
				Doc:     "Port is the port the service exposes.",
			},
		},
	},
	// ── MCPUpstreamOpenAPISpec ────────────────────────────────────────────────────
	{
		Name: "MCPUpstreamOpenAPISpec",
		Doc:  "MCPUpstreamOpenAPISpec configures the OpenAPI spec source.",
		Fields: []fieldSpec{
			{
				Name:     "ConfigMapRef",
				Type:     "*ConfigMapKeyRef",
				JSONTag:  "`json:\"configMapRef,omitempty\"`",
				Doc:      "ConfigMapRef references a ConfigMap containing the OpenAPI spec. Mutually exclusive with URL and AutoDiscover.",
				Optional: true,
			},
			{
				Name:     "URL",
				Type:     "string",
				JSONTag:  "`json:\"url,omitempty\"`",
				Doc:      "URL is the URL from which the OpenAPI spec is fetched. Mutually exclusive with ConfigMapRef and AutoDiscover.",
				Optional: true,
			},
			{
				Name:     "AutoDiscover",
				Type:     "*AutoDiscoverSpec",
				JSONTag:  "`json:\"autoDiscover,omitempty\"`",
				Doc:      "AutoDiscover configures automatic OpenAPI spec discovery from the upstream. Mutually exclusive with ConfigMapRef and URL.",
				Optional: true,
			},
		},
	},
	// ── ConfigMapKeyRef ───────────────────────────────────────────────────────────
	{
		Name: "ConfigMapKeyRef",
		Doc:  "ConfigMapKeyRef references a specific key within a ConfigMap.",
		Fields: []fieldSpec{
			{
				Name:    "Name",
				Type:    "string",
				JSONTag: "`json:\"name\"`",
				Doc:     "Name is the name of the ConfigMap.",
			},
			{
				Name:    "Key",
				Type:    "string",
				JSONTag: "`json:\"key\"`",
				Doc:     "Key is the key in the ConfigMap data.",
			},
		},
	},
	// ── AutoDiscoverSpec ──────────────────────────────────────────────────────────
	{
		Name: "AutoDiscoverSpec",
		Doc:  "AutoDiscoverSpec configures automatic OpenAPI discovery.",
		Fields: []fieldSpec{
			{
				Name:     "Path",
				Type:     "string",
				JSONTag:  "`json:\"path,omitempty\"`",
				Doc:      "Path is the URL path at which the upstream serves its OpenAPI spec.",
				Optional: true,
			},
		},
	},
	// ── MCPUpstreamOverlaySpec ────────────────────────────────────────────────────
	{
		Name: "MCPUpstreamOverlaySpec",
		Doc:  "MCPUpstreamOverlaySpec configures an OpenAPI Overlay document.",
		Fields: []fieldSpec{
			{
				Name:     "ConfigMapRef",
				Type:     "*ConfigMapKeyRef",
				JSONTag:  "`json:\"configMapRef,omitempty\"`",
				Doc:      "ConfigMapRef references a ConfigMap containing the overlay document.",
				Optional: true,
			},
		},
	},
	// ── MCPUpstreamStatus ─────────────────────────────────────────────────────────
	{
		Name: "MCPUpstreamStatus",
		Doc:  "MCPUpstreamStatus defines the observed state of MCPUpstream.",
		Fields: []fieldSpec{
			{
				Name:     "Conditions",
				Type:     "[]metav1.Condition",
				JSONTag:  "`json:\"conditions,omitempty\"`",
				Doc:      "Conditions represents the latest available observations of the MCPUpstream state.",
				Optional: true,
			},
			{
				Name:     "AssignedProxy",
				Type:     "string",
				JSONTag:  "`json:\"assignedProxy,omitempty\"`",
				Doc:      "AssignedProxy is the name of the MCPProxy this upstream is currently assigned to.",
				Optional: true,
			},
			{
				Name:    "ToolCount",
				Type:    "int",
				JSONTag: "`json:\"toolCount,omitempty\"`",
				Doc:     "ToolCount is the number of MCP tools this upstream contributes.",
			},
		},
	},
	// ── MCPUpstreamList ───────────────────────────────────────────────────────────
	{
		Name:        "MCPUpstreamList",
		Doc:         "MCPUpstreamList contains a list of MCPUpstream.",
		TypeMarkers: []string{"+kubebuilder:object:root=true"},
		Fields: []fieldSpec{
			{Embedded: true, Type: "metav1.TypeMeta", JSONTag: "`json:\",inline\"`"},
			{Embedded: true, Type: "metav1.ListMeta", JSONTag: "`json:\"metadata,omitempty\"`"},
			{Name: "Items", Type: "[]MCPUpstream", JSONTag: "`json:\"items\"`", Doc: "Items is the list of MCPUpstream resources."},
		},
	},
}

// crdTypeSpecs is the complete mapping of config types to generated CRD spec types.
// The generator reads doc comments from the config source types where ConfigField is set.
// When ConfigField is empty or the doc is not found, the explicit Doc field is used.
var crdTypeSpecs = []typeSpec{
	// ── ProxyServerSpec ──────────────────────────────────────────────────────────
	{
		Name: "ProxyServerSpec",
		Doc:  "ProxyServerSpec configures the MCP HTTP server.",
		Src:  "ServerConfig",
		Fields: []fieldSpec{
			{
				Name:        "Port",
				ConfigField: "Port",
				Type:        "int32",
				JSONTag:     "`json:\"port,omitempty\"`",
				Markers:     []string{"+kubebuilder:validation:Minimum=1", "+kubebuilder:validation:Maximum=65535"},
				Optional:    true,
				Doc:         "Port is the port the proxy server listens on. Defaults to 8080.",
			},
			{
				Name:     "Transport",
				Type:     "[]string",
				JSONTag:  "`json:\"transport,omitempty\"`",
				Doc:      "Transport is the list of MCP transport protocols to enable (e.g. sse, streamable-http).",
				Optional: true,
			},
			{
				Name:     "TLS",
				Type:     "*ProxyTLSSpec",
				JSONTag:  "`json:\"tls,omitempty\"`",
				Doc:      "TLS configures TLS termination for the proxy server.",
				Optional: true,
			},
		},
	},
	// ── ProxyTLSSpec ─────────────────────────────────────────────────────────────
	{
		Name: "ProxyTLSSpec",
		Doc:  "ProxyTLSSpec references a Secret containing TLS credentials.",
		Fields: []fieldSpec{
			{
				Name:    "SecretName",
				Type:    "string",
				JSONTag: "`json:\"secretName\"`",
				Doc:     "SecretName is the name of the Secret containing tls.crt and tls.key.",
			},
		},
	},
	// ── ProxyNamingSpec ──────────────────────────────────────────────────────────
	{
		Name: "ProxyNamingSpec",
		Doc:  "ProxyNamingSpec configures tool name generation.",
		Src:  "NamingConfig",
		Fields: []fieldSpec{
			{
				Name:        "Separator",
				ConfigField: "Separator",
				Type:        "string",
				JSONTag:     "`json:\"separator,omitempty\"`",
				Optional:    true,
				Doc:         "Separator is the string inserted between tool name segments.",
			},
			{
				Name:        "MaxLength",
				ConfigField: "MaxLength",
				Type:        "int",
				JSONTag:     "`json:\"maxLength,omitempty\"`",
				Optional:    true,
				Doc:         "MaxLength is the maximum length of a generated tool name.",
			},
			{
				Name:        "ConflictResolution",
				ConfigField: "ConflictResolution",
				Type:        "string",
				JSONTag:     "`json:\"conflictResolution,omitempty\"`",
				Markers:     []string{"+kubebuilder:validation:Enum=error;truncate;hash"},
				Optional:    true,
				Doc:         "ConflictResolution controls how naming conflicts are resolved.",
			},
		},
	},
	// ── ProxyInboundAuthSpec ─────────────────────────────────────────────────────
	{
		Name: "ProxyInboundAuthSpec",
		Doc:  "ProxyInboundAuthSpec configures inbound authentication.",
		Src:  "InboundAuthConfig",
		Fields: []fieldSpec{
			{
				Name:    "Strategy",
				Type:    "string",
				JSONTag: "`json:\"strategy\"`",
				Markers: []string{"+kubebuilder:validation:Enum=jwt;none"},
				Doc:     "Strategy is the auth strategy: jwt|none.",
			},
			{
				Name:     "JWT",
				Type:     "*JWTAuthSpec",
				JSONTag:  "`json:\"jwt,omitempty\"`",
				Doc:      "JWT configures JWT-based inbound auth.",
				Optional: true,
			},
		},
	},
	// ── JWTAuthSpec ──────────────────────────────────────────────────────────────
	{
		Name: "JWTAuthSpec",
		Doc:  "JWTAuthSpec configures JWT Bearer token validation.",
		Src:  "JWTAuthConfig",
		Fields: []fieldSpec{
			{
				Name:    "JWKSUrl",
				Type:    "string",
				JSONTag: "`json:\"jwksUrl\"`",
				Doc:     "JWKSUrl is the URL of the JWKS endpoint.",
			},
		},
	},
	// ── ProxyTelemetrySpec ───────────────────────────────────────────────────────
	{
		Name: "ProxyTelemetrySpec",
		Doc:  "ProxyTelemetrySpec configures observability.",
		Src:  "TelemetryConfig",
		Fields: []fieldSpec{
			{
				Name:        "Enabled",
				ConfigField: "Enabled",
				Type:        "bool",
				JSONTag:     "`json:\"enabled\"`",
				Doc:         "Enabled enables telemetry export.",
			},
			{
				Name:        "OTLPEndpoint",
				ConfigField: "OTLPEndpoint",
				Type:        "string",
				JSONTag:     "`json:\"otlpEndpoint,omitempty\"`",
				Optional:    true,
				Doc:         "OTLPEndpoint is the OTLP gRPC endpoint (e.g. otel-collector:4317).",
			},
		},
	},
	// ── MCPUpstreamCommandSpec ───────────────────────────────────────────────────
	{
		Name: "MCPUpstreamCommandSpec",
		Doc:  "MCPUpstreamCommandSpec defines a single command-backed MCP tool in the CRD.",
		Src:  "CommandConfig",
		Fields: []fieldSpec{
			{
				Name:        "ToolName",
				ConfigField: "ToolName",
				Type:        "string",
				JSONTag:     "`json:\"toolName\"`",
				Doc:         "ToolName is the tool name (without prefix).",
			},
			{
				Name:        "Description",
				ConfigField: "Description",
				Type:        "string",
				JSONTag:     "`json:\"description,omitempty\"`",
				Optional:    true,
				Doc:         "Description is the human-readable tool description.",
			},
			{
				Name:        "Command",
				ConfigField: "Command",
				Type:        "string",
				JSONTag:     "`json:\"command\"`",
				Doc:         "Command is the Go text/template command string.",
			},
			{
				Name:        "Shell",
				ConfigField: "Shell",
				Type:        "bool",
				JSONTag:     "`json:\"shell,omitempty\"`",
				Optional:    true,
				Doc:         "Shell enables shell mode (sh -c) with auto-quoting. Default false.",
			},
			{
				Name:        "WorkingDir",
				ConfigField: "WorkingDir",
				Type:        "string",
				JSONTag:     "`json:\"workingDir,omitempty\"`",
				Optional:    true,
				Doc:         "WorkingDir sets the working directory for the child process.",
			},
			{
				Name:        "Timeout",
				ConfigField: "Timeout",
				Type:        "string",
				JSONTag:     "`json:\"timeout,omitempty\"`",
				Optional:    true,
				Doc:         `Timeout per execution (e.g. "30s").`,
			},
			{
				Name:        "MaxOutput",
				ConfigField: "MaxOutput",
				Type:        "int64",
				JSONTag:     "`json:\"maxOutput,omitempty\"`",
				Optional:    true,
				Doc:         "MaxOutput caps bytes captured from stdout/stderr. 0 = 1 MiB default.",
			},
			{
				Name:     "Env",
				Type:     "map[string]string",
				JSONTag:  "`json:\"env,omitempty\"`",
				Optional: true,
				Doc:      "Env is a map of additional environment variables.",
			},
			{
				Name:     "InputSchema",
				Type:     "*MCPUpstreamCommandInputSchema",
				JSONTag:  "`json:\"inputSchema,omitempty\"`",
				Optional: true,
				Doc:      "InputSchema defines the JSON Schema for tool arguments.",
			},
		},
	},
	// ── MCPUpstreamCommandInputSchema ────────────────────────────────────────────
	{
		Name: "MCPUpstreamCommandInputSchema",
		Doc:  "MCPUpstreamCommandInputSchema is the JSON Schema for a command tool's input.",
		Src:  "CommandInputSchema",
		Fields: []fieldSpec{
			{
				Name:        "Type",
				ConfigField: "Type",
				Type:        "string",
				JSONTag:     "`json:\"type,omitempty\"`",
				Optional:    true,
				Doc:         `Type is the JSON Schema type (default "object").`,
			},
			{
				Name:     "Properties",
				Type:     "map[string]MCPUpstreamCommandSchemaProperty",
				JSONTag:  "`json:\"properties,omitempty\"`",
				Optional: true,
				Doc:      "Properties defines the schema properties.",
			},
			{
				Name:        "Required",
				ConfigField: "Required",
				Type:        "[]string",
				JSONTag:     "`json:\"required,omitempty\"`",
				Optional:    true,
				Doc:         "Required lists required property names.",
			},
		},
	},
	// ── MCPUpstreamCommandSchemaProperty ─────────────────────────────────────────
	{
		Name: "MCPUpstreamCommandSchemaProperty",
		Doc:  "MCPUpstreamCommandSchemaProperty describes a single property in a command input schema.",
		Src:  "CommandSchemaProperty",
		Fields: []fieldSpec{
			{
				Name:        "Type",
				ConfigField: "Type",
				Type:        "string",
				JSONTag:     "`json:\"type,omitempty\"`",
				Optional:    true,
				Doc:         "Type is the JSON Schema type.",
			},
			{
				Name:        "Description",
				ConfigField: "Description",
				Type:        "string",
				JSONTag:     "`json:\"description,omitempty\"`",
				Optional:    true,
				Doc:         "Description is the human-readable description.",
			},
		},
	},
	// ── MCPUpstreamValidationSpec ────────────────────────────────────────────────
	{
		Name: "MCPUpstreamValidationSpec",
		Doc:  "MCPUpstreamValidationSpec configures request/response validation against the OpenAPI schema.",
		Src:  "ValidationConfig",
		Fields: []fieldSpec{
			{
				Name:        "ValidateRequest",
				ConfigField: "ValidateRequest",
				Type:        "bool",
				JSONTag:     "`json:\"validateRequest,omitempty\"`",
				Doc:         "ValidateRequest enables request validation against the OpenAPI schema.",
			},
			{
				Name:        "ValidateResponse",
				ConfigField: "ValidateResponse",
				Type:        "bool",
				JSONTag:     "`json:\"validateResponse,omitempty\"`",
				Doc:         "ValidateResponse enables response validation against the OpenAPI schema.",
			},
		},
	},
	// ── MCPUpstreamTransportSpec ─────────────────────────────────────────────────
	{
		Name: "MCPUpstreamTransportSpec",
		Doc:  "MCPUpstreamTransportSpec configures HTTP transport settings for the upstream.",
		Src:  "TransportConfig",
		Fields: []fieldSpec{
			{
				Name:        "MaxIdleConns",
				ConfigField: "MaxIdleConns",
				Type:        "int",
				JSONTag:     "`json:\"maxIdleConns,omitempty\"`",
				Optional:    true,
				Doc:         "MaxIdleConns is the maximum number of idle connections.",
			},
			{
				Name:     "TLS",
				Type:     "*UpstreamTLSSpec",
				JSONTag:  "`json:\"tls,omitempty\"`",
				Optional: true,
				Doc:      "TLS configures TLS for outbound connections.",
			},
		},
	},
	// ── UpstreamTLSSpec ──────────────────────────────────────────────────────────
	{
		Name: "UpstreamTLSSpec",
		Doc:  "UpstreamTLSSpec configures TLS for outbound connections.",
		Fields: []fieldSpec{
			{
				Name:    "SecretName",
				Type:    "string",
				JSONTag: "`json:\"secretName\"`",
				Doc:     "SecretName is the name of the Secret containing CA/client TLS credentials.",
			},
		},
	},
	// ── MCPUpstreamOutboundAuthSpec ──────────────────────────────────────────────
	{
		Name: "MCPUpstreamOutboundAuthSpec",
		Doc:  "MCPUpstreamOutboundAuthSpec configures outbound auth for the upstream.",
		Src:  "OutboundAuthConfig",
		Fields: []fieldSpec{
			{
				Name:    "Strategy",
				Type:    "string",
				JSONTag: "`json:\"strategy\"`",
				Markers: []string{"+kubebuilder:validation:Enum=bearer;oauth2_client_credentials;none"},
				Doc:     "Strategy is the outbound auth strategy: bearer|oauth2_client_credentials|none.",
			},
			{
				Name:     "Bearer",
				Type:     "*BearerSpec",
				JSONTag:  "`json:\"bearer,omitempty\"`",
				Optional: true,
				Doc:      "Bearer configures bearer token authentication.",
			},
			{
				Name:     "OAuth2",
				Type:     "*OAuth2Spec",
				JSONTag:  "`json:\"oauth2,omitempty\"`",
				Optional: true,
				Doc:      "OAuth2 configures OAuth2 client credentials flow.",
			},
		},
	},
	// ── BearerSpec ───────────────────────────────────────────────────────────────
	{
		Name: "BearerSpec",
		Doc:  "BearerSpec holds config for bearer token authentication.",
		Fields: []fieldSpec{
			{
				Name:     "SecretRef",
				Type:     "*SecretRef",
				JSONTag:  "`json:\"secretRef,omitempty\"`",
				Optional: true,
				Doc:      `SecretRef references a Secret containing the bearer token (key: "token").`,
			},
		},
	},
	// ── OAuth2Spec ───────────────────────────────────────────────────────────────
	{
		Name: "OAuth2Spec",
		Doc:  "OAuth2Spec configures OAuth2 client credentials.",
		Fields: []fieldSpec{
			{
				Name:    "TokenURL",
				Type:    "string",
				JSONTag: "`json:\"tokenURL\"`",
				Doc:     "TokenURL is the OAuth2 token endpoint.",
			},
			{
				Name:     "SecretRef",
				Type:     "*SecretRef",
				JSONTag:  "`json:\"secretRef,omitempty\"`",
				Optional: true,
				Doc:      "SecretRef references a Secret containing client_id and client_secret.",
			},
		},
	},
	// ── SecretRef ────────────────────────────────────────────────────────────────
	{
		Name: "SecretRef",
		Doc:  "SecretRef references a Kubernetes Secret.",
		Fields: []fieldSpec{
			{
				Name:    "Name",
				Type:    "string",
				JSONTag: "`json:\"name\"`",
				Doc:     "Name is the name of the Secret.",
			},
		},
	},
}

// specGenTemplate is the Go source template for spec_gen.go.
// It produces a properly annotated Go file with kubebuilder markers.
const specGenTemplate = `// Code generated by "go run ./cmd/crdgen". DO NOT EDIT.
// Source: pkg/config/config.go
// Regenerate with: make generate-crds

package v1alpha1
{{range .}}
// {{.Doc}}
type {{.Name}} struct {
{{range .Fields}}{{range .Markers}}	// {{.}}
{{end}}{{if .Optional}}	// +optional
{{end}}	// {{.Doc}}
	{{.Name}} {{.Type}} {{.JSONTag}}
{{end}}}
{{end}}`

// configDoc holds parsed doc comments from pkg/config/config.go.
// Key is "TypeName.FieldName", value is the trimmed comment text.
type configDoc map[string]string

// parseConfigDocs parses pkg/config/config.go and returns field doc comments
// keyed by "TypeName.FieldName".
func parseConfigDocs(configPath string) (configDoc, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, configPath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}

	docs := make(configDoc)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			typeName := ts.Name.Name
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					continue
				}
				var comment string
				switch {
				case field.Doc != nil:
					comment = strings.TrimSpace(field.Doc.Text())
				case field.Comment != nil:
					comment = strings.TrimSpace(field.Comment.Text())
				}
				// Trim leading "// " from first line if present.
				comment = strings.TrimPrefix(comment, "// ")
				if comment != "" {
					for _, name := range field.Names {
						key := typeName + "." + name.Name
						docs[key] = comment
					}
				}
			}
		}
	}
	return docs, nil
}

// resolvedField is a fieldSpec with the doc comment resolved from config.
type resolvedField struct {
	fieldSpec
	// ResolvedDoc is the final doc comment to use.
	ResolvedDoc string
}

// resolvedType is a typeSpec with all field docs resolved.
type resolvedType struct {
	typeSpec
	ResolvedFields []resolvedField
}

// resolveSpecs resolves doc comments from the config source file.
func resolveSpecs(configPath string) ([]resolvedType, error) {
	docs, err := parseConfigDocs(configPath)
	if err != nil {
		return nil, err
	}

	result := make([]resolvedType, 0, len(crdTypeSpecs))
	for _, ts := range crdTypeSpecs {
		rt := resolvedType{typeSpec: ts}
		for _, f := range ts.Fields {
			rf := resolvedField{fieldSpec: f}
			rf.ResolvedDoc = f.Doc
			if rf.ResolvedDoc == "" && f.ConfigField != "" && ts.Src != "" {
				key := ts.Src + "." + f.ConfigField
				if d, ok := docs[key]; ok {
					rf.ResolvedDoc = d
				}
			}
			if rf.ResolvedDoc == "" {
				rf.ResolvedDoc = f.Name + " configures " + strings.ToLower(f.Name) + "."
			}
			rt.ResolvedFields = append(rt.ResolvedFields, rf)
		}
		result = append(result, rt)
	}
	return result, nil
}

// templateData is the data passed to specGenTemplate.
type templateData struct {
	Name   string
	Doc    string
	Fields []templateFieldData
}

// templateFieldData is the field data passed to the template.
type templateFieldData struct {
	Name     string
	Type     string
	JSONTag  string
	Markers  []string
	Optional bool
	Doc      string
}

// GenerateSpecContent generates the content of spec_gen.go from the config type mapping.
// configPath is the absolute path to pkg/config/config.go.
func GenerateSpecContent(configPath string) ([]byte, error) {
	resolved, err := resolveSpecs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolving type specs: %w", err)
	}

	// Build template data.
	data := make([]templateData, 0, len(resolved))
	for _, rt := range resolved {
		td := templateData{
			Name: rt.Name,
			Doc:  rt.Doc,
		}
		for _, rf := range rt.ResolvedFields {
			td.Fields = append(td.Fields, templateFieldData{
				Name:     rf.Name,
				Type:     rf.Type,
				JSONTag:  rf.JSONTag,
				Markers:  rf.Markers,
				Optional: rf.Optional,
				Doc:      rf.ResolvedDoc,
			})
		}
		data = append(data, td)
	}

	tmpl, err := template.New("spec_gen").Parse(specGenTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing spec template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing spec template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Return unformatted on parse error so the caller can diagnose.
		return buf.Bytes(), fmt.Errorf("formatting generated code (raw output follows): %w", err)
	}
	return formatted, nil
}

// WriteSpecFile generates and writes pkg/crd/v1alpha1/spec_gen.go.
func WriteSpecFile(repoRoot string) error {
	configPath := filepath.Join(repoRoot, "pkg", "config", "config.go")
	content, err := GenerateSpecContent(configPath)
	if err != nil {
		return fmt.Errorf("generating spec content: %w", err)
	}

	outPath := filepath.Join(repoRoot, SpecGenPath)
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	return nil
}

// ValidateSpecFile checks that pkg/crd/v1alpha1/spec_gen.go matches what would be generated.
// Returns (true, nil) if up-to-date, (false, nil) if out of date, or (false, err) on errors.
func ValidateSpecFile(repoRoot string) (bool, error) {
	configPath := filepath.Join(repoRoot, "pkg", "config", "config.go")
	expected, err := GenerateSpecContent(configPath)
	if err != nil {
		return false, fmt.Errorf("generating expected spec content: %w", err)
	}

	specPath := filepath.Join(repoRoot, SpecGenPath)
	existing, err := os.ReadFile(specPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", specPath, err)
	}

	return bytes.Equal(expected, existing), nil
}

// GenerateTypesContent generates the content of types_gen.go from crdRootTypeSpecs.
func GenerateTypesContent() ([]byte, error) {
	var sb strings.Builder

	sb.WriteString("// Code generated by \"go run ./cmd/crdgen\". DO NOT EDIT.\n")
	sb.WriteString("// Regenerate with: make generate-crds\n\n")
	sb.WriteString("package v1alpha1\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\tcorev1 \"k8s.io/api/core/v1\"\n")
	sb.WriteString("\tmetav1 \"k8s.io/apimachinery/pkg/apis/meta/v1\"\n")
	sb.WriteString(")\n")

	for _, ts := range crdRootTypeSpecs {
		sb.WriteString("\n")
		for _, marker := range ts.TypeMarkers {
			sb.WriteString("// " + marker + "\n")
		}
		if len(ts.TypeMarkers) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("// " + ts.Doc + "\n")
		sb.WriteString("type " + ts.Name + " struct {\n")
		for _, f := range ts.Fields {
			if f.Embedded {
				sb.WriteString("\t" + f.Type + " " + f.JSONTag + "\n")
			} else {
				for _, marker := range f.Markers {
					sb.WriteString("\t// " + marker + "\n")
				}
				if f.Optional {
					sb.WriteString("\t// +optional\n")
				}
				if f.Doc != "" {
					sb.WriteString("\t// " + f.Doc + "\n")
				}
				sb.WriteString("\t" + f.Name + " " + f.Type + " " + f.JSONTag + "\n")
			}
		}
		sb.WriteString("}\n")
	}

	sb.WriteString("\nfunc init() {\n")
	sb.WriteString("\tSchemeBuilder.Register(&MCPProxy{}, &MCPProxyList{})\n")
	sb.WriteString("\tSchemeBuilder.Register(&MCPUpstream{}, &MCPUpstreamList{})\n")
	sb.WriteString("}\n")

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		return []byte(sb.String()), fmt.Errorf("formatting generated types (raw output follows): %w", err)
	}
	return formatted, nil
}

// WriteTypesFile generates and writes pkg/crd/v1alpha1/types_gen.go.
func WriteTypesFile(repoRoot string) error {
	content, err := GenerateTypesContent()
	if err != nil {
		return fmt.Errorf("generating types content: %w", err)
	}

	outPath := filepath.Join(repoRoot, TypesGenPath)
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	return nil
}

// ValidateTypesFile checks that pkg/crd/v1alpha1/types_gen.go matches what would be generated.
// Returns (true, nil) if up-to-date, (false, nil) if out of date, or (false, err) on errors.
func ValidateTypesFile(repoRoot string) (bool, error) {
	expected, err := GenerateTypesContent()
	if err != nil {
		return false, fmt.Errorf("generating expected types content: %w", err)
	}

	typesPath := filepath.Join(repoRoot, TypesGenPath)
	existing, err := os.ReadFile(typesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", typesPath, err)
	}

	return bytes.Equal(expected, existing), nil
}
