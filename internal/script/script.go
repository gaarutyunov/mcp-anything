// Package script implements JavaScript-backed MCP tool execution via Grafana Sobek.
// It provides the Def type for script execution, and BuildTools for converting
// ScriptConfig entries into RegistryEntry-compatible tool descriptors.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/grafana/sobek"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/transform"
)

// defaultFetchTimeout is the HTTP timeout used by ctx.fetch() when the script has no timeout set.
const defaultFetchTimeout = 30 * time.Second

// Def holds the runtime definition for a JavaScript-backed MCP tool.
// It is immutable after construction and safe for concurrent use — each Execute
// call creates its own sobek.Runtime (Sobek is not goroutine-safe).
type Def struct {
	// Program is the pre-compiled Sobek bytecode for the script.
	Program *sobek.Program

	// Timeout is applied per-execution via vm.Interrupt.
	// A zero value means no additional timeout (the caller's context applies).
	Timeout time.Duration

	// Env is a map of environment variables exposed to the script via ctx.env.
	Env map[string]string

	// HTTPClient is used by ctx.fetch(). Must have a timeout set.
	HTTPClient *http.Client

	// Pool bounds the number of concurrent JS runtimes for this script upstream.
	// Acquire blocks if all slots are in use, preventing OOM under high concurrency.
	Pool *runtime.Pool
}

// Execute runs the JavaScript script with the provided MCP tool arguments.
// A fresh sobek.Runtime is created for each execution (Sobek is not goroutine-safe).
// The script must assign a callable function to the global __mcp_default__ variable
// (handled by CompileScript wrapping).
//
// Returns the JSON-serialised script return value and any error.
// JavaScript exceptions are returned as errors.
func (d *Def) Execute(ctx context.Context, args map[string]any) ([]byte, error) {
	release, err := d.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("script: %w", err)
	}
	defer release()

	rt := sobek.New()

	// Interrupt the runtime after the timeout deadline, from a separate goroutine.
	var interruptTimer *time.Timer
	if d.Timeout > 0 {
		interruptTimer = time.AfterFunc(d.Timeout, func() {
			rt.Interrupt("script execution timed out")
		})
		defer interruptTimer.Stop()
	}

	// Set up the sandbox: ctx.fetch, ctx.env, ctx.log, console.
	ctxObj := d.buildContextObject(ctx, rt)
	if err := rt.Set("__mcp_ctx__", ctxObj); err != nil {
		return nil, fmt.Errorf("setting context object: %w", err)
	}

	// Run the pre-compiled program. This defines __mcp_default__ in the runtime.
	if _, err := rt.RunProgram(d.Program); err != nil {
		return nil, fmt.Errorf("running script: %w", err)
	}

	// Extract and call the default export function.
	mainFn, ok := sobek.AssertFunction(rt.Get("__mcp_default__"))
	if !ok {
		return nil, fmt.Errorf("script does not export a callable default function (assign to module.exports or use 'export default')")
	}

	argsVal := rt.ToValue(args)
	result, err := mainFn(sobek.Undefined(), argsVal, rt.Get("__mcp_ctx__"))
	if err != nil {
		return nil, fmt.Errorf("script execution failed: %w", err)
	}

	exported := result.Export()
	out, err := json.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("marshalling script result: %w", err)
	}
	return out, nil
}

