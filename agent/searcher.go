package agent

import "context"

// CodeResult is the unified result type returned by all Searcher implementations.
type CodeResult struct {
	File    string
	StartLn int
	EndLn   int
	Text    string
	Score   float32
}

// Searcher is the single abstraction for codebase search.
// Implementations can differ in how they compute relevance (BM25, API vectors,
// local ONNX vectors, etc.) but all surface the same interface.
//
// Current implementations:
//   - *BM25Index      — keyword/identifier search, zero dependencies (Kind: "bm25")
//   - *VecSearcher    — dense vector search via embedding API (Kind: "vector-api")
//
// Planned implementations:
//   - *LocalVecSearcher — dense vector search via local ONNX model (Kind: "vector-local")
type Searcher interface {
	// Search returns the top-k most relevant code chunks for the query.
	Search(ctx context.Context, query string, k int) ([]CodeResult, error)

	// Kind returns a short identifier for the backend (e.g. "bm25", "vector-api").
	Kind() string
}
