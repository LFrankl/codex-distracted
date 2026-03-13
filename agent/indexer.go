package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex/llm"
)

const (
	embedBatchSize = 20
	indexFileName  = "chunks.bin"
	metaFileName   = "meta.json"
)

// skipDirs are directory names that are never indexed.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, ".next": true, ".nuxt": true,
	"__pycache__": true, ".venv": true, "venv": true,
}

// IndexMeta tracks what was indexed and with which model.
type IndexMeta struct {
	Provider   string           `json:"provider"`
	EmbedModel string           `json:"embed_model"`
	Dim        int              `json:"dim"`
	FileMtimes map[string]int64 `json:"file_mtimes"` // relPath → unix mtime
	IndexedAt  time.Time        `json:"indexed_at"`
}

// Indexer builds and incrementally updates the vector index for a project.
type Indexer struct {
	workDir    string
	indexDir   string
	provider   string
	embedModel string
	client     *llm.Client
	out        io.Writer
}

// NewIndexer creates an Indexer for the given working directory.
func NewIndexer(workDir, provider, embedModel string, client *llm.Client, out io.Writer) *Indexer {
	return &Indexer{
		workDir:    workDir,
		indexDir:   ProjectIndexDir(workDir),
		provider:   provider,
		embedModel: embedModel,
		client:     client,
		out:        out,
	}
}

// ProjectIndexDir returns the local index directory for a workDir.
// Uses the first 8 hex chars of SHA-256(workDir) as the directory name.
func ProjectIndexDir(workDir string) string {
	h := sha256.Sum256([]byte(workDir))
	id := fmt.Sprintf("%x", h[:4])
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "index", id)
}

// EmbedModel returns the configured embedding model name.
func (idx *Indexer) EmbedModel() string { return idx.embedModel }

// HasIndex returns true if a non-empty index exists for this workDir.
func (idx *Indexer) HasIndex() bool {
	_, err := os.Stat(filepath.Join(idx.indexDir, indexFileName))
	return err == nil
}

// LoadIndex loads the vector index from disk.
func (idx *Indexer) LoadIndex() (*VecIndex, error) {
	return LoadVecIndex(filepath.Join(idx.indexDir, indexFileName))
}

