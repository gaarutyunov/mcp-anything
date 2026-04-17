// Package js registers the "inbound/js" and "outbound/js" middleware strategies
// and the "js/auth" and "js/script" runtime pools.
// Import this package (blank import) to make all of the above available.
package js

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grafana/sobek"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/auth/outbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
	pkgruntime "github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

const defaultTimeout = 500 * time.Millisecond
const defaultFetchTimeout = 30 * time.Second

// noCacheExpiry is a short-lived expiry (1 second) used when the JS script
// returns no expiry. This prevents double-execution within the same RoundTrip() call,
// while still refreshing credentials on subsequent requests.
const noCacheExpiry = int64(1)

func init() {
	// Register runtime pools for JS auth and script execution.
	pkgruntime.Register("js/auth", func(_ context.Context, cfg config.RuntimeConfig) (pkgruntime.Runtime, error) {
		max := cfg.JS.MaxAuthVMs
		if max == 0 {
			max = int(pkgruntime.DefaultMaxAuthVMs)
		}
		if max < 0 {
			return nil, fmt.Errorf("runtime.js.max_auth_vms must be > 0, got %d", max)
		}
		return pkgruntime.NewPool(int64(max))
	})
	pkgruntime.Register("js/script", func(_ context.Context, cfg config.RuntimeConfig) (pkgruntime.Runtime, error) {
		max := cfg.JS.MaxScriptVMs
		if max == 0 {
			max = int(pkgruntime.DefaultMaxScriptVMs)
		}
		if max < 0 {
			return nil, fmt.Errorf("runtime.js.max_script_vms must be > 0, got %d", max)
		}
		return pkgruntime.NewPool(int64(max))
	})

	// Register inbound and outbound middleware strategies.
	pkgmiddleware.Register("inbound/js", func(_ context.Context, cfg any) (pkgmiddleware.Builder, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/js: expected *config.InboundAuthConfig, got %T", cfg)
		}
		if ic.JSAuthPool == nil {
			return nil, fmt.Errorf("js inbound auth requires runtime pools; set InboundAuthConfig.JSAuthPool")
		}
		return NewValidator(ic.JS, ic.JSAuthPool)
	})
	pkgmiddleware.Register("outbound/js", func(_ context.Context, cfg any) (pkgmiddleware.Builder, error) {
		oc, ok := cfg.(*config.OutboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("outbound/js: expected *config.OutboundAuthConfig, got %T", cfg)
		}
		if oc.JSAuthPool == nil {
			return nil, fmt.Errorf("js outbound auth requires runtime pools; set OutboundAuthConfig.JSAuthPool")
		}
		return NewProvider(oc.Upstream, oc.JS, oc.JSAuthPool)
	})
}

// scriptCache is a thread-safe key-value cache with optional TTL,
// shared across JS script invocations to provide ctx.cache.get/set functionality.
type scriptCache struct {
	mu    sync.Mutex
	items map[string]scriptCacheItem
}

type scriptCacheItem struct {
	value     any
	expiresAt time.Time
}

func newScriptCache() *scriptCache {
	return &scriptCache{items: make(map[string]scriptCacheItem)}
}

func (c *scriptCache) get(key string) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		delete(c.items, key)
		return nil
	}
	return item.value
}

func (c *scriptCache) set(key string, value any, ttlSeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttlSeconds > 0 {
		exp = time.Now().Add(time.Duration(ttlSeconds * float64(time.Second)))
	}
	c.items[key] = scriptCacheItem{value: value, expiresAt: exp}
}

// credentialCache caches the token, expiry, and raw headers returned by outbound JS scripts.
type credentialCache struct {
	mu         sync.Mutex
	token      string
	expiry     int64 // unix timestamp; 0 = fetch on next call
	rawHeaders map[string]string
}

// Validator implements inbound.TokenValidator using a sandboxed Sobek JS runtime.
// The JavaScript script receives (token, ctx) and must return an object:
//
//	{ allowed: bool, status?: number, error?: string, subject?: string, extra_headers?: object }
//
// A fresh sobek.Runtime is created per call (Sobek is not goroutine-safe).
// The pre-compiled program is reused across calls. The shared pool bounds
// the maximum number of concurrent JS runtimes to prevent OOM under load.
type Validator struct {
	program     *sobek.Program
	timeout     time.Duration
	env         map[string]string
	scriptCache *scriptCache
	httpClient  *http.Client
	pool        config.PoolAcquirer
	Next        http.Handler
}

