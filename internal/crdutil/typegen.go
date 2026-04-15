// Package crdutil generates CRD spec types from proxy/upstream config type definitions.
//
// The generator reads pkg/config/config.go using the Go AST and produces
// pkg/crd/v1alpha1/spec_gen.go containing mirror types with JSON tags
// instead of koanf tags. Doc comments are inherited from the config types.
//
// To add a new generated type: add an entry to SpecMapping and re-run
// "make generate-crds".
package crdutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
)

// HashBytes returns the SHA-256 hex digest of b.
// Use this instead of bytes.Equal when comparing generated vs on-disk file
// content so that the comparison does not require holding both copies in memory
// simultaneously and the diagnostic message can include the digest values.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SpecGenPath is the path (relative to repo root) of the generated spec types file.
const SpecGenPath = "pkg/crd/v1alpha1/spec_gen.go"

// SpecMapping defines which config types in pkg/config/config.go are
// mirrored as CRD spec types in pkg/crd/v1alpha1/spec_gen.go.
// Order determines the order of type declarations in the generated file.
//
// Rules for adding an entry:
//   - All non-skipped fields must resolve to either a Go builtin, time.Duration
//     (which becomes string), or another type already in SpecMapping.
//   - Fields tagged with koanf:"-" are automatically skipped.
//   - To add a new generated type: add the entry here and run "make generate-crds".
var SpecMapping = []struct {
	ConfigType string // exported type name in pkg/config/config.go
	CRDType    string // generated type name in spec_gen.go
}{
	{"JWTAuthConfig", "JWTAuthSpec"},
	{"NamingConfig", "ProxyNamingSpec"},
	{"SlugRulesConfig", "ProxySlugRulesSpec"},
	{"TelemetryConfig", "ProxyTelemetrySpec"},
	{"CommandConfig", "MCPUpstreamCommandSpec"},
	{"CommandInputSchema", "MCPUpstreamCommandInputSchema"},
	{"CommandSchemaProperty", "MCPUpstreamCommandSchemaProperty"},
	{"ValidationConfig", "MCPUpstreamValidationSpec"},
}

// generatedField is a single field in a generated struct.
type generatedField struct {
	Name    string // Go field name (same as config)
	Type    string // Go type string (name mapping applied, time.Duration → string)
	JSONTag string // JSON field name (camelCase from koanf tag)
	Doc     string // doc comment text (trimmed); empty means no comment
}

// generatedType is a generated struct declaration.
type generatedType struct {
	Name   string
	Doc    string // type-level doc comment from config
	Fields []generatedField
}

// GenerateSpecContent parses configPath and returns the Go source for spec_gen.go.
func GenerateSpecContent(configPath string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, configPath, nil, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", configPath, err)
	}

	// Build reverse map: configTypeName → crdTypeName.
	nameMap := make(map[string]string, len(SpecMapping))
	for _, e := range SpecMapping {
		nameMap[e.ConfigType] = e.CRDType
	}

	// Index all struct types declared in the file.
	typeIndex := buildTypeIndex(f)

	var types []generatedType
	for _, entry := range SpecMapping {
		ts, ok := typeIndex[entry.ConfigType]
		if !ok {
			return "", fmt.Errorf("type %q not found in %s", entry.ConfigType, configPath)
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return "", fmt.Errorf("type %q is not a struct in %s", entry.ConfigType, configPath)
		}

		gt := generatedType{
			Name: entry.CRDType,
			Doc:  extractTypeDoc(ts),
		}
		for _, field := range st.Fields.List {
			gf, err := convertField(field, nameMap)
			if err != nil {
				return "", fmt.Errorf("field in %s: %w", entry.ConfigType, err)
			}
			if gf != nil {
				gt.Fields = append(gt.Fields, *gf)
			}
		}
		types = append(types, gt)
	}

	return renderSpecContent(types)
}

// WriteSpecFile generates spec_gen.go from pkg/config/config.go.
func WriteSpecFile(repoRoot string) error {
	configPath := filepath.Join(repoRoot, "pkg", "config", "config.go")
	content, err := GenerateSpecContent(configPath)
	if err != nil {
		return fmt.Errorf("generating spec content: %w", err)
	}
	return os.WriteFile(filepath.Join(repoRoot, SpecGenPath), []byte(content), 0o644)
}

// ValidateSpecFile checks that spec_gen.go is up-to-date with config.go.
// Returns (true, nil) if up-to-date, (false, nil) if stale, (false, err) on error.
func ValidateSpecFile(repoRoot string) (bool, error) {
	configPath := filepath.Join(repoRoot, "pkg", "config", "config.go")
	want, err := GenerateSpecContent(configPath)
	if err != nil {
		return false, fmt.Errorf("generating expected content: %w", err)
	}
	got, err := os.ReadFile(filepath.Join(repoRoot, SpecGenPath))
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", SpecGenPath, err)
	}
	return HashBytes([]byte(want)) == HashBytes(got), nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// buildTypeIndex returns a map from type name to *ast.TypeSpec for all
// named types declared in f, attaching the GenDecl doc when the TypeSpec
// has no own doc.
func buildTypeIndex(f *ast.File) map[string]*ast.TypeSpec {
	idx := make(map[string]*ast.TypeSpec)
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			if ts.Doc == nil {
				ts.Doc = gd.Doc
			}
			idx[ts.Name.Name] = ts
		}
	}
	return idx
}

