package openapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// PrefixedTool holds the naming metadata for a generated tool, used for conflict detection.
type PrefixedTool struct {
	PrefixedName   string
	OriginalPath   string
	OriginalMethod string
}

var (
	nonAlnumUnderscore = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
	runOfUnderscores   = regexp.MustCompile(`_+`)
)

// Slugify derives a tool base name from HTTP method and path using the given slug rules.
// Steps applied in order:
//  1. Determine method-based verb prefix
//  2. Strip the leading / from path
//  3. If rules.ReplaceBraces: remove { and }
//  4. If rules.ReplaceSlashes: replace / with _
//  5. Replace remaining non-alphanumeric, non-underscore characters with _
//  6. If rules.Lowercase: convert to lowercase
//  7. If rules.CollapseSeparators: replace runs of _ with single _
//  8. Trim leading/trailing _
//  9. Prepend the verb prefix
func Slugify(method, path string, hasPathParams bool, rules config.SlugRulesConfig) string {
	verb := slugVerb(method, hasPathParams)

	s := strings.TrimPrefix(path, "/")

	if rules.ReplaceBraces {
		s = strings.ReplaceAll(s, "{", "")
		s = strings.ReplaceAll(s, "}", "")
	}

	if rules.ReplaceSlashes {
		s = strings.ReplaceAll(s, "/", "_")
	}

	s = nonAlnumUnderscore.ReplaceAllString(s, "_")

	if rules.Lowercase {
		s = strings.ToLower(s)
	}

	if rules.CollapseSeparators {
		s = runOfUnderscores.ReplaceAllString(s, "_")
	}

	s = strings.Trim(s, "_")

	return verb + "_" + s
}

// slugVerb returns the verb prefix for the given HTTP method and whether the path has params.
func slugVerb(method string, hasPathParams bool) string {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		if !hasPathParams {
			return "list"
		}
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodDelete:
		return "delete"
	case http.MethodPatch:
		return "patch"
	default:
		return strings.ToLower(method)
	}
}

// ToolBaseName returns the base name (without upstream prefix) for a tool.
// Priority: x-mcp-tool-name extension > operationId > Slugify(method, path).
func ToolBaseName(op *openapi3.Operation, method, path string, hasPathParams bool, rules config.SlugRulesConfig) string {
	// x-mcp-tool-name override: used as-is without slugification.
	if val, ok := op.Extensions["x-mcp-tool-name"]; ok {
		if name := extractExtensionString(val); name != "" {
			return name
		}
	}

	// operationId: apply the same sanitisation rules as Slugify, without verb prefix.
	if op.OperationID != "" {
		return sanitizeIdentifier(op.OperationID, rules)
	}

	return Slugify(method, path, hasPathParams, rules)
}

// sanitizeIdentifier applies the slug sanitisation rules to an arbitrary identifier
// (such as operationId) without prepending a verb prefix.
func sanitizeIdentifier(id string, rules config.SlugRulesConfig) string {
	s := id

	if rules.ReplaceBraces {
		s = strings.ReplaceAll(s, "{", "")
		s = strings.ReplaceAll(s, "}", "")
	}

	if rules.ReplaceSlashes {
		s = strings.ReplaceAll(s, "/", "_")
	}

	s = nonAlnumUnderscore.ReplaceAllString(s, "_")

	if rules.Lowercase {
		s = strings.ToLower(s)
	}

	if rules.CollapseSeparators {
		s = runOfUnderscores.ReplaceAllString(s, "_")
	}

	return strings.Trim(s, "_")
}

// extractExtensionString extracts a string value from an OpenAPI extension.
// kin-openapi stores YAML string extensions as plain Go strings.
func extractExtensionString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		// JSON raw message — unquote properly to handle escape sequences.
		s := strings.TrimSpace(string(v))
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
		return s
	}
	return ""
}

// PrefixedName returns "{prefix}{separator}{baseName}", truncating baseName so
// the total length does not exceed maxLength. If prefix+separator alone meets or
// exceeds maxLength, the result is truncated to maxLength runes (no baseName appended).
// If maxLength is 0, no truncation is applied.
func PrefixedName(baseName, prefix, separator string, maxLength int) string {
	if maxLength > 0 {
		prefixPart := prefix + separator
		prefixRunes := []rune(prefixPart)
		if len(prefixRunes) >= maxLength {
			// prefix+separator alone meets or exceeds the limit — truncate to maxLength.
			return string(prefixRunes[:maxLength])
		}
		allowedBase := maxLength - len(prefixRunes)
		runes := []rune(baseName)
		if len(runes) > allowedBase {
			baseName = string(runes[:allowedBase])
		}
	}
	return prefix + separator + baseName
}

// TruncateDescription truncates desc to at most maxLength runes, appending
// suffix when truncation occurs. If suffix alone is longer than maxLength, the
// suffix is itself clipped to maxLength. If maxLength is 0 or negative, desc is
// returned unchanged.
func TruncateDescription(desc string, maxLength int, suffix string) string {
	descRunes := []rune(desc)
	if maxLength <= 0 || len(descRunes) <= maxLength {
		return desc
	}
	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= maxLength {
		return string(suffixRunes[:maxLength])
	}
	cutAt := maxLength - len(suffixRunes)
	return string(descRunes[:cutAt]) + suffix
}

// DetectConflicts checks for duplicate PrefixedName values in tools and applies
// the given resolution strategy:
//   - "error": returns an error listing all conflicting names
//   - "first_wins": keeps the first occurrence, drops duplicates (logs WARN per dropped tool)
//   - "skip": drops all tools involved in any conflict (logs WARN per dropped tool)
func DetectConflicts(tools []PrefixedTool, resolution string) ([]PrefixedTool, error) {
	// Count occurrences of each prefixed name.
	counts := make(map[string]int, len(tools))
	for _, t := range tools {
		counts[t.PrefixedName]++
	}

	// Collect conflicting names.
	conflicts := make(map[string]bool)
	for name, count := range counts {
		if count > 1 {
			conflicts[name] = true
		}
	}

	if len(conflicts) == 0 {
		return tools, nil
	}

	switch resolution {
	case "error":
		names := make([]string, 0, len(conflicts))
		for name := range conflicts {
			names = append(names, name)
		}
		return nil, fmt.Errorf("tool name conflicts detected: %v", names)

	case "first_wins":
		result := make([]PrefixedTool, 0, len(tools))
		seen := make(map[string]bool)
		for _, t := range tools {
			if conflicts[t.PrefixedName] {
				if seen[t.PrefixedName] {
					slog.Warn("dropping duplicate tool (first_wins)",
						"tool", t.PrefixedName,
						"path", t.OriginalPath,
						"method", t.OriginalMethod,
					)
					continue
				}
				seen[t.PrefixedName] = true
			}
			result = append(result, t)
		}
		return result, nil

	case "skip":
		result := make([]PrefixedTool, 0, len(tools))
		for _, t := range tools {
			if conflicts[t.PrefixedName] {
				slog.Warn("skipping conflicting tool (skip)",
					"tool", t.PrefixedName,
					"path", t.OriginalPath,
					"method", t.OriginalMethod,
				)
				continue
			}
			result = append(result, t)
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unknown conflict_resolution %q: must be one of error, first_wins, skip", resolution)
	}
}
