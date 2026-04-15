package lua

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/runtime"
)

func writeLuaScript(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "lua_*.lua")
	if err != nil {
		t.Fatalf("create temp lua file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write lua script: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

func newTestValidator(t *testing.T, script string, timeout time.Duration) *Validator {
	t.Helper()
	path := writeLuaScript(t, script)
	pool := runtime.NewPool(runtime.DefaultMaxAuthVMs)
	v, err := NewValidator(config.LuaAuthConfig{ScriptPath: path, Timeout: timeout}, pool)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

func TestValidatorAllowsValidToken(t *testing.T) {
	v := newTestValidator(t, `
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

func TestValidatorDeniesToken(t *testing.T) {
	v := newTestValidator(t, `
local token = ...
return false, 401, {}, "forbidden"
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for denied token")
	}
	var denied *inbound.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected *inbound.DeniedError, got %T: %v", err, err)
	}
	if denied.Status != 401 {
		t.Errorf("denied.Status = %d, want 401", denied.Status)
	}
	if denied.Message != "forbidden" {
		t.Errorf("denied.Message = %q, want %q", denied.Message, "forbidden")
	}
}

func TestValidatorTimeoutEnforced(t *testing.T) {
	v := newTestValidator(t, `
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

func TestValidatorCompileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lua")
	if err := os.WriteFile(path, []byte(`this is not valid lua @@##`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pool := runtime.NewPool(runtime.DefaultMaxAuthVMs)
	_, err := NewValidator(config.LuaAuthConfig{ScriptPath: path, Timeout: 500 * time.Millisecond}, pool)
	if err == nil {
		t.Fatal("expected compile error for invalid Lua")
	}
}

func TestValidatorOsSandboxed(t *testing.T) {
	// os.getenv should fail because os library is not loaded.
	v := newTestValidator(t, `
local token = ...
local val = os.getenv("HOME")
return true, 200, {}, ""
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "any-token")
	if err == nil {
		t.Fatal("expected error: os should be nil in sandbox")
	}
}

func TestValidatorIoSandboxed(t *testing.T) {
	// io.open should fail because io library is not loaded.
	v := newTestValidator(t, `
local token = ...
local f = io.open("/etc/passwd", "r")
return true, 200, {}, ""
`, 500*time.Millisecond)

	_, err := v.ValidateToken(context.Background(), "any-token")
	if err == nil {
		t.Fatal("expected error: io should be nil in sandbox")
	}
}

func TestValidatorExtraHeaders(t *testing.T) {
	v := newTestValidator(t, `
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

func TestValidatorConcurrentNoDataRace(t *testing.T) {
	v := newTestValidator(t, `
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
