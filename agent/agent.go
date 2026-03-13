package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codex/llm"
)

const systemPrompt = `You are Codex, an AI coding assistant. You help users write, edit, understand, and debug code.

You have access to tools for reading/writing files and executing shell commands. Use them proactively to:
- Explore the codebase before making changes
- Verify your changes by running tests or builds
- Create complete, working implementations

Guidelines:
- Always read existing files before editing them
- After writing code, run tests or build to verify it works
- Be concise in explanations but thorough in implementation
- When asked to build a project, create all necessary files

Current working directory: %s`

// Agent orchestrates the LLM + tool loop
type Agent struct {
	client   *llm.Client
	tools    *ToolRegistry
	messages []llm.Message
	maxSteps int
	out      io.Writer
	stats    SessionStats
}

func New(client *llm.Client, workDir string, maxSteps int, out io.Writer, approver Approver) *Agent {
	return &Agent{
		client:   client,
		tools:    NewToolRegistry(workDir, approver),
		maxSteps: maxSteps,
		out:      out,
	}
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
		prompt := fmt.Sprintf(systemPrompt, workDir)

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
				fmt.Fprint(a.out, "\n\033[1;36mAssistant:\033[0m ")
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
		fmt.Fprintf(a.out, "\033[2m  [%s | %s]\033[0m\n", turn.String(), a.stats.String())
	}

	return contentBuilder.String(), finalToolCalls, nil
}

func (a *Agent) printToolCall(tc llm.ToolCall) {
	fmt.Fprintf(a.out, "\n\033[1;33m▶ Tool:\033[0m %s", tc.Function.Name)

	// Pretty print args
	var args map[string]any
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) == nil {
		// Show key args inline
		switch tc.Function.Name {
		case "read_file", "write_file":
			if path, ok := args["path"]; ok {
				fmt.Fprintf(a.out, "(%v)", path)
			}
		case "shell_exec":
			if cmd, ok := args["command"]; ok {
				fmt.Fprintf(a.out, "(%v)", cmd)
			}
		case "grep_files":
			if p, ok := args["pattern"]; ok {
				fmt.Fprintf(a.out, "(%v)", p)
			}
		case "list_files":
			if p, ok := args["path"]; ok {
				fmt.Fprintf(a.out, "(%v)", p)
			}
		}
	}
	fmt.Fprintln(a.out)
}

func (a *Agent) printToolResult(name string, result ToolResult) {
	icon := "\033[1;32m✓\033[0m"
	if result.IsError {
		icon = "\033[1;31m✗\033[0m"
	}

	// Truncate long outputs for display
	content := result.Content
	const maxDisplay = 800
	if len(content) > maxDisplay {
		lines := strings.Split(content, "\n")
		if len(lines) > 20 {
			content = strings.Join(lines[:20], "\n") +
				fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
		}
	}

	fmt.Fprintf(a.out, "%s %s result:\n\033[2m%s\033[0m\n", icon, name, content)
}