// buildContextObject constructs the JS ctx object exposed to scripts.
// It provides ctx.fetch(url, opts), ctx.env, and ctx.log(level, msg).
func (d *Def) buildContextObject(ctx context.Context, rt *sobek.Runtime) *sobek.Object {
	ctxObj := rt.NewObject()

	// ctx.env — read-only environment variables for this script.
	envObj := rt.NewObject()
	for k, v := range d.Env {
		expanded := os.ExpandEnv(v)
		if err := envObj.Set(k, expanded); err != nil {
			slog.Warn("script: failed to set env var in JS context", "key", k, "error", err)
		}
	}
	if err := ctxObj.Set("env", envObj); err != nil {
		slog.Warn("script: failed to set ctx.env", "error", err)
	}

	// ctx.log(level, msg) — structured logging.
	if err := ctxObj.Set("log", func(level, msg string) {
		switch strings.ToLower(level) {
		case "debug":
			slog.Debug(msg)
		case "warn", "warning":
			slog.Warn(msg)
		case "error":
			slog.Error(msg)
		default:
			slog.Info(msg)
		}
	}); err != nil {
		slog.Warn("script: failed to set ctx.log", "error", err)
	}

	// ctx.fetch(url, opts) — sandboxed HTTP client.
	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultFetchTimeout}
	}
	if err := ctxObj.Set("fetch", func(call sobek.FunctionCall) sobek.Value {
		return d.jsFetch(ctx, rt, httpClient, call)
	}); err != nil {
		slog.Warn("script: failed to set ctx.fetch", "error", err)
	}

	// console.log/warn/error — map to slog.
	consoleObj := rt.NewObject()
	makeConsoleLogger := func(logFn func(msg string, args ...any)) func(call sobek.FunctionCall) sobek.Value {
		return func(call sobek.FunctionCall) sobek.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, arg := range call.Arguments {
				parts = append(parts, arg.String())
			}
			logFn(strings.Join(parts, " "))
			return sobek.Undefined()
		}
	}
	_ = consoleObj.Set("log", makeConsoleLogger(func(msg string, _ ...any) { slog.Info(msg) }))
	_ = consoleObj.Set("warn", makeConsoleLogger(func(msg string, _ ...any) { slog.Warn(msg) }))
	_ = consoleObj.Set("error", makeConsoleLogger(func(msg string, _ ...any) { slog.Error(msg) }))
	_ = consoleObj.Set("debug", makeConsoleLogger(func(msg string, _ ...any) { slog.Debug(msg) }))
	if err := rt.Set("console", consoleObj); err != nil {
		slog.Warn("script: failed to set console", "error", err)
	}

	return ctxObj
}

// jsFetch implements ctx.fetch(url, opts) for scripts.
// opts is optional; supported fields: method (string), headers (object), body (string).
// On success, returns the parsed JSON response (or raw text if not JSON).
// Throws a JS exception on HTTP or network errors.
func (d *Def) jsFetch(ctx context.Context, rt *sobek.Runtime, client *http.Client, call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) == 0 {
		panic(rt.NewTypeError("ctx.fetch requires a URL argument"))
	}

	rawURL := call.Arguments[0].String()
	method := "GET"
	var bodyReader io.Reader
	headers := map[string]string{}

	// Parse optional opts argument.
	if len(call.Arguments) >= 2 {
		opts := call.Arguments[1].ToObject(rt)
		if m := opts.Get("method"); m != nil && !sobek.IsUndefined(m) && !sobek.IsNull(m) {
			method = strings.ToUpper(m.String())
		}
		if h := opts.Get("headers"); h != nil && !sobek.IsUndefined(h) && !sobek.IsNull(h) {
			headersObj := h.ToObject(rt)
			for _, k := range headersObj.Keys() {
				headers[k] = headersObj.Get(k).String()
			}
		}
		if b := opts.Get("body"); b != nil && !sobek.IsUndefined(b) && !sobek.IsNull(b) {
			bodyReader = strings.NewReader(b.String())
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: creating request: %w", err)))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: %w", err)))
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("script: closing fetch response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: reading response body: %w", err)))
	}

	if resp.StatusCode >= 400 {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))))
	}

	// Try to parse as JSON; fall back to raw string.
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		return rt.ToValue(parsed)
	}
	return rt.ToValue(string(body))
}

// Tool holds a script tool's MCP metadata and execution definition.
// It is the script-equivalent of command.Tool.
type Tool struct {
	PrefixedName string
	OriginalName string
	MCPTool      *sdkmcp.Tool
	Def          *Def
	Transforms   *transform.CompiledTransforms
}

