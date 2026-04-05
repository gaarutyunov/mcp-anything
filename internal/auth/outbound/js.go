package outbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grafana/sobek"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
	"github.com/gaarutyunov/mcp-anything/internal/script"
)

const defaultJSOutboundTimeout = 500 * time.Millisecond
const defaultJSOutboundFetchTimeout = 30 * time.Second

// jsOutboundNoCacheExpiry is a short-lived expiry (1 second) used when the JS script
// returns no expiry. This prevents double-execution within the same RoundTrip() call,
// while still refreshing credentials on subsequent requests.
const jsOutboundNoCacheExpiry = int64(1)

// JSProvider implements TokenProvider using a JavaScript (Sobek) script.
// The script receives (upstream, ctx) and must return an object:
//
//	{ token?: string, raw_headers?: object, expiry?: number, error?: string }
//
// A fresh sobek.Runtime is created per script invocation (Sobek is not goroutine-safe).
// The pre-compiled program is reused. Results are cached at the Go level to avoid
// re-invoking the script on every request. The shared pool bounds the maximum number
// of concurrent JS runtimes to prevent OOM under load.
type JSProvider struct {
	upstreamName string
	program      *sobek.Program
	timeout      time.Duration
	env          map[string]string
	cache        *jsOutboundCache
	scriptCache  *jsOutboundScriptCache // persists across callScript invocations
	httpClient   *http.Client
	pool         *runtime.Pool
}

type jsOutboundCache struct {
	mu         sync.Mutex
	token      string
	expiry     int64 // unix timestamp; 0 = fetch on next call
	rawHeaders map[string]string
}

// NewJSProvider creates a JSProvider by reading and pre-compiling the JS script.
// pool bounds the number of concurrent JS runtimes; it is shared with the inbound
// JS auth validator to enforce a single global limit for all auth scripts.
func NewJSProvider(upstreamName string, cfg config.JSOutboundConfig, pool *runtime.Pool) (*JSProvider, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading js outbound script %q: %w", cfg.ScriptPath, err)
	}
	prog, err := script.CompileScript(cfg.ScriptPath, string(src))
	if err != nil {
		return nil, fmt.Errorf("compiling js outbound script %q: %w", cfg.ScriptPath, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultJSOutboundTimeout
	}
	return &JSProvider{
		upstreamName: upstreamName,
		program:      prog,
		timeout:      timeout,
		env:          cfg.Env,
		cache:        &jsOutboundCache{},
		scriptCache:  newJSOutboundScriptCache(),
		httpClient:   &http.Client{Timeout: defaultJSOutboundFetchTimeout},
		pool:         pool,
	}, nil
}

