// Package command provides the command-backed upstream builder and executor for mcp-anything.
// It implements the upstream.Builder interface for type "command" upstreams.
// Call Register() at startup to register the builder with the pkg/upstream global registry.
package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/transform"
)

// DefaultMaxOutputBytes is the maximum bytes captured from stdout or stderr when
// MaxOutput is not configured.
const DefaultMaxOutputBytes = 1 << 20 // 1 MiB

// Def holds the runtime definition for a command-backed MCP tool.
type Def struct {
	// Command is the Go text/template command string.
	// In non-shell mode it is split on whitespace into tokens, each rendered independently.
	// In shell mode the entire string is rendered and passed to sh -c.
	Command string

	// Env is a map of additional environment variables. Values support ${ENV_VAR} expansion.
	Env map[string]string

	// WorkingDir sets the working directory for the child process.
	WorkingDir string

	// Timeout is applied per-execution via context.WithTimeout.
	// A zero value means no additional timeout (the caller's context applies).
	Timeout time.Duration

	// MaxOutput caps the number of bytes captured from stdout and stderr individually.
	// A zero or negative value uses DefaultMaxOutputBytes.
	MaxOutput int64

	// Shell causes the command to run via "sh -c" with all template values shell-quoted.
	// When false (default), the command is tokenised and executed directly via exec.Command,
	// which is inherently safe against shell-injection attacks.
	Shell bool
}

// Execute runs the command with the provided MCP tool arguments.
// In non-shell mode, template values are interpolated verbatim into individual argument
// tokens, which are then passed directly to the OS — no shell injection is possible.
// In shell mode, all string template values are automatically shell-quoted before the
// rendered command string is passed to "sh -c".
//
// It returns stdout bytes, stderr bytes, and any error (including non-zero exit codes).
func (d *Def) Execute(ctx context.Context, args map[string]any) ([]byte, []byte, error) {
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}

	cmd, err := d.buildCmd(ctx, args)
	if err != nil {
		return nil, nil, fmt.Errorf("building command: %w", err)
	}

	// Inherit the process environment and apply overrides.
	cmd.Env = os.Environ()
	for k, v := range d.Env {
		expanded, expandErr := expandBraced(v)
		if expandErr != nil {
			return nil, nil, fmt.Errorf("expanding env var %q: %w", k, expandErr)
		}
		cmd.Env = append(cmd.Env, k+"="+expanded)
	}

	if d.WorkingDir != "" {
		cmd.Dir = d.WorkingDir
	}

	maxOut := d.MaxOutput
	if maxOut <= 0 {
		maxOut = DefaultMaxOutputBytes
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, remaining: maxOut}
	cmd.Stderr = &limitWriter{w: &stderr, remaining: maxOut}

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// buildCmd constructs the exec.Cmd for the given arguments.
func (d *Def) buildCmd(ctx context.Context, args map[string]any) (*exec.Cmd, error) {
	if d.Shell {
		rendered, err := renderShellTemplate(d.Command, args)
		if err != nil {
			return nil, fmt.Errorf("rendering shell command template: %w", err)
		}
		return exec.CommandContext(ctx, "sh", "-c", rendered), nil
	}

	// Non-shell mode: split on whitespace and render each token independently.
	tokens := strings.Fields(d.Command)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	rendered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		r, err := renderToken(tok, args)
		if err != nil {
			return nil, fmt.Errorf("rendering token %q: %w", tok, err)
		}
		rendered = append(rendered, r)
	}
	return exec.CommandContext(ctx, rendered[0], rendered[1:]...), nil
}

// Tool holds a command tool's MCP metadata and execution definition.
// It is the command-equivalent of openapi.ValidatedTool and is consumed by
// Builder.Build() to construct RegistryEntry objects.
type Tool struct {
	PrefixedName string
	OriginalName string
	MCPTool      *sdkmcp.Tool
	Def          *Def
	Transforms   *transform.CompiledTransforms
}

