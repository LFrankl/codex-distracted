package agent

import (
	"path/filepath"
	"strings"
)

const (
	chunkLines   = 60 // target lines per chunk
	chunkOverlap = 15 // overlap lines between consecutive chunks
	smallFile    = 80 // files with ≤ this many lines are one chunk
)

// FileChunk is a slice of source ready for embedding.
type FileChunk struct {
	RelPath string
	StartLn int // 1-based
	EndLn   int // 1-based, inclusive
	Text    string
}

// ChunkFile splits file content into overlapping chunks.
// Small files (≤ smallFile lines) are returned as a single chunk.
func ChunkFile(relPath, content string) []FileChunk {
	lines := strings.Split(content, "\n")
	n := len(lines)

	if n <= smallFile {
		return []FileChunk{{RelPath: relPath, StartLn: 1, EndLn: n, Text: content}}
	}

	var chunks []FileChunk
	start := 0
	for start < n {
		end := start + chunkLines
		if end > n {
			end = n
		}
		chunks = append(chunks, FileChunk{
			RelPath: relPath,
			StartLn: start + 1,
			EndLn:   end,
			Text:    strings.Join(lines[start:end], "\n"),
		})
		if end == n {
			break
		}
		start += chunkLines - chunkOverlap
	}
	return chunks
}

// shouldIndex returns true if the file should be included in the RAG index.
func shouldIndex(relPath string) bool {
	// Skip hidden paths
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	base := filepath.Base(relPath)
	ext := strings.ToLower(filepath.Ext(relPath))

	// Skip generated Go files
	if strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		strings.HasSuffix(base, ".generated.go") {
		return false
	}

	// Skip minified web assets
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") {
		return false
	}

	// Allow known text extensions (empty ext covers Makefile, Dockerfile, etc.)
	textExts := map[string]bool{
		".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".py": true, ".rb": true, ".java": true, ".kt": true, ".swift": true,
		".c": true, ".cpp": true, ".h": true, ".hpp": true, ".rs": true,
		".cs": true, ".php": true, ".scala": true, ".clj": true,
		".sh": true, ".bash": true, ".zsh": true, ".fish": true,
		".yaml": true, ".yml": true, ".json": true, ".toml": true, ".ini": true,
		".xml": true, ".html": true, ".css": true, ".scss": true, ".sass": true,
		".md": true, ".txt": true, ".rst": true,
		".sql": true, ".graphql": true, ".proto": true,
		".vue": true, ".svelte": true,
		"": true,
	}
	return textExts[ext]
}
