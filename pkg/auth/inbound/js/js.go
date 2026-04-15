// Package js registers the "inbound/js" middleware strategy.
// Import this package (blank import) to make the strategy available via middleware.New().
package js

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

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgmiddleware "github.com/gaarutyunov/mcp-anything/pkg/middleware"
)

const defaultTimeout = 500 * time.Millisecond
const defaultFetchTimeout = 30 * time.Second

func init() {
	pkgmiddleware.Register("inbound/js", func(_ context.Context, cfg any) (func(http.Handler) http.Handler, error) {
		ic, ok := cfg.(*config.InboundAuthConfig)
		if !ok {
			return nil, fmt.Errorf("inbound/js: expected *config.InboundAuthConfig, got %T", cfg)
		}
		if ic.JSAuthPool == nil {
			return nil, fmt.Errorf("js inbound auth requires runtime pools; set InboundAuthConfig.JSAuthPool")
		}
		v, err := NewValidator(ic.JS, ic.JSAuthPool)
		if err != nil {
			return nil, err
		}
		return inbound.ValidatorMiddleware(v, ""), nil
	})
}

// Validator implements TokenValidator using a sandboxed Sobek JS runtime.
// The JavaScript script receives (token, ctx) and must return an object:
//
//	{ allowed: bool, status?: number, error?: string, subject?: string, extra_headers?: object }
//
// A fresh sobek.Runtime is created per call (Sobek is not goroutine-safe).
// The pre-compiled program is reused across calls. The shared pool bounds
// the maximum number of concurrent JS runtimes to prevent OOM under load.
type Validator struct {
	program    *sobek.Program
	timeout    time.Duration
	env        map[string]string
	cache      *jsCache
	httpClient *http.Client
	pool       config.PoolAcquirer
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
		program:    prog,
		timeout:    timeout,
		env:        cfg.Env,
		cache:      newJSCache(),
		httpClient: &http.Client{Timeout: defaultFetchTimeout},
		pool:       pool,
	}, nil
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

	ctxObj := v.buildCtxObject(scriptCtx, rt)
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

	return parseJSResult(result)
}

// parseJSResult extracts TokenInfo from the JS script's return value.
func parseJSResult(result sobek.Value) (*inbound.TokenInfo, error) {
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

// buildCtxObject constructs the JS ctx object exposed to auth scripts.
// Provides: ctx.env, ctx.log, ctx.jwt.decode, ctx.cache.get/set, ctx.fetch.
func (v *Validator) buildCtxObject(ctx context.Context, rt *sobek.Runtime) *sobek.Object {
	ctxObj := rt.NewObject()

	// ctx.env — read-only environment variables.
	envObj := rt.NewObject()
	for k, val := range v.env {
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
		val := v.cache.get(key)
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
		v.cache.set(key, val, ttl)
		return sobek.Undefined()
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.cache.set", "error", err)
	}
	if err := ctxObj.Set("cache", cacheObj); err != nil {
		slog.Warn("js auth: failed to set ctx.cache", "error", err)
	}

	// ctx.fetch(url, opts) — sandboxed HTTP client.
	if err := ctxObj.Set("fetch", func(call sobek.FunctionCall) sobek.Value {
		return jsFetch(ctx, rt, v.httpClient, call)
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.fetch", "error", err)
	}

	return ctxObj
}

// jsFetch implements ctx.fetch(url, opts) for inbound auth scripts.
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

// jsCache is a thread-safe key-value cache with optional TTL,
// shared across ValidateToken calls for the same Validator instance.
type jsCache struct {
	mu    sync.Mutex
	items map[string]jsCacheItem
}

type jsCacheItem struct {
	value     any
	expiresAt time.Time
}

func newJSCache() *jsCache {
	return &jsCache{items: make(map[string]jsCacheItem)}
}

func (c *jsCache) get(key string) any {
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

func (c *jsCache) set(key string, value any, ttlSeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttlSeconds > 0 {
		exp = time.Now().Add(time.Duration(ttlSeconds * float64(time.Second)))
	}
	c.items[key] = jsCacheItem{value: value, expiresAt: exp}
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
