// Package lua registers the "inbound/lua" and "outbound/lua" middleware strategies.
// Import this package (blank import) to make both strategies available via middleware.New().
package lua

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

const defaultTimeout = 500 * time.Millisecond

// noCacheExpiry is a short-lived expiry (1 second) used when the Lua script returns
// expiry=0. This prevents double-execution within the same RoundTrip() call (e.g.,
// RawHeaders() followed by Token()), while still refreshing credentials on subsequent requests.
const noCacheExpiry = int64(1)

func init() {
	pkgmiddleware.Register("inbound/lua", func(_ context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/lua: expected *config.InboundAuthConfig, got %T", cfg)
		}
		if ic.LuaAuthPool == nil {
			return nil, fmt.Errorf("lua inbound auth requires runtime pools; set InboundAuthConfig.LuaAuthPool")
		}
		v, err := NewValidator(ic.Lua, ic.LuaAuthPool)
		if err != nil {
			return nil, err
		}
		return inbound.ValidatorMiddleware(v, ""), nil
	})
	pkgmiddleware.Register("outbound/lua", func(_ context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/lua: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		if oc.LuaAuthPool == nil {
			return nil, fmt.Errorf("lua outbound auth requires runtime pools; set OutboundAuthConfig.LuaAuthPool")
		}
		p, err := NewProvider(oc.Upstream, oc.Lua, oc.LuaAuthPool)
		if err != nil {
			return nil, err
		}
		return outbound.Middleware(p), nil
	})
}

// Validator implements inbound.TokenValidator using a sandboxed gopher-lua VM.
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
		return nil, &inbound.DeniedError{Status: status, Message: errMsg}
	}

	info := &inbound.TokenInfo{Subject: "lua-authenticated", Extra: make(map[string]any)}
	for k, val := range extraHeaders {
		info.Extra["header:"+k] = val
	}
	return info, nil
}

// Provider implements outbound.TokenProvider using a Lua script.
// The script receives (upstream, cached_token, cached_expiry) as arguments and must return:
// token (string), expiry_unix (int), raw_headers (table), error_msg (string).
// The shared pool bounds the maximum number of concurrent Lua runtimes to prevent OOM.
type Provider struct {
	upstreamName string
	proto        *lua.FunctionProto
	pool         config.PoolAcquirer
	timeout      time.Duration
	cache        providerCache
}

type providerCache struct {
	mu         sync.Mutex
	token      string
	expiry     int64 // unix timestamp; 0 = fetch on next call; noCacheExpiry = short-lived
	rawHeaders map[string]string
}

// NewProvider creates a Provider by reading and pre-compiling the Lua script.
// pool bounds the number of concurrent Lua runtimes; it is shared with the inbound
// Lua auth validator to enforce a single global limit for all auth scripts.
func NewProvider(upstreamName string, cfg config.LuaOutboundConfig, pool config.PoolAcquirer) (*Provider, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading lua outbound script %q: %w", cfg.ScriptPath, err)
	}

	proto, err := compileLuaSource(string(src), cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("compiling lua outbound script %q: %w", cfg.ScriptPath, err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &Provider{
		upstreamName: upstreamName,
		proto:        proto,
		timeout:      timeout,
		pool:         pool,
	}, nil
}

// Token returns the current token, invoking the Lua script if the cache has expired.
// Returns empty string if the script provides raw headers instead.
func (p *Provider) Token(ctx context.Context) (string, error) {
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
func (p *Provider) RawHeaders(ctx context.Context) (map[string]string, error) {
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
func (p *Provider) ensureToken(ctx context.Context) error {
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
	// When the script returns expiry=0 (no persistent cache), use a short-lived
	// 1-second window to prevent double-execution within the same RoundTrip() call
	// (e.g., RawHeaders() followed by Token()). The cache expires naturally after 1 second.
	if expiry == 0 {
		p.cache.expiry = now + noCacheExpiry
	} else {
		p.cache.expiry = expiry
	}
	p.cache.rawHeaders = rawHeaders
	return nil
}

// callLua invokes the Lua script and returns the four result values.
func (p *Provider) callLua(ctx context.Context, cachedToken string, cachedExpiry int64) (token string, expiry int64, rawHeaders map[string]string, err error) {
	release, acquireErr := p.pool.Acquire(ctx)
	if acquireErr != nil {
		return "", 0, nil, fmt.Errorf("lua outbound auth: %w", acquireErr)
	}
	defer release()

	L := newSandboxedVM()
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

	// Validate and pop 4 return values in reverse order (top of stack = last return value).
	// Return order: token (1), expiry_unix (2), raw_headers (3), error_msg (4).
	if tp := L.Get(-1).Type(); tp != lua.LTString {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: error_msg (return 4) must be string, got %s", tp)
	}
	errMsg := L.ToString(-1)
	L.Pop(1)

	if tp := L.Get(-1).Type(); tp != lua.LTTable && tp != lua.LTNil {
		return "", 0, nil, fmt.Errorf("lua get_upstream_token: raw_headers (return 3) must be table or nil, got %s", tp)
	}
	rawHeaders = luaTableToMap(L.ToTable(-1))
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