// extractTypeDoc returns the trimmed doc comment for a TypeSpec.
func extractTypeDoc(ts *ast.TypeSpec) string {
	if ts.Doc == nil {
		return ""
	}
	return strings.TrimSpace(ts.Doc.Text())
}

// convertField converts an AST struct field to a generatedField.
// Returns nil for skipped fields (koanf:"-", koanf:"", or embedded).
func convertField(field *ast.Field, nameMap map[string]string) (*generatedField, error) {
	// Embedded fields (no names) don't appear in our config types.
	if len(field.Names) == 0 {
		return nil, nil
	}

	// Parse the koanf tag to determine the JSON field name.
	koanfVal := ""
	if field.Tag != nil {
		koanfVal = reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("koanf")
	}
	// Skip programmatic-only and untagged fields.
	if koanfVal == "-" || koanfVal == "" {
		return nil, nil
	}

	// Convert the Go type expression, applying the name map.
	goType, err := convertTypeExpr(field.Type, nameMap)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field.Names[0].Name, err)
	}

	return &generatedField{
		Name:    field.Names[0].Name,
		Type:    goType,
		JSONTag: snakeToCamel(koanfVal),
		Doc:     extractFieldDoc(field),
	}, nil
}

// convertTypeExpr converts an AST type expression to its Go string representation,
// applying nameMap to rename config types → CRD types, and time.Duration → string.
func convertTypeExpr(expr ast.Expr, nameMap map[string]string) (string, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		// Check the name map first (config type → CRD type rename).
		if mapped, ok := nameMap[t.Name]; ok {
			return mapped, nil
		}
		// Uppercase identifiers not in the map are config types that should
		// have been added to SpecMapping. Lowercase identifiers are Go builtins.
		if len(t.Name) > 0 && unicode.IsUpper(rune(t.Name[0])) {
			return "", fmt.Errorf(
				"type %q is not in SpecMapping — add it to internal/crdutil/typegen.go and re-run make generate-crds",
				t.Name,
			)
		}
		return t.Name, nil

	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf("unexpected selector expression")
		}
		// time.Duration becomes string in CRD types.
		if pkg.Name == "time" && t.Sel.Name == "Duration" {
			return "string", nil
		}
		// Other package-qualified types (e.g. metav1.LabelSelector) are passed through.
		return pkg.Name + "." + t.Sel.Name, nil

	case *ast.StarExpr:
		inner, err := convertTypeExpr(t.X, nameMap)
		if err != nil {
			return "", err
		}
		return "*" + inner, nil

	case *ast.ArrayType:
		inner, err := convertTypeExpr(t.Elt, nameMap)
		if err != nil {
			return "", err
		}
		return "[]" + inner, nil

	case *ast.MapType:
		key, err := convertTypeExpr(t.Key, nameMap)
		if err != nil {
			return "", err
		}
		val, err := convertTypeExpr(t.Value, nameMap)
		if err != nil {
			return "", err
		}
		return "map[" + key + "]" + val, nil

	default:
		return "", fmt.Errorf("unsupported type expression %T", expr)
	}
}

// extractFieldDoc returns the trimmed doc comment for a struct field.
// It prefers the block comment before the field; falls back to the inline comment.
func extractFieldDoc(field *ast.Field) string {
	if field.Doc != nil {
		return strings.TrimSpace(field.Doc.Text())
	}
	if field.Comment != nil {
		return strings.TrimSpace(field.Comment.Text())
	}
	return ""
}

// snakeToCamel converts snake_case to camelCase (e.g. "tool_name" → "toolName").
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			r := []rune(parts[i])
			r[0] = unicode.ToUpper(r[0])
			parts[i] = string(r)
		}
	}
	return strings.Join(parts, "")
}

// renderSpecContent renders a list of generatedType values to a formatted
// Go source file.
func renderSpecContent(types []generatedType) (string, error) {
	var sb strings.Builder

	sb.WriteString(`// Code generated by "go run ./cmd/crdgen". DO NOT EDIT.
// Source: pkg/config/config.go
// Regenerate with: make generate-crds

package v1alpha1
`)

	for _, gt := range types {
		sb.WriteString("\n")

		// Type-level doc comment.
		if gt.Doc != "" {
			for _, line := range strings.Split(gt.Doc, "\n") {
				if line == "" {
					sb.WriteString("//\n")
				} else {
					sb.WriteString("// " + line + "\n")
				}
			}
		}

		sb.WriteString("type " + gt.Name + " struct {\n")
		for _, f := range gt.Fields {
			// Every generated field is optional (omitempty in JSON tag).
			sb.WriteString("\t// +optional\n")
			if f.Doc != "" {
				for _, line := range strings.Split(f.Doc, "\n") {
					if line == "" {
						sb.WriteString("\t//\n")
					} else {
						sb.WriteString("\t// " + line + "\n")
					}
				}
			}
			sb.WriteString(fmt.Sprintf("\t%s %s `json:\"%s,omitempty\"`\n", f.Name, f.Type, f.JSONTag))
		}
		sb.WriteString("}\n")
	}

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		return "", fmt.Errorf("formatting generated code: %w\nraw source:\n%s", err, sb.String())
	}
	return string(formatted), nil
}
