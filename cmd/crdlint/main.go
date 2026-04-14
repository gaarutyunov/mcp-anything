// Package main implements the CRD linter for mcp-anything.
//
// It validates that:
//  1. pkg/crd/v1alpha1/spec_gen.go exactly matches what would be generated from
//     pkg/config/config.go (catches cases where config types changed but the spec
//     file was not regenerated).
//  2. The CRD YAML files in charts/mcp-anything/crds/ exactly match what
//     controller-gen would generate from pkg/crd/v1alpha1/.
//
// Usage:
//
//	go run ./cmd/crdlint
//
// Exit codes:
//
//	0  All CRD files are up-to-date.
//	1  One or more files are out of date or missing. Run "make generate-crds".
package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gaarutyunov/mcp-anything/internal/crdutil"
)

const controllerGenVersion = "v0.17.0"

// crdOutputDirs lists all helm chart directories that must contain up-to-date CRD manifests.
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
	ok, err := run()
	if err != nil {
		slog.Error("crdlint failed", "error", err)
		os.Exit(1)
	}
	if !ok {
		os.Exit(1)
	}
}

func run() (bool, error) {
	ctx := context.Background()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return false, fmt.Errorf("finding repo root: %w", err)
	}
	slog.Info("validating CRDs", "repo_root", repoRoot)

	allOK := true

	// ── Phase 1: Validate spec_gen.go ────────────────────────────────────────────
	slog.Info("phase 1: validating spec_gen.go")
	specOK, err := crdutil.ValidateSpecFile(repoRoot)
	if err != nil {
		return false, fmt.Errorf("validating spec file: %w", err)
	}
	if !specOK {
		slog.Error("spec_gen.go is out of date — run: make generate-crds",
			"path", crdutil.SpecGenPath)
		allOK = false
	} else {
		slog.Info("spec_gen.go is up to date", "path", crdutil.SpecGenPath)
	}

	// ── Phase 2: Validate CRD YAML files ─────────────────────────────────────────
	slog.Info("phase 2: validating CRD YAML files")

	tmpDir, err := os.MkdirTemp("", "mcp-crdlint-*")
	if err != nil {
		return false, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := runControllerGen(ctx, repoRoot, tmpDir); err != nil {
		return false, fmt.Errorf("running controller-gen: %w", err)
	}

	for _, outDir := range crdOutputDirs {
		absOutDir := filepath.Join(repoRoot, outDir)
		for src, dst := range crdRenames {
			generated, err := os.ReadFile(filepath.Join(tmpDir, src))
			if err != nil {
				return false, fmt.Errorf("reading generated CRD %s: %w", src, err)
			}

			dstPath := filepath.Join(absOutDir, dst)
			existing, err := os.ReadFile(dstPath)
			if err != nil {
				if os.IsNotExist(err) {
					slog.Error("CRD file missing — run: make generate-crds",
						"path", filepath.Join(outDir, dst))
					allOK = false
					continue
				}
				return false, fmt.Errorf("reading %s: %w", dstPath, err)
			}

			if !bytes.Equal(generated, existing) {
				slog.Error("CRD file is out of date — run: make generate-crds",
					"path", filepath.Join(outDir, dst))
				allOK = false
			} else {
				slog.Info("CRD file is up to date", "path", filepath.Join(outDir, dst))
			}
		}
	}

	if allOK {
		slog.Info("all CRD files are up to date")
	} else {
		fmt.Fprintln(os.Stderr, "\nCRD files are out of date. Run:\n\n  make generate-crds\n\nand commit the updated files.")
	}
	return allOK, nil
}

// runControllerGen invokes controller-gen via "go run" to generate CRD manifests.
func runControllerGen(ctx context.Context, repoRoot, outDir string) error {
	tool := fmt.Sprintf("sigs.k8s.io/controller-tools/cmd/controller-gen@%s", controllerGenVersion)
	args := []string{
		"run", tool,
		"crd",
		"paths=./pkg/crd/v1alpha1/...",
		fmt.Sprintf("output:crd:dir=%s", outDir),
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed version string
	cmd.Dir = repoRoot
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("controller-gen: %w\nstderr: %s", err, stderr.String())
	}
	return nil
}

// findRepoRoot walks up from the working directory to find the repository root,
// identified by the presence of go.mod.
func findRepoRoot() (string, error) {
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
