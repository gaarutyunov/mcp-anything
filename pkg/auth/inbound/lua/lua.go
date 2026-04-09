// Package lua registers the "lua" inbound auth strategy.
// Import this package (blank import) to make the strategy available via inbound.New().
package lua

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

const defaultTimeout = 500 * time.Millisecond

func init() {
	inbound.Register("lua", func(_ context.Context, cfg *config.InboundAuthConfig) (inbound.TokenValidator, string, error) {
		if cfg.LuaAuthPool == nil {
			return nil, "", fmt.Errorf("lua inbound auth requires runtime pools; set InboundAuthConfig.LuaAuthPool")
		}
		v, err := NewValidator(cfg.Lua, cfg.LuaAuthPool)
		return v, "", err
	})
}

// Validator implements TokenValidator using a sandboxed gopher-lua VM.
// The Lua script receives the token as its first argument (via ...) and must return:
// allowed (bool), status (int), extra_headers (table), error_msg (string).
// The shared pool bounds the maximum number of concurrent Lua runtimes to prevent OOM.
type Validator struct {
	proto   *lua.FunctionProto
	pool    config.PoolAcquirer
	timeout time.Duration
}

// NewValidator creates a Validator by reading and pre-compiling the Lua
// script at cfg.ScriptPath to bytecode. The bytecode is reused across calls.
// pool bounds the number of concurrent Lua runtimes; it is shared with the outbound
// Lua auth provider to enforce a single global limit for all auth scripts.
func NewValidator(cfg config.LuaAuthConfig, pool config.PoolAcquirer) (*Validator, error) {
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
		timeout = defaultTimeout
	}

	return &Validator{
		proto:   proto,
		timeout: timeout,
		pool:    pool,
	}, nil
}

// ValidateToken calls the Lua script with the token and returns identity info on success.
func (v *Validator) ValidateToken(ctx context.Context, token string) (*inbound.TokenInfo, error) {
	release, err := v.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("lua auth: %w", err)
	}
	defer release()

	L := newSandboxedVM()
	defer L.Close()

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

	info := &inbound.TokenInfo{Subject: "lua-authenticated", Extra: make(map[string]any)}
	for k, val := range extraHeaders {
		info.Extra["header:"+k] = val
	}
	return info, nil
}

// newSandboxedVM creates a new LState with only safe stdlib modules opened.
// I/O, OS, package, debug, and coroutine modules are intentionally excluded.
// dofile and loadfile are explicitly removed from the base library to prevent
// scripts from reading local files via the base library's file-loading functions.
func newSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 64})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	// Remove file-loading globals exposed by OpenBase to prevent sandbox escape.
	L.SetGlobal("dofile", lua.LNil)
	L.SetGlobal("loadfile", lua.LNil)
	return L
}

// compileLuaSource parses and compiles a Lua source string to bytecode.
func compileLuaSource(src, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, fmt.Errorf("parsing lua source %q: %w", name, err)
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