// NewValidator creates a Validator by reading and pre-compiling the JS script.
// pool bounds the number of concurrent JS runtimes; it is shared with the outbound
// JS auth provider to enforce a single global limit for all auth scripts.
func NewValidator(cfg config.JSAuthConfig, pool config.PoolAcquirer) (*Validator, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading js auth script %q: %w", cfg.ScriptPath, err)
	}
	prog, err := compileScript(cfg.ScriptPath, string(src))
	if err != nil {
		return nil, fmt.Errorf("compiling js auth script %q: %w", cfg.ScriptPath, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Validator{
		program:     prog,
		timeout:     timeout,
		env:         cfg.Env,
		scriptCache: newScriptCache(),
		httpClient:  &http.Client{Timeout: defaultFetchTimeout},
		pool:        pool,
	}, nil
}

// Build implements middleware.Builder. It returns a Validator wired to next, sharing compiled program and cache.
func (v *Validator) Build(next http.Handler) http.Handler {
	return &Validator{
		program:     v.program,
		timeout:     v.timeout,
		env:         v.env,
		scriptCache: v.scriptCache,
		httpClient:  v.httpClient,
		pool:        v.pool,
		Next:        next,
	}
}

// ServeHTTP implements http.Handler. It extracts a Bearer token and validates it via JS script.
func (v *Validator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := inbound.ExtractBearerToken(r)
	if token == "" {
		inbound.WriteUnauthorized(w, r, "missing_token")
		return
	}
	info, err := v.ValidateToken(r.Context(), token)
	if err != nil {
		var denied *inbound.DeniedError
		if errors.As(err, &denied) {
			inbound.WriteDenied(w, r, denied)
		} else {
			inbound.WriteUnauthorized(w, r, "invalid_token")
		}
		return
	}
	v.Next.ServeHTTP(w, r.WithContext(inbound.WithTokenInfo(r.Context(), info)))
}

// ValidateToken runs the JS script with the token and returns identity info on success.
func (v *Validator) ValidateToken(ctx context.Context, token string) (*inbound.TokenInfo, error) {
	release, err := v.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("js auth: %w", err)
	}
	defer release()

	rt := sobek.New()

	// scriptCtx bounds ctx.fetch HTTP calls to the script deadline.
	scriptCtx := ctx
	if v.timeout > 0 {
		var cancel context.CancelFunc
		scriptCtx, cancel = context.WithTimeout(ctx, v.timeout)
		defer cancel()
		timer := time.AfterFunc(v.timeout, func() {
			rt.Interrupt("js auth script timed out")
		})
		defer timer.Stop()
	}

	ctxObj := buildCtxObject(scriptCtx, rt, v.env, v.scriptCache, v.httpClient)
	if err := rt.Set("__mcp_ctx__", ctxObj); err != nil {
		return nil, fmt.Errorf("setting js auth ctx: %w", err)
	}

	if _, err := rt.RunProgram(v.program); err != nil {
		return nil, fmt.Errorf("running js auth script: %w", err)
	}

	mainFn, ok := sobek.AssertFunction(rt.Get("__mcp_default__"))
	if !ok {
		return nil, fmt.Errorf("js auth script does not export a callable default function")
	}

	result, err := mainFn(sobek.Undefined(), rt.ToValue(token), ctxObj)
	if err != nil {
		return nil, fmt.Errorf("js check_auth: %w", err)
	}

	return parseInboundResult(result)
}

