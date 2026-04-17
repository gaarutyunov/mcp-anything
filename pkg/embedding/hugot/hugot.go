// Package hugot registers the hugot embedding provider, which runs ONNX models
// in-process using a pure-Go backend (no CGO required).
//
// This package is isolated from pkg/embedding/embedding.go because
// github.com/knights-analytics/hugot pulls in a large dependency tree
// (gomlx, onnx-gomlx, etc.). Users who do not need in-process ONNX inference
// never pay this cost.
//
// Import with a blank import to make the "hugot" provider available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/embedding/hugot"
package hugot

import (
	"context"
	"fmt"

	khugot "github.com/knights-analytics/hugot"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/embedding"
)

func init() {
	embedding.Register("hugot", func(_ context.Context, cfg *config.EmbeddingSpec) (embedding.Func, error) {
		if cfg.Hugot == nil {
			return nil, fmt.Errorf("embedding.hugot config is required for provider %q", "hugot")
		}
		return newHugotEmbeddingFunc(cfg.Hugot)
	})
}

// newHugotEmbeddingFunc creates an embedding Func backed by an in-process ONNX model.
// The session and pipeline are created once and reused across calls.
func newHugotEmbeddingFunc(cfg *config.HugotEmbedSpec) (embedding.Func, error) {
	if cfg.ModelPath == "" {
		return nil, fmt.Errorf("hugot: model_path is required")
	}

	session, err := khugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("hugot: creating session: %w", err)
	}

	onnxFilename := cfg.OnnxFilename
	if onnxFilename == "" {
		onnxFilename = "model.onnx"
	}

	pipelineCfg := khugot.FeatureExtractionConfig{
		ModelPath:    cfg.ModelPath,
		Name:         "mcp-anything-embedding",
		OnnxFilename: onnxFilename,
	}

	pipeline, err := khugot.NewPipeline(session, pipelineCfg)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("hugot: creating pipeline: %w", err)
	}

	return func(ctx context.Context, text string) ([]float32, error) {
		output, runErr := pipeline.RunPipeline([]string{text})
		if runErr != nil {
			return nil, fmt.Errorf("hugot: running pipeline: %w", runErr)
		}
		if len(output.Embeddings) == 0 {
			return nil, fmt.Errorf("hugot: no embeddings returned")
		}
		return output.Embeddings[0], nil
	}, nil
}
