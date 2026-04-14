// Package crdlint provides a go/analysis linter that verifies generated CRD
// files are up to date with their source types and configuration.
//
// The linter fires only when golangci-lint analyses the pkg/crd/v1alpha1
// package and checks three things:
//
//  1. types_gen.go matches what crdgen would produce from the root type specs.
//  2. spec_gen.go matches what crdgen would produce from pkg/config/config.go.
//  3. CRD YAML files in charts/mcp-anything/crds/ match controller-gen output.
//
// Fix any failures reported by this linter by running:
//
//	make generate-crds
package crdlint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/tools/go/analysis"

	"github.com/gaarutyunov/mcp-anything/internal/crdutil"
)

const controllerGenVersion = "v0.17.0"

// crdOutputDirs lists all chart directories that must contain up-to-date CRD manifests.
var crdOutputDirs = []string{
	"charts/mcp-anything/crds",
}

// crdRenames maps the controller-gen output filenames to the canonical names
// used in the helm chart directories.
var crdRenames = map[string]string{
	"mcp-anything.ai_mcpproxies.yaml":   "mcpproxy.yaml",
	"mcp-anything.ai_mcpupstreams.yaml": "mcpupstream.yaml",
}

// Analyzer is the go/analysis analyzer for CRD file freshness checks.
var Analyzer = &analysis.Analyzer{
	Name: "crdlint",
	Doc:  "checks that types_gen.go, spec_gen.go, and CRD YAML manifests are up to date — run make generate-crds to fix",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	if len(pass.Files) == 0 {
		return nil, nil
	}

	// Only execute for the crd/v1alpha1 package to avoid running for every
	// package golangci-lint analyses.  We use the file path rather than
	// pass.Pkg.Path() because pass.Pkg may be nil in LoadModeSyntax mode.
	repoRoot, err := findRepoRoot(pass)
	if err != nil {
		return nil, fmt.Errorf("crdlint: finding repo root: %w", err)
	}

	// Only run for the pkg/crd/v1alpha1 package, identified by the file
	// path of the first file relative to the repo root.
	pos := pass.Fset.File(pass.Files[0].Pos())
	if pos == nil {
		return nil, nil
	}
	relPath, err := filepath.Rel(repoRoot, filepath.Dir(pos.Name()))
	if err != nil || filepath.ToSlash(relPath) != "pkg/crd/v1alpha1" {
		return nil, nil
	}

	// Use the position of the package keyword in the first file as the
	// diagnostic anchor for all repo-level findings.
	anchor := pass.Files[0].Package

	// ── Phase 1: Validate types_gen.go ───────────────────────────────────────
	typesOK, err := crdutil.ValidateTypesFile(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("crdlint: validating types_gen.go: %w", err)
	}
	if !typesOK {
		pass.Reportf(anchor, "%s is out of date — run: make generate-crds", crdutil.TypesGenPath)
	}

	// ── Phase 2: Validate spec_gen.go ────────────────────────────────────────
	specOK, err := crdutil.ValidateSpecFile(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("crdlint: validating spec_gen.go: %w", err)
	}
	if !specOK {
		pass.Reportf(anchor, "%s is out of date — run: make generate-crds", crdutil.SpecGenPath)
	}

	// ── Phase 3: Validate CRD YAML files ─────────────────────────────────────
	tmpDir, err := os.MkdirTemp("", "mcp-crdlint-*")
	if err != nil {
		return nil, fmt.Errorf("crdlint: creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := runControllerGen(context.Background(), repoRoot, tmpDir); err != nil {
		return nil, fmt.Errorf("crdlint: running controller-gen: %w", err)
	}

	for _, outDir := range crdOutputDirs {
		for src, dst := range crdRenames {
			generated, err := os.ReadFile(filepath.Join(tmpDir, src))
			if err != nil {
				return nil, fmt.Errorf("crdlint: reading generated CRD %s: %w", src, err)
			}

			dstPath := filepath.Join(repoRoot, outDir, dst)
			existing, err := os.ReadFile(dstPath)
			if err != nil {
				if os.IsNotExist(err) {
					pass.Reportf(anchor, "CRD file %s is missing — run: make generate-crds", filepath.Join(outDir, dst))
					continue
				}
				return nil, fmt.Errorf("crdlint: reading %s: %w", dstPath, err)
			}

			if !bytes.Equal(generated, existing) {
				pass.Reportf(anchor, "CRD file %s is out of date — run: make generate-crds", filepath.Join(outDir, dst))
			}
		}
	}

	return nil, nil
}

// runControllerGen invokes controller-gen via "go run" to generate CRD
// manifests from the pkg/crd/v1alpha1 package into outDir.
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

// findRepoRoot locates the repository root (directory containing go.mod) by
// walking up from the directory of the first file in the analysed package.
func findRepoRoot(pass *analysis.Pass) (string, error) {
	pos := pass.Fset.File(pass.Files[0].Pos())
	if pos == nil {
		return "", fmt.Errorf("no position info for package files")
	}

	dir := filepath.Dir(pos.Name())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", filepath.Dir(pos.Name()))
		}
		dir = parent
	}
}
