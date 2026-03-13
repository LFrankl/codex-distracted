package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex/llm"
)

// ToolResult holds the output of a tool call
type ToolResult struct {
	Content     string
	IsError     bool
	Instruction string // non-empty when user chose "Other instructions" at approval prompt
}

// ToolRegistry maps tool names to their definitions and handlers
type ToolRegistry struct {
	workDir     string
	defs        []llm.Tool
	approver    Approver
	undo        UndoStack
	rag         *ragState   // nil when RAG is not available
	client      *llm.Client // used by run_task to spawn sub-agents
	depth       int         // 0 = main agent, 1 = sub-agent (run_task blocked)
	preApproved bool        // set by agent loop for batch-approved concurrent patch execution
}

func NewToolRegistry(workDir string, approver Approver, client *llm.Client, depth int) *ToolRegistry {
	r := &ToolRegistry{
		workDir:  workDir,
		approver: approver,
		client:   client,
		depth:    depth,
	}
	r.defs = []llm.Tool{
		r.defReadFile(),
		r.defWriteFile(),
		r.defPatchFile(),
		r.defListFiles(),
		r.defFindFiles(),
		r.defShellExec(),
		r.defGrepFiles(),
		r.defMoveFile(),
		r.defDeleteFile(),
		r.defHTTPRequest(),
		r.defGitStatus(),
		r.defGitDiff(),
		r.defGitLog(),
		r.defGitCommit(),
		r.defGitBranch(),
		r.defGitPull(),
		r.defGitPush(),
		r.defWebFetch(),
		r.defFileOutline(),
		r.defSemanticSearch(),
		r.defRunTask(), // registered at all depths; blocked at runtime when depth > 0
	}
	return r
}

func (r *ToolRegistry) Definitions() []llm.Tool {
	return r.defs
}

