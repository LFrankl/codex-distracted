package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex/llm"
)

// ragState holds the active Searcher for the semantic_search tool.
type ragState struct {
	searcher Searcher
}

// SetSearcher attaches a Searcher implementation to the tool registry.
// Calling it again replaces the previous searcher (vector takes priority over BM25).
func (r *ToolRegistry) SetSearcher(s Searcher) {
	r.rag = &ragState{searcher: s}
}

// SetRAG is a convenience wrapper for attaching a VecSearcher.
func (r *ToolRegistry) SetRAG(index *VecIndex, client *llm.Client, embedModel string) {
	r.SetSearcher(NewVecSearcher(index, client, embedModel))
}

// SetBM25 is a convenience wrapper for attaching a BM25Index as the searcher.
func (r *ToolRegistry) SetBM25(idx *BM25Index) {
	r.SetSearcher(idx)
}

func (r *ToolRegistry) defSemanticSearch() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "semantic_search",
			Description: "Search the project codebase by meaning. " +
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
	if r.rag == nil {
		return ToolResult{Content: "no index available — run /index first to build the codebase index", IsError: true}
	}
	if args.Query == "" {
		return ToolResult{Content: "query is required", IsError: true}
	}
	if args.TopK <= 0 || args.TopK > 10 {
		args.TopK = 5
	}

	results, err := r.rag.searcher.Search(context.Background(), args.Query, args.TopK)
	if err != nil {
		return ToolResult{Content: "search failed: " + err.Error(), IsError: true}
	}
	if len(results) == 0 {
		return ToolResult{Content: "no results found"}
	}

	var sb strings.Builder
	for _, res := range results {
		fmt.Fprintf(&sb, "### %s  lines %d–%d  (score %.2f)\n\n",
			res.File, res.StartLn, res.EndLn, res.Score)
		sb.WriteString(res.Text)
		sb.WriteString("\n\n")
	}
	return ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
}
