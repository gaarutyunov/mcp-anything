// Package ui implements interactive HTML UI loading and rendering for MCP Apps.
// It supports two modes:
//   - Static HTML: a pre-loaded HTML file served as-is.
//   - Render script: a JavaScript function executed by Sobek at resource-fetch time,
//     receiving a ctx object with toolName, description, schema, env, fetch, and log.
//
// The Loader is immutable after construction and safe for concurrent reads.
// Each RenderHTML call on a script-based Loader creates its own Sobek runtime.
package ui

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

	"github.com/grafana/sobek"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

const defaultFetchTimeout = 30 * time.Second

// Loader holds the loaded UI for a single tool.
// Construct via New, NewStaticLoader, or NewScriptLoader.
type Loader struct {
	// staticHTML is non-empty for static HTML mode.
	staticHTML string

	// Fields for script mode (staticHTML == ""):
	program    *sobek.Program
	env        map[string]string
	httpClient *http.Client
	pool       config.PoolAcquirer
}

// New creates a Loader from a ToolUIConfig.
// Script takes precedence over static when both paths are set.
// pool is required when cfg.Script is non-empty.
func New(cfg *config.ToolUIConfig, env map[string]string, httpClient *http.Client, pool config.PoolAcquirer) (*Loader, error) {
	if cfg.Script != "" {
		if pool == nil {
			return nil, fmt.Errorf("ui: JSScriptPool must be set when using a render script")
		}
		return NewScriptLoader(cfg.Script, env, httpClient, pool)
	}
	if cfg.Static != "" {
		return NewStaticLoader(cfg.Static)
	}
	return nil, fmt.Errorf("ui: no source configured (set static or script path)")
}

// NewStaticLoader loads HTML from path and returns a Loader that serves it as-is.
// Returns an error if the file cannot be read or contains only whitespace.
func NewStaticLoader(path string) (*Loader, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading static UI file %q: %w", path, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, fmt.Errorf("static UI file %q is empty", path)
	}
	return &Loader{staticHTML: string(content)}, nil
}

// NewScriptLoader reads and pre-compiles the render script at path.
// Returns a fatal error if the file cannot be read or the script contains syntax errors.
func NewScriptLoader(path string, env map[string]string, httpClient *http.Client, pool config.PoolAcquirer) (*Loader, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading UI render script %q: %w", path, err)
	}
	prog, err := compileRenderScript(path, string(src))
	if err != nil {
		return nil, fmt.Errorf("compiling UI render script %q: %w", path, err)
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: defaultFetchTimeout}
	}
	return &Loader{
		program:    prog,
		env:        env,
		httpClient: client,
		pool:       pool,
	}, nil
}

// RenderHTML generates the HTML for a tool.
// For static loaders it returns the pre-loaded HTML.
// For script loaders it acquires a pool slot and executes the Sobek render script.
func (l *Loader) RenderHTML(ctx context.Context, toolName, description string, schema any) (string, error) {
	if l.staticHTML != "" {
		return l.staticHTML, nil
	}
	return l.renderScript(ctx, toolName, description, schema)
}

// ResourceHandler returns an sdkmcp.ResourceHandler that serves the tool's HTML UI.
// toolName, description, schema, and resourceURI are captured in the closure at registration time.
func (l *Loader) ResourceHandler(toolName, description string, schema any, resourceURI string) sdkmcp.ResourceHandler {
	// Convert the schema to a JSON-round-tripped Go value so that Sobek receives
	// a plain map[string]any rather than a library-specific struct.
	schemaVal := toJSONValue(schema)
	return func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		html, err := l.RenderHTML(ctx, toolName, description, schemaVal)
		if err != nil {
			return nil, fmt.Errorf("rendering UI for %q: %w", toolName, err)
		}
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{{
				URI:      resourceURI,
				MIMEType: "text/html",
				Text:     html,
			}},
		}, nil
	}
}