// Run builds or incrementally updates the index.
// force=true re-embeds all files regardless of mtime.
func (idx *Indexer) Run(ctx context.Context, force bool) error {
	if err := os.MkdirAll(idx.indexDir, 0755); err != nil {
		return err
	}

	// Load existing meta and index
	meta := &IndexMeta{
		Provider:   idx.provider,
		EmbedModel: idx.embedModel,
		FileMtimes: make(map[string]int64),
	}
	var vecIdx *VecIndex

	if !force {
		if m, err := loadIndexMeta(idx.indexDir); err == nil {
			if m.EmbedModel != idx.embedModel || m.Provider != idx.provider {
				fmt.Fprintf(idx.out, "\033[2m[index: embed model changed, rebuilding]\033[0m\n")
				force = true
			} else {
				meta = m
			}
		}
		if !force {
			if v, err := LoadVecIndex(filepath.Join(idx.indexDir, indexFileName)); err == nil {
				vecIdx = v
			}
		}
	}

	if vecIdx == nil {
		vecIdx = &VecIndex{}
	}
	if force {
		vecIdx.Chunks = nil
		meta.FileMtimes = make(map[string]int64)
	}

	// Scan all indexable files
	type fileEntry struct {
		relPath string
		mtime   int64
	}
	var allFiles []fileEntry

	err := filepath.WalkDir(idx.workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(idx.workDir, path)
		if !shouldIndex(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		allFiles = append(allFiles, fileEntry{rel, info.ModTime().Unix()})
		return nil
	})
	if err != nil {
		return err
	}

	// Determine which files need re-indexing
	var toIndex []fileEntry
	for _, f := range allFiles {
		if force || meta.FileMtimes[f.relPath] != f.mtime {
			toIndex = append(toIndex, f)
		}
	}

	// Remove chunks for deleted files
	current := make(map[string]bool, len(allFiles))
	for _, f := range allFiles {
		current[f.relPath] = true
	}
	for path := range meta.FileMtimes {
		if !current[path] {
			vecIdx.RemoveFile(path)
			delete(meta.FileMtimes, path)
		}
	}

	if len(toIndex) == 0 {
		fmt.Fprintf(idx.out, "\033[2m[index up to date — %d files, %d chunks]\033[0m\n",
			len(allFiles), len(vecIdx.Chunks))
		return nil
	}

	fmt.Fprintf(idx.out, "\033[2m[indexing %d changed file(s) across %d total...]\033[0m\n",
		len(toIndex), len(allFiles))

	// Build chunks for changed files
	var chunks []FileChunk
	for _, f := range toIndex {
		content, err := os.ReadFile(filepath.Join(idx.workDir, f.relPath))
		if err != nil {
			continue
		}
		vecIdx.RemoveFile(f.relPath) // drop stale chunks
		chunks = append(chunks, ChunkFile(f.relPath, string(content))...)
	}

	if len(chunks) == 0 {
		return nil
	}

	// Embed in batches
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		// Prepend filename as context so the model understands where the code lives
		texts[i] = "# " + c.RelPath + "\n" + c.Text
	}

	var allVecs [][]float32
	for i := 0; i < len(texts); i += embedBatchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := i + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		fmt.Fprintf(idx.out, "\033[2m[embedding chunks %d–%d / %d]\033[0m\r",
			i+1, end, len(texts))
		vecs, err := idx.client.Embed(ctx, idx.embedModel, texts[i:end])
		if err != nil {
			return fmt.Errorf("embed batch %d: %w", i/embedBatchSize+1, err)
		}
		allVecs = append(allVecs, vecs...)
	}
	fmt.Fprintln(idx.out) // clear the \r line

	// Record dimension on first embed
	if len(allVecs) > 0 && vecIdx.Dim == 0 {
		vecIdx.Dim = len(allVecs[0])
		meta.Dim = vecIdx.Dim
	}

	// Append new chunks
	for i, c := range chunks {
		vecIdx.Chunks = append(vecIdx.Chunks, Chunk{
			File:    c.RelPath,
			StartLn: c.StartLn,
			EndLn:   c.EndLn,
			Text:    c.Text,
			Vec:     allVecs[i],
		})
	}

	// Update mtimes for re-indexed files
	for _, f := range toIndex {
		meta.FileMtimes[f.relPath] = f.mtime
	}
	meta.IndexedAt = time.Now()

	// Persist
	if err := vecIdx.Save(filepath.Join(idx.indexDir, indexFileName)); err != nil {
		return fmt.Errorf("save index: %w", err)
	}
	if err := saveIndexMeta(idx.indexDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}

	fmt.Fprintf(idx.out, "\033[32m✓\033[0m \033[2mindex ready — %d files, %d chunks, %d-dim\033[0m\n",
		len(allFiles), len(vecIdx.Chunks), vecIdx.Dim)
	return nil
}

// Status prints index statistics to idx.out.
func (idx *Indexer) Status() {
	meta, err := loadIndexMeta(idx.indexDir)
	if err != nil {
		fmt.Fprintf(idx.out, "\033[2mno index for this project — run /index to build\033[0m\n")
		return
	}
	vi, err := LoadVecIndex(filepath.Join(idx.indexDir, indexFileName))
	if err != nil {
		fmt.Fprintf(idx.out, "\033[2mmeta found but index file missing — run /index\033[0m\n")
		return
	}
	fmt.Fprintf(idx.out, "\033[2mindex: %d files · %d chunks · %d-dim · model: %s (%s)\033[0m\n",
		len(meta.FileMtimes), len(vi.Chunks), meta.Dim, meta.EmbedModel, meta.Provider)
	fmt.Fprintf(idx.out, "\033[2mlast updated: %s\033[0m\n",
		meta.IndexedAt.Format("2006-01-02 15:04:05"))
}

func loadIndexMeta(indexDir string) (*IndexMeta, error) {
	data, err := os.ReadFile(filepath.Join(indexDir, metaFileName))
	if err != nil {
		return nil, err
	}
	var m IndexMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.FileMtimes == nil {
		m.FileMtimes = make(map[string]int64)
	}
	return &m, nil
}

func saveIndexMeta(indexDir string, m *IndexMeta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(indexDir, metaFileName), data, 0644)
}
