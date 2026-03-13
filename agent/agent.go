package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex/llm"
)

// corePrompt is the lean, always-active system prompt.
// Tool-specific rules live in tool descriptions; task-specific rules are injected dynamically.
const corePrompt = `You are distracted-codex, a coding assistant. Do exactly what was asked — nothing more.

Working directory: %s

ABSOLUTE RULES (never violate):
1. NEVER commit, stage, or push unless explicitly asked.
2. NEVER create files not requested — no tests, no README, no examples.
3. Answer factual questions directly — no tools needed.
4. If the user's message IS a shell command (ls, go build, npm install…), run it immediately with shell_exec.
5. STOP the moment the request is satisfied. Do not continue, speculate, or narrate next steps.`

// debugContext is prepended to the user message when a debug/fix intent is detected.
// Appears right before the message → highest LLM attention.
const debugContext = `[DEBUG PROTOCOL — injected because your message looks like a bug/error report]

Before any tool call:
• No error text? Ask ONE question: "Can you paste the exact error / stack trace?"
• Error already has file + line? Go there directly. Do NOT run build/test/serve commands
  to "gather more info" — you already have it. Never reproduce an error the user already showed you.

Hypothesis-first discipline (CRITICAL):
• Before EVERY tool call, state in one sentence what you expect to find and why.
  If you cannot state a reason, do not make the call.
• After each tool call, explicitly update: "Found X → hypothesis confirmed/revised/eliminated."
• This prevents aimless exploration and makes each step accountable.

3-step diagnostic budget:
• You get at most 3 diagnostic tool calls (reads + greps) before you must either fix or escalate.
• Step 1: targeted read/grep at the most likely location.
• Step 2: if step 1 was inconclusive, ONE more targeted search — narrower and more precise.
• Step 3: if still stuck, STOP. Do not try a 4th approach. Escalate to the user (see below).
• When stuck, go DEEPER (more precise search), never BROADER (build system, config files,
  unrelated modules). Broadening scope is the wrong response to being stuck.

Escalate instead of looping:
• After the budget is spent without a clear root cause, tell the user:
  "I've checked [A, B, C]. Still uncertain about [X]. Can you [specific ask]?"
• Secondary files (config, build system, type definitions) only when the error explicitly
  implicates them — syntax/runtime errors are almost never config problems.

Structural / consistency errors (mismatched tokens, undefined symbol, type mismatch, import not found…):
• The error line is where the compiler gave up — the actual mistake is usually before it.
• Search the ENTIRE relevant scope for ALL instances of the relevant construct.
  Wrong: grep only for the specific symbol named in the error.
  Right: grep for the whole class (all declarations, all opening tokens, all usages).
• Count opens vs closes, declarations vs usages — find the imbalance systematically.

Pause after diagnosis:
• State "Root cause: X. Fix: Y. Proceed?" and WAIT for confirmation before patching.
• Exception: single-line trivial fix → just do it.

Fix & verify:
• Minimal patch only — do not refactor surrounding code.
• Re-run the failing command once to confirm. If still failing, revise hypothesis — do not retry
  the same fix with minor variations.`

// planContext is prepended to the user message when a feature/change intent is detected.
const planContext = `[IMPLEMENT PROTOCOL — injected because your message looks like a feature/change request]

Before starting:
• Ambiguous requirement? Ask ONE clarifying question first (e.g. "Should this field be optional?").
• List ALL files that need changing before reading any of them.

Read efficiently (batch reads):
• Read ALL affected files in ONE response. Never read one, wait, then decide to read another.
• Files >80 lines: file_outline → read only the relevant section.
• grep_files to locate symbols; semantic_search for conceptual exploration.

Implement efficiently (batch writes):
• Patch ALL files in ONE response. Pattern read-A → patch-A → read-B → patch-B is FORBIDDEN.
• Same file, multiple edits: use patch_file "patches": [{"old_str":…,"new_str":…}, …] array.
• Independent modules (separate frontend/backend dirs): use run_task — expand ~ to absolute path first.
• Write exactly ONE implementation — no variant zoo.

Verify: build / run tests. Fix failures before declaring done.
Report: 2–4 bullets on what changed and why. Then stop.`

