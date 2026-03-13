package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const memoryFile = ".codex.md"

// loadProjectMemory looks for .codex.md only in workDir itself.
// Searching parent dirs caused wrong memory files to be loaded when running
// the binary from a directory that is a child of another project.
func loadProjectMemory(workDir string) (content, foundPath string) {
	path := filepath.Join(workDir, memoryFile)
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", ""
	}
	return string(data), path
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
