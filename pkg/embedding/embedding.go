// Package embedding provides the IoC registry for embedding providers used by
// the semantic tool search feature. Built-in chromem-go providers are registered
// via init() in this package. The hugot (in-process ONNX) provider is isolated in
// the sub-package pkg/embedding/hugot to avoid pulling in its heavy dependency tree
// for users who do not need in-process inference.
//
// Use the all sub-package to import all built-in providers including hugot:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/embedding/all"
package embedding

import (
	"context"
	"fmt"
	"os"
	"sync"

	chromem "github.com/philippgille/chromem-go"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// Func is the embedding function type used to embed text into a vector.
// Re-exported from chromem-go so that provider sub-packages (e.g. hugot) can
// implement it without depending directly on chromem-go.
type Func = chromem.EmbeddingFunc

// ProviderFactory creates an embedding Func from an EmbeddingConfig.
type ProviderFactory func(ctx context.Context, cfg *config.EmbeddingConfig) (Func, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]ProviderFactory)
)

// Register adds a factory for the given provider name.
// Typically called from init() in provider sub-packages.
func Register(provider string, factory ProviderFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[provider] = factory
}

// New creates an embedding Func from the given config.
// Returns an error for unknown providers.
// Provider sub-packages must be imported (blank import) before calling New.
func New(ctx context.Context, cfg *config.EmbeddingConfig) (Func, error) {
	mu.RLock()
	f, ok := registry[cfg.Provider]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown embedding provider %q — import the provider package or pkg/embedding/all", cfg.Provider)
	}
	return f(ctx, cfg)
}

func init() {
	// Register all chromem-go built-in providers. These are handled by a single
	// dispatch function; no sub-package per provider since chromem-go already
	// manages them and new providers are picked up automatically on version bump.
	for _, p := range []string{
		"openai", "openai_compat", "ollama",
		"cohere", "mistral", "jina", "mixedbread",
		"vertex", "azure_openai", "localai",
	} {
		p := p // capture for closure
		Register(p, func(_ context.Context, cfg *config.EmbeddingConfig) (Func, error) {
			return newBuiltinEmbeddingFunc(cfg)
		})
	}
}

// expandEnv expands ${ENV_VAR} references in a config string value.
func expandEnv(s string) string {
	return os.ExpandEnv(s)
}

// newBuiltinEmbeddingFunc dispatches to the correct chromem-go constructor.
func newBuiltinEmbeddingFunc(cfg *config.EmbeddingConfig) (chromem.EmbeddingFunc, error) {
	switch cfg.Provider {
	case "openai":
		if cfg.OpenAI == nil {
			return nil, fmt.Errorf("embedding.openai config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncOpenAI(
			expandEnv(cfg.OpenAI.APIKey),
			chromem.EmbeddingModelOpenAI(cfg.OpenAI.Model),
		), nil

	case "openai_compat":
		if cfg.OpenAICompat == nil {
			return nil, fmt.Errorf("embedding.openai_compat config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncOpenAICompat(
			cfg.OpenAICompat.BaseURL,
			expandEnv(cfg.OpenAICompat.APIKey),
			cfg.OpenAICompat.Model,
			nil,
		), nil

	case "ollama":
		if cfg.Ollama == nil {
			return nil, fmt.Errorf("embedding.ollama config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncOllama(cfg.Ollama.Model, cfg.Ollama.BaseURL), nil

	case "cohere":
		if cfg.Cohere == nil {
			return nil, fmt.Errorf("embedding.cohere config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncCohere(
			expandEnv(cfg.Cohere.APIKey),
			chromem.EmbeddingModelCohere(cfg.Cohere.Model),
		), nil

	case "mistral":
		if cfg.Mistral == nil {
			return nil, fmt.Errorf("embedding.mistral config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncMistral(expandEnv(cfg.Mistral.APIKey)), nil

	case "jina":
		if cfg.Jina == nil {
			return nil, fmt.Errorf("embedding.jina config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncJina(
			expandEnv(cfg.Jina.APIKey),
			chromem.EmbeddingModelJina(cfg.Jina.Model),
		), nil

	case "mixedbread":
		if cfg.Mixedbread == nil {
			return nil, fmt.Errorf("embedding.mixedbread config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncMixedbread(
			expandEnv(cfg.Mixedbread.APIKey),
			chromem.EmbeddingModelMixedbread(cfg.Mixedbread.Model),
		), nil

	case "vertex":
		if cfg.Vertex == nil {
			return nil, fmt.Errorf("embedding.vertex config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncVertex(
			expandEnv(cfg.Vertex.APIKey),
			cfg.Vertex.Project,
			chromem.EmbeddingModelVertex(cfg.Vertex.Model),
		), nil

	case "azure_openai":
		if cfg.AzureOpenAI == nil {
			return nil, fmt.Errorf("embedding.azure_openai config is required for provider %q", cfg.Provider)
		}
		return chromem.NewEmbeddingFuncAzureOpenAI(
			expandEnv(cfg.AzureOpenAI.APIKey),
			cfg.AzureOpenAI.DeploymentURL,
			cfg.AzureOpenAI.APIVersion,
			cfg.AzureOpenAI.Model,
		), nil

	case "localai":
		model := ""
		if cfg.OpenAICompat != nil {
			model = cfg.OpenAICompat.Model
		}
		return chromem.NewEmbeddingFuncLocalAI(model), nil

	default:
		return nil, fmt.Errorf("unknown built-in embedding provider %q", cfg.Provider)
	}
}
