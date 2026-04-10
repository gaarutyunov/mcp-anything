package script_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
	"github.com/gaarutyunov/mcp-anything/pkg/upstream/script"
)

// TestCompileScript_ValidScript verifies that a valid script compiles without error.
func TestCompileScript_ValidScript(t *testing.T) {
	src := `export default function(args, ctx) { return {ok: true}; }`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if prog == nil {
		t.Fatal("expected non-nil program")
	}
}

// TestCompileScript_SyntaxError verifies that a script with a syntax error returns an error.
func TestCompileScript_SyntaxError(t *testing.T) {
	src := `export default function(args, ctx) { return {`
	_, err := script.CompileScript("bad", src)
	if err == nil {
		t.Fatal("expected compilation error for invalid script")
	}
}

// testPool returns a small runtime pool suitable for unit tests.
func testPool() *runtime.Pool {
	return runtime.NewPool(runtime.DefaultMaxScriptVMs)
}

// TestDef_Execute_SimpleReturn verifies that a simple return value is serialized as JSON.
func TestDef_Execute_SimpleReturn(t *testing.T) {
	src := `export default function(args, ctx) { return {message: args.greeting}; }`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{Program: prog, Pool: testPool()}

	out, err := def.Execute(context.Background(), map[string]any{"greeting": "hello"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["message"] != "hello" {
		t.Errorf("expected message=hello, got: %v", result["message"])
	}
}

// TestDef_Execute_ModuleExports verifies that module.exports = function(...) style works.
func TestDef_Execute_ModuleExports(t *testing.T) {
	src := `module.exports = function(args, ctx) { return {n: args.n * 2}; }`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{Program: prog, Pool: testPool()}
	out, err := def.Execute(context.Background(), map[string]any{"n": 5})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// JSON numbers unmarshal as float64.
	if result["n"] != float64(10) {
		t.Errorf("expected n=10, got: %v", result["n"])
	}
}

// TestDef_Execute_JSException verifies that a JS exception is returned as an error.
func TestDef_Execute_JSException(t *testing.T) {
	src := `export default function(args, ctx) { throw new Error("boom"); }`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{Program: prog, Pool: testPool()}
	_, err = def.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from JS exception")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to contain 'boom', got: %v", err)
	}
}

// TestDef_Execute_Timeout verifies that a long-running script is interrupted by the timeout.
func TestDef_Execute_Timeout(t *testing.T) {
	src := `export default function(args, ctx) {
		var n = 0;
		while (true) { n++; }
		return n;
	}`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{
		Program: prog,
		Timeout: 100 * time.Millisecond,
		Pool:    testPool(),
	}
	_, err = def.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestDef_Execute_NoDefaultExport verifies that a script without a default export returns an error.
func TestDef_Execute_NoDefaultExport(t *testing.T) {
	src := `var x = 42;` // no export default, no module.exports
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{Program: prog, Pool: testPool()}
	_, err = def.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing default export")
	}
}

// TestDef_Execute_CtxEnv verifies that ctx.env exposes configured environment variables.
func TestDef_Execute_CtxEnv(t *testing.T) {
	src := `export default function(args, ctx) { return {val: ctx.env.MY_VAR}; }`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{
		Program: prog,
		Env:     map[string]string{"MY_VAR": "hello_from_env"},
		Pool:    testPool(),
	}
	out, err := def.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["val"] != "hello_from_env" {
		t.Errorf("expected val=hello_from_env, got: %v", result["val"])
	}
}

