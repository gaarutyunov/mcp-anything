package outbound

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

const defaultLuaOutboundTimeout = 500 * time.Millisecond

// LuaProvider implements TokenProvider using a Lua script.
// The script receives (upstream, cached_token, cached_expiry) as arguments and must return:
// token (string), expiry_unix (int), raw_headers (table), error_msg (string).
type LuaProvider struct {
	upstreamName string
	proto        *lua.FunctionProto
	pool         sync.Pool
	timeout      time.Duration
	cache        luaProviderCache
}

type luaProviderCache struct {
	mu         sync.Mutex
	token      string
	expiry     int64 // unix timestamp; 0 = no caching
	rawHeaders map[string]string
}

// NewLuaProvider creates a LuaProvider by reading and pre-compiling the Lua script.
func NewLuaProvider(upstreamName string, cfg config.LuaOutboundConfig) (*LuaProvider, error) {
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

	p := &LuaProvider{
		upstreamName: upstreamName,
		proto:        proto,
		timeout:      timeout,
	}
	p.pool = sync.Pool{New: func() any { return newOutboundSandboxedVM() }}
	return p, nil
}

// Token returns the current token, invoking the Lua script if the cache has expired.
// Returns empty string if the script provides raw headers instead.
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
	// Return a copy to prevent callers from mutating the cache.
	out := make(map[string]string, len(p.cache.rawHeaders))
	for k, v := range p.cache.rawHeaders {
		out[k] = v
	}
	return out, nil
}

// ensureToken refreshes the cached credentials if expired or absent.
func (p *LuaProvider) ensureToken(ctx context.Context) error {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()

	now := time.Now().Unix()
	if p.cache.expiry != 0 && now < p.cache.expiry {
		return nil // cache still valid
	}

	token, expiry, rawHeaders, err := p.callLua(ctx, p.cache.token, p.cache.expiry)
	if err != nil {
		return err
	}

	p.cache.token = token
	p.cache.expiry = expiry
	p.cache.rawHeaders = rawHeaders
	return nil
}

// callLua invokes the Lua script and returns the four result values.
func (p *LuaProvider) callLua(ctx context.Context, cachedToken string, cachedExpiry int64) (token string, expiry int64, rawHeaders map[string]string, err error) {
	L := p.pool.Get().(*lua.LState)
	defer func() { //nolint:contextcheck // intentionally reset to background to avoid context leak into pooled VM
		L.SetContext(context.Background())
		p.pool.Put(L)
	}()

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

	// Pop 4 return values in reverse order.
	errMsg := L.ToString(-1)
	L.Pop(1)
	rawHeaders = luaOutboundTableToMap(L.ToTable(-1))
	L.Pop(1)
	expiry = int64(L.ToInt(-1))
	L.Pop(1)
	token = L.ToString(-1)
	L.Pop(1)

	if errMsg != "" {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token error: %s", errMsg)
	}
	return token, expiry, rawHeaders, nil
}

// newOutboundSandboxedVM creates a new sandboxed LState for outbound use.
func newOutboundSandboxedVM() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 64})
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	lua.OpenMath(L)
	return L
}

// compileLuaOutboundSource parses and compiles a Lua source string to bytecode.
func compileLuaOutboundSource(src, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, err
	}
	return lua.Compile(chunk, name)
}

// luaOutboundTableToMap converts a *lua.LTable to map[string]string.
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
