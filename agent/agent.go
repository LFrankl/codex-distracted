package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex/llm"
)

const systemPrompt = `You are distracted-codex, a minimal coding assistant. Do ONLY what was explicitly asked.

Available tools: read_file, write_file, patch_file, list_files, find_files, shell_exec,
grep_files, move_file, delete_file, http_request, git_status, git_diff, git_log, git_commit.

STRICT RULES — violating any of these is wrong:
1. NEVER list files or explore directories speculatively.
2. NEVER create test files, README files, or example files unless explicitly requested.
3. NEVER run shell commands unless explicitly asked to — EXCEPT rule 8 below.
4. NEVER commit, stage, or push unless explicitly asked.
5. NEVER add more files than what was requested. "Write X" = create X only.
6. Only read a file if you need its exact content right now to complete the task.
7. Answer factual questions directly — do not call any tools first.
8. If the user's message IS a shell command (e.g. "ls", "pwd", "go build", "npm install"),
   run it immediately with shell_exec — no explanation needed.
   Do NOT translate it into list_files or read_file; just execute it.
9. If patch_file fails with "old_str not found", the error includes the current file content.
   Use THAT content to construct the correct old_str. Never retry blindly with a different guess.
10. BATCH tool calls: when creating or modifying multiple independent files, return ALL
    tool calls in a single response — do NOT wait for each result before writing the next.
    Writing 5 files = one response with 5 write_file calls, not 5 round trips.

Tool guidance:
- find_files: use for glob searches like "*.go" or "src/**/*.ts" — faster than shell find
- grep_files: use to search file contents for a pattern
- move_file / delete_file: support undo via /undo
- http_request: use to test local API endpoints (GET/POST)

Examples:
- User: "ls"                    → shell_exec("ls"), done.
- User: "cat main.go"           → shell_exec("cat main.go"), done.
- User: "find all .go files"    → find_files("**/*.go"), done.
- User: "write a fibonacci function" → write fib.go, done. No tests, no README.
- User: "fix main.go line 42"   → read_file(main.go around line 42), patch, done.

When implementing a function:
- Write exactly ONE version — the most straightforward correct implementation.
- Do NOT provide multiple variants unless asked to compare.

Working directory: %s`

const thoroughPrompt = `You are distracted-codex, a senior engineer assistant. Work in a structured, professional manner.

Available tools: read_file, write_file, patch_file, list_files, find_files, shell_exec,
grep_files, move_file, delete_file, http_request, git_status, git_diff, git_log, git_commit.

Workflow — follow these phases in order:

1. UNDERSTAND (before touching any file)
   - Read the files directly relevant to the task. Skip unrelated ones.
   - Use find_files / grep_files to locate code; git_log or git_diff for recent changes.
   - Form a clear mental model before writing a single line.

2. PLAN (think before acting)
   - State your approach in 2–3 sentences before using any write/patch tool.
   - If the task is ambiguous, ask ONE clarifying question — then proceed.

3. IMPLEMENT (make changes)
   - Edit only the files necessary. Don't touch unrelated code.
   - Prefer patch_file over write_file for existing files.
   - Follow existing code style, naming conventions, and patterns in the repo.
   - BATCH independent writes: return multiple write_file/patch_file calls in one response.

4. VERIFY (confirm correctness)
   - If tests exist, run them. If the project builds, compile it.
   - Use http_request to test API endpoints if relevant.
   - If verification fails, fix the issue before declaring done.
   - Do NOT skip this phase on non-trivial changes.

5. REPORT (brief summary)
   - State what was changed and why, in 2–4 bullet points.
   - If there are known limitations or follow-up tasks, mention them.

Guardrails that apply even in thorough mode:
- Do NOT create test files, READMEs, or extra files unless asked.
- Do NOT commit unless explicitly asked.
- Do NOT refactor code unrelated to the task.
- One implementation per function — no variant zoo.
- If patch_file fails with "old_str not found", the error includes the current file content.
  Use THAT content to construct the correct old_str. Never retry blindly with a different guess.

Working directory: %s`

// Agent orchestrates the LLM + tool loop
type Agent struct {
	client   *llm.Client
	tools    *ToolRegistry
	messages []llm.Message
	maxSteps int
	out      io.Writer
	stats    SessionStats
	prompt   string // system prompt template (uses %s for workDir)
	thorough bool
}

func New(client *llm.Client, workDir string, maxSteps int, out io.Writer, approver Approver, thorough bool) *Agent {
	a := &Agent{
		client:   client,
		tools:    NewToolRegistry(workDir, approver),
		maxSteps: maxSteps,
		out:      out,
		thorough: thorough,
	}
	if thorough {
		a.prompt = thoroughPrompt
	} else {
		a.prompt = systemPrompt
	}
	return a
}