// TestDef_Execute_CtxFetch verifies that ctx.fetch makes an HTTP request and returns JSON.
func TestDef_Execute_CtxFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"Fido"}`))
	}))
	defer server.Close()

	src := `export default function(args, ctx) {
		var pet = ctx.fetch(ctx.env.BASE_URL + "/pets/1");
		return {pet_name: pet.name};
	}`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{
		Program:    prog,
		Env:        map[string]string{"BASE_URL": server.URL},
		HTTPClient: server.Client(),
		Pool:       testPool(),
	}
	out, err := def.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["pet_name"] != "Fido" {
		t.Errorf("expected pet_name=Fido, got: %v", result["pet_name"])
	}
}

// TestDef_Execute_CtxFetchError verifies that ctx.fetch throws on HTTP 4xx/5xx.
func TestDef_Execute_CtxFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	src := `export default function(args, ctx) {
		return ctx.fetch(ctx.env.BASE_URL + "/missing");
	}`
	prog, err := script.CompileScript("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	def := &script.Def{
		Program:    prog,
		Env:        map[string]string{"BASE_URL": server.URL},
		HTTPClient: server.Client(),
		Pool:       testPool(),
	}
	_, err = def.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from ctx.fetch 404")
	}
}

// TestBuildTools_Valid verifies that valid script configs build successfully.
func TestBuildTools_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/test.js"
	if err := os.WriteFile(scriptPath, []byte(`export default function(args, ctx) { return {ok: true}; }`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgs := []config.ScriptConfig{
		{
			ToolName:   "my_tool",
			ScriptPath: scriptPath,
			InputSchema: config.CommandInputSchema{
				Type:     "object",
				Required: []string{"x"},
				Properties: map[string]config.CommandSchemaProperty{
					"x": {Type: "string", Description: "input"},
				},
			},
		},
	}
	upCfg := &config.UpstreamConfig{Name: "test", ToolPrefix: "test"}
	namingCfg := &config.NamingConfig{Separator: "__"}

	pool := runtime.NewPool(runtime.DefaultMaxScriptVMs)
	tools, err := script.BuildTools(cfgs, upCfg, namingCfg, nil, pool)
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].PrefixedName != "test__my_tool" {
		t.Errorf("expected test__my_tool, got %q", tools[0].PrefixedName)
	}
}

// TestBuildTools_MissingToolName verifies that missing tool_name returns an error.
func TestBuildTools_MissingToolName(t *testing.T) {
	cfgs := []config.ScriptConfig{{ScriptPath: "/some/path.js"}}
	upCfg := &config.UpstreamConfig{Name: "test", ToolPrefix: "test"}
	namingCfg := &config.NamingConfig{Separator: "__"}
	pool := runtime.NewPool(runtime.DefaultMaxScriptVMs)

	_, err := script.BuildTools(cfgs, upCfg, namingCfg, nil, pool)
	if err == nil {
		t.Fatal("expected error for missing tool_name")
	}
}

// TestBuildTools_MissingScriptPath verifies that missing script_path returns an error.
func TestBuildTools_MissingScriptPath(t *testing.T) {
	cfgs := []config.ScriptConfig{{ToolName: "my_tool"}}
	upCfg := &config.UpstreamConfig{Name: "test", ToolPrefix: "test"}
	namingCfg := &config.NamingConfig{Separator: "__"}
	pool := runtime.NewPool(runtime.DefaultMaxScriptVMs)

	_, err := script.BuildTools(cfgs, upCfg, namingCfg, nil, pool)
	if err == nil {
		t.Fatal("expected error for missing script_path")
	}
}

// TestBuildTools_ScriptSyntaxError verifies that a script with a parse error is rejected at startup.
func TestBuildTools_ScriptSyntaxError(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/bad.js"
	if err := os.WriteFile(scriptPath, []byte(`export default function(args { invalid syntax`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgs := []config.ScriptConfig{{ToolName: "bad", ScriptPath: scriptPath}}
	upCfg := &config.UpstreamConfig{Name: "test", ToolPrefix: "test"}
	namingCfg := &config.NamingConfig{Separator: "__"}
	pool := runtime.NewPool(runtime.DefaultMaxScriptVMs)

	_, err := script.BuildTools(cfgs, upCfg, namingCfg, nil, pool)
	if err == nil {
		t.Fatal("expected error for syntax error in script")
	}
}
