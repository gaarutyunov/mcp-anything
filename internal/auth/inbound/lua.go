package inbound

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

const defaultLuaInboundTimeout = 500 * time.Millisecond

// LuaValidator implements TokenValidator using a sandboxed gopher-lua VM pool.
// The Lua script receives the token as its first argument (via ...) and must return:
// allowed (bool), status (int), extra_headers (table), error_msg (string).
type LuaValidator struct {
	proto   *lua.FunctionProto
	pool    sync.Pool
	timeout time.Duration
}

// NewLuaValidator creates a LuaValidator by reading and pre-compiling the Lua
// script at cfg.ScriptPath to bytecode. The bytecode is reused across all pooled VMs.
func NewLuaValidator(cfg config.LuaAuthConfig) (*LuaValidator, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading lua auth script %q: %w", cfg.ScriptPath, err)
	}

	proto, err := compileLuaSource(string(src), cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("compiling lua auth script %q: %w", cfg.ScriptPath, err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultLuaInboundTimeout
	}

	v := &LuaValidator{
		proto:   proto,
		timeout: timeout,
	}
	v.pool = sync.Pool{New: func() any { return newSandboxedVM() }}
	return v, nil
}

// ValidateToken calls the Lua script with the token and returns identity info on success.
func (v *LuaValidator) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
	L := v.pool.Get().(*lua.LState)
	defer func() { //nolint:contextcheck // intentionally reset to background to avoid context leak into pooled VM
		L.SetContext(context.Background())
		v.pool.Put(L)
	}()

	tctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	L.SetContext(tctx)

	fn := L.NewFunctionFromProto(v.proto)
	L.Push(fn)
	L.Push(lua.LString(token))
	if err := L.PCall(1, 4, nil); err != nil {
		return nil, fmt.Errorf("lua check_auth: %w", err)
	}

	// Pop 4 return values in reverse order (top of stack = last return value).
	errMsg := L.ToString(-1)
	L.Pop(1)
	extraHeaders := luaTableToMap(L.ToTable(-1))
	L.Pop(1)
	status := int(L.ToInt(-1))
	L.Pop(1)
	allowed := L.ToBool(-1)
	L.Pop(1)

	if !allowed {
		return nil, fmt.Errorf("lua auth denied (status %d): %s", status, errMsg)
	}

	info := &TokenInfo{Subject: "lua-authenticated", Extra: make(map[string]any)}
	for k, val := range extraHeaders {
		info.Extra["header:"+k] = val
	}
	return info, nil
}

// newSandboxedVM creates a new LState with only safe stdlib modules opened.
// I/O, OS, package, debug, and coroutine modules are intentionally excluded.
func newSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 64})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	return L
}

// compileLuaSource parses and compiles a Lua source string to bytecode.
func compileLuaSource(src, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, err
	}
	return lua.Compile(chunk, name)
}

// luaTableToMap converts a *lua.LTable to map[string]string.
// Non-string keys and values are skipped.
func luaTableToMap(tbl *lua.LTable) map[string]string {
	m := make(map[string]string)
	if tbl == nil {
		return m
	}
	tbl.ForEach(func(k, val lua.LValue) {
		ks, ok := k.(lua.LString)
		if !ok {
			return
		}
		vs, ok := val.(lua.LString)
		if !ok {
			return
		}
		m[string(ks)] = string(vs)
	})
	return m
}
