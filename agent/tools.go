package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codex/llm"
)

// ToolResult holds the output of a tool call
type ToolResult struct {
	Content string
	IsError bool
}

// ToolRegistry maps tool names to their definitions and handlers
type ToolRegistry struct {
	workDir  string
	defs     []llm.Tool
	approver Approver
	undo     UndoStack
}

func NewToolRegistry(workDir string, approver Approver) *ToolRegistry {
	r := &ToolRegistry{workDir: workDir, approver: approver}
	r.defs = []llm.Tool{
		r.defReadFile(),
		r.defWriteFile(),
		r.defPatchFile(),
		r.defListFiles(),
		r.defShellExec(),
		r.defGrepFiles(),
		r.defGitStatus(),
		r.defGitDiff(),
		r.defGitLog(),
		r.defGitCommit(),
	}
	return r
}

func (r *ToolRegistry) Definitions() []llm.Tool {
	return r.defs
}

func (r *ToolRegistry) Execute(name, argsJSON string) ToolResult {
	switch name {
	case "read_file":
		return r.readFile(argsJSON)
	case "write_file":
		return r.writeFile(argsJSON)
	case "list_files":
		return r.listFiles(argsJSON)
	case "shell_exec":
		return r.shellExec(argsJSON)
	case "patch_file":
		return r.patchFile(argsJSON)
	case "grep_files":
		return r.grepFiles(argsJSON)
	case "git_status":
		return r.gitStatus(argsJSON)
	case "git_diff":
		return r.gitDiff(argsJSON)
	case "git_log":
		return r.gitLog(argsJSON)
	case "git_commit":
		return r.gitCommit(argsJSON)
	default:
		return ToolResult{Content: fmt.Sprintf("unknown tool: %s", name), IsError: true}
	}
}

// --- Tool: read_file ---

func (r *ToolRegistry) defReadFile() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "read_file",
			Description: "Read the contents of a file. Returns file content as text.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path, relative to working directory or absolute",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "Start line number (1-based, optional)",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "End line number (1-based, optional)",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (r *ToolRegistry) readFile(argsJSON string) ToolResult {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	path := r.resolvePath(args.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}
	}

	content := string(data)
	if args.StartLine > 0 || args.EndLine > 0 {
		lines := strings.Split(content, "\n")
		start := args.StartLine - 1
		if start < 0 {
			start = 0
		}
		end := args.EndLine
		if end <= 0 || end > len(lines) {
			end = len(lines)
		}
		if start >= len(lines) {
			return ToolResult{Content: "start_line out of range", IsError: true}
		}
		// Add line numbers
		var sb strings.Builder
		for i, line := range lines[start:end] {
			fmt.Fprintf(&sb, "%4d | %s\n", start+i+1, line)
		}
		return ToolResult{Content: sb.String()}
	}

	// Add line numbers for full file
	lines := strings.Split(content, "\n")
	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%4d | %s\n", i+1, line)
	}
	return ToolResult{Content: sb.String()}
}

// --- Tool: write_file ---

func (r *ToolRegistry) defWriteFile() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "write_file",
			Description: "Write content to a file. Creates the file and parent directories if they don't exist. Use this to create or overwrite files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path, relative to working directory or absolute",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content to write to the file",
					},
					"append": map[string]any{
						"type":        "boolean",
						"description": "If true, append to existing file instead of overwriting",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

func (r *ToolRegistry) writeFile(argsJSON string) ToolResult {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	path := r.resolvePath(args.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return ToolResult{Content: fmt.Sprintf("mkdir error: %v", err), IsError: true}
	}

	// Backup before overwrite (not needed for append since original is preserved)
	if !args.Append {
		r.undo.Push(path)
	}

	flag := os.O_CREATE | os.O_WRONLY
	if args.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(path, flag, 0644)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("open error: %v", err), IsError: true}
	}
	defer f.Close()

	if _, err := f.WriteString(args.Content); err != nil {
		return ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}
	}

	lines := strings.Count(args.Content, "\n") + 1
	return ToolResult{Content: fmt.Sprintf("Written %d bytes (%d lines) to %s", len(args.Content), lines, args.Path)}
}

// --- Tool: list_files ---

func (r *ToolRegistry) defListFiles() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "list_files",
			Description: "List files and directories. Supports glob patterns.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory path or glob pattern (e.g., 'src/**/*.go'). Defaults to working directory.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "List recursively (default false)",
					},
				},
			},
		},
	}
}

