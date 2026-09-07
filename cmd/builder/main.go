// Package main is the entry point for the mcp-anything builder CLI.
//
// The builder compiles a custom mcp-anything binary that includes only the
// registry-implementing packages you specify — similar to how xcaddy builds
// custom Caddy binaries.
//
// Usage:
//
//	mcp-anything-builder --target=proxy \
//	    --package=github.com/gaarutyunov/mcp-anything/pkg/cache/redis \
//	    --package=github.com/gaarutyunov/mcp-anything/pkg/session/postgres \
//	    --output=bin/my-proxy
//
// Supported targets: proxy, operator, caddy, kong
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gaarutyunov/mcp-anything/pkg/builder"
)

// Set by goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// packageList is a repeatable --package flag.
type packageList []string

func (p *packageList) String() string { return strings.Join(*p, ", ") }
func (p *packageList) Set(v string) error {
	*p = append(*p, v)
	return nil
}

func main() {
	slog.Info("mcp-anything builder", "version", version, "commit", commit, "date", date)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("build failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var (
		target    string
		output    string
		moduleDir string
		ldflags   string
		packages  packageList
	)

	fs := flag.NewFlagSet("mcp-anything-builder", flag.ContinueOnError)
	fs.StringVar(&target, "target", builder.TargetProxy,
		"Output binary type: proxy, operator, caddy, or kong")
	fs.StringVar(&output, "output", "",
		"Destination path for the compiled binary (default: bin/<target>)")
	fs.StringVar(&moduleDir, "module-dir", "",
		"Root of the mcp-anything module (default: current directory)")
	fs.StringVar(&ldflags, "ldflags", "",
		"Extra -ldflags passed to go build")
	fs.Var(&packages, "package",
		"Registry package to include; may be specified multiple times.\n"+
			"Example: --package=github.com/gaarutyunov/mcp-anything/pkg/cache/redis")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: mcp-anything-builder [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Build a custom mcp-anything binary with selected registry packages.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample — proxy with only redis cache and JWT auth:\n")
		fmt.Fprintf(os.Stderr, "  mcp-anything-builder \\\n")
		fmt.Fprintf(os.Stderr, "    --target=proxy \\\n")
		fmt.Fprintf(os.Stderr, "    --package=github.com/gaarutyunov/mcp-anything/pkg/cache/redis \\\n")
		fmt.Fprintf(os.Stderr, "    --package=github.com/gaarutyunov/mcp-anything/pkg/auth/inbound/jwt \\\n")
		fmt.Fprintf(os.Stderr, "    --package=github.com/gaarutyunov/mcp-anything/pkg/upstream/http \\\n")
		fmt.Fprintf(os.Stderr, "    --output=bin/my-proxy\n")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	b, err := builder.New(builder.Config{
		Target:     target,
		Packages:   packages,
		OutputFile: output,
		ModuleDir:  moduleDir,
		LDFlags:    ldflags,
	})
	if err != nil {
		return err
	}

	return b.Build(ctx)
}