// renderScript executes the pre-compiled render script in a fresh Sobek runtime.
func (l *Loader) renderScript(ctx context.Context, toolName, description string, schema any) (string, error) {
	release, err := l.pool.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquiring render script pool slot: %w", err)
	}
	defer release()

	rt := sobek.New()

	ctxObj := l.buildCtxObject(ctx, rt, toolName, description, schema)
	if setErr := rt.Set("__mcp_ui_ctx__", ctxObj); setErr != nil {
		return "", fmt.Errorf("setting context object: %w", setErr)
	}

	if _, runErr := rt.RunProgram(l.program); runErr != nil {
		return "", fmt.Errorf("running render script: %w", runErr)
	}

	renderFn, ok := sobek.AssertFunction(rt.Get("__mcp_render__"))
	if !ok {
		return "", fmt.Errorf("render script does not export a callable default function (use 'export default' or module.exports)")
	}

	result, callErr := renderFn(sobek.Undefined(), ctxObj)
	if callErr != nil {
		return "", fmt.Errorf("render script execution failed: %w", callErr)
	}

	exported := result.Export()
	if exported == nil {
		return "", fmt.Errorf("render script returned nil; it must return an HTML string")
	}
	s, ok := exported.(string)
	if !ok {
		return "", fmt.Errorf("render script must return a string, got %T", exported)
	}
	return s, nil
}

// buildCtxObject constructs the JS ctx object exposed to render scripts.
// It provides: toolName, description, schema, env, fetch, log.
func (l *Loader) buildCtxObject(ctx context.Context, rt *sobek.Runtime, toolName, description string, schema any) *sobek.Object {
	ctxObj := rt.NewObject()

	_ = ctxObj.Set("toolName", toolName)
	_ = ctxObj.Set("description", description)
	_ = ctxObj.Set("schema", rt.ToValue(schema))

	// ctx.env — read-only environment variables.
	envObj := rt.NewObject()
	for k, v := range l.env {
		expanded := os.ExpandEnv(v)
		if err := envObj.Set(k, expanded); err != nil {
			slog.Warn("ui: failed to set env var in render ctx", "key", k, "error", err)
		}
	}
	_ = ctxObj.Set("env", envObj)

	// ctx.log(level, msg) — structured logging.
	_ = ctxObj.Set("log", func(level, msg string) {
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
	})

	// ctx.fetch(url, opts) — sandboxed HTTP client.
	client := l.httpClient
	if client == nil {
		client = &http.Client{Timeout: defaultFetchTimeout}
	}
	_ = ctxObj.Set("fetch", func(call sobek.FunctionCall) sobek.Value {
		return jsFetch(ctx, rt, client, call)
	})

	// console.log/warn/error/debug — map to slog.
	consoleObj := rt.NewObject()
	makeConsoleLogger := func(logFn func(string, ...any)) func(sobek.FunctionCall) sobek.Value {
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
		slog.Warn("ui: failed to set console", "error", err)
	}

	return ctxObj
}

// compileRenderScript wraps and compiles a render script source into a sobek.Program.
func compileRenderScript(name, src string) (*sobek.Program, error) {
	wrapped := wrapRenderScript(src)
	prog, err := sobek.Compile(name, wrapped, false)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return prog, nil
}

// wrapRenderScript transforms the user source to extract the default export
// into the global __mcp_render__ variable.
// Supports both ES module style (export default function) and CJS style (module.exports).
func wrapRenderScript(src string) string {
	processed := strings.ReplaceAll(src, "export default ", "__mcp_render__ = ")
	return `
var __mcp_render__;
var exports = {};
var module = {exports: exports};
` + processed + `
if (typeof __mcp_render__ === 'undefined' && typeof module.exports === 'function') {
    __mcp_render__ = module.exports;
}
`
}

// jsFetch implements ctx.fetch(url, opts) for render scripts.
// Supported opts fields: method (string), headers (object), body (string).
// Returns the parsed JSON response, or the raw text if not JSON.
// Panics with a JS exception on network or HTTP error.
func jsFetch(ctx context.Context, rt *sobek.Runtime, client *http.Client, call sobek.FunctionCall) sobek.Value {
	if len(call.Arguments) == 0 {
		panic(rt.NewTypeError("ctx.fetch requires a URL argument"))
	}

	rawURL := call.Arguments[0].String()
	method := "GET"
	var bodyReader io.Reader
	headers := map[string]string{}

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
			slog.Warn("ui: closing fetch response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: reading response body: %w", err)))
	}

	if resp.StatusCode >= 400 {
		panic(rt.NewGoError(fmt.Errorf("ctx.fetch: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))))
	}

	var parsed any
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr == nil {
		return rt.ToValue(parsed)
	}
	return rt.ToValue(string(body))
}

// toJSONValue converts v to a JSON-round-tripped Go value (map[string]any, etc.)
// so that Sobek receives plain Go maps rather than opaque library structs.
func toJSONValue(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		return v
	}
	return result
}