func (r *ToolRegistry) listFiles(argsJSON string) ToolResult {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	if args.Path == "" {
		args.Path = "."
	}

	path := r.resolvePath(args.Path)

	// Check if it's a glob pattern
	if strings.ContainsAny(args.Path, "*?[") {
		matches, err := filepath.Glob(path)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("glob error: %v", err), IsError: true}
		}
		return ToolResult{Content: strings.Join(matches, "\n")}
	}

	var entries []string
	if args.Recursive {
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Skip hidden directories
			if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != path {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(r.workDir, p)
			if d.IsDir() {
				entries = append(entries, rel+"/")
			} else {
				entries = append(entries, rel)
			}
			return nil
		})
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("walk error: %v", err), IsError: true}
		}
	} else {
		infos, err := os.ReadDir(path)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("readdir error: %v", err), IsError: true}
		}
		for _, info := range infos {
			name := info.Name()
			if info.IsDir() {
				name += "/"
			}
			entries = append(entries, name)
		}
	}

	if len(entries) == 0 {
		return ToolResult{Content: "(empty)"}
	}
	return ToolResult{Content: strings.Join(entries, "\n")}
}

// --- Tool: shell_exec ---

func (r *ToolRegistry) defShellExec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "shell_exec",
			Description: "Execute a shell command and return stdout+stderr. Use for running tests, building, installing packages, etc.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute",
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Working directory for the command (optional, defaults to project working dir)",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Timeout in seconds (default 30, max 120)",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (r *ToolRegistry) shellExec(argsJSON string) ToolResult {
	var args struct {
		Command        string `json:"command"`
		WorkingDir     string `json:"working_dir"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	if args.TimeoutSeconds <= 0 || args.TimeoutSeconds > 120 {
		args.TimeoutSeconds = 30
	}

	// Show command and request approval
	fmt.Printf("\n\033[2m  $ %s\033[0m\n", args.Command)
	if !r.approver("Execute shell command", args.Command) {
		return ToolResult{Content: "shell_exec cancelled by user", IsError: true}
	}

	workDir := r.workDir
	if args.WorkingDir != "" {
		workDir = r.resolvePath(args.WorkingDir)
	}

	cmd := exec.Command("bash", "-c", args.Command)
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		if result == "" {
			result = err.Error()
		}
		return ToolResult{
			Content: fmt.Sprintf("Exit error: %v\n\n%s", err, result),
			IsError: true,
		}
	}

	if result == "" {
		result = "(no output)"
	}
	return ToolResult{Content: result}
}

// --- Tool: grep_files ---

func (r *ToolRegistry) defGrepFiles() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "grep_files",
			Description: "Search for a pattern in files. Returns matching lines with file and line number context.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Search pattern (regex or literal string)",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory or file to search in (default: working directory)",
					},
					"file_pattern": map[string]any{
						"type":        "string",
						"description": "File glob pattern filter (e.g., '*.go', '*.py')",
					},
					"case_sensitive": map[string]any{
						"type":        "boolean",
						"description": "Case sensitive search (default true)",
					},
					"context_lines": map[string]any{
						"type":        "integer",
						"description": "Number of context lines before/after match (default 2)",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (r *ToolRegistry) grepFiles(argsJSON string) ToolResult {
	var args struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		FilePattern   string `json:"file_pattern"`
		CaseSensitive *bool  `json:"case_sensitive"`
		ContextLines  int    `json:"context_lines"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	grepArgs := []string{"-rn", "--include-error-files=false"}

	caseSensitive := true
	if args.CaseSensitive != nil {
		caseSensitive = *args.CaseSensitive
	}
	if !caseSensitive {
		grepArgs = append(grepArgs, "-i")
	}

	contextLines := 2
	if args.ContextLines > 0 {
		contextLines = args.ContextLines
	}
	grepArgs = append(grepArgs, fmt.Sprintf("-C%d", contextLines))

	if args.FilePattern != "" {
		grepArgs = append(grepArgs, "--include="+args.FilePattern)
	}

	grepArgs = append(grepArgs, args.Pattern)

	searchPath := r.workDir
	if args.Path != "" {
		searchPath = r.resolvePath(args.Path)
	}
	grepArgs = append(grepArgs, searchPath)

	cmd := exec.Command("grep", grepArgs...)
	output, _ := cmd.CombinedOutput()
	result := string(output)

	if result == "" {
		return ToolResult{Content: "No matches found"}
	}
	return ToolResult{Content: result}
}

// --- Tool: patch_file ---

func (r *ToolRegistry) defPatchFile() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "patch_file",
			Description: `Edit a file by replacing specific content. Prefer this over write_file when modifying existing files.

Two modes (pick one per call):
1. String mode: provide old_str + new_str — finds the exact string and replaces it.
2. Line mode: provide start_line + end_line + new_content — replaces that line range.

Tips:
- old_str must match the file exactly (including indentation).
- Use read_file first to see the exact content before patching.
- For multiple independent edits in the same file, make separate patch_file calls.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path, relative to working directory or absolute",
					},
					"old_str": map[string]any{
						"type":        "string",
						"description": "[String mode] Exact substring to find and replace. Must be unique in the file.",
					},
					"new_str": map[string]any{
						"type":        "string",
						"description": "[String mode] Replacement text. Use empty string to delete old_str.",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "[Line mode] First line to replace (1-based, inclusive)",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "[Line mode] Last line to replace (1-based, inclusive)",
					},
					"new_content": map[string]any{
						"type":        "string",
						"description": "[Line mode] Text that replaces lines start_line..end_line. May contain newlines. Use empty string to delete those lines.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (r *ToolRegistry) patchFile(argsJSON string) ToolResult {
	var args struct {
		Path       string `json:"path"`
		OldStr     string `json:"old_str"`
		NewStr     string `json:"new_str"`
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		NewContent string `json:"new_content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	path := r.resolvePath(args.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}
	}
	original := string(data)

	// Decide mode
	useLineMode := args.StartLine > 0 || args.EndLine > 0
	useStrMode := args.OldStr != ""

	if useLineMode && useStrMode {
		return ToolResult{Content: "specify either old_str or start_line/end_line, not both", IsError: true}
	}
	if !useLineMode && !useStrMode {
		return ToolResult{Content: "provide old_str (string mode) or start_line+end_line (line mode)", IsError: true}
	}

	// Show diff preview before applying
	fileLines := splitLines(original)
	fmt.Println()
	if useStrMode {
		startLine := findLineNumber(original, args.OldStr)
		PrintDiff(os.Stdout, args.Path, splitLines(args.OldStr), splitLines(args.NewStr), startLine, 3)
	} else {
		endLine := args.EndLine
		if endLine > len(fileLines) {
			endLine = len(fileLines)
		}
		PrintDiffWithContext(os.Stdout, args.Path, fileLines, splitLines(args.NewContent), args.StartLine, endLine, 3)
	}
	fmt.Println()

	if !r.approver("Apply patch", args.Path) {
		return ToolResult{Content: "patch_file cancelled by user", IsError: true}
	}

	// Backup original before applying patch
	r.undo.Push(path)

	var patched string

	if useStrMode {
		patched, err = patchByString(original, args.OldStr, args.NewStr)
	} else {
		patched, err = patchByLines(original, args.StartLine, args.EndLine, args.NewContent)
	}
	if err != nil {
		return ToolResult{Content: err.Error(), IsError: true}
	}

	if err := os.WriteFile(path, []byte(patched), 0644); err != nil {
		return ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}
	}

	// Build summary
	oldLines := strings.Count(original, "\n") + 1
	newLines := strings.Count(patched, "\n") + 1
	delta := newLines - oldLines
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	return ToolResult{
		Content: fmt.Sprintf("Patched %s (%d → %d lines, %s%d)", args.Path, oldLines, newLines, sign, delta),
	}
}

