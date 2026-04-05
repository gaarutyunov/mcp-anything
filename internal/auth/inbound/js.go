package inbound

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
	"github.com/gaarutyunov/mcp-anything/internal/script"
)

const defaultJSInboundTimeout = 500 * time.Millisecond
const defaultJSInboundFetchTimeout = 30 * time.Second

// JSValidator implements TokenValidator using a sandboxed Sobek JS runtime.
// The JavaScript script receives (token, ctx) and must return an object:
//
//	{ allowed: bool, status?: number, error?: string, subject?: string, extra_headers?: object }
//
// A fresh sobek.Runtime is created per call (Sobek is not goroutine-safe).
// The pre-compiled program is reused across calls.
type JSValidator struct {
	program    *sobek.Program
	timeout    time.Duration
	env        map[string]string
	cache      *jsInboundCache
	httpClient *http.Client
}

// NewJSValidator creates a JSValidator by reading and pre-compiling the JS script.
func NewJSValidator(cfg config.JSAuthConfig) (*JSValidator, error) {
	src, err := os.ReadFile(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("reading js auth script %q: %w", cfg.ScriptPath, err)
	}
	prog, err := script.CompileScript(cfg.ScriptPath, string(src))
	if err != nil {
		return nil, fmt.Errorf("compiling js auth script %q: %w", cfg.ScriptPath, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultJSInboundTimeout
	}
	return &JSValidator{
		program:    prog,
		timeout:    timeout,
		env:        cfg.Env,
		cache:      newJSInboundCache(),
		httpClient: &http.Client{Timeout: defaultJSInboundFetchTimeout},
	}, nil
}

// ValidateToken runs the JS script with the token and returns identity info on success.
func (v *JSValidator) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
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

	return parseJSInboundResult(result)
}

// parseJSInboundResult extracts TokenInfo from the JS script's return value.
func parseJSInboundResult(result sobek.Value) (*TokenInfo, error) {
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
		return nil, &DeniedError{Status: status, Message: errMsg}
	}

	info := &TokenInfo{Subject: "js-authenticated", Extra: make(map[string]any)}
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
func (v *JSValidator) buildCtxObject(ctx context.Context, rt *sobek.Runtime) *sobek.Object {
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
		return jsInboundFetch(ctx, rt, v.httpClient, call)
	}); err != nil {
		slog.Warn("js auth: failed to set ctx.fetch", "error", err)
	}

	return ctxObj
}

// jsInboundFetch implements ctx.fetch(url, opts) for inbound auth scripts.
func jsInboundFetch(ctx context.Context, rt *sobek.Runtime, client *http.Client, call sobek.FunctionCall) sobek.Value {
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

// jsInboundCache is a thread-safe key-value cache with optional TTL,
// shared across ValidateToken calls for the same JSValidator instance.
type jsInboundCache struct {
	mu    sync.Mutex
	items map[string]jsInboundCacheItem
}

type jsInboundCacheItem struct {
	value     any
	expiresAt time.Time
}

func newJSInboundCache() *jsInboundCache {
	return &jsInboundCache{items: make(map[string]jsInboundCacheItem)}
}

func (c *jsInboundCache) get(key string) any {
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

func (c *jsInboundCache) set(key string, value any, ttlSeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttlSeconds > 0 {
		exp = time.Now().Add(time.Duration(ttlSeconds * float64(time.Second)))
	}
	c.items[key] = jsInboundCacheItem{value: value, expiresAt: exp}
}
