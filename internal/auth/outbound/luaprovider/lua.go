// Package luaprovider registers the "lua" outbound auth strategy.
package luaprovider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/gaarutyunov/mcp-anything/internal/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

const defaultLuaOutboundTimeout = 500 * time.Millisecond

// noCacheExpiry is a short-lived expiry (1 second) used when the Lua script returns
// expiry=0. This prevents double-execution within the same RoundTrip() call.
const noCacheExpiry = int64(1)

func init() {
	outbound.RegisterProvider("lua", func(_ context.Context, cfg *config.OutboundAuthConfig, pools *runtime.Registry) (outbound.TokenProvider, error) {
		return NewLuaProvider(cfg.Upstream, cfg.Lua, pools.LuaAuth)
	})
}

// LuaProvider implements TokenProvider using a Lua script.
type LuaProvider struct {
	upstreamName string
	proto        *lua.FunctionProto
	pool         *runtime.Pool
	timeout      time.Duration
	cache        luaProviderCache
}

type luaProviderCache struct {
	mu         sync.Mutex
	token      string
	expiry     int64
	rawHeaders map[string]string
}

// NewLuaProvider creates a LuaProvider by reading and pre-compiling the Lua script.
func NewLuaProvider(upstreamName string, cfg config.LuaOutboundConfig, pool *runtime.Pool) (*LuaProvider, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading lua outbound script %q: %w", cfg.ScriptPath, err)
	}

	proto, err := compileLuaOutboundSource(string(src), cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("compiling lua outbound script %q: %w", cfg.ScriptPath, err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultLuaOutboundTimeout
	}

	return &LuaProvider{
		upstreamName: upstreamName,
		proto:        proto,
		timeout:      timeout,
		pool:         pool,
	}, nil
}

// Token returns the current token, invoking the Lua script if the cache has expired.
func (p *LuaProvider) Token(ctx context.Context) (string, error) {
	if err := p.ensureToken(ctx); err != nil {
		return "", err
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if len(p.cache.rawHeaders) > 0 {
		return "", nil
	}
	return p.cache.token, nil
}

// RawHeaders returns the raw headers map, invoking the Lua script if the cache has expired.
func (p *LuaProvider) RawHeaders(ctx context.Context) (map[string]string, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if len(p.cache.rawHeaders) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(p.cache.rawHeaders))
	for k, v := range p.cache.rawHeaders {
		out[k] = v
	}
	return out, nil
}

func (p *LuaProvider) ensureToken(ctx context.Context) error {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()

	now := time.Now().Unix()
	if p.cache.expiry != 0 && now < p.cache.expiry {
		return nil
	}

	token, expiry, rawHeaders, err := p.callLua(ctx, p.cache.token, p.cache.expiry)
	if err != nil {
		return err
	}

	p.cache.token = token
	if expiry == 0 {
		p.cache.expiry = now + noCacheExpiry
	} else {
		p.cache.expiry = expiry
	}
	p.cache.rawHeaders = rawHeaders
	return nil
}

func (p *LuaProvider) callLua(ctx context.Context, cachedToken string, cachedExpiry int64) (token string, expiry int64, rawHeaders map[string]string, err error) {
	release, acquireErr := p.pool.Acquire(ctx)
	if acquireErr != nil {
		return "", 0, nil, fmt.Errorf("lua outbound auth: %w", acquireErr)
	}
	defer release()

	L := newOutboundSandboxedVM()
	defer L.Close()

	tctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	L.SetContext(tctx)

	fn := L.NewFunctionFromProto(p.proto)
	L.Push(fn)
	L.Push(lua.LString(p.upstreamName))
	L.Push(lua.LString(cachedToken))
	L.Push(lua.LNumber(cachedExpiry))
	if err := L.PCall(3, 4, nil); err != nil {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: %w", err)
	}

	if tp := L.Get(-1).Type(); tp != lua.LTString {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: error_msg (return 4) must be string, got %s", tp)
	}
	errMsg := L.ToString(-1)
	L.Pop(1)

	if tp := L.Get(-1).Type(); tp != lua.LTTable && tp != lua.LTNil {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: raw_headers (return 3) must be table or nil, got %s", tp)
	}
	rawHeaders = luaOutboundTableToMap(L.ToTable(-1))
	L.Pop(1)

	if tp := L.Get(-1).Type(); tp != lua.LTNumber {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: expiry_unix (return 2) must be number, got %s", tp)
	}
	expiry = int64(L.ToInt(-1))
	L.Pop(1)

	if tp := L.Get(-1).Type(); tp != lua.LTString {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: token (return 1) must be string, got %s", tp)
	}
	token = L.ToString(-1)
	L.Pop(1)

	if errMsg != "" {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token error: %s", errMsg)
	}
	return token, expiry, rawHeaders, nil
}

func newOutboundSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 64})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	L.SetGlobal("dofile", lua.LNil)
	L.SetGlobal("loadfile", lua.LNil)
	return L
}

func compileLuaOutboundSource(src, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, fmt.Errorf("parsing lua source %q: %w", name, err)
	}
	return lua.Compile(chunk, name)
}

func luaOutboundTableToMap(tbl *lua.LTable) map[string]string {
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