// patchByString replaces the first (and only) occurrence of oldStr.
// Returns an error if oldStr is not found or is ambiguous (appears more than once).
func patchByString(content, oldStr, newStr string) (string, error) {
	count := strings.Count(content, oldStr)
	if count == 0 {
		// Provide a helpful hint showing nearby content
		return "", fmt.Errorf("old_str not found in file — check indentation and whitespace")
	}
	if count > 1 {
		return "", fmt.Errorf("old_str matches %d locations; make it more specific by including more context", count)
	}
	return strings.Replace(content, oldStr, newStr, 1), nil
}

// patchByLines replaces lines [startLine, endLine] (1-based, inclusive) with newContent.
func patchByLines(content string, startLine, endLine int, newContent string) (string, error) {
	lines := strings.Split(content, "\n")
	n := len(lines)

	if startLine < 1 || startLine > n {
		return "", fmt.Errorf("start_line %d out of range (file has %d lines)", startLine, n)
	}
	if endLine < startLine {
		return "", fmt.Errorf("end_line %d must be >= start_line %d", endLine, startLine)
	}
	if endLine > n {
		endLine = n
	}

	var parts []string
	parts = append(parts, lines[:startLine-1]...)
	if newContent != "" {
		parts = append(parts, strings.Split(newContent, "\n")...)
	}
	parts = append(parts, lines[endLine:]...)

	return strings.Join(parts, "\n"), nil
}

// --- helpers ---

func (r *ToolRegistry) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(r.workDir, path)
}