// parseInboundResult extracts TokenInfo from the JS script's return value.
func parseInboundResult(result sobek.Value) (*inbound.TokenInfo, error) {
	exported := result.Export()
	resMap, ok := exported.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("js check_auth: return value must be an object, got %T", exported)
	}

	allowed, _ := resMap["allowed"].(bool)
	if !allowed {
		status := 401
		if s, ok := resMap["status"]; ok {
			switch sv := s.(type) {
			case int64:
				status = int(sv)
			case float64:
				status = int(sv)
			}
		}
		errMsg := ""
		if e, ok := resMap["error"].(string); ok {
			errMsg = e
		}
		return nil, &inbound.DeniedError{Status: status, Message: errMsg}
	}

	info := &inbound.TokenInfo{Subject: "js-authenticated", Extra: make(map[string]any)}
	if sub, ok := resMap["subject"].(string); ok && sub != "" {
		info.Subject = sub
	}
	if extraHeaders, ok := resMap["extra_headers"].(map[string]any); ok {
		for k, val := range extraHeaders {
			if vs, ok := val.(string); ok {
				info.Extra["header:"+k] = vs
			}
		}
	}
	return info, nil
}

// Provider implements outbound.TokenProvider using a JavaScript (Sobek) script.
// The script receives (upstream, ctx) and must return an object:
//
//	{ token?: string, raw_headers?: object, expiry?: number, error?: string }
//
// A fresh sobek.Runtime is created per script invocation (Sobek is not goroutine-safe).
// The pre-compiled program is reused. Results are cached at the Go level to avoid
// re-invoking the script on every request. The shared pool bounds the maximum number
// of concurrent JS runtimes to prevent OOM under load.
type Provider struct {
	upstreamName string
	program      *sobek.Program
	timeout      time.Duration
	env          map[string]string
	credentials  credentialCache
	scriptCache  *scriptCache // persists across callScript invocations
	httpClient   *http.Client
	pool         config.PoolAcquirer
	Next         http.Handler
}

// NewProvider creates a Provider by reading and pre-compiling the JS script.
// pool bounds the number of concurrent JS runtimes; it is shared with the inbound
// JS auth validator to enforce a single global limit for all auth scripts.
func NewProvider(upstreamName string, cfg config.JSOutboundConfig, pool config.PoolAcquirer) (*Provider, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading js outbound script %q: %w", cfg.ScriptPath, err)
	}
	prog, err := compileScript(cfg.ScriptPath, string(src))
	if err != nil {
		return nil, fmt.Errorf("compiling js outbound script %q: %w", cfg.ScriptPath, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{
		upstreamName: upstreamName,
		program:      prog,
		timeout:      timeout,
		env:          cfg.Env,
		scriptCache:  newScriptCache(),
		httpClient:   &http.Client{Timeout: defaultFetchTimeout},
		pool:         pool,
	}, nil
}

// Build implements middleware.Builder. It returns a Provider wired to next, sharing compiled program and cache.
func (p *Provider) Build(next http.Handler) http.Handler {
	return &Provider{
		upstreamName: p.upstreamName,
		program:      p.program,
		timeout:      p.timeout,
		env:          p.env,
		scriptCache:  p.scriptCache,
		httpClient:   p.httpClient,
		pool:         p.pool,
		Next:         next,
	}
}

// ServeHTTP implements http.Handler. It injects JS-script-derived credentials into the request context.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawHeaders, err := p.RawHeaders(ctx)
	if err != nil {
		p.Next.ServeHTTP(w, r.WithContext(outbound.WithAuthResult(ctx, outbound.AuthErrResult(err))))
		return
	}
	if len(rawHeaders) > 0 {
		ctx = outbound.WithHeaders(ctx, rawHeaders)
	} else {
		token, tokenErr := p.Token(ctx)
		if tokenErr != nil {
			p.Next.ServeHTTP(w, r.WithContext(outbound.WithAuthResult(ctx, outbound.AuthErrResult(tokenErr))))
			return
		}
		if token != "" {
			ctx = outbound.WithHeaders(ctx, map[string]string{"Authorization": "Bearer " + token})
		}
	}
	p.Next.ServeHTTP(w, r.WithContext(ctx))
}

// Token returns the current Bearer token, invoking the JS script if the cache has expired.
// Returns empty string if the script provides raw headers instead.
func (p *Provider) Token(ctx context.Context) (string, error) {
	if err := p.ensureCredentials(ctx); err != nil {
		return "", err
	}
	p.credentials.mu.Lock()
	defer p.credentials.mu.Unlock()
	if len(p.credentials.rawHeaders) > 0 {
		return "", nil
	}
	return p.credentials.token, nil
}

