// Package builder implements an xcaddy-style custom binary builder for mcp-anything.
// It generates a temporary Go module that imports only the selected registry
// component packages, then compiles it into a standalone binary.
//
// Usage:
//
//	b, err := builder.New(builder.Config{
//	    Target:     builder.TargetProxy,
//	    Packages:   []string{"github.com/gaarutyunov/mcp-anything/pkg/cache/redis"},
//	    OutputFile: "/tmp/my-proxy",
//	})
//	if err != nil { ... }
//	if err := b.Build(ctx); err != nil { ... }
package builder

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	// TargetProxy builds a standalone mcp-anything proxy.
	TargetProxy = "proxy"
	// TargetOperator builds the Kubernetes operator.
	TargetOperator = "operator"
	// TargetCaddy builds a Caddy binary with the mcp-anything module embedded.
	TargetCaddy = "caddy"
	// TargetKong builds a Kong Go plugin binary wrapping mcp-anything.
	TargetKong = "kong"
)

const (
	localModule = "github.com/gaarutyunov/mcp-anything"
	// pseudoVer is the canonical pseudo-version for local replace directives.
	pseudoVer = "v0.0.0-00010101000000-000000000000"
)

// Config holds the build configuration.
type Config struct {
	// Target is the output binary type: proxy, operator, caddy, or kong.
	Target string

	// Packages lists the fully-qualified import paths of registry-implementing
	// packages to include, e.g. "github.com/gaarutyunov/mcp-anything/pkg/cache/redis".
	Packages []string

	// OutputFile is the destination path for the compiled binary.
	// Defaults to "bin/<target>" relative to ModuleDir.
	OutputFile string

	// ModuleDir is the root of the mcp-anything module (where go.mod lives).
	// Defaults to the current working directory.
	ModuleDir string

	// LDFlags is passed verbatim to go build -ldflags.
	LDFlags string

	// Stderr captures build output; defaults to os.Stderr.
	Stderr io.Writer
}

// Builder generates and compiles a custom mcp-anything binary.
type Builder struct {
	cfg Config
}

// New validates cfg and returns a Builder.
func New(cfg Config) (*Builder, error) {
	if cfg.Target == "" {
		return nil, fmt.Errorf("builder: target must not be empty")
	}
	if _, ok := registeredTargets[cfg.Target]; !ok {
		return nil, fmt.Errorf("builder: unknown target %q; must be one of: proxy, operator, caddy, kong", cfg.Target)
	}
	if cfg.ModuleDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("builder: getting working directory: %w", err)
		}
		cfg.ModuleDir = wd
	}
	abs, err := filepath.Abs(cfg.ModuleDir)
	if err != nil {
		return nil, fmt.Errorf("builder: resolving module directory: %w", err)
	}
	cfg.ModuleDir = abs

	if cfg.OutputFile == "" {
		cfg.OutputFile = filepath.Join(cfg.ModuleDir, "bin", cfg.Target)
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	return &Builder{cfg: cfg}, nil
}

// Build generates a temporary Go module with the selected packages, compiles it,
// and writes the binary to cfg.OutputFile.
func (b *Builder) Build(ctx context.Context) error {
	tmpDir, err := os.MkdirTemp("", "mcp-builder-*")
	if err != nil {
		return fmt.Errorf("builder: creating temp dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			slog.Warn("builder: failed to clean up temp dir", "path", tmpDir, "error", rmErr)
		}
	}()

	target := registeredTargets[b.cfg.Target]
	slog.Info("builder: generating source",
		"target", b.cfg.Target,
		"packages", b.cfg.Packages,
		"output", b.cfg.OutputFile,
	)

	if err := b.writeGoMod(tmpDir, target); err != nil {
		return fmt.Errorf("builder: writing go.mod: %w", err)
	}
	if err := b.copyGoSum(tmpDir); err != nil {
		return fmt.Errorf("builder: copying go.sum: %w", err)
	}
	if err := b.writeMain(tmpDir, target); err != nil {
		return fmt.Errorf("builder: generating main.go: %w", err)
	}
	if target.needsDownload {
		if err := b.goCmd(ctx, tmpDir, "mod", "download"); err != nil {
			return fmt.Errorf("builder: go mod download: %w", err)
		}
	}
	if err := b.compile(ctx, tmpDir); err != nil {
		return fmt.Errorf("builder: compiling: %w", err)
	}
	slog.Info("builder: done", "output", b.cfg.OutputFile)
	return nil
}

func (b *Builder) writeGoMod(dir string, t targetSpec) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "module mcp-anything-custom\n\ngo 1.24\n\nrequire (\n")
	fmt.Fprintf(&buf, "\t%s %s\n", localModule, pseudoVer)
	for _, req := range t.extraRequire {
		fmt.Fprintf(&buf, "\t%s\n", req)
	}
	fmt.Fprintf(&buf, ")\n\nreplace %s => %s\n", localModule, b.cfg.ModuleDir)

	return os.WriteFile(filepath.Join(dir, "go.mod"), buf.Bytes(), 0o644)
}

// copyGoSum copies go.sum from the module root to the temp dir so that transitive
// dependencies are already verified without needing to contact the sum database.
func (b *Builder) copyGoSum(dir string) error {
	src := filepath.Join(b.cfg.ModuleDir, "go.sum")
	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil // module has no go.sum yet
	}
	if err != nil {
		return fmt.Errorf("reading go.sum: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "go.sum"), data, 0o644)
}

func (b *Builder) writeMain(dir string, t targetSpec) error {
	tmpl, err := template.New("main").Parse(t.mainTemplate)
	if err != nil {
		return fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData{Packages: b.cfg.Packages}); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting generated source: %w\nsource:\n%s", err, buf.String())
	}
	return os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644)
}

func (b *Builder) compile(ctx context.Context, dir string) error {
	outputFile, err := filepath.Abs(b.cfg.OutputFile)
	if err != nil {
		return fmt.Errorf("resolving output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputFile), 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	args := []string{"build", "-o", outputFile}
	if b.cfg.LDFlags != "" {
		args = append(args, "-ldflags", b.cfg.LDFlags)
	}
	args = append(args, ".")
	return b.goCmd(ctx, dir, args...)
}

func (b *Builder) goCmd(ctx context.Context, dir string, args ...string) error {
	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(b.cfg.Stderr, &stderrBuf)

	// Inherit env but clear GOFLAGS to avoid interference; disable sum DB so
	// the local replacement module does not require a go.sum entry.
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "GOSUMDB=off", "GOFLAGS=-mod=mod")
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w\nstderr: %s", args[0], err, stderrBuf.String())
	}
	return nil
}

type templateData struct {
	Packages []string
}

type targetSpec struct {
	mainTemplate string
	// extraRequire lists additional "module version" strings added to go.mod.
	extraRequire []string
	// needsDownload runs go mod download before build to fetch extra requirements.
	needsDownload bool
}