// Token returns the current Bearer token, invoking the JS script if the cache has expired.
// Returns empty string if the script provides raw headers instead.
func (p *JSProvider) Token(ctx context.Context) (string, error) {
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

// RawHeaders returns the raw headers map, invoking the JS script if the cache has expired.
func (p *JSProvider) RawHeaders(ctx context.Context) (map[string]string, error) {
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

// ensureToken refreshes the cached credentials if expired or absent.
func (p *JSProvider) ensureToken(ctx context.Context) error {
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()

	now := time.Now().Unix()
	if p.cache.expiry != 0 && now < p.cache.expiry {
		return nil
	}

	token, expiry, rawHeaders, err := p.callScript(ctx)
	if err != nil {
		return err
	}

	p.cache.token = token
	if expiry == 0 {
		p.cache.expiry = now + jsOutboundNoCacheExpiry
	} else {
		p.cache.expiry = expiry
	}
	p.cache.rawHeaders = rawHeaders
	return nil
}

// callScript invokes the JS script and returns (token, expiry, rawHeaders, error).
func (p *JSProvider) callScript(ctx context.Context) (token string, expiry int64, rawHeaders map[string]string, err error) {
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

	ctxObj := p.buildCtxObject(scriptCtx, rt, p.scriptCache)
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

	return parseJSOutboundResult(result)
}

// parseJSOutboundResult extracts token, expiry, and rawHeaders from the JS return value.
func parseJSOutboundResult(result sobek.Value) (token string, expiry int64, rawHeaders map[string]string, err error) {
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

// buildCtxObject constructs the JS ctx object for outbound auth scripts.
// Provides: ctx.env, ctx.log, ctx.jwt.decode, ctx.cache.get/set, ctx.fetch.
func (p *JSProvider) buildCtxObject(ctx context.Context, rt *sobek.Runtime, scriptCache *jsOutboundScriptCache) *sobek.Object {
	ctxObj := rt.NewObject()

	// ctx.env — read-only environment variables.
	envObj := rt.NewObject()
	for k, val := range p.env {
		expanded := os.ExpandEnv(val)
		if err := envObj.Set(k, expanded); err != nil {
			slog.Warn("js outbound auth: failed to set env var", "key", k, "error", err)
		}
	}
	if err := ctxObj.Set("env", envObj); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.env", "error", err)
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
		slog.Warn("js outbound auth: failed to set ctx.log", "error", err)
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
		slog.Warn("js outbound auth: failed to set ctx.jwt.decode", "error", err)
	}
	if err := ctxObj.Set("jwt", jwtObj); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.jwt", "error", err)
	}

	// ctx.cache.get(key) / ctx.cache.set(key, value, ttlSeconds) — shared in-memory cache.
	// This cache persists across callScript invocations via the JSProvider.cache field.
	// The scriptCache here is a per-invocation wrapper that safely exports values back to Go.
	cacheObj := rt.NewObject()
	if err := cacheObj.Set("get", func(call sobek.FunctionCall) sobek.Value {
		if len(call.Arguments) == 0 {
			return sobek.Null()
		}
		key := call.Arguments[0].String()
		val := scriptCache.get(key)
		if val == nil {
			return sobek.Null()
		}
		return rt.ToValue(val)
	}); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.cache.get", "error", err)
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
		scriptCache.set(key, val, ttl)
		return sobek.Undefined()
	}); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.cache.set", "error", err)
	}
	if err := ctxObj.Set("cache", cacheObj); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.cache", "error", err)
	}

	// ctx.fetch(url, opts) — sandboxed HTTP client.
	if err := ctxObj.Set("fetch", func(call sobek.FunctionCall) sobek.Value {
		return jsOutboundFetch(ctx, rt, p.httpClient, call)
	}); err != nil {
		slog.Warn("js outbound auth: failed to set ctx.fetch", "error", err)
	}

	return ctxObj
}

// jsOutboundFetch implements ctx.fetch(url, opts) for outbound auth scripts.
func jsOutboundFetch(ctx context.Context, rt *sobek.Runtime, client *http.Client, call sobek.FunctionCall) sobek.Value {
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
			slog.Warn("js outbound auth: closing fetch response body", "error", closeErr)
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

// jsOutboundScriptCache is a thread-safe key-value cache with optional TTL.
// It is shared across callScript invocations within the same JSProvider,
// allowing scripts to cache tokens between invocations via ctx.cache.get/set.
type jsOutboundScriptCache struct {
	mu    sync.Mutex
	items map[string]jsOutboundScriptCacheItem
}

type jsOutboundScriptCacheItem struct {
	value     any
	expiresAt time.Time
}

func newJSOutboundScriptCache() *jsOutboundScriptCache {
	return &jsOutboundScriptCache{items: make(map[string]jsOutboundScriptCacheItem)}
}

func (c *jsOutboundScriptCache) get(key string) any {
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

func (c *jsOutboundScriptCache) set(key string, value any, ttlSeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttlSeconds > 0 {
		exp = time.Now().Add(time.Duration(ttlSeconds * float64(time.Second)))
	}
	c.items[key] = jsOutboundScriptCacheItem{value: value, expiresAt: exp}
}