// RawHeaders returns the raw headers map, invoking the JS script if the cache has expired.
func (p *Provider) RawHeaders(ctx context.Context) (map[string]string, error) {
	if err := p.ensureCredentials(ctx); err != nil {
		return nil, err
	}
	p.credentials.mu.Lock()
	defer p.credentials.mu.Unlock()
	if len(p.credentials.rawHeaders) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(p.credentials.rawHeaders))
	for k, v := range p.credentials.rawHeaders {
		out[k] = v
	}
	return out, nil
}

// ensureCredentials refreshes the cached credentials if expired or absent.
func (p *Provider) ensureCredentials(ctx context.Context) error {
	p.credentials.mu.Lock()
	defer p.credentials.mu.Unlock()

	now := time.Now().Unix()
	if p.credentials.expiry != 0 && now < p.credentials.expiry {
		return nil
	}

	token, expiry, rawHeaders, err := p.callScript(ctx)
	if err != nil {
		return err
	}

	p.credentials.token = token
	if expiry == 0 {
		p.credentials.expiry = now + noCacheExpiry
	} else {
		p.credentials.expiry = expiry
	}
	p.credentials.rawHeaders = rawHeaders
	return nil
}

// callScript invokes the JS script and returns (token, expiry, rawHeaders, error).
func (p *Provider) callScript(ctx context.Context) (token string, expiry int64, rawHeaders map[string]string, err error) {
	release, acquireErr := p.pool.Acquire(ctx)
	if acquireErr != nil {
		return "", 0, nil, fmt.Errorf("js outbound auth: %w", acquireErr)
	}
	defer release()

	rt := sobek.New()

	// scriptCtx bounds ctx.fetch HTTP calls to the script deadline.
	scriptCtx := ctx
	if p.timeout > 0 {
		var cancel context.CancelFunc
		scriptCtx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
		timer := time.AfterFunc(p.timeout, func() {
			rt.Interrupt("js outbound auth script timed out")
		})
		defer timer.Stop()
	}

	ctxObj := buildCtxObject(scriptCtx, rt, p.env, p.scriptCache, p.httpClient)
	if err := rt.Set("__mcp_ctx__", ctxObj); err != nil {
		return "", 0, nil, fmt.Errorf("setting js outbound ctx: %w", err)
	}

	if _, err := rt.RunProgram(p.program); err != nil {
		return "", 0, nil, fmt.Errorf("running js outbound auth script: %w", err)
	}

	mainFn, ok := sobek.AssertFunction(rt.Get("__mcp_default__"))
	if !ok {
		return "", 0, nil, fmt.Errorf("js outbound auth script does not export a callable default function")
	}

	result, err := mainFn(sobek.Undefined(), rt.ToValue(p.upstreamName), ctxObj)
	if err != nil {
		return "", 0, nil, fmt.Errorf("js get_upstream_token: %w", err)
	}

	return parseOutboundResult(result)
}

// parseOutboundResult extracts token, expiry, and rawHeaders from the JS return value.
func parseOutboundResult(result sobek.Value) (token string, expiry int64, rawHeaders map[string]string, err error) {
	exported := result.Export()
	resMap, ok := exported.(map[string]any)
	if !ok {
		return "", 0, nil, fmt.Errorf("js get_upstream_token: return value must be an object, got %T", exported)
	}

	if errMsg, ok := resMap["error"].(string); ok && errMsg != "" {
		return "", 0, nil, fmt.Errorf("js get_upstream_token error: %s", errMsg)
	}

	if t, ok := resMap["token"].(string); ok {
		token = t
	}

	if e, ok := resMap["expiry"]; ok {
		switch ev := e.(type) {
		case int64:
			expiry = ev
		case float64:
			expiry = int64(ev)
		}
	}

	if rh, ok := resMap["raw_headers"].(map[string]any); ok {
		rawHeaders = make(map[string]string, len(rh))
		for k, v := range rh {
			if vs, ok := v.(string); ok {
				rawHeaders[k] = vs
			}
		}
	}

	if token == "" && len(rawHeaders) == 0 {
		return "", 0, nil, fmt.Errorf("js get_upstream_token: script must return token or raw_headers")
	}

	return token, expiry, rawHeaders, nil
}

