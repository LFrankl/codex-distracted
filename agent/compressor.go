package agent

import (
	"context"
	"fmt"
	"strings"

	"codex/llm"
)

// compressionThreshold is the estimated token count that triggers compression.
const compressionThreshold = 4000

// keepRecentMessages is how many recent messages to preserve verbatim.
const keepRecentMessages = 4

// maxAssistantRunes is the max length of an assistant text message stored in history.
// Long explanations don't need to be replayed verbatim; a truncated version is fine.
const maxAssistantRunes = 800

// estimateTokens approximates token count across all message types.
func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		switch v := m.Content.(type) {
		case string:
			total += len([]rune(v)) / 3
		}
		// tool call arguments
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments) / 3
		}
		// tool results stored as Name field (role == "tool")
		if m.Role == "tool" {
			if s, ok := m.Content.(string); ok {
				total += len([]rune(s)) / 3
			}
		}
	}
	return total
}

// maybeCompress checks whether the message history is approaching the context limit
// and, if so, summarizes the older portion via an LLM call.
func (a *Agent) maybeCompress(ctx context.Context) error {
	if estimateTokens(a.messages) < compressionThreshold {
		return nil
	}
	// Need at least: system + keepRecent + some old messages to compress
	if len(a.messages) < keepRecentMessages+3 {
		return nil
	}

	cutoff := len(a.messages) - keepRecentMessages
	toCompress := a.messages[1:cutoff] // skip system prompt
	recent := a.messages[cutoff:]

	summary, err := a.summarize(ctx, toCompress)
	if err != nil {
		return err
	}

	compressed := []llm.Message{
		a.messages[0], // keep system prompt
		{Role: "user", Content: "[Earlier conversation summary]\n" + summary},
		{Role: "assistant", Content: "Understood."},
	}
	compressed = append(compressed, recent...)

	fmt.Fprintf(a.out, "\033[2m[context compressed: %d messages → summary]\033[0m\n", len(toCompress))
	a.messages = compressed
	return nil
}

// summarize asks the LLM to produce a concise summary of a message slice.
// It includes tool calls and tool results so file operations are preserved.
func (a *Agent) summarize(ctx context.Context, msgs []llm.Message) (string, error) {
	var sb strings.Builder

	for _, m := range msgs {
		switch m.Role {
		case "user":
			if s, ok := m.Content.(string); ok && s != "" {
				fmt.Fprintf(&sb, "[user]: %s\n\n", s)
			}
		case "assistant":
			if s, ok := m.Content.(string); ok && s != "" {
				fmt.Fprintf(&sb, "[assistant]: %s\n\n", s)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&sb, "[tool_call: %s] %s\n\n", tc.Function.Name, tc.Function.Arguments)
			}
		case "tool":
			if s, ok := m.Content.(string); ok && s != "" {
				// Truncate large tool results in the summarization input too
				runes := []rune(s)
				if len(runes) > 600 {
					s = string(runes[:600]) + "…"
				}
				fmt.Fprintf(&sb, "[tool_result: %s]: %s\n\n", m.Name, s)
			}
		}
	}

	req := []llm.Message{
		{
			Role: "user",
			Content: "Summarize the following conversation concisely. Focus on:\n" +
				"- Files created or modified (list each file and what changed)\n" +
				"- Key decisions and their reasons\n" +
				"- Current state of the codebase\n" +
				"- Any errors encountered and how they were resolved\n" +
				"Use bullet points. Be specific about file names and code changes.\n\n" +
				sb.String(),
		},
	}

	msg, err := a.client.NonStreamChat(ctx, req, nil)
	if err != nil {
		return "", fmt.Errorf("summarize: %w", err)
	}

	if s, ok := msg.Content.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("unexpected summary content type")
}

// trimAssistantContent truncates long assistant text replies before storing in history.
// Tool calls are never trimmed.
func trimAssistantContent(msg llm.Message) llm.Message {
	if msg.Role != "assistant" {
		return msg
	}
	if s, ok := msg.Content.(string); ok {
		runes := []rune(s)
		if len(runes) > maxAssistantRunes {
			msg.Content = string(runes[:maxAssistantRunes]) + "…"
		}
	}
	return msg
}
