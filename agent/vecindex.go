package agent

import (
	"encoding/gob"
	"math"
	"os"
	"sort"
)

// Chunk is a slice of a source file stored in the vector index.
type Chunk struct {
	File    string    // relative path from workDir
	StartLn int       // 1-based
	EndLn   int       // 1-based, inclusive
	Text    string    // raw text content
	Vec     []float32 // embedding vector
}

// SearchResult pairs a chunk with its similarity score.
type SearchResult struct {
	Chunk      Chunk
	Similarity float32
}

// VecIndex is an in-memory flat vector index backed by gob on disk.
type VecIndex struct {
	Chunks []Chunk
	Dim    int
}

// Search returns the top-k most similar chunks to the query vector.
func (idx *VecIndex) Search(query []float32, k int) []SearchResult {
	type scored struct {
		i   int
		sim float32
	}
	scores := make([]scored, len(idx.Chunks))
	for i, c := range idx.Chunks {
		scores[i] = scored{i, cosineSim(query, c.Vec)}
	}
	sort.Slice(scores, func(a, b int) bool { return scores[a].sim > scores[b].sim })
	if k > len(scores) {
		k = len(scores)
	}
	results := make([]SearchResult, k)
	for i := range results {
		results[i] = SearchResult{idx.Chunks[scores[i].i], scores[i].sim}
	}
	return results
}

// RemoveFile removes all chunks belonging to the given relative path.
func (idx *VecIndex) RemoveFile(relPath string) {
	out := idx.Chunks[:0]
	for _, c := range idx.Chunks {
		if c.File != relPath {
			out = append(out, c)
		}
	}
	idx.Chunks = out
}

// Save serializes the index to disk using gob encoding.
func (idx *VecIndex) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(idx)
}

// LoadVecIndex deserializes a previously saved index.
func LoadVecIndex(path string) (*VecIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var idx VecIndex
	if err := gob.NewDecoder(f).Decode(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}
