// Package testkit provides helpers for testing with mcp-anything as a library.
package testkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gaarutyunov/mcp-anything/pkg/mcpanything"
)

// TestProxy wraps a Proxy for use in tests.
// It manages lifecycle and provides convenience methods.
type TestProxy struct {
	proxy  *mcpanything.Proxy
	cancel context.CancelFunc
	errCh  chan error
}

// NewTestProxy creates a test proxy from a config file path.
// The proxy is not started until Start() is called.
func NewTestProxy(t *testing.T, configPath string, opts ...mcpanything.Option) *TestProxy {
	t.Helper()

	allOpts := []mcpanything.Option{mcpanything.WithConfigPath(configPath)}
	allOpts = append(allOpts, opts...)

	proxy, err := mcpanything.New(allOpts...)
	if err != nil {
		t.Fatalf("creating test proxy: %v", err)
	}
	return &TestProxy{proxy: proxy, errCh: make(chan error, 1)}
}

// Start launches the proxy in a background goroutine.
// The proxy is automatically stopped when the test finishes.
func (tp *TestProxy) Start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	tp.cancel = cancel

	go func() {
		tp.errCh <- tp.proxy.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		if err := <-tp.errCh; err != nil && err != context.Canceled {
			t.Errorf("test proxy error: %v", err)
		}
	})
}

// WriteConfig writes a YAML config string to a temporary file and returns the path.
// The file is automatically cleaned up when the test finishes.
func WriteConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

// MockProvider implements a simple mock tool provider for testing.
type MockProvider struct {
	name      string
	executeFn func(ctx context.Context, tool string, args map[string]any) (string, error)
}

// NewMockProvider creates a new MockProvider with the given name and execute function.
func NewMockProvider(name string, fn func(ctx context.Context, tool string, args map[string]any) (string, error)) *MockProvider {
	return &MockProvider{name: name, executeFn: fn}
}

// Name returns the provider name.
func (m *MockProvider) Name() string { return m.name }

// Execute calls the mock function.
func (m *MockProvider) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	if m.executeFn == nil {
		return "", fmt.Errorf("no execute function configured for mock provider %q", m.name)
	}
	return m.executeFn(ctx, tool, args)
}