func (r *ToolRegistry) Execute(ctx context.Context, name, argsJSON string) ToolResult {
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
	case "find_files":
		return r.findFiles(argsJSON)
	case "move_file":
		return r.moveFile(argsJSON)
	case "delete_file":
		return r.deleteFile(argsJSON)
	case "http_request":
		return r.httpRequest(argsJSON)
	case "git_status":
		return r.gitStatus(argsJSON)
	case "git_diff":
		return r.gitDiff(argsJSON)
	case "git_log":
		return r.gitLog(argsJSON)
	case "git_commit":
		return r.gitCommit(argsJSON)
	case "git_branch":
		return r.gitBranch(argsJSON)
	case "git_pull":
		return r.gitPull(argsJSON)
	case "git_push":
		return r.gitPush(argsJSON)
	case "web_fetch":
		return r.webFetch(argsJSON)
	case "file_outline":
		return r.fileOutline(argsJSON)
	case "semantic_search":
		return r.semanticSearch(argsJSON)
	case "run_task":
		return r.runTask(ctx, argsJSON)
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
			Name: "shell_exec",
			Description: `Execute a shell command and return stdout+stderr.

BACKGROUND EXECUTION: append ' &' to run a long-lived process without blocking.
REQUIRED for: dev servers, 'go run', 'npm run dev', any process that stays running.
Example: shell_exec("cd /abs/path && go run main.go &")
Returns immediately with the PID. Use http_request to verify the server started.

Use for: running tests, building, installing packages, starting servers.`,
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

// isSafeReadOnly returns true for commands that only read state and never mutate files or
// processes. These are auto-approved without prompting the user.
func isSafeReadOnly(command string) bool {
	// Trim leading env var assignments like "FOO=bar cmd ..."
	cmd := strings.TrimSpace(command)

	// Extract the base command (first token, ignoring env var prefix)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}
	base := filepath.Base(parts[0])

	safeCommands := map[string]bool{
		// filesystem inspection
		"ls": true, "ll": true, "la": true, "dir": true,
		"pwd": true, "echo": true, "cat": true, "head": true, "tail": true,
		"wc": true, "stat": true, "file": true, "du": true, "df": true,
		"find": true, "tree": true, "realpath": true,
		// text processing (read-only)
		"grep": true, "rg": true, "ag": true, "awk": true, "sed": true,
		"sort": true, "uniq": true, "cut": true, "tr": true, "diff": true,
		"less": true, "more": true, "strings": true, "hexdump": true,
		// system info
		"which": true, "whereis": true, "type": true, "env": true, "printenv": true,
		"uname": true, "hostname": true, "whoami": true, "id": true, "date": true,
		"uptime": true, "ps": true, "top": true, "htop": true,
		// language/tool versions
		"go": true, "node": true, "python": true, "python3": true, "ruby": true,
		"java": true, "rustc": true, "cargo": true, "npm": true, "yarn": true,
		"pnpm": true, "bun": true, "pip": true, "pip3": true,
		// git read-only
		"git": true,
		// network info (read-only)
		"curl": true, "wget": true, "ping": true, "nslookup": true, "dig": true,
		"netstat": true, "ss": true, "lsof": true,
	}

	if !safeCommands[base] {
		return false
	}

	// Special cases: git is only safe for read-only subcommands
	if base == "git" {
		safeGitSubs := map[string]bool{
			"status": true, "log": true, "diff": true, "show": true,
			"branch": true, "tag": true, "remote": true, "fetch": true,
			"ls-files": true, "rev-parse": true, "describe": true,
			"shortlog": true, "blame": true, "stash": true, "config": true,
		}
		if len(parts) < 2 {
			return false
		}
		return safeGitSubs[parts[1]]
	}

	// curl/wget with -X POST or -d flag is a write operation — treat as unsafe
	if base == "curl" || base == "wget" {
		for _, p := range parts[1:] {
			if p == "-X" || p == "--request" || p == "-d" || p == "--data" ||
				p == "--data-raw" || p == "--data-binary" || p == "--upload-file" || p == "-T" {
				return false
			}
			// -XPOST, -XPUT etc. (combined flag)
			if strings.HasPrefix(p, "-X") && len(p) > 2 {
				method := strings.ToUpper(p[2:])
				if method != "GET" && method != "HEAD" {
					return false
				}
			}
		}
	}

	return true
}

// isMkdirOnly returns true if the command is exclusively creating directories
// (e.g. "mkdir foo", "mkdir -p a/b/c", "mkdir -p a && mkdir -p b").
// Used to silently skip redundant mkdir calls in sub-agents.
func isMkdirOnly(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	// Split on && or ; and check each segment is just mkdir
	segments := strings.FieldsFunc(cmd, func(r rune) bool { return r == ';' })
	var parts []string
	for _, seg := range segments {
		for _, p := range strings.Split(seg, "&&") {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		tokens := strings.Fields(part)
		if len(tokens) == 0 {
			continue
		}
		if filepath.Base(tokens[0]) != "mkdir" {
			return false
		}
	}
	return true
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

	// In sub-agents (depth > 0), silently skip pure mkdir commands — write_file
	// already creates parent directories automatically, so mkdir is always redundant.
	if r.depth > 0 && isMkdirOnly(args.Command) {
		return ToolResult{Content: "(skipped: write_file creates directories automatically)"}
	}

	// Auto-approve safe read-only commands; prompt only for mutating ones.
	fmt.Printf("\n\033[2m  $ %s\033[0m\n", args.Command)
	if !isSafeReadOnly(args.Command) {
		if ok, instr := r.approver("Execute shell command", args.Command); !ok {
			if instr != "" {
				return ToolResult{Content: "shell_exec cancelled — user provided new instruction", Instruction: instr}
			}
			return ToolResult{Content: "shell_exec cancelled by user", IsError: true}
		}
	}

	workDir := r.workDir
	if args.WorkingDir != "" {
		workDir = r.resolvePath(args.WorkingDir)
	}

	cmd := exec.Command("bash", "-c", args.Command)
	cmd.Dir = workDir

	// Background command (ends with &): detach and return immediately.
	if strings.HasSuffix(strings.TrimSpace(args.Command), "&") {
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return ToolResult{Content: "failed to start: " + err.Error(), IsError: true}
		}
		return ToolResult{Content: fmt.Sprintf("started in background (pid %d)", cmd.Process.Pid)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(args.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "bash", "-c", args.Command)
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	result := string(output)

	if ctx.Err() == context.DeadlineExceeded {
		return ToolResult{Content: fmt.Sprintf("timed out after %ds\n\n%s", args.TimeoutSeconds, result), IsError: true}
	}
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

MULTI-EDIT IN ONE CALL: Use the "patches" array to apply several string replacements to the
same file in a single call. This is ALWAYS preferred over multiple patch_file calls on the same file.
  Example: patches=[{"old_str":"a","new_str":"b"},{"old_str":"c","new_str":"d"}]
  Patches are applied top-to-bottom; earlier ones must not shift the text needed by later ones.

Tips:
- old_str must match the file exactly (including indentation).
- Use read_file first to see the exact content before patching.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path, relative to working directory or absolute",
					},
					"old_str": map[string]any{
						"type":        "string",
						"description": "[Single string mode] Exact substring to find and replace.",
					},
					"new_str": map[string]any{
						"type":        "string",
						"description": "[Single string mode] Replacement text. Use empty string to delete old_str.",
					},
					"patches": map[string]any{
						"type":        "array",
						"description": "[Multi-edit mode] Apply multiple string replacements in one call. Each item has old_str and new_str.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"old_str": map[string]any{"type": "string"},
								"new_str": map[string]any{"type": "string"},
							},
							"required": []string{"old_str", "new_str"},
						},
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
		Patches    []struct {
			OldStr string `json:"old_str"`
			NewStr string `json:"new_str"`
		} `json:"patches"`
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

	// Multi-edit mode: patches array takes priority
	if len(args.Patches) > 0 {
		fmt.Println()
		current := original
		for _, p := range args.Patches {
			startLine := findLineNumber(current, p.OldStr)
			PrintDiff(os.Stdout, args.Path, splitLines(p.OldStr), splitLines(p.NewStr), startLine, 3)
		}
		fmt.Println()

		if !r.preApproved {
			if ok, instr := r.approver("Apply patches", args.Path); !ok {
				if instr != "" {
					return ToolResult{Content: "patch cancelled — user provided new instruction", Instruction: instr}
				}
				return ToolResult{Content: "patch_file cancelled by user", IsError: true}
			}
		}

		r.undo.Push(path)
		for i, p := range args.Patches {
			current, err = patchByString(current, p.OldStr, p.NewStr)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					return ToolResult{
						Content: fmt.Sprintf("patch %d/%d: %s\n\nCurrent content of %s:\n%s",
							i+1, len(args.Patches), err.Error(), args.Path, current),
						IsError: true,
					}
				}
				return ToolResult{Content: fmt.Sprintf("patch %d/%d: %s", i+1, len(args.Patches), err.Error()), IsError: true}
			}
		}
		if err := os.WriteFile(path, []byte(current), 0644); err != nil {
			return ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}
		}
		oldLines := strings.Count(original, "\n") + 1
		newLines := strings.Count(current, "\n") + 1
		delta := newLines - oldLines
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		return ToolResult{
			Content: fmt.Sprintf("Patched %s (%d edits, %d → %d lines, %s%d)", args.Path, len(args.Patches), oldLines, newLines, sign, delta),
		}
	}

	// Decide single-edit mode
	useLineMode := args.StartLine > 0 || args.EndLine > 0
	useStrMode := args.OldStr != ""

	if useLineMode && useStrMode {
		return ToolResult{Content: "specify either old_str or start_line/end_line, not both", IsError: true}
	}
	if !useLineMode && !useStrMode {
		return ToolResult{Content: "provide old_str (string mode), patches array (multi-edit), or start_line+end_line (line mode)", IsError: true}
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

	if !r.preApproved {
		if ok, instr := r.approver("Apply patch", args.Path); !ok {
			if instr != "" {
				return ToolResult{Content: "patch cancelled — user provided new instruction", Instruction: instr}
			}
			return ToolResult{Content: "patch_file cancelled by user", IsError: true}
		}
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
		// For old_str not found, include the current file content so the LLM
		// can see the real state and construct a correct old_str without re-reading.
		if useStrMode && strings.Contains(err.Error(), "not found") {
			return ToolResult{
				Content: fmt.Sprintf(
					"%s\n\nCurrent content of %s:\n%s",
					err.Error(), args.Path, original,
				),
				IsError: true,
			}
		}
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

// --- Tool: find_files ---

func (r *ToolRegistry) defFindFiles() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "find_files",
			Description: "Find files matching a glob pattern. More precise than list_files. Examples: '*.go', '**/*.ts', 'src/**/*.json'.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern relative to working directory, e.g. '**/*.go' or 'src/*.ts'",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (r *ToolRegistry) findFiles(argsJSON string) ToolResult {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Pattern == "" {
		return ToolResult{Content: "pattern is required", IsError: true}
	}

	var matches []string
	err := filepath.WalkDir(r.workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(r.workDir, path)
		matched, _ := filepath.Match(args.Pattern, rel)
		// Also try matching just the filename for simple patterns like "*.go"
		if !matched {
			matched, _ = filepath.Match(args.Pattern, d.Name())
		}
		if matched && !d.IsDir() {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return ToolResult{Content: "walk error: " + err.Error(), IsError: true}
	}
	if len(matches) == 0 {
		return ToolResult{Content: "no files matched " + args.Pattern}
	}
	return ToolResult{Content: strings.Join(matches, "\n")}
}

// --- Tool: move_file ---

func (r *ToolRegistry) defMoveFile() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "move_file",
			Description: "Move or rename a file. The original content is saved for undo.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"src": map[string]any{
						"type":        "string",
						"description": "Source path (relative or absolute)",
					},
					"dst": map[string]any{
						"type":        "string",
						"description": "Destination path (relative or absolute)",
					},
				},
				"required": []string{"src", "dst"},
			},
		},
	}
}

