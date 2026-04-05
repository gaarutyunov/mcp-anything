// Package luavalidator registers the "lua" inbound auth strategy.
package luavalidator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/gaarutyunov/mcp-anything/internal/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

const defaultLuaInboundTimeout = 500 * time.Millisecond

func init() {
	inbound.RegisterValidator("lua", func(_ context.Context, cfg *config.InboundAuthConfig, pools *runtime.Registry) (inbound.TokenValidator, string, error) {
		v, err := NewLuaValidator(cfg.Lua, pools.LuaAuth)
		return v, "", err
	})
}

// LuaValidator implements TokenValidator using a sandboxed gopher-lua VM.
type LuaValidator struct {
	proto   *lua.FunctionProto
	pool    *runtime.Pool
	timeout time.Duration
}

// NewLuaValidator creates a LuaValidator by reading and pre-compiling the Lua script.
func NewLuaValidator(cfg config.LuaAuthConfig, pool *runtime.Pool) (*LuaValidator, error) {
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

	return &LuaValidator{
		proto:   proto,
		timeout: timeout,
		pool:    pool,
	}, nil
}

// ValidateToken calls the Lua script with the token and returns identity info on success.
func (v *LuaValidator) ValidateToken(ctx context.Context, token string) (*inbound.TokenInfo, error) {
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

func newSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 64})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	L.SetGlobal("dofile", lua.LNil)
	L.SetGlobal("loadfile", lua.LNil)
	return L
}

func compileLuaSource(src, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, fmt.Errorf("parsing lua source %q: %w", name, err)
	}
	return lua.Compile(chunk, name)
}

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