// SetThorough switches the agent between default and thorough mode.
// It replaces the system message so the new mode takes effect immediately.
func (a *Agent) SetThorough(on bool) {
	a.thorough = on
	if on {
		a.prompt = thoroughPrompt
	} else {
		a.prompt = systemPrompt
	}
	// Replace system message in-place so the change applies to the current session
	workDir := a.tools.workDir
	newSystem := llm.Message{Role: "system", Content: fmt.Sprintf(a.prompt, workDir)}
	if len(a.messages) > 0 && a.messages[0].Role == "system" {
		a.messages[0] = newSystem
	}
	// (if no messages yet, Run() will build it fresh)
}

// IsThorough reports the current mode.
func (a *Agent) IsThorough() bool { return a.thorough }

// Messages returns the current conversation history.
func (a *Agent) Messages() []llm.Message { return a.messages }

// SetMessages replaces the conversation history (used when loading a session).
func (a *Agent) SetMessages(msgs []llm.Message) { a.messages = msgs }

// Stats returns accumulated token usage for this session.
func (a *Agent) Stats() SessionStats { return a.stats }

// Undo reverts the most recent file write or patch.
func (a *Agent) Undo() (string, error) { return a.tools.undo.Pop() }

// UndoLen returns how many undo steps are available.
func (a *Agent) UndoLen() int { return a.tools.undo.Len() }

// Reset clears conversation history (keeps system prompt)
func (a *Agent) Reset() {
	a.messages = nil
}

// Run processes a user message through the agent loop
func (a *Agent) Run(ctx context.Context, userMsg string) error {
	// Initialize system prompt on first message
	if len(a.messages) == 0 {
		workDir := a.tools.workDir
		prompt := fmt.Sprintf(a.prompt, workDir)

		if mem, memPath := loadProjectMemory(workDir); mem != "" {
			prompt += "\n\n## Project Memory (.codex.md)\n" + mem
			printMemoryLoaded(a.out, memPath, mem)
		}

		a.messages = append(a.messages, llm.Message{
			Role:    "system",
			Content: prompt,
		})
	}

	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: userMsg,
	})

	for step := 0; step < a.maxSteps; step++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Compress context if approaching token limit
		if err := a.maybeCompress(ctx); err != nil {
			fmt.Fprintf(a.out, "\033[2m[compression failed: %v]\033[0m\n", err)
		}

		response, toolCalls, err := a.callLLM(ctx)
		if err != nil {
			return fmt.Errorf("LLM error: %w", err)
		}

		// Add assistant message to history (trim long text replies)
		assistantMsg := llm.Message{Role: "assistant"}
		if response != "" {
			assistantMsg.Content = response
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = toolCalls
		}
		a.messages = append(a.messages, trimAssistantContent(assistantMsg))

		// No tool calls = done
		if len(toolCalls) == 0 {
			break
		}

		// Execute tool calls — independent calls run concurrently.
		type tcResult struct {
			tc      llm.ToolCall
			result  ToolResult
			content string
		}
		results := make([]tcResult, len(toolCalls))

		// Determine which tools are safe to run concurrently (read-only or independent writes).
		// Approval-gated tools (shell_exec, patch_file, git_commit) run serially to keep
		// the approval prompt readable.
		needsSerial := func(name string) bool {
			switch name {
			case "shell_exec", "patch_file", "git_commit", "move_file", "delete_file":
				return true
			}
			return false
		}

		// Split into serial and parallel groups preserving order.
		// Run parallel group first (all at once), then serial ones in order.
		// Simple approach: if ANY call needs serial, run all serially to preserve ordering.
		anySerial := false
		for _, tc := range toolCalls {
			if needsSerial(tc.Function.Name) {
				anySerial = true
				break
			}
		}

		if !anySerial && len(toolCalls) > 1 {
			// All calls are safe to parallelize (write_file, read_file, find_files, etc.)
			type indexedResult struct {
				i      int
				result ToolResult
			}
			ch := make(chan indexedResult, len(toolCalls))
			for i, tc := range toolCalls {
				i, tc := i, tc
				go func() {
					ch <- indexedResult{i, a.tools.Execute(tc.Function.Name, tc.Function.Arguments)}
				}()
			}
			for range toolCalls {
				r := <-ch
				results[r.i] = tcResult{tc: toolCalls[r.i], result: r.result}
			}
		} else {
			for i, tc := range toolCalls {
				results[i] = tcResult{tc: tc, result: a.tools.Execute(tc.Function.Name, tc.Function.Arguments)}
			}
		}

		// Print and store results in order.
		for i := range results {
			r := &results[i]
			a.printToolCall(r.tc)
			a.printToolResult(r.tc.Function.Name, r.result)

			content := r.result.Content
			if r.tc.Function.Name == "shell_exec" || r.tc.Function.Name == "grep_files" {
				const maxShellRunes = 2000
				if runes := []rune(content); len(runes) > maxShellRunes {
					content = string(runes[:maxShellRunes]) + "\n…(truncated)"
				}
			}
			r.content = content

			a.messages = append(a.messages, llm.Message{
				Role:       "tool",
				ToolCallID: r.tc.ID,
				Name:       r.tc.Function.Name,
				Content:    r.content,
			})
		}
	}

	return nil
}

