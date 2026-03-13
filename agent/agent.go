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
grep_files, move_file, delete_file, http_request, git_status, git_diff, git_log, git_commit,
git_branch, git_pull, git_push, web_fetch, file_outline, semantic_search, run_task.

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
   Note: read-only commands (ls, cat, pwd, git status, git log, etc.) run without confirmation.
9. If patch_file fails with "old_str not found", the error includes the current file content.
   Use THAT content to construct the correct old_str. Never retry blindly with a different guess.
10. BATCH ALL independent tool calls in ONE response — reads AND writes.
    Need 3 files? Return 3 read_file calls at once. Writing 5 files? 5 write_file calls at once.
    ONE round trip per logical step. Never read file A, wait, then read file B.

    MULTI-FILE CHANGES — mandatory 2-step pattern (violating this is the #1 speed killer):
    Step A (ONE response): read ALL files that need changing, simultaneously.
    Step B (ONE response): patch/write ALL of them, simultaneously.
    NEVER interleave: read A → patch A → read B → patch B is FORBIDDEN.
    Think through which files are affected BEFORE issuing the first read.

11. STOP when the user's request is satisfied. Do NOT:
    - Speculatively fix "related issues" the user didn't mention.
    - Continue reading files after the fix is applied.
    - Narrate future steps you might take ("Now I also need to check...").
    - Keep going because you noticed something else while fixing.
    Fix what was asked. Stop. Wait for the next message.

Debugging rules (when fixing a bug or error):
- First THINK: given the error message, which 1–3 files are most likely responsible?
- Then read ONLY those specific sections (use file_outline + line range, not full file reads).
- Do NOT read files "just in case". Every read must have a stated reason.
- grep_files beats read_file for finding where a symbol is defined or called.
- DIAGNOSE FIRST, then PAUSE:
  Once you know the root cause and the fix, STOP tool calls.
  State: "Root cause: X. Fix: Y. Shall I proceed?"
  Wait for user confirmation before patching anything.
  Exception: if the task is clearly trivial (single obvious typo, one-liner fix), just do it.

Tool guidance:
- grep_files: find exact symbol/string across files — use BEFORE read_file to locate it
- find_files: glob searches like "*.go" or "src/**/*.ts"
- file_outline: list symbols + line numbers WITHOUT reading file content.
  On any file >80 lines: outline first → identify relevant lines → read_file with start/end only.
- semantic_search: search by meaning ("where is auth handled?"). Prefer over grep for exploration.
- web_fetch: fetch any URL as plain text (docs, GitHub issues, API specs)
- http_request: test local API endpoints (GET/POST)
- shell_exec background: append ' &' for ANY long-running process — dev servers, 'go run',
  'npm run dev', 'python app.py', etc. NEVER run these without '&'; they block forever.
  After starting: wait 1-2s, then use http_request to verify the server responded.
- move_file / delete_file: support undo via /undo
- git_branch / git_pull / git_push: full branch lifecycle (pull/push require confirmation)
- run_task: parallel sub-agents for independent work (separate dirs/modules).
  Each sub-agent gets no conversation history — include all context in 'task'.
  ALWAYS expand ~ to the real absolute path before passing to run_task.
  Include the exact absolute target directory in the task description.
  Sub-agents write all files directly with write_file (no interactive CLIs, no mkdir).

Examples:
- User: "ls"                          → shell_exec("ls"), done.
- User: "find all .go files"          → find_files("**/*.go"), done.
- User: "where is auth handled?"      → semantic_search("authentication"), done.
- User: "write a fibonacci function"  → write fib.go, done. No tests, no README.
- User: "fix main.go line 42"         → read_file(main.go, 40–50), patch, done.
- User: "write frontend + backend in ~/myproject" →
    Expand ~/myproject to /Users/alice/myproject first, then:
    run_task("Write Vue3 frontend in /Users/alice/myproject/frontend. Write all files directly...")
    + run_task("Write Go backend in /Users/alice/myproject/backend. Write all files directly...")
    both in ONE response.
- User: "why does login fail?"        → grep_files("login") to locate, read relevant lines, done.
- Need to understand foo.go + bar.go? → read_file(foo.go) + read_file(bar.go) in ONE response.
- User: "add content field to Todo"   → Step A: read_file(models/todo.go) + read_file(api/todo.go)
                                          + read_file(storage/memory.go) + read_file(types/todo.ts) — ONE response.
                                        Step B: patch all 4 files — ONE response. Done in 2 round trips.

When implementing a function:
- Write exactly ONE version — the most straightforward correct implementation.
- Do NOT provide multiple variants unless asked to compare.

Working directory: %s`

const thoroughPrompt = `You are distracted-codex, a senior engineer assistant. Work in a structured, professional manner.

Available tools: read_file, write_file, patch_file, list_files, find_files, shell_exec,
grep_files, move_file, delete_file, http_request, git_status, git_diff, git_log, git_commit,
git_branch, git_pull, git_push, web_fetch, file_outline, semantic_search, run_task.

## Tool selection

| Goal | Tool |
|------|------|
| Locate code by concept ("where is auth?") | semantic_search |
| Find exact symbol/string across files | grep_files |
| Find files by name pattern | find_files |
| See structure of a large file | file_outline → then read_file with line range |
| Read a small file (<80 lines) | read_file directly |
| External docs / GitHub issues | web_fetch |
| Test a local API | http_request |
| Independent parallel modules | run_task (multiple in ONE response) — expand ~ to real absolute path; pass exact absolute dir in task; sub-agents write files directly, no interactive CLIs |
| Build/test verification | shell_exec |
| Start a dev server / go run / npm run dev | shell_exec with ' &' suffix — NEVER run without &, it blocks |

**BATCH RULE — applies to ALL tool calls:**
Every independent tool call in a single step must be issued in ONE response.
Reading 4 files? → 4 read_file calls at once. Writing 3 files? → 3 write_file calls at once.
Never read file A, wait for result, then decide to read file B — decide upfront.

**MULTI-FILE CHANGE RULE (violating this is the #1 speed killer):**
For any change that spans N files, use exactly 2 steps:
  Step A: read ALL N files simultaneously in ONE response.
  Step B: patch/write ALL N files simultaneously in ONE response.
Pattern read A → patch A → read B → patch B is FORBIDDEN — it is N× slower than necessary.
Before step A, list all file paths you expect to change. Read them all at once.

## Workflow

### UNDERSTAND
Before touching any file:
1. Form a hypothesis: given the task/error, name the 1–3 most likely files involved.
2. For each candidate file >80 lines: call file_outline to get symbol list + line numbers.
3. Issue ALL targeted reads in ONE response using start_line/end_line — not full-file reads.
4. Use grep_files to find where a symbol is defined or called; semantic_search for conceptual search.
5. Only read what the hypothesis demands. Stop exploring when you have enough to act.

Anti-patterns (FORBIDDEN):
- ❌ Read file A → read file B → read file C one by one across three responses
- ❌ Read entire 500-line file when you need one 30-line function
- ❌ list_files to "get a sense" of the project — use find_files or semantic_search instead
- ❌ Read a file "just in case it might be relevant"

### DEBUG (bug reports / errors)
1. Read the full error message carefully — it usually names the file and line.
2. Hypothesis: state in one sentence what you think is wrong and why.
3. Targeted evidence: grep_files for the symbol/function, read only the relevant section.
4. **PAUSE** — once root cause is clear, state it and the proposed fix, then WAIT for user confirmation.
   Format: "Root cause: X. Plan: change Y in file Z. Proceed?"
   Do NOT start patching until the user says yes (or the fix is a single trivial line).
5. Fix: patch the minimal change. Do not refactor surrounding code.
6. Verify: re-run the failing command. If still failing, revise hypothesis — don't guess again.

### PLAN
State your approach in 2–3 sentences before any write/patch.
**Always ask for confirmation before starting implementation** — even if the plan seems obvious.
One question max. Once confirmed, proceed without further interruption.

### IMPLEMENT
- Edit only files necessary. Prefer patch_file over write_file for existing files.
- Follow existing code style, naming, and patterns.
- BATCH all independent writes in one response.
- Use run_task for large independent modules (e.g. separate frontend/backend).
  Always expand ~ first (shell_exec("echo $HOME") if needed), then pass absolute paths to run_task.

### VERIFY
Run tests or compile. Use http_request for API endpoints.
Fix failures before declaring done — do NOT skip this phase.

### REPORT
2–4 bullet points: what changed, why, any known limitations.

## Guardrails
- Do NOT create test files, READMEs, or extra files unless asked.
- Do NOT commit unless explicitly asked.
- Do NOT refactor code unrelated to the task.
- One implementation per function — no variant zoo.
- patch_file failure "old_str not found" → use the file content in the error to fix old_str. Never retry blindly.
- **STOP after the task is done.** Do not speculatively fix adjacent issues the user didn't mention.
  After REPORT, wait for the next user message. Do not continue reading or patching.

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
		tools:    NewToolRegistry(workDir, approver, client, 0),
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
// Sanitizes the history so it never starts mid tool-call sequence.
func (a *Agent) SetMessages(msgs []llm.Message) {
	if len(msgs) == 0 {
		a.messages = msgs
		return
	}
	// Keep system prompt (index 0) then sanitize the rest.
	if msgs[0].Role == "system" && len(msgs) > 1 {
		tail := sanitizeRecent(msgs[1:])
		a.messages = append(msgs[:1:1], tail...)
	} else {
		a.messages = msgs
	}
}

// Stats returns accumulated token usage for this session.
func (a *Agent) Stats() SessionStats { return a.stats }

// Undo reverts the most recent file write or patch.
func (a *Agent) Undo() (string, error) { return a.tools.undo.Pop() }

// UndoLen returns how many undo steps are available.
func (a *Agent) UndoLen() int { return a.tools.undo.Len() }

// SetSearcher attaches any Searcher implementation to the agent.
// Calling it multiple times replaces the previous searcher.
func (a *Agent) SetSearcher(s Searcher) {
	a.tools.SetSearcher(s)
}

// SetRAG is a convenience wrapper: creates a VecSearcher and attaches it.
func (a *Agent) SetRAG(index *VecIndex, embedModel string) {
	a.tools.SetRAG(index, a.client, embedModel)
}

// SetBM25 is a convenience wrapper: attaches a BM25Index as the searcher.
func (a *Agent) SetBM25(idx *BM25Index) {
	a.tools.SetBM25(idx)
}

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
			if isContextLengthError(err) && len(a.messages) > keepRecentMessages+3 {
				fmt.Fprintf(a.out, "\033[33m[context limit hit — compressing and retrying]\033[0m\n")
				if cerr := a.forceCompress(ctx); cerr != nil {
					return fmt.Errorf("LLM error: %w (compression also failed: %v)", err, cerr)
				}
				response, toolCalls, err = a.callLLM(ctx)
			}
			if err != nil {
				return fmt.Errorf("LLM error: %w", err)
			}
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

		// Count sub-agent tasks so we can show a banner before they start.
		subAgentCount := 0
		for _, tc := range toolCalls {
			if tc.Function.Name == "run_task" {
				subAgentCount++
			}
		}
		if subAgentCount > 1 {
			fmt.Fprintf(a.out, "\n  \033[2m⟳ delegating to %d sub-agents in parallel\033[0m\n", subAgentCount)
		} else if subAgentCount == 1 {
			fmt.Fprintf(a.out, "\n  \033[2m⟳ delegating to sub-agent\033[0m\n")
		}

		var pendingInstruction string

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
					ch <- indexedResult{i, a.tools.Execute(ctx, tc.Function.Name, tc.Function.Arguments)}
				}()
			}
			for range toolCalls {
				r := <-ch
				results[r.i] = tcResult{tc: toolCalls[r.i], result: r.result}
			}
		} else {
			for i, tc := range toolCalls {
				results[i] = tcResult{tc: tc, result: a.tools.Execute(ctx, tc.Function.Name, tc.Function.Arguments)}
				if results[i].result.Instruction != "" {
					pendingInstruction = results[i].result.Instruction
					// Cancel remaining tool calls so the message sequence stays valid.
					for j := i + 1; j < len(toolCalls); j++ {
						results[j] = tcResult{
							tc:     toolCalls[j],
							result: ToolResult{Content: "cancelled: user provided new instructions"},
						}
					}
					break
				}
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

		// If the user redirected via "Other instructions", inject as a new user turn.
		if pendingInstruction != "" {
			a.messages = append(a.messages, llm.Message{
				Role:    "user",
				Content: pendingInstruction,
			})
			continue
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
	case "run_task":
		if t, ok := args["task"].(string); ok {
			r := []rune(t)
			if len(r) > 60 {
				return string(r[:60]) + "…"
			}
			return t
		}
	case "semantic_search":
		return pick("query")
	case "web_fetch":
		return pick("url")
	case "file_outline":
		return pick("path")
	case "git_branch":
		action := pick("action")
		if action == "" {
			action = "list"
		}
		if name := pick("name"); name != "" {
			return action + " " + name
		}
		return action
	case "git_pull":
		remote := pick("remote")
		if remote == "" {
			remote = "origin"
		}
		if b := pick("branch"); b != "" {
			return remote + "/" + b
		}
		return remote
	case "git_push":
		remote := pick("remote")
		if remote == "" {
			remote = "origin"
		}
		if b := pick("branch"); b != "" {
			return remote + "/" + b
		}
		return remote
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

