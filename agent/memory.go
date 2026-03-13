package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const memoryFile = ".codex.md"

// loadProjectMemory looks for .codex.md in workDir (and parent dirs up to 3 levels).
// Returns the file content, or "" if not found.
func loadProjectMemory(workDir string) (content, foundPath string) {
	dir := workDir
	for range 3 {
		path := filepath.Join(dir, memoryFile)
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data), path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// printMemoryLoaded writes a notice to out when project memory is found.
func printMemoryLoaded(out io.Writer, path string, content string) {
	lines := strings.Count(content, "\n") + 1
	rel := path
	if abs, err := filepath.Abs(path); err == nil {
		rel = abs
	}
	fmt.Fprintf(out, "\033[2m[project memory: %s (%d lines)]\033[0m\n", rel, lines)
}