// BuildTools converts a slice of CommandConfig entries into Tool descriptors.
// It validates each entry (non-empty tool_name and command, parseable template)
// and returns an error if any entry is invalid.
func BuildTools(cfgs []config.CommandConfig, upstreamCfg *config.UpstreamConfig, namingCfg *config.NamingConfig) ([]*Tool, error) {
	sep := namingCfg.Separator
	prefix := upstreamCfg.ToolPrefix

	tools := make([]*Tool, 0, len(cfgs))
	seenNames := make(map[string]bool, len(cfgs))

	for i, cfg := range cfgs {
		if cfg.ToolName == "" {
			return nil, fmt.Errorf("command[%d]: tool_name is required", i)
		}
		if cfg.Command == "" {
			return nil, fmt.Errorf("command %q: command string is required", cfg.ToolName)
		}

		// Validate that the command template is parseable.
		if err := validateTemplate(cfg.Command); err != nil {
			return nil, fmt.Errorf("command %q: invalid command template: %w", cfg.ToolName, err)
		}
		// In non-shell mode, validate that whitespace-splitting produces complete tokens.
		if !cfg.Shell {
			if err := validateNonShellTokens(cfg.Command); err != nil {
				return nil, fmt.Errorf("command %q: %w", cfg.ToolName, err)
			}
		}

		prefixedName := prefix + sep + cfg.ToolName
		if seenNames[prefixedName] {
			return nil, fmt.Errorf("duplicate tool_name %q in command upstream %q", cfg.ToolName, upstreamCfg.Name)
		}
		seenNames[prefixedName] = true

		inputSchema := buildJSONSchema(cfg.InputSchema)

		mcpTool := &sdkmcp.Tool{
			Name:        prefixedName,
			Description: cfg.Description,
			InputSchema: inputSchema,
		}

		def := &Def{
			Command:    cfg.Command,
			Env:        cfg.Env,
			WorkingDir: cfg.WorkingDir,
			Timeout:    cfg.Timeout,
			MaxOutput:  cfg.MaxOutput,
			Shell:      cfg.Shell,
		}

		// Compile identity response and error transforms (no request transform needed —
		// args are interpolated directly into the command template).
		compiled, err := transform.Compile(
			transform.DefaultResponseExpr,
			transform.DefaultResponseExpr,
			transform.DefaultErrorExpr,
		)
		if err != nil {
			return nil, fmt.Errorf("command %q: compiling transforms: %w", cfg.ToolName, err)
		}

		tools = append(tools, &Tool{
			PrefixedName: prefixedName,
			OriginalName: cfg.ToolName,
			MCPTool:      mcpTool,
			Def:          def,
			Transforms:   compiled,
		})
	}

	return tools, nil
}

// buildJSONSchema converts a CommandInputSchema config into a jsonschema.Schema.
func buildJSONSchema(s config.CommandInputSchema) *jsonschema.Schema {
	schemaType := s.Type
	if schemaType == "" {
		schemaType = "object"
	}
	schema := &jsonschema.Schema{
		Type:     schemaType,
		Required: s.Required,
	}
	if len(s.Properties) > 0 {
		schema.Properties = make(map[string]*jsonschema.Schema, len(s.Properties))
		for name, prop := range s.Properties {
			p := &jsonschema.Schema{}
			if prop.Type != "" {
				p.Type = prop.Type
			}
			if prop.Description != "" {
				p.Description = prop.Description
			}
			schema.Properties[name] = p
		}
	}
	return schema
}

// validateTemplate checks that the command template is parseable without executing it.
func validateTemplate(tmplStr string) error {
	_, err := template.New("").Parse(tmplStr)
	return err
}

// validateNonShellTokens checks that the command, when split on whitespace, produces
// complete argument tokens. It rejects commands with spaced template delimiters
// (e.g. "{{ .msg }}" splits into fragments) which strings.Fields cannot handle.
func validateNonShellTokens(command string) error {
	for _, tok := range strings.Fields(command) {
		hasOpen := strings.HasPrefix(tok, "{{")
		hasClose := strings.HasSuffix(tok, "}}")
		if tok == "{{" || (hasOpen && !hasClose) {
			return fmt.Errorf("template delimiter fragment %q in non-shell command; use {{.var}} without spaces or enable shell: true", tok)
		}
		if hasClose && !hasOpen {
			return fmt.Errorf("dangling \"}}\" in token %q; template delimiters must not span whitespace in non-shell mode", tok)
		}
	}
	return nil
}