// BuildTools converts a slice of ScriptConfig entries into Tool descriptors.
// It validates each entry (non-empty tool_name and script_path, readable file,
// parseable script) and returns an error if any entry is invalid.
// httpClient is used by ctx.fetch() in the script execution; if nil, a default
// client with a 30s timeout is used.
// pool bounds the number of concurrent JS runtimes; all tools in the upstream
// share the same pool so the limit applies per upstream, not per tool.
func BuildTools(cfgs []config.ScriptConfig, upstreamCfg *config.UpstreamConfig, namingCfg *config.NamingConfig, httpClient *http.Client, pool *runtime.Pool) ([]*Tool, error) {
	sep := namingCfg.Separator
	prefix := upstreamCfg.ToolPrefix

	tools := make([]*Tool, 0, len(cfgs))
	seenNames := make(map[string]bool, len(cfgs))

	for i, cfg := range cfgs {
		if cfg.ToolName == "" {
			return nil, fmt.Errorf("script[%d]: tool_name is required", i)
		}
		if cfg.ScriptPath == "" {
			return nil, fmt.Errorf("script %q: script_path is required", cfg.ToolName)
		}

		src, err := os.ReadFile(cfg.ScriptPath)
		if err != nil {
			return nil, fmt.Errorf("script %q: reading script file %q: %w", cfg.ToolName, cfg.ScriptPath, err)
		}

		prog, err := CompileScript(cfg.ToolName, string(src))
		if err != nil {
			return nil, fmt.Errorf("script %q: compiling script: %w", cfg.ToolName, err)
		}

		prefixedName := prefix + sep + cfg.ToolName
		if seenNames[prefixedName] {
			return nil, fmt.Errorf("duplicate tool_name %q in script upstream %q", cfg.ToolName, upstreamCfg.Name)
		}
		seenNames[prefixedName] = true

		inputSchema := buildJSONSchema(cfg.InputSchema)
		mcpTool := &sdkmcp.Tool{
			Name:        prefixedName,
			Description: cfg.Description,
			InputSchema: inputSchema,
		}

		client := httpClient
		if client == nil {
			timeout := defaultFetchTimeout
			if cfg.Timeout > 0 {
				timeout = cfg.Timeout
			}
			client = &http.Client{Timeout: timeout}
		}

		def := &Def{
			Program:    prog,
			Timeout:    cfg.Timeout,
			Env:        cfg.Env,
			HTTPClient: client,
			Pool:       pool,
		}

		// Compile identity transforms (args are passed directly to the JS function).
		compiled, err := transform.Compile(
			transform.DefaultResponseExpr,
			transform.DefaultResponseExpr,
			transform.DefaultErrorExpr,
		)
		if err != nil {
			return nil, fmt.Errorf("script %q: compiling transforms: %w", cfg.ToolName, err)
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

// CompileScript pre-processes and compiles a JavaScript script source into a
// sobek.Program for reuse across multiple Execute calls.
//
// The script is wrapped to support common export patterns:
//   - ES module style:  export default function(args, ctx) { ... }
//   - CJS style:        module.exports = function(args, ctx) { ... }
//
// After the wrapper runs, the global variable __mcp_default__ holds the callable.
func CompileScript(name, src string) (*sobek.Program, error) {
	wrapped := wrapScript(src)
	prog, err := sobek.Compile(name+".js", wrapped, false)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return prog, nil
}

// wrapScript transforms the user script source to extract the default export
// into the __mcp_default__ global variable.
func wrapScript(src string) string {
	// Replace ES module "export default" with a __mcp_default__ assignment.
	// This handles the common patterns:
	//   export default function(...) { ... }
	//   export default (args, ctx) => { ... }
	//   export default { ... }
	processed := strings.ReplaceAll(src, "export default ", "__mcp_default__ = ")

	return `
var __mcp_default__;
var exports = {};
var module = {exports: exports};
` + processed + `
if (typeof __mcp_default__ === 'undefined' && typeof module.exports === 'function') {
    __mcp_default__ = module.exports;
}
`
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

// ToTextResult converts script output bytes into a success CallToolResult.
func ToTextResult(out []byte) *sdkmcp.CallToolResult {
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		text = string(out)
	}
	// Try to re-encode JSON output for consistent formatting.
	var v any
	if json.Unmarshal(out, &v) == nil {
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

// ToErrorResult converts a script failure into an error CallToolResult.
func ToErrorResult(execErr error) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: execErr.Error()},
		},
	}
}
