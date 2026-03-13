package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex/llm"
)

const systemPrompt = `You are Codex, a concise coding assistant. Do exactly what was asked — nothing more.

Rules:
- Only use tools when necessary. Do NOT explore files speculatively.
- Only read a file if you need its exact content to complete the task.
- Do NOT run tests, builds, or shell commands unless the user explicitly asks.
- Do NOT commit, stage, or push git changes unless explicitly asked.
- Do NOT add extra features, comments, or refactors beyond the request.
- Answer questions directly without running tools first.
- When editing: read the relevant section → patch → done.
- When creating: write the file → done.

Working directory: %s`

const thoroughPrompt = `You are Codex, a thorough coding assistant. Work carefully and completely.

You have access to tools for reading/writing files and executing shell commands. Use them to:
- Explore the codebase before making changes
- Verify your changes by running tests or builds
- Create complete, working implementations

Guidelines:
- Read existing files before editing them
- After writing code, run tests or build to verify it works
- Be thorough in implementation

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
}

func New(client *llm.Client, workDir string, maxSteps int, out io.Writer, approver Approver, thorough bool) *Agent {
	a := &Agent{
		client:   client,
		tools:    NewToolRegistry(workDir, approver),
		maxSteps: maxSteps,
		out:      out,
	}
	if thorough {
		a.prompt = thoroughPrompt
	} else {
		a.prompt = systemPrompt
	}
	return a
}

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

		// Add assistant message to history
		assistantMsg := llm.Message{Role: "assistant"}
		if response != "" {
			assistantMsg.Content = response
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = toolCalls
		}
		a.messages = append(a.messages, assistantMsg)

		// No tool calls = done
		if len(toolCalls) == 0 {
			break
		}

		// Execute tool calls
		for _, tc := range toolCalls {
			a.printToolCall(tc)
			result := a.tools.Execute(tc.Function.Name, tc.Function.Arguments)
			a.printToolResult(tc.Function.Name, result)

			a.messages = append(a.messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result.Content,
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

