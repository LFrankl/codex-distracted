package agent

import (
	"fmt"
	"io"
	"strings"
)

const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
	colorReset  = "\033[0m"
)

// PrintDiff renders a colored unified diff to w.
//
// filePath  - shown in the header
// oldLines  - original content lines (no trailing newline on each)
// newLines  - replacement content lines
// startLine - 1-based line number where the change begins in the file (0 = unknown)
// context   - number of unchanged lines to show before/after the change
func PrintDiff(w io.Writer, filePath string, oldLines, newLines []string, startLine, context int) {
	if context <= 0 {
		context = 3
	}

	removed := len(oldLines)
	added := len(newLines)

	// Hunk header
	oldStart := startLine
	if oldStart <= 0 {
		oldStart = 1
	}
	newStart := oldStart

	fmt.Fprintf(w, "%s--- %s%s\n", colorRed, filePath, colorReset)
	fmt.Fprintf(w, "%s+++ %s%s\n", colorGreen, filePath, colorReset)
	fmt.Fprintf(w, "%s@@ -%d,%d +%d,%d @@%s\n",
		colorCyan,
		oldStart, removed,
		newStart, added,
		colorReset,
	)

	for _, line := range oldLines {
		fmt.Fprintf(w, "%s-%s%s\n", colorRed, line, colorReset)
	}
	for _, line := range newLines {
		fmt.Fprintf(w, "%s+%s%s\n", colorGreen, line, colorReset)
	}
}

// PrintDiffWithContext renders a diff that includes surrounding file lines as context.
//
// fileLines - all lines of the file before patching
// startLine / endLine - 1-based inclusive range being replaced
// newLines  - replacement lines
func PrintDiffWithContext(w io.Writer, filePath string, fileLines, newLines []string, startLine, endLine, context int) {
	if context <= 0 {
		context = 3
	}

	// Context before
	ctxStart := startLine - 1 - context // 0-based
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := endLine + context // 0-based exclusive
	if ctxEnd > len(fileLines) {
		ctxEnd = len(fileLines)
	}

	removed := endLine - startLine + 1
	added := len(newLines)

	// Hunk numbers
	oldFrom := ctxStart + 1
	oldCount := ctxEnd - ctxStart
	newCount := oldCount - removed + added

	fmt.Fprintf(w, "%s--- %s%s\n", colorRed, filePath, colorReset)
	fmt.Fprintf(w, "%s+++ %s%s\n", colorGreen, filePath, colorReset)
	fmt.Fprintf(w, "%s@@ -%d,%d +%d,%d @@%s\n",
		colorCyan,
		oldFrom, oldCount,
		oldFrom, newCount,
		colorReset,
	)

	// Context before
	for i := ctxStart; i < startLine-1; i++ {
		fmt.Fprintf(w, "%s %s%s\n", colorDim, fileLines[i], colorReset)
	}
	// Removed lines
	for i := startLine - 1; i < endLine; i++ {
		fmt.Fprintf(w, "%s-%s%s\n", colorRed, fileLines[i], colorReset)
	}
	// Added lines
	for _, line := range newLines {
		fmt.Fprintf(w, "%s+%s%s\n", colorGreen, line, colorReset)
	}
	// Context after
	for i := endLine; i < ctxEnd; i++ {
		fmt.Fprintf(w, "%s %s%s\n", colorDim, fileLines[i], colorReset)
	}
}

// splitLines splits s into lines without trailing newlines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Trim trailing empty line caused by a final \n
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// findLineNumber returns the 1-based line where substr starts in content, or 0 if not found.
func findLineNumber(content, substr string) int {
	idx := strings.Index(content, substr)
	if idx < 0 {
		return 0
	}
	return strings.Count(content[:idx], "\n") + 1
}
