// Package main is a custom golangci-lint binary that bundles the crdlint
// module plugin so that "golangci-lint run" can enforce CRD file freshness.
//
// Build with:
//
//	go build -o bin/golangci-lint-custom ./cmd/golangci-lint-custom
//
// Then use in place of golangci-lint:
//
//	./bin/golangci-lint-custom run ./...
package main

import (
	"fmt"
	"os"

	"github.com/golangci/golangci-lint/v2/pkg/commands"

	_ "github.com/gaarutyunov/mcp-anything/pkg/analysis/crdlint" // registers the crdlint plugin
)

func main() {
	if err := commands.Execute(commands.BuildInfo{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
