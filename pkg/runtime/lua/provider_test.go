package lua

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

func newTestProvider(t *testing.T, scriptPath string, timeout time.Duration) *Provider {
	t.Helper()
	pool, err := runtime.NewPool(runtime.DefaultMaxAuthVMs)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	p, err := NewProvider("test-upstream", config.LuaOutboundSpec{ScriptPath: scriptPath, Timeout: timeout}, pool)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func TestLuaProviderTokenCached(t *testing.T) {
	futureExpiry := time.Now().Add(10 * time.Second).Unix()
	path := writeLuaScript(t, `
local upstream, cached_token, cached_expiry = ...
return "test-token", `+strconv.FormatInt(futureExpiry, 10)+`, {}, ""
`)
	p := newTestProvider(t, path, 500*time.Millisecond)

	tok1, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() 1st: %v", err)
	}
	if tok1 != "test-token" {
		t.Errorf("1st token = %q, want %q", tok1, "test-token")
	}

	// Second call: cache still valid (expiry is in the future), so token returned without re-calling script.
	tok2, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() 2nd: %v", err)
	}
	if tok2 != "test-token" {
		t.Errorf("2nd token = %q, want %q", tok2, "test-token")
	}
	// Verify cache is set and expiry matches.
	p.cache.mu.Lock()
	cacheExpiry := p.cache.expiry
	p.cache.mu.Unlock()
	if cacheExpiry != futureExpiry {
		t.Errorf("cache expiry = %d, want %d", cacheExpiry, futureExpiry)
	}
}

func TestLuaProviderNoCacheRefreshesOnNextRequest(t *testing.T) {
	// expiry_unix == 0 from the script means short-lived caching (1 second) to prevent
	// double-execution within a single RoundTrip() (RawHeaders + Token call pair).
	// After expiry, the script re-runs on the next request.
	path := writeLuaScript(t, `
local upstream, cached_token, cached_expiry = ...
return "dynamic-token", 0, {}, ""
`)
	p := newTestProvider(t, path, 500*time.Millisecond)

	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok != "dynamic-token" {
		t.Errorf("token = %q, want %q", tok, "dynamic-token")
	}

	// Simulate a new request arriving after the short-lived cache expires
	// by manually clearing the cache (as would happen after ~1 second in production).
	p.cache.mu.Lock()
	p.cache.expiry = 0
	p.cache.token = ""
	p.cache.rawHeaders = nil
	p.cache.mu.Unlock()

	tok2, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() 2nd call: %v", err)
	}
	if tok2 != "dynamic-token" {
		t.Errorf("2nd token = %q, want %q", tok2, "dynamic-token")
	}
}

func TestLuaProviderRawHeaders(t *testing.T) {
	path := writeLuaScript(t, `
local upstream, cached_token, cached_expiry = ...
return "", 0, {["X-API-Key"] = "key123", ["X-Tenant"] = "acme"}, ""
`)
	p := newTestProvider(t, path, 500*time.Millisecond)

	// Token should be empty when raw headers are present.
	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok != "" {
		t.Errorf("Token() = %q, want empty when raw headers present", tok)
	}

	// Reset cache so RawHeaders re-calls the script.
	p.cache.mu.Lock()
	p.cache.token = ""
	p.cache.expiry = 0
	p.cache.rawHeaders = nil
	p.cache.mu.Unlock()

	headers, err := p.RawHeaders(context.Background())
	if err != nil {
		t.Fatalf("RawHeaders(): %v", err)
	}
	if headers["X-API-Key"] != "key123" {
		t.Errorf("X-API-Key = %q, want %q", headers["X-API-Key"], "key123")
	}
	if headers["X-Tenant"] != "acme" {
		t.Errorf("X-Tenant = %q, want %q", headers["X-Tenant"], "acme")
	}
}

func TestLuaProviderTimeoutEnforced(t *testing.T) {
	// Script loops forever; context timeout should kill it.
	path := writeLuaScript(t, `
local upstream, cached_token, cached_expiry = ...
while true do end
return "token", 0, {}, ""
`)
	p := newTestProvider(t, path, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := p.Token(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
