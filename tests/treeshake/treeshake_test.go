//go:build treeshake

// Package treeshake_test verifies that the mcp-anything package graph is properly
// tree-shakeable: importing only a subset of packages pulls in only the expected
// subset of transitive dependencies.
//
// Run with: go test -tags treeshake ./tests/treeshake/
package treeshake_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandOnly_TreeShake verifies that importing pkg/mcpanything and
// pkg/upstream/command does NOT pull in heavy dependencies used only by
// the script, http, or auth strategy sub-packages.
func TestCommandOnly_TreeShake(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repoRoot := findRepoRoot(t)

	writeFile(t, filepath.Join(dir, "go.mod"), "module treeshaketest\n\ngo 1.21\n\nrequire github.com/gaarutyunov/mcp-anything v0.0.0\n\nreplace github.com/gaarutyunov/mcp-anything => "+repoRoot+"\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/mcpanything"
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/command"
)

func main() {}
`)

	runGoCmd(t, dir, "mod", "tidy")

	gosum := readFile(t, filepath.Join(dir, "go.sum"))
	forbidden := []string{
		"grafana/sobek",
		"getkin/kin-openapi",
		"gopher-lua",
		"coreos/go-oidc",
		"zitadel/oidc",
	}
	for _, pkg := range forbidden {
		if strings.Contains(gosum, pkg) {
			t.Errorf("tree-shaking failure: mcpanything+upstream/command pulls in forbidden dep %q", pkg)
		}
	}
}

// TestMemoryCacheOnly_TreeShake verifies that importing pkg/cache/memory does NOT
// pull in the redis/go-redis package (which is only needed by pkg/cache/redis).
func TestMemoryCacheOnly_TreeShake(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repoRoot := findRepoRoot(t)

	writeFile(t, filepath.Join(dir, "go.mod"), "module treeshaketest\n\ngo 1.21\n\nrequire github.com/gaarutyunov/mcp-anything v0.0.0\n\nreplace github.com/gaarutyunov/mcp-anything => "+repoRoot+"\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/cache/memory"
)

func main() {}
`)

	runGoCmd(t, dir, "mod", "tidy")

	gosum := readFile(t, filepath.Join(dir, "go.sum"))
	forbidden := []string{
		"redis/go-redis",
	}
	for _, pkg := range forbidden {
		if strings.Contains(gosum, pkg) {
			t.Errorf("tree-shaking failure: cache/memory pulls in forbidden dep %q", pkg)
		}
	}
}

// TestHTTPBearer_TreeShake verifies that importing pkg/upstream/http and
// pkg/auth/outbound/bearer does NOT pull in Lua or JavaScript runtime deps.
func TestHTTPBearer_TreeShake(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repoRoot := findRepoRoot(t)

	writeFile(t, filepath.Join(dir, "go.mod"), "module treeshaketest\n\ngo 1.21\n\nrequire github.com/gaarutyunov/mcp-anything v0.0.0\n\nreplace github.com/gaarutyunov/mcp-anything => "+repoRoot+"\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/upstream/http"
	_ "github.com/gaarutyunov/mcp-anything/pkg/auth/outbound/bearer"
)

func main() {}
`)

	runGoCmd(t, dir, "mod", "tidy")

	gosum := readFile(t, filepath.Join(dir, "go.sum"))
	forbidden := []string{
		"grafana/sobek",
		"gopher-lua",
	}
	for _, pkg := range forbidden {
		if strings.Contains(gosum, pkg) {
			t.Errorf("tree-shaking failure: upstream/http+auth/outbound/bearer pulls in forbidden dep %q", pkg)
		}
	}
}

// TestEmbeddingWithoutHugot_TreeShake verifies that importing pkg/embedding (the
// base embedding registry with built-in chromem-go providers) does NOT pull in
// the hugot ONNX inference stack (knights-analytics/hugot, gomlx, etc.).
// Only importing pkg/embedding/hugot (or pkg/embedding/all) should bring in
// those heavy transitive dependencies.
func TestEmbeddingWithoutHugot_TreeShake(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repoRoot := findRepoRoot(t)

	writeFile(t, filepath.Join(dir, "go.mod"), "module treeshaketest\n\ngo 1.21\n\nrequire github.com/gaarutyunov/mcp-anything v0.0.0\n\nreplace github.com/gaarutyunov/mcp-anything => "+repoRoot+"\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	_ "github.com/gaarutyunov/mcp-anything/pkg/embedding"
)

func main() {}
`)

	runGoCmd(t, dir, "mod", "tidy")

	gosum := readFile(t, filepath.Join(dir, "go.sum"))
	forbidden := []string{
		"knights-analytics/hugot",
		"knights-analytics/tokenizers",
		"gomlx/gomlx",
		"yalue/onnxruntime_go",
	}
	for _, pkg := range forbidden {
		if strings.Contains(gosum, pkg) {
			t.Errorf("tree-shaking failure: pkg/embedding pulls in forbidden dep %q", pkg)
		}
	}
}

// findRepoRoot walks up from the test's working directory to find the go.mod root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod: walked up to filesystem root")
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(b)
}

func runGoCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v in %s: %v\n%s", args, dir, err, out)
	}
}