// callLLM sends messages and streams the response
func (a *Agent) callLLM(ctx context.Context) (string, []llm.ToolCall, error) {
	var contentBuilder strings.Builder
	var finalToolCalls []llm.ToolCall
	var turnUsage *llm.Usage

	spinner := newSpinner(a.out)
	spinner.Start("Thinking")
	defer spinner.Stop() // ensure cleanup on early return

	// prefixPrinted tracks whether we've emitted the "Assistant:" header.
	// It is printed lazily on the first content token so the spinner line
	// is fully cleared before any text appears.
	prefixPrinted := false

	err := a.client.Chat(ctx, a.messages, a.tools.Definitions(), func(event llm.StreamEvent) {
		if event.Error != nil {
			return
		}
		if event.Done {
			finalToolCalls = event.ToolCalls
			turnUsage = event.Usage
			spinner.Stop()
			if contentBuilder.Len() > 0 {
				fmt.Fprintln(a.out) // newline after streamed text
			}
			return
		}
		if event.Content != "" {
			if !prefixPrinted {
				spinner.Stop()
				fmt.Fprint(a.out, "\n\033[36m◈\033[0m ")
				prefixPrinted = true
			}
			fmt.Fprint(a.out, event.Content)
			contentBuilder.WriteString(event.Content)
		}
	})

	if err != nil {
		return "", nil, err
	}

	// Display token usage if provider reports it
	if turnUsage != nil {
		turn := TurnStats{turnUsage.PromptTokens, turnUsage.CompletionTokens}
		a.stats.Add(turn)
		fmt.Fprintf(a.out, "  \033[2m↑%d ↓%d", turn.PromptTokens, turn.CompletionTokens)
		if a.stats.Turns > 1 {
			fmt.Fprintf(a.out, "  ·  total ↑%d ↓%d", a.stats.PromptTokens, a.stats.CompletionTokens)
		}
		fmt.Fprint(a.out, "\033[0m\n")
	}

	return contentBuilder.String(), finalToolCalls, nil
}

func (a *Agent) printToolCall(tc llm.ToolCall) {
	detail := toolDetail(tc)
	if detail != "" {
		fmt.Fprintf(a.out, "\n  \033[33m◆\033[0m \033[1m%s\033[0m  \033[2m%s\033[0m\n",
			tc.Function.Name, detail)
	} else {
		fmt.Fprintf(a.out, "\n  \033[33m◆\033[0m \033[1m%s\033[0m\n", tc.Function.Name)
	}
}

func toolDetail(tc llm.ToolCall) string {
	var args map[string]any
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				s := fmt.Sprintf("%v", v)
				if s != "" && s != "<nil>" {
					return s
				}
			}
		}
		return ""
	}
	switch tc.Function.Name {
	case "read_file", "write_file", "patch_file":
		return pick("path")
	case "shell_exec":
		return pick("command")
	case "grep_files":
		return pick("pattern")
	case "list_files":
		return pick("path")
	case "git_commit":
		return pick("message")
	case "git_diff":
		if staged, ok := args["staged"].(bool); ok && staged {
			return "staged"
		}
		return pick("base")
	case "git_log":
		if n, ok := args["n"].(float64); ok && int(n) != 10 {
			return fmt.Sprintf("-%d", int(n))
		}
	}
	return ""
}

func (a *Agent) printToolResult(_ string, result ToolResult) {
	content := strings.TrimRight(result.Content, "\n")
	lines := strings.Split(content, "\n")

	const maxLines = 15
	truncated := 0
	if len(lines) > maxLines {
		truncated = len(lines) - maxLines
		lines = lines[:maxLines]
	}

	if result.IsError {
		fmt.Fprintf(a.out, "  \033[31m✗\033[0m \033[2m%s\033[0m\n", lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(a.out, "    \033[2m%s\033[0m\n", l)
		}
		return
	}

	if len(lines) <= 1 {
		// Single line: show inline
		fmt.Fprintf(a.out, "  \033[32m✓\033[0m \033[2m%s\033[0m\n", lines[0])
	} else {
		// Multi-line: gutter
		fmt.Fprintf(a.out, "  \033[32m✓\033[0m\n")
		for _, l := range lines {
			fmt.Fprintf(a.out, "  \033[2m│ %s\033[0m\n", l)
		}
		if truncated > 0 {
			fmt.Fprintf(a.out, "  \033[2m│ … (%d more lines)\033[0m\n", truncated)
		}
	}
}

