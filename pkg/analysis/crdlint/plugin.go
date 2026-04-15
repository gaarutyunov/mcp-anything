package crdlint

import (
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("crdlint", newPlugin)
}

type linterPlugin struct{}

func newPlugin(_ any) (register.LinterPlugin, error) {
	return &linterPlugin{}, nil
}

func (p *linterPlugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{Analyzer}, nil
}

// GetLoadMode returns LoadModeSyntax because the CRD linter only needs the
// package to be syntactically loaded — it does not perform type analysis.
func (p *linterPlugin) GetLoadMode() string {
	return register.LoadModeSyntax
}
