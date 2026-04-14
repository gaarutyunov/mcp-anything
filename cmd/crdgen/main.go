// Package main implements the CRD generator for mcp-anything.
//
// It runs in two phases:
//
//  1. Spec generation: reads pkg/config/config.go and generates
//     pkg/crd/v1alpha1/spec_gen.go — Go types derived from the proxy/upstream
//     config types with JSON tags (koanf→camelCase) and inherited doc comments.
//
//  2. YAML + deepcopy generation: invokes controller-gen to produce CRD YAML
//     manifests and zz_generated.deepcopy.go from pkg/crd/v1alpha1/.
//
// Kubernetes-specific types (MCPProxy, MCPUpstream, SecretRef-based TLS, etc.)
// live in the hand-written pkg/crd/v1alpha1/types.go and are not generated.
//
// Usage:
//
//	go run ./cmd/crdgen
//
// The generator is idempotent: running it twice produces the same output.
// Run it whenever pkg/config/config.go or pkg/crd/v1alpha1/types.go changes,
// then commit the updated files together with the Go changes.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/gaarutyunov/mcp-anything/internal/crdutil"
)

const controllerGenVersion = "v0.17.0"

// crdOutputDirs lists all helm chart directories that contain CRD manifests.
var crdOutputDirs = []string{
	"charts/mcp-anything/crds",
}

// crdRenames maps the controller-gen output filenames to the canonical names
// used in the helm chart directories.
var crdRenames = map[string]string{
	"mcp-anything.ai_mcpproxies.yaml":   "mcpproxy.yaml",
	"mcp-anything.ai_mcpupstreams.yaml": "mcpupstream.yaml",
}

func main() {
	if err := run(); err != nil {
		slog.Error("crdgen failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("finding repo root: %w", err)
	}
	slog.Info("generating CRDs", "repo_root", repoRoot)

	// ── Phase 1: Generate spec_gen.go from config types ──────────────────────────
	slog.Info("phase 1: generating CRD spec types from pkg/config/config.go")
	if err := crdutil.WriteSpecFile(repoRoot); err != nil {
		return fmt.Errorf("writing spec file: %w", err)
	}
	slog.Info("wrote spec file", "path", crdutil.SpecGenPath)

	// ── Phase 2: Generate CRD YAML + deepcopy via controller-gen ─────────────────
	slog.Info("phase 2: generating CRD YAML and deepcopy via controller-gen")

	tmpDir, err := os.MkdirTemp("", "mcp-crds-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := runControllerGen(ctx, repoRoot, tmpDir); err != nil {
		return fmt.Errorf("running controller-gen: %w", err)
	}

	// Read the generated CRD YAML files.
	generated := make(map[string][]byte)
	for src := range crdRenames {
		data, err := os.ReadFile(filepath.Join(tmpDir, src))
		if err != nil {
			return fmt.Errorf("reading generated CRD %s: %w", src, err)
		}
		generated[src] = data
	}

	// Write CRD YAML to each output directory.
	for _, outDir := range crdOutputDirs {
		absOutDir := filepath.Join(repoRoot, outDir)
		if err := os.MkdirAll(absOutDir, 0o755); err != nil {
			return fmt.Errorf("creating output dir %s: %w", absOutDir, err)
		}
		for src, dst := range crdRenames {
			dstPath := filepath.Join(absOutDir, dst)
			if err := os.WriteFile(dstPath, generated[src], 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", dstPath, err)
			}
			slog.Info("wrote CRD", "path", filepath.Join(outDir, dst))
		}
	}

	slog.Info("CRD generation complete")
	return nil
}

// runControllerGen invokes controller-gen via "go run" to generate both CRD
// manifests (written to outDir) and deepcopy methods (written in-place to
// pkg/crd/v1alpha1/zz_generated.deepcopy.go).
func runControllerGen(ctx context.Context, repoRoot, outDir string) error {
	tool := fmt.Sprintf("sigs.k8s.io/controller-tools/cmd/controller-gen@%s", controllerGenVersion)
	args := []string{
		"run", tool,
		"crd",
		"object:headerFile=",
		"paths=./pkg/crd/v1alpha1/...",
		fmt.Sprintf("output:crd:dir=%s", outDir),
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed version string
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("controller-gen: %w\nstderr: %s", err, stderr.String())
	}
	return nil
}

// findRepoRoot walks up from the directory containing this source file to find
// the repository root, identified by the presence of go.mod.
func findRepoRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return findRepoRootFromWd()
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return findRepoRootFromWd()
}

// findRepoRootFromWd walks up from the working directory to find go.mod.
func findRepoRootFromWd() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory of %s", dir)
		}
		dir = parent
	}
}