// thoroughAddendum is appended on top of the relevant context block in thorough mode.
// It adds a structured workflow and requires explicit plan confirmation.
const thoroughAddendum = `
[THOROUGH MODE — additional constraints]
Workflow: UNDERSTAND → PLAN (confirm with user) → IMPLEMENT → VERIFY → REPORT
• UNDERSTAND: form a clear hypothesis; batch all reads; use file_outline for large files.
• PLAN: always present the plan and ask "Proceed?" before writing a single line of code.
• VERIFY: never skip — run build/tests and fix any failure before reporting.
• REPORT: 2–4 bullets, then stop. Do not continue after reporting.`

// Agent orchestrates the LLM + tool loop
type Agent struct {
	client       *llm.Client
	tools        *ToolRegistry
	messages     []llm.Message
	maxSteps     int
	out          io.Writer
	stats        SessionStats
	thorough     bool
	customPrompt string // non-empty overrides corePrompt (used by sub-agents)
}

func New(client *llm.Client, workDir string, maxSteps int, out io.Writer, approver Approver, thorough bool) *Agent {
	return &Agent{
		client:   client,
		tools:    NewToolRegistry(workDir, approver, client, 0),
		maxSteps: maxSteps,
		out:      out,
		thorough: thorough,
	}
}

// SetThorough switches the agent between default and thorough mode.
// The system prompt is always corePrompt; thorough only affects per-message context injection.
func (a *Agent) SetThorough(on bool) {
	a.thorough = on
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

// detectIntent classifies a user message as "debug", "plan", or "" (general).
// Used to decide which context block to inject before the message.
func detectIntent(msg string) string {
	lower := strings.ToLower(msg)
	score := func(keywords []string) int {
		n := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				n++
			}
		}
		return n
	}
	debugKW := []string{
		"error", "bug", "crash", "fail", "broken", "fix", "debug", "issue", "wrong",
		"exception", "panic", "not work", "doesn't work", "栈", "报错", "错误", "崩溃",
		"失败", "不行", "出错", "问题", "修复", "排查", "修",
	}
	planKW := []string{
		"add", "create", "write", "implement", "build", "make", "new", "feature",
		"develop", "refactor", "improve", "update", "change", "modify", "support",
		"新增", "添加", "创建", "实现", "开发", "功能", "写", "做", "支持",
		"重构", "优化", "更新", "修改", "改",
	}
	ds, ps := score(debugKW), score(planKW)
	if ds > 0 && ds >= ps {
		return "debug"
	}
	if ps > 0 {
		return "plan"
	}
	return ""
}

// injectContext prepends the appropriate protocol block to the user message.
// The block appears immediately before the message for maximum LLM attention.
func (a *Agent) injectContext(userMsg string) string {
	var block string
	if a.thorough {
		// Thorough mode always injects both debug and plan context, plus the addendum.
		block = debugContext + "\n\n" + planContext + thoroughAddendum
	} else {
		switch detectIntent(userMsg) {
		case "debug":
			block = debugContext
		case "plan":
			block = planContext
		}
	}
	if block == "" {
		return userMsg
	}
	return block + "\n\n---\n\n" + userMsg
}

