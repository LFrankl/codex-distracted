package agent

import (
	"context"
	"fmt"

	"codex/llm"
)

// VecSearcher wraps a VecIndex and an embedding API client.
// It implements Searcher by calling the remote /embeddings endpoint to
// convert the query string into a vector, then doing cosine similarity search.
type VecSearcher struct {
	index      *VecIndex
	client     *llm.Client
	embedModel string
}

func NewVecSearcher(index *VecIndex, client *llm.Client, embedModel string) *VecSearcher {
	return &VecSearcher{index: index, client: client, embedModel: embedModel}
}

func (v *VecSearcher) Search(ctx context.Context, query string, k int) ([]CodeResult, error) {
	vecs, err := v.client.Embed(ctx, v.embedModel, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	raw := v.index.Search(vecs[0], k)
	results := make([]CodeResult, len(raw))
	for i, r := range raw {
		results[i] = CodeResult{
			File:    r.Chunk.File,
			StartLn: r.Chunk.StartLn,
			EndLn:   r.Chunk.EndLn,
			Text:    r.Chunk.Text,
			Score:   r.Similarity,
		}
	}
	return results, nil
}

func (v *VecSearcher) Kind() string { return "vector-api" }

// ---------------------------------------------------------------------------
// LocalVecSearcher — placeholder for future fully-local dense vector search.
//
// Intended implementation: load an ONNX embedding model (e.g. all-MiniLM-L6-v2,
// nomic-embed-text, or a quantized code model) via onnxruntime_go or a llamafile
// subprocess, embed queries locally, search the same VecIndex.
//
// To implement:
//  1. Add an ONNX runtime binding (e.g. github.com/yalue/onnxruntime_go).
//  2. Download a small embedding model (~22 MB for MiniLM) on first /index run.
//  3. Replace the embed API call in VecSearcher with a local inference call.
//  4. Set Kind() = "vector-local".
//
// Until then this type is intentionally unexported and not wired up.
// ---------------------------------------------------------------------------

// localVecSearcher is a stub — not yet functional.
type localVecSearcher struct {
	index     *VecIndex
	modelPath string // path to .onnx model file
}

func (l *localVecSearcher) Search(_ context.Context, _ string, _ int) ([]CodeResult, error) {
	return nil, fmt.Errorf("local vector search not yet implemented (see vecsearcher.go)")
}

func (l *localVecSearcher) Kind() string { return "vector-local" }
