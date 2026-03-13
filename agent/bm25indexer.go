package agent

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// BM25Indexer builds and persists a BM25 index for a project directory.
// It requires no external API — indexing is pure local computation.
type BM25Indexer struct {
	workDir  string
	indexDir string
	out      io.Writer
}

func NewBM25Indexer(workDir string, out io.Writer) *BM25Indexer {
	return &BM25Indexer{
		workDir:  workDir,
		indexDir: ProjectIndexDir(workDir), // shared dir with vector index
		out:      out,
	}
}

func (b *BM25Indexer) HasIndex() bool {
	_, err := os.Stat(filepath.Join(b.indexDir, bm25FileName))
	return err == nil
}

func (b *BM25Indexer) LoadIndex() (*BM25Index, error) {
	return loadBM25Index(b.indexDir)
}

// Run scans the working directory, builds a BM25 index, and saves it to disk.
// Unlike the vector indexer, this is always a full rebuild (fast, no API cost).
func (b *BM25Indexer) Run(ctx context.Context) error {
	var chunks []FileChunk

	err := filepath.WalkDir(b.workDir, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(b.workDir, path)
		if !shouldIndex(rel) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		chunks = append(chunks, ChunkFile(rel, string(content))...)
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(b.out, "  \033[2mbuilding BM25 index: %d chunks…\033[0m\n", len(chunks))

	idx := buildBM25(chunks)
	if err := idx.Save(b.indexDir); err != nil {
		return fmt.Errorf("save BM25 index: %w", err)
	}

	nFiles := uniqueFileCount(chunks)
	fmt.Fprintf(b.out, "\033[32m✓\033[0m BM25 index: %d chunks · %d files\n", len(idx.Docs), nFiles)
	return nil
}

func buildBM25(chunks []FileChunk) *BM25Index {
	df := make(map[string]int)
	docs := make([]bm25Doc, 0, len(chunks))
	totalLen := 0

	for _, c := range chunks {
		terms := tokenizeCode(c.Text)
		freq := make(map[string]int, len(terms))
		for _, t := range terms {
			freq[t]++
		}
		for t := range freq {
			df[t]++
		}
		totalLen += len(terms)
		docs = append(docs, bm25Doc{
			File:    c.RelPath,
			StartLn: c.StartLn,
			EndLn:   c.EndLn,
			Text:    c.Text,
			Freq:    freq,
			Len:     len(terms),
		})
	}

	avgLen := 1.0
	if len(docs) > 0 {
		avgLen = float64(totalLen) / float64(len(docs))
	}
	return &BM25Index{Docs: docs, DF: df, AvgLen: avgLen}
}

func uniqueFileCount(chunks []FileChunk) int {
	seen := make(map[string]bool)
	for _, c := range chunks {
		seen[c.RelPath] = true
	}
	return len(seen)
}