// Run processes a user message through the agent loop
func (a *Agent) Run(ctx context.Context, userMsg string) error {
	a.tools.ResetRunState() // reset per-message diagnostic tracking

	// Initialize system prompt on first message
	if len(a.messages) == 0 {
		workDir := a.tools.workDir
		base := corePrompt
		if a.customPrompt != "" {
			base = a.customPrompt
		}
		prompt := fmt.Sprintf(base, workDir)

		if mem, memPath := loadProjectMemory(workDir); mem != "" {
			prompt += "\n\n## Project Memory (.codex.md)\n" + mem
			printMemoryLoaded(a.out, memPath, mem)
		}

		a.messages = append(a.messages, llm.Message{
			Role:    "system",
			Content: prompt,
		})
	}

	content := userMsg
	if a.customPrompt == "" { // sub-agents have their own structured prompt, skip injection
		content = a.injectContext(userMsg)
	}
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: content,
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

		// Tool classification:
		//   parallel  — no approval needed (read_file, write_file, find_files, grep_files, …)
		//   patchOnly — patch_file only; supports batch approval + concurrent execution
		//   serial    — needs individual approval, one at a time (shell_exec, git ops, move, delete)
		classifyTool := func(name string) string {
			switch name {
			case "shell_exec", "git_commit", "git_pull", "git_push", "move_file", "delete_file":
				return "serial"
			case "patch_file":
				return "patchOnly"
			default:
				return "parallel"
			}
		}

		classes := make([]string, len(toolCalls))
		hasPatch, hasSerial, hasParallel := false, false, false
		for i, tc := range toolCalls {
			classes[i] = classifyTool(tc.Function.Name)
			switch classes[i] {
			case "serial":
				hasSerial = true
			case "patchOnly":
				hasPatch = true
			default:
				hasParallel = true
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

		runParallel := func(indices []int) {
			type indexedResult struct {
				i      int
				result ToolResult
			}
			ch := make(chan indexedResult, len(indices))
			for _, i := range indices {
				i, tc := i, toolCalls[i]
				go func() {
					ch <- indexedResult{i, a.tools.Execute(ctx, tc.Function.Name, tc.Function.Arguments)}
				}()
			}
			for range indices {
				r := <-ch
				results[r.i] = tcResult{tc: toolCalls[r.i], result: r.result}
			}
		}

		// Case 1: only parallel tools (read_file, write_file, find_files, …) — all concurrent.
		// Case 2: only patch_file calls — batch approval then concurrent.
		// Case 3: mixed or serial present — run everything serially preserving order.
		switch {
		case !hasSerial && !hasPatch:
			// All parallel tools.
			if len(toolCalls) > 1 {
				indices := make([]int, len(toolCalls))
				for i := range indices {
					indices[i] = i
				}
				runParallel(indices)
			} else {
				results[0] = tcResult{tc: toolCalls[0], result: a.tools.Execute(ctx, toolCalls[0].Function.Name, toolCalls[0].Function.Arguments)}
			}

		case !hasSerial && hasPatch && !hasParallel && len(toolCalls) > 1:
			// All patch_file — batch approval then concurrent execution.
			fmt.Printf("\n\033[1;33mApply %d patches?\033[0m\n", len(toolCalls))
			if ok, instr := a.tools.approver(fmt.Sprintf("Apply %d patches", len(toolCalls)), ""); !ok {
				if instr != "" {
					pendingInstruction = instr
					for i, tc := range toolCalls {
						results[i] = tcResult{tc: tc, result: ToolResult{Content: "cancelled: user provided new instructions"}}
					}
				} else {
					for i, tc := range toolCalls {
						results[i] = tcResult{tc: tc, result: ToolResult{Content: "patch_file cancelled by user", IsError: true}}
					}
				}
				break
			}
			a.tools.preApproved = true
			indices := make([]int, len(toolCalls))
			for i := range indices {
				indices[i] = i
			}
			runParallel(indices)
			a.tools.preApproved = false

		case !hasSerial && hasPatch && hasParallel:
			// Mixed parallel + patch: run parallel group first, then patches with batch approval.
			var parallelIdx, patchIdx []int
			for i, c := range classes {
				if c == "parallel" {
					parallelIdx = append(parallelIdx, i)
				} else {
					patchIdx = append(patchIdx, i)
				}
			}
			if len(parallelIdx) > 0 {
				runParallel(parallelIdx)
			}
			// Patches: batch approval if >1, else normal execution.
			if len(patchIdx) > 1 {
				fmt.Printf("\n\033[1;33mApply %d patches?\033[0m\n", len(patchIdx))
				if ok, instr := a.tools.approver(fmt.Sprintf("Apply %d patches", len(patchIdx)), ""); ok {
					a.tools.preApproved = true
					runParallel(patchIdx)
					a.tools.preApproved = false
				} else if instr != "" {
					pendingInstruction = instr
					for _, i := range patchIdx {
						results[i] = tcResult{tc: toolCalls[i], result: ToolResult{Content: "cancelled: user provided new instructions"}}
					}
				} else {
					for _, i := range patchIdx {
						results[i] = tcResult{tc: toolCalls[i], result: ToolResult{Content: "patch_file cancelled by user", IsError: true}}
					}
				}
			} else if len(patchIdx) == 1 {
				i := patchIdx[0]
				results[i] = tcResult{tc: toolCalls[i], result: a.tools.Execute(ctx, toolCalls[i].Function.Name, toolCalls[i].Function.Arguments)}
				if results[i].result.Instruction != "" {
					pendingInstruction = results[i].result.Instruction
				}
			}

		default:
			// Contains serial tools — run everything serially.
			for i, tc := range toolCalls {
				results[i] = tcResult{tc: tc, result: a.tools.Execute(ctx, tc.Function.Name, tc.Function.Arguments)}
				if results[i].result.Instruction != "" {
					pendingInstruction = results[i].result.Instruction
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