// buildCtxObject constructs the JS ctx object exposed to auth scripts.
// Provides: ctx.env, ctx.log, ctx.jwt.decode, ctx.cache.get/set, ctx.fetch.
func buildCtxObject(ctx context.Context, rt *sobek.Runtime, env map[string]string, sc *scriptCache, client *http.Client) *sobek.Object {
	ctxObj := rt.NewObject()

	// ctx.env — read-only environment variables.
	envObj := rt.NewObject()
	for k, val := range env {
		expanded := os.ExpandEnv(val)
		if err := envObj.Set(k, expanded); err != nil {
			slog.Warn("js auth: failed to set env var", "key", k, "error", err)
		}
	}
	if err := ctxObj.Set("env", envObj); err != nil {
		slog.Warn("js auth: failed to set ctx.env", "error", err)
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
		slog.Warn("js auth: failed to set ctx.log", "error", err)
	}

	// ctx.jwt.decode(token) — decode a JWT payload without verification.
	jwtObj := rt.NewObject()
	if err := jwtObj.Set("decode", func(call sobek.FunctionCall) sobek.Value {
		if len(call.Arguments) == 0 {
			panic(rt.NewTypeError("ctx.jwt.decode requires a token argument"))
		}
		tok := call.Arguments[0].String()
		parts := strings.Split(tok, ".")
		if len(parts) < 2 {
			panic(rt.NewGoError(fmt.Errorf("ctx.jwt.decode: invalid JWT format")))
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("ctx.jwt.decode: decoding payload: %w", err)))
		}
		var claims any
		if err := json.Unmarshal(payload, &claims); err != nil {
			panic(rt.NewGoError(fmt.Errorf("ctx.jwt.decode: parsing claims: %w", err)))
		}
		return rt.ToValue(claims)
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.jwt.decode", "error", err)
	}
	if err := ctxObj.Set("jwt", jwtObj); err != nil {
		slog.Warn("js auth: failed to set ctx.jwt", "error", err)
	}

	// ctx.cache.get(key) / ctx.cache.set(key, value, ttlSeconds) — shared in-memory cache.
	cacheObj := rt.NewObject()
	if err := cacheObj.Set("get", func(call sobek.FunctionCall) sobek.Value {
		if len(call.Arguments) == 0 {
			return sobek.Null()
		}
		key := call.Arguments[0].String()
		val := sc.get(key)
		if val == nil {
			return sobek.Null()
		}
		return rt.ToValue(val)
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.cache.get", "error", err)
	}
	if err := cacheObj.Set("set", func(call sobek.FunctionCall) sobek.Value {
		if len(call.Arguments) < 2 {
			return sobek.Undefined()
		}
		key := call.Arguments[0].String()
		val := call.Arguments[1].Export()
		ttl := float64(0)
		if len(call.Arguments) >= 3 {
			ttl = call.Arguments[2].ToFloat()
		}
		sc.set(key, val, ttl)
		return sobek.Undefined()
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.cache.set", "error", err)
	}
	if err := ctxObj.Set("cache", cacheObj); err != nil {
		slog.Warn("js auth: failed to set ctx.cache", "error", err)
	}

	// ctx.fetch(url, opts) — sandboxed HTTP client.
	if err := ctxObj.Set("fetch", func(call sobek.FunctionCall) sobek.Value {
		return jsFetch(ctx, rt, client, call)
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.fetch", "error", err)
	}

	return ctxObj
}

// jsFetch implements ctx.fetch(url, opts) for auth scripts.
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
			slog.Warn("js auth: closing fetch response body", "error", closeErr)
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
	if err := json.Unmarshal(body, &parsed); err == nil {
		return rt.ToValue(parsed)
	}
	return rt.ToValue(string(body))
}

// compileScript pre-processes and compiles a JavaScript script source into a
// sobek.Program for reuse across multiple Execute calls.
//
// The script is wrapped to support common export patterns:
//   - ES module style:  export default function(args, ctx) { ... }
//   - CJS style:        module.exports = function(args, ctx) { ... }
//
// After the wrapper runs, the global variable __mcp_default__ holds the callable.
func compileScript(name, src string) (*sobek.Program, error) {
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
