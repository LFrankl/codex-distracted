package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex/llm"
)

// ragState holds live RAG state attached to a ToolRegistry.
type ragState struct {
	index      *VecIndex
	client     *llm.Client
	embedModel string
}

// SetRAG attaches a vector index and embedding client to the tool registry.
// Should be called after NewToolRegistry when an index is available.
func (r *ToolRegistry) SetRAG(index *VecIndex, client *llm.Client, embedModel string) {
	r.rag = &ragState{index: index, client: client, embedModel: embedModel}
}

func (r *ToolRegistry) defSemanticSearch() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "semantic_search",
			Description: "Search the project codebase by meaning using vector similarity. " +
				"Returns the most relevant code chunks for a natural language query. " +
				"Use this when you don't know where something lives — e.g. 'authentication logic', 'database connection', 'error handler'. " +
				"Requires /index to have been run first.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Natural language description of what you're looking for",
					},
					"top_k": map[string]any{
						"type":        "integer",
						"description": "Number of results to return (default 5, max 10)",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (r *ToolRegistry) semanticSearch(argsJSON string) ToolResult {
	var args struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if r.rag == nil || r.rag.index == nil {
		return ToolResult{Content: "no index available — run /index first to build the codebase index", IsError: true}
	}
	if args.Query == "" {
		return ToolResult{Content: "query is required", IsError: true}
	}
	if args.TopK <= 0 || args.TopK > 10 {
		args.TopK = 5
	}

	vecs, err := r.rag.client.Embed(context.Background(), r.rag.embedModel, []string{args.Query})
	if err != nil {
		return ToolResult{Content: "embed query: " + err.Error(), IsError: true}
	}

	results := r.rag.index.Search(vecs[0], args.TopK)
	if len(results) == 0 {
		return ToolResult{Content: "no results found"}
	}

	var sb strings.Builder
	for _, res := range results {
		fmt.Fprintf(&sb, "### %s  lines %d–%d  (score %.2f)\n\n",
			res.Chunk.File, res.Chunk.StartLn, res.Chunk.EndLn, res.Similarity)
		sb.WriteString(res.Chunk.Text)
		sb.WriteString("\n\n")
	}
	return ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
}