func (r *ToolRegistry) moveFile(argsJSON string) ToolResult {
	var args struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	src := r.resolvePath(args.Src)
	dst := r.resolvePath(args.Dst)

	if _, err := os.Stat(src); err != nil {
		return ToolResult{Content: fmt.Sprintf("source not found: %s", args.Src), IsError: true}
	}

	// Backup source for undo (undo will restore it at src; dst will remain but that's acceptable)
	r.undo.Push(src)

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return ToolResult{Content: "mkdir error: " + err.Error(), IsError: true}
	}
	if err := os.Rename(src, dst); err != nil {
		return ToolResult{Content: "move error: " + err.Error(), IsError: true}
	}
	return ToolResult{Content: fmt.Sprintf("moved %s → %s", args.Src, args.Dst)}
}

// --- Tool: delete_file ---

func (r *ToolRegistry) defDeleteFile() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "delete_file",
			Description: "Delete a file. The content is backed up and can be restored with undo.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path to delete (relative or absolute)",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (r *ToolRegistry) deleteFile(argsJSON string) ToolResult {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	path := r.resolvePath(args.Path)

	if _, err := os.Stat(path); err != nil {
		return ToolResult{Content: fmt.Sprintf("file not found: %s", args.Path), IsError: true}
	}

	// Backup for undo before deleting
	r.undo.Push(path)

	if err := os.Remove(path); err != nil {
		return ToolResult{Content: "delete error: " + err.Error(), IsError: true}
	}
	return ToolResult{Content: fmt.Sprintf("deleted %s", args.Path)}
}

