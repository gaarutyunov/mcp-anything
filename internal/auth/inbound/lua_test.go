package inbound

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/internal/config"
	"github.com/gaarutyunov/mcp-anything/internal/runtime"
)

func writeLuaScript(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "auth_*.lua")
	if err != nil {
		t.Fatalf("create temp lua file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write lua script: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

func newValidator(t *testing.T, script string, timeout time.Duration) *LuaValidator {
	t.Helper()
	path := writeLuaScript(t, script)
	pool := runtime.NewPool(runtime.DefaultMaxAuthVMs)
	v, err := NewLuaValidator(config.LuaAuthConfig{ScriptPath: path, Timeout: timeout}, pool)
	if err != nil {
		t.Fatalf("NewLuaValidator: %v", err)
	}
	return v
}

func TestLuaValidatorAllowsValidToken(t *testing.T) {
	v := newValidator(t, `
local token = ...
return true, 200, {}, ""
`, 500*time.Millisecond)

	info, err := v.ValidateToken(context.Background(), "any-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil TokenInfo")
	}
	if info.Subject != "lua-authenticated" {
		t.Errorf("subject = %q, want %q", info.Subject, "lua-authenticated")
	}
}

func TestLuaValidatorDeniesToken(t *testing.T) {
	v := newValidator(t, `
local token = ...
return false, 401, {}, "forbidden"
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for denied token")
	}
}

func TestLuaValidatorTimeoutEnforced(t *testing.T) {
	v := newValidator(t, `
local token = ...
while true do end
return true, 200, {}, ""
`, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := v.ValidateToken(ctx, "any-token")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestLuaValidatorCompileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lua")
	if err := os.WriteFile(path, []byte(`this is not valid lua @@##`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pool := runtime.NewPool(runtime.DefaultMaxAuthVMs)
	_, err := NewLuaValidator(config.LuaAuthConfig{ScriptPath: path, Timeout: 500 * time.Millisecond}, pool)
	if err == nil {
		t.Fatal("expected compile error for invalid Lua")
	}
}

func TestLuaValidatorOsSandboxed(t *testing.T) {
	// os.getenv should fail because os library is not loaded.
	v := newValidator(t, `
local token = ...
local val = os.getenv("HOME")
return true, 200, {}, ""
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "any-token")
	if err == nil {
		t.Fatal("expected error: os should be nil in sandbox")
	}
}

func TestLuaValidatorIoSandboxed(t *testing.T) {
	// io.open should fail because io library is not loaded.
	v := newValidator(t, `
local token = ...
local f = io.open("/etc/passwd", "r")
return true, 200, {}, ""
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "any-token")
	if err == nil {
		t.Fatal("expected error: io should be nil in sandbox")
	}
}

func TestLuaValidatorExtraHeaders(t *testing.T) {
	v := newValidator(t, `
local token = ...
return true, 200, {["X-User-ID"] = "user-42", ["X-Role"] = "admin"}, ""
`, 500*time.Millisecond)

	info, err := v.ValidateToken(context.Background(), "any-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Extra["header:X-User-ID"] != "user-42" {
		t.Errorf("extra[header:X-User-ID] = %v, want %q", info.Extra["header:X-User-ID"], "user-42")
	}
	if info.Extra["header:X-Role"] != "admin" {
		t.Errorf("extra[header:X-Role] = %v, want %q", info.Extra["header:X-Role"], "admin")
	}
}

func TestLuaValidatorConcurrentNoDataRace(t *testing.T) {
	v := newValidator(t, `
local token = ...
if token == "good" then
    return true, 200, {}, ""
end
return false, 401, {}, "bad token"
`, 500*time.Millisecond)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			token := "good"
			if i%2 == 0 {
				token = "bad"
			}
			_, _ = v.ValidateToken(context.Background(), token)
		}(i)
	}
	wg.Wait()
}
