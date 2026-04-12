package caddy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// mockHandler records whether it was called.
type mockHandler struct {
	called bool
	body   string
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.body))
}

// nextHandler is a caddyhttp.Handler stub that records calls.
type nextHandler struct {
	called bool
}

func (n *nextHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) error {
	n.called = true
	w.WriteHeader(http.StatusNotFound)
	return nil
}

func TestCaddyModule(t *testing.T) {
	info := MCPAnything{}.CaddyModule()
	if info.ID != "http.handlers.mcpanything" {
		t.Errorf("expected module ID http.handlers.mcpanything, got %s", info.ID)
	}
	if info.New == nil {
		t.Error("CaddyModule.New must not be nil")
	}
	mod := info.New()
	if _, ok := mod.(*MCPAnything); !ok {
		t.Errorf("New() returned %T, want *MCPAnything", mod)
	}
}

func TestUnmarshalCaddyfile_WithConfigPath(t *testing.T) {
	input := `mcpanything {
		config_path /etc/mcp-anything/config.yaml
	}`
	d := caddyfile.NewTestDispenser(input)
	m := &MCPAnything{}
	if err := m.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ConfigPath != "/etc/mcp-anything/config.yaml" {
		t.Errorf("ConfigPath = %q, want /etc/mcp-anything/config.yaml", m.ConfigPath)
	}
}

func TestUnmarshalCaddyfile_EmptyBlock(t *testing.T) {
	input := `mcpanything {}`
	d := caddyfile.NewTestDispenser(input)
	m := &MCPAnything{}
	if err := m.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty", m.ConfigPath)
	}
}

func TestUnmarshalCaddyfile_UnknownDirective(t *testing.T) {
	input := `mcpanything {
		unknown_key value
	}`
	d := caddyfile.NewTestDispenser(input)
	m := &MCPAnything{}
	if err := m.UnmarshalCaddyfile(d); err == nil {
		t.Error("expected error for unknown directive, got nil")
	}
}

func TestServeHTTP_ExactMatch(t *testing.T) {
	mh := &mockHandler{body: "mcp response"}
	m := &MCPAnything{handlers: map[string]http.Handler{"/mcp": mh}}

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	next := &nextHandler{}

	if err := m.ServeHTTP(rr, req, next); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if !mh.called {
		t.Error("expected MCP handler to be called for exact match /mcp")
	}
	if next.called {
		t.Error("next handler must not be called when path matches")
	}
}

func TestServeHTTP_PrefixMatch(t *testing.T) {
	mh := &mockHandler{body: "mcp response"}
	m := &MCPAnything{handlers: map[string]http.Handler{"/mcp": mh}}

	req := httptest.NewRequest(http.MethodPost, "/mcp/session/abc", nil)
	rr := httptest.NewRecorder()
	next := &nextHandler{}

	if err := m.ServeHTTP(rr, req, next); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if !mh.called {
		t.Error("expected MCP handler for prefix match /mcp/session/abc")
	}
	if next.called {
		t.Error("next handler must not be called when path prefix matches")
	}
}

func TestServeHTTP_NoMatch_CallsNext(t *testing.T) {
	mh := &mockHandler{}
	m := &MCPAnything{handlers: map[string]http.Handler{"/mcp": mh}}

	req := httptest.NewRequest(http.MethodGet, "/api/other", nil)
	rr := httptest.NewRecorder()
	next := &nextHandler{}

	if err := m.ServeHTTP(rr, req, next); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if mh.called {
		t.Error("MCP handler must not be called for non-matching path")
	}
	if !next.called {
		t.Error("next handler must be called when no path matches")
	}
}

func TestServeHTTP_MultipleEndpoints_ExactMatch(t *testing.T) {
	mhRW := &mockHandler{body: "rw"}
	mhRO := &mockHandler{body: "ro"}
	m := &MCPAnything{
		handlers: map[string]http.Handler{
			"/mcp":          mhRW,
			"/mcp/readonly": mhRO,
		},
	}
	next := &nextHandler{}

	req := httptest.NewRequest(http.MethodGet, "/mcp/readonly", nil)
	rr := httptest.NewRecorder()
	if err := m.ServeHTTP(rr, req, next); err != nil {
		t.Fatalf("ServeHTTP error: %v", err)
	}
	if !mhRO.called {
		t.Error("expected /mcp/readonly handler for exact match")
	}
	if mhRW.called {
		t.Error("/mcp handler must not be called for /mcp/readonly")
	}
}
