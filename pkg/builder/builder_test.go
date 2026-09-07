package builder

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_validTargets(t *testing.T) {
	targets := []string{TargetProxy, TargetOperator, TargetCaddy, TargetKong}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			b, err := New(Config{Target: target})
			if err != nil {
				t.Fatalf("New(%q): unexpected error: %v", target, err)
			}
			if b == nil {
				t.Fatal("New returned nil builder")
			}
		})
	}
}

func TestNew_unknownTarget(t *testing.T) {
	_, err := New(Config{Target: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("error should mention 'unknown target', got: %v", err)
	}
}

func TestNew_emptyTarget(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestNew_defaults(t *testing.T) {
	b, err := New(Config{Target: TargetProxy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.cfg.OutputFile == "" {
		t.Error("OutputFile should have a default value")
	}
	if b.cfg.ModuleDir == "" {
		t.Error("ModuleDir should have a default value")
	}
	if b.cfg.Stderr == nil {
		t.Error("Stderr should have a default value")
	}
}

func TestNew_outputFileDefault(t *testing.T) {
	b, err := New(Config{Target: TargetProxy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(b.cfg.OutputFile, filepath.Join("bin", "proxy")) {
		t.Errorf("default OutputFile should end with bin/proxy, got: %s", b.cfg.OutputFile)
	}
}

func TestWriteGoMod_noExtraRequire(t *testing.T) {
	dir := t.TempDir()
	b, _ := New(Config{Target: TargetProxy, ModuleDir: "/fake/module"})

	if err := b.writeGoMod(dir, proxyTarget); err != nil {
		t.Fatalf("writeGoMod: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "mcp-anything-custom") {
		t.Error("go.mod should contain module name mcp-anything-custom")
	}
	if !strings.Contains(content, localModule) {
		t.Errorf("go.mod should contain %s", localModule)
	}
	if !strings.Contains(content, pseudoVer) {
		t.Errorf("go.mod should contain pseudo-version %s", pseudoVer)
	}
	if !strings.Contains(content, "replace "+localModule+" => /fake/module") {
		t.Error("go.mod should contain replace directive")
	}
}

func TestWriteGoMod_extraRequire(t *testing.T) {
	dir := t.TempDir()
	b, _ := New(Config{Target: TargetKong, ModuleDir: "/fake/module"})

	if err := b.writeGoMod(dir, kongTarget); err != nil {
		t.Fatalf("writeGoMod: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	content := string(data)

	for _, req := range kongTarget.extraRequire {
		if !strings.Contains(content, req) {
			t.Errorf("go.mod should contain extra require %q", req)
		}
	}
}

func TestWriteMain_allTargets(t *testing.T) {
	for name, spec := range registeredTargets {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			b, err := New(Config{Target: name})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if err := b.writeMain(dir, spec); err != nil {
				t.Fatalf("writeMain for target %q: %v", name, err)
			}

			data, err := os.ReadFile(filepath.Join(dir, "main.go"))
			if err != nil {
				t.Fatalf("reading main.go: %v", err)
			}
			if len(data) == 0 {
				t.Error("main.go should not be empty")
			}
			if !strings.Contains(string(data), "package main") {
				t.Error("main.go should declare package main")
			}
		})
	}
}

func TestWriteMain_packagesIncluded(t *testing.T) {
	dir := t.TempDir()
	pkg := "github.com/gaarutyunov/mcp-anything/pkg/cache/redis"
	b, _ := New(Config{Target: TargetProxy, Packages: []string{pkg}})

	if err := b.writeMain(dir, proxyTarget); err != nil {
		t.Fatalf("writeMain: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	if !strings.Contains(string(data), pkg) {
		t.Errorf("main.go should contain import %q", pkg)
	}
}

func TestWriteMain_emptyPackages(t *testing.T) {
	dir := t.TempDir()
	b, _ := New(Config{Target: TargetProxy, Packages: nil})

	if err := b.writeMain(dir, proxyTarget); err != nil {
		t.Fatalf("writeMain with no packages: %v", err)
	}
}

func TestCopyGoSum_absent(t *testing.T) {
	dir := t.TempDir()
	moduleDir := t.TempDir() // no go.sum here
	b, _ := New(Config{Target: TargetProxy, ModuleDir: moduleDir})

	// Should not error when go.sum is absent.
	if err := b.copyGoSum(dir); err != nil {
		t.Fatalf("copyGoSum with no go.sum: %v", err)
	}

	// Destination go.sum should not be created.
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); !os.IsNotExist(err) {
		t.Error("go.sum should not be created when source is absent")
	}
}

func TestCopyGoSum_present(t *testing.T) {
	dir := t.TempDir()
	moduleDir := t.TempDir()

	// Write a fake go.sum.
	content := []byte("github.com/example/pkg v1.0.0 h1:fake\n")
	if err := os.WriteFile(filepath.Join(moduleDir, "go.sum"), content, 0o644); err != nil {
		t.Fatalf("writing source go.sum: %v", err)
	}

	b, _ := New(Config{Target: TargetProxy, ModuleDir: moduleDir})
	if err := b.copyGoSum(dir); err != nil {
		t.Fatalf("copyGoSum: %v", err)
	}

	copied, err := os.ReadFile(filepath.Join(dir, "go.sum"))
	if err != nil {
		t.Fatalf("reading copied go.sum: %v", err)
	}
	if !bytes.Equal(copied, content) {
		t.Error("copied go.sum content should match source")
	}
}

func TestRegisteredTargets_complete(t *testing.T) {
	expected := []string{TargetProxy, TargetOperator, TargetCaddy, TargetKong}
	for _, name := range expected {
		if _, ok := registeredTargets[name]; !ok {
			t.Errorf("target %q not registered", name)
		}
	}
}
