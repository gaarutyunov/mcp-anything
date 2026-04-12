// Package search implements the semantic tool search index used by the
// search_tools MCP tool. It wraps chromem-go to provide an in-process
// vector store with brute-force cosine similarity search.
package search

import (
	"context"
	"encoding/json"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	chromem "github.com/philippgille/chromem-go"
)

// ToolDef is a minimal tool definition returned in search results.
// It includes everything the LLM needs to immediately call the tool.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Index is an in-process semantic search index for MCP tools.
// It is immutable after construction and safe for concurrent reads.
type Index struct {
	collection  *chromem.Collection
	toolsByName map[string]*sdkmcp.Tool
}

// Build creates a new search index from the given tool list using the provided
// embedding function. Documents are embedded with concurrency=5.
// An empty tool list is valid; Search on it will always return no results.
func Build(ctx context.Context, tools []*sdkmcp.Tool, embeddingFunc chromem.EmbeddingFunc) (*Index, error) {
	db := chromem.NewDB()
	coll, err := db.CreateCollection("tools", nil, embeddingFunc)
	if err != nil {
		return nil, fmt.Errorf("creating search collection: %w", err)
	}

	toolsByName := make(map[string]*sdkmcp.Tool, len(tools))
	docs := make([]chromem.Document, 0, len(tools))
	for _, tool := range tools {
		content := tool.Name
		if tool.Description != "" {
			content += ": " + tool.Description
		}
		docs = append(docs, chromem.Document{
			ID:      tool.Name,
			Content: content,
		})
		toolsByName[tool.Name] = tool
	}

	if len(docs) > 0 {
		const concurrency = 5
		if err := coll.AddDocuments(ctx, docs, concurrency); err != nil {
			return nil, fmt.Errorf("indexing tools: %w", err)
		}
	}

	return &Index{collection: coll, toolsByName: toolsByName}, nil
}

// Search returns at most limit tool definitions whose names/descriptions best
// match the natural-language query. Results are ordered by descending similarity.
func (idx *Index) Search(ctx context.Context, query string, limit int) ([]ToolDef, error) {
	if limit <= 0 {
		limit = 5
	}

	count := idx.collection.Count()
	if count == 0 {
		return nil, nil
	}
	if limit > count {
		limit = count
	}

	results, err := idx.collection.Query(ctx, query, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("querying search index: %w", err)
	}

	defs := make([]ToolDef, 0, len(results))
	for _, r := range results {
		tool, ok := idx.toolsByName[r.ID]
		if !ok {
			continue
		}
		schema, _ := json.Marshal(tool.InputSchema)
		defs = append(defs, ToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	return defs, nil
}
