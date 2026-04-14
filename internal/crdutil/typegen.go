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

// SpecGenPath is the relative path (from repo root) of the generated types file.
const SpecGenPath = "pkg/crd/v1alpha1/spec_gen.go"

// typeSpec defines a CRD spec type to generate from a config type.
type typeSpec struct {
	Name   string      // generated Go type name
	Doc    string      // type-level doc comment
	Src    string      // source config type name (for doc comment lookup)
	Fields []fieldSpec // fields to include in the generated type
}

// fieldSpec defines a single field in a generated CRD type.
type fieldSpec struct {
	Name        string   // Go field name in the generated struct
	ConfigField string   // config struct field name (used to inherit doc comments)
	Type        string   // Go type string
	JSONTag     string   // full JSON struct tag, e.g. `json:"port,omitempty"`
	Markers     []string // kubebuilder markers (written as // +kubebuilder:... comments)
	Optional    bool     // if true, add // +optional marker
	Doc         string   // explicit doc comment (overrides the config field comment)
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