// expandBraced expands ${VAR} references in value using the process environment.
// It returns an error if plain $VAR (unbraced) syntax is used, or if a ${VAR}
// reference resolves to an unset variable. This enforces the project's ${ENV_VAR} convention.
func expandBraced(value string) (string, error) {
	if !strings.ContainsRune(value, '$') {
		return value, nil // fast path: no expansion needed
	}
	var result strings.Builder
	i := 0
	for i < len(value) {
		if value[i] != '$' {
			result.WriteByte(value[i])
			i++
			continue
		}
		// Found '$' — check what follows.
		if i+1 >= len(value) {
			result.WriteByte('$')
			i++
			continue
		}
		if value[i+1] != '{' {
			return "", fmt.Errorf("unbraced $VAR syntax at position %d; use ${VAR} instead", i)
		}
		// ${VAR} — find the closing '}'.
		rest := value[i+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return "", fmt.Errorf("unclosed ${ in env value")
		}
		varName := rest[:end]
		if varName == "" {
			return "", fmt.Errorf("empty variable name in ${}")
		}
		expanded, ok := os.LookupEnv(varName)
		if !ok {
			return "", fmt.Errorf("env var ${%s} is not set", varName)
		}
		result.WriteString(expanded)
		i += 2 + end + 1 // skip past closing '}'
	}
	return result.String(), nil
}

// renderToken renders a single template token (no shell quoting — used in direct exec mode).
func renderToken(tmplStr string, args map[string]any) (string, error) {
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, args); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return buf.String(), nil
}

// shellSafeString is a string type whose fmt.Stringer implementation returns
// the shell-quoted form. When used as a template variable, the template engine
// calls String() automatically, producing a safely-quoted shell argument.
type shellSafeString string

func (s shellSafeString) String() string {
	return shellQuote(string(s))
}

// shellSafeValue recursively wraps all string values in v with shellSafeString
// so that nested strings in maps and slices are also shell-quoted on interpolation.
func shellSafeValue(v any) any {
	switch val := v.(type) {
	case string:
		return shellSafeString(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = shellSafeValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = shellSafeValue(vv)
		}
		return out
	default:
		return v
	}
}

// renderShellTemplate renders the command template for shell execution.
// All string values in args are wrapped recursively as shellSafeString so that
// the template engine automatically shell-quotes them when interpolated,
// including strings nested inside maps and slices.
func renderShellTemplate(tmplStr string, args map[string]any) (string, error) {
	safeArgs := make(map[string]any, len(args))
	for k, v := range args {
		safeArgs[k] = shellSafeValue(v)
	}
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, safeArgs); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return buf.String(), nil
}

// shellQuote wraps s in single quotes, escaping embedded single quotes.
// The result is safe for inclusion in a shell command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ToTextResult converts command stdout into a success CallToolResult.
// It attempts to pretty-print the output if it is valid JSON; otherwise
// the raw output is returned as-is.
func ToTextResult(stdout []byte) *sdkmcp.CallToolResult {
	text := strings.TrimRight(string(stdout), "\n")
	if len(text) == 0 {
		text = string(stdout)
	}

	// Try to re-encode JSON output for consistent formatting.
	var v any
	if json.Unmarshal(stdout, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			text = string(b)
		}
	}

	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// ToErrorResult converts a command failure into an error CallToolResult.
func ToErrorResult(stderr []byte, execErr error) *sdkmcp.CallToolResult {
	msg := strings.TrimRight(string(stderr), "\n")
	if msg == "" {
		msg = execErr.Error()
	}
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: msg},
		},
	}
}

// limitWriter wraps an io.Writer and silently discards bytes once the limit is reached.
type limitWriter struct {
	w         io.Writer
	remaining int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	origLen := len(p)
	if lw.remaining <= 0 {
		return origLen, nil // silently discard
	}
	n := int64(origLen)
	if n > lw.remaining {
		p = p[:lw.remaining]
	}
	written, err := lw.w.Write(p)
	lw.remaining -= int64(written)
	// Report the original length to the caller so exec.Cmd does not see a short write.
	return origLen, err
}
