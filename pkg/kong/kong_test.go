package kong

import (
	"net/http"
	"testing"
)

// mockHandler records whether ServeHTTP was called and the body to write.
type mockHandler struct {
	called bool
	body   string
	status int
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
	_, _ = w.Write([]byte(h.body))
}

func TestNew(t *testing.T) {
	v := New()
	if _, ok := v.(*Config); !ok {
		t.Errorf("New() returned %T, want *Config", v)
	}
}

func TestFindHandler_ExactMatch(t *testing.T) {
	mh := &mockHandler{}
	conf := &Config{handlers: map[string]http.Handler{"/mcp": mh}}

	h := conf.findHandler("/mcp")
	if h != mh {
		t.Fatal("expected exact-match handler for /mcp")
	}
}

func TestFindHandler_PrefixMatch(t *testing.T) {
	mh := &mockHandler{}
	conf := &Config{handlers: map[string]http.Handler{"/mcp": mh}}

	h := conf.findHandler("/mcp/session/abc123")
	if h != mh {
		t.Fatal("expected prefix-match handler for /mcp/session/abc123")
	}
}

func TestFindHandler_NoMatch(t *testing.T) {
	mh := &mockHandler{}
	conf := &Config{handlers: map[string]http.Handler{"/mcp": mh}}

	h := conf.findHandler("/api/other")
	if h != nil {
		t.Fatal("expected nil handler for non-matching path /api/other")
	}
}

func TestFindHandler_MultipleEndpoints_ExactMatch(t *testing.T) {
	mhRW := &mockHandler{body: "rw"}
	mhRO := &mockHandler{body: "ro"}
	conf := &Config{
		handlers: map[string]http.Handler{
			"/mcp":          mhRW,
			"/mcp/readonly": mhRO,
		},
	}

	h := conf.findHandler("/mcp/readonly")
	if h != mhRO {
		t.Error("exact match /mcp/readonly should return the /mcp/readonly handler")
	}
}

func TestResponseCapture_DefaultStatus(t *testing.T) {
	rw := newResponseCapture()
	if rw.code != http.StatusOK {
		t.Errorf("default status = %d, want %d", rw.code, http.StatusOK)
	}
}

func TestResponseCapture_WriteHeader(t *testing.T) {
	rw := newResponseCapture()
	rw.WriteHeader(http.StatusCreated)
	if rw.code != http.StatusCreated {
		t.Errorf("code = %d, want %d", rw.code, http.StatusCreated)
	}
}

func TestResponseCapture_Write(t *testing.T) {
	rw := newResponseCapture()
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if string(rw.buf) != "hello" {
		t.Errorf("buf = %q, want %q", rw.buf, "hello")
	}
}

func TestResponseCapture_MultipleWrites(t *testing.T) {
	rw := newResponseCapture()
	_, _ = rw.Write([]byte("hello"))
	_, _ = rw.Write([]byte(" world"))
	if string(rw.buf) != "hello world" {
		t.Errorf("buf = %q, want %q", rw.buf, "hello world")
	}
}

func TestResponseCapture_Header(t *testing.T) {
	rw := newResponseCapture()
	rw.Header().Set("Content-Type", "application/json")
	if rw.hdr.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rw.hdr.Get("Content-Type"))
	}
}