// --- Tool: http_request ---

func (r *ToolRegistry) defHTTPRequest() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "http_request",
			Description: "Make an HTTP request (GET or POST) to a URL. Useful for testing APIs.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method": map[string]any{
						"type":        "string",
						"description": "HTTP method: GET or POST",
					},
					"url": map[string]any{
						"type":        "string",
						"description": "Full URL including scheme, e.g. http://localhost:8080/api/users",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Request body (for POST), typically JSON string",
					},
					"headers": map[string]any{
						"type":        "object",
						"description": "Optional HTTP headers as key-value pairs",
					},
				},
				"required": []string{"method", "url"},
			},
		},
	}
}

func (r *ToolRegistry) httpRequest(argsJSON string) ToolResult {
	var args struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	method := strings.ToUpper(args.Method)
	if method != "GET" && method != "POST" {
		return ToolResult{Content: "only GET and POST are supported", IsError: true}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var bodyReader *strings.Reader
	if args.Body != "" {
		bodyReader = strings.NewReader(args.Body)
	} else {
		bodyReader = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(ctx, method, args.URL, bodyReader)
	if err != nil {
		return ToolResult{Content: "request build error: " + err.Error(), IsError: true}
	}
	if args.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ToolResult{Content: "request error: " + err.Error(), IsError: true}
	}
	defer resp.Body.Close()

	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	body := strings.TrimSpace(string(buf[:n]))

	result := fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, body)
	return ToolResult{Content: result, IsError: resp.StatusCode >= 400}
}

// --- helpers ---

func (r *ToolRegistry) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(r.workDir, path)
}
