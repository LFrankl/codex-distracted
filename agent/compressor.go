package agent

import (
	"context"
	"fmt"
	"strings"

	"codex/llm"
)

// compressionThreshold is the estimated token count that triggers compression.
// Most providers support 8k–32k; we compress at 12k to leave headroom.
const compressionThreshold = 12000

// keepRecentMessages is how many recent messages to preserve verbatim.
const keepRecentMessages = 6

// estimateTokens approximates token count: ~3 chars per token for Chinese+English mix.
func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		switch v := m.Content.(type) {
		case string:
			total += len(v) / 3
		}
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments) / 3
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
		{Role: "assistant", Content: "Understood, I have context from the earlier conversation."},
	}
	compressed = append(compressed, recent...)

	fmt.Fprintf(a.out, "\033[2m[context compressed: %d messages → summary]\033[0m\n", len(toCompress))
	a.messages = compressed
	return nil
}

// summarize asks the LLM to produce a concise summary of a message slice.
func (a *Agent) summarize(ctx context.Context, msgs []llm.Message) (string, error) {
	var sb strings.Builder
	for _, m := range msgs {
		role := m.Role
		content := ""
		switch v := m.Content.(type) {
		case string:
			content = v
		}
		if content == "" {
			continue
		}
		fmt.Fprintf(&sb, "[%s]: %s\n\n", role, content)
	}

	req := []llm.Message{
		{
			Role: "user",
			Content: "Summarize the following conversation concisely, preserving key decisions, " +
				"files created/modified, and important context. Use bullet points.\n\n" + sb.String(),
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
