package agent

import (
	"context"
	"encoding/gob"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const bm25FileName = "bm25.bin"

// BM25 tuning parameters (standard defaults)
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

type bm25Doc struct {
	File    string
	StartLn int
	EndLn   int
	Text    string
	Freq    map[string]int // term → count
	Len     int            // total terms in doc
}

// BM25Index is a fully local, zero-API searchable index.
type BM25Index struct {
	Docs   []bm25Doc
	DF     map[string]int // term → number of docs containing it
	AvgLen float64
}

// Search implements Searcher. Returns the top-k most relevant docs for the query.
func (idx *BM25Index) Search(_ context.Context, query string, k int) ([]CodeResult, error) {
	terms := tokenizeCode(query)
	if len(terms) == 0 || len(idx.Docs) == 0 {
		return nil, nil
	}

	N := float64(len(idx.Docs))
	scores := make([]float64, len(idx.Docs))

	for _, term := range terms {
		df := float64(idx.DF[term])
		if df == 0 {
			continue
		}
		idf := math.Log((N-df+0.5)/(df+0.5) + 1)
		for i, doc := range idx.Docs {
			tf := float64(doc.Freq[term])
			if tf == 0 {
				continue
			}
			norm := tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*float64(doc.Len)/idx.AvgLen))
			scores[i] += idf * norm
		}
	}

	type ranked struct {
		idx   int
		score float64
	}
	var top []ranked
	for i, s := range scores {
		if s > 0 {
			top = append(top, ranked{i, s})
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].score > top[j].score })

	if k > len(top) {
		k = len(top)
	}
	maxPossible := float64(len(terms)) * math.Log(2) * (bm25K1 + 1)
	if maxPossible == 0 {
		maxPossible = 1
	}

	results := make([]CodeResult, k)
	for i, r := range top[:k] {
		doc := idx.Docs[r.idx]
		norm := float32(r.score / maxPossible)
		if norm > 1 {
			norm = 1
		}
		results[i] = CodeResult{
			File:    doc.File,
			StartLn: doc.StartLn,
			EndLn:   doc.EndLn,
			Text:    doc.Text,
			Score:   norm,
		}
	}
	return results, nil
}

// Kind implements Searcher.
func (idx *BM25Index) Kind() string { return "bm25" }

func (idx *BM25Index) Save(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, bm25FileName))
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(idx)
}

func loadBM25Index(dir string) (*BM25Index, error) {
	f, err := os.Open(filepath.Join(dir, bm25FileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var idx BM25Index
	return &idx, gob.NewDecoder(f).Decode(&idx)
}

// tokenizeCode splits source text into lowercase tokens for BM25 indexing.
// Handles camelCase, snake_case, identifiers, and keywords.
func tokenizeCode(text string) []string {
	seen := make(map[string]bool)
	var tokens []string

	add := func(t string) {
		t = strings.ToLower(t)
		if len(t) >= 2 && !seen[t] {
			seen[t] = true
			tokens = append(tokens, t)
		}
	}

	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		word := cur.String()
		add(word)
		for _, part := range splitCamelCase(word) {
			add(part)
		}
		cur.Reset()
	}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// splitCamelCase splits a camelCase or PascalCase word into parts.
// "getUserById" → ["get", "User", "By", "Id"]
// "HTTPRequest" → ["HTTP", "Request"]
func splitCamelCase(s string) []string {
	runes := []rune(s)
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) {
			prevUpper := unicode.IsUpper(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if !prevUpper || nextLower {
				parts = append(parts, string(runes[start:i]))
				start = i
			}
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}
