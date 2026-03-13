package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"codex/llm"
)

const maxSubAgentSteps = 25

// SubAgentResult captures what a sub-agent produced.
type SubAgentResult struct {
	Summary      string   // last assistant text response
	FilesChanged []string // paths written or patched
	Err          error
}

// runSubAgent creates a sandboxed, auto-approving agent for an independent task.
// All output is discarded; results are extracted from the message history afterward.
func runSubAgent(ctx context.Context, task, parentCtx string, client *llm.Client, workDir string) SubAgentResult {
	ag := &Agent{
		client:   client,
		tools:    NewToolRegistry(workDir, AutoApprover(), client, 1),
		maxSteps: maxSubAgentSteps,
		out:      io.Discard,
		prompt:   systemPrompt,
	}

	fullTask := task
	if parentCtx != "" {
		fullTask = "Context from parent:\n" + parentCtx + "\n\n---\n\n" + task
	}

	err := ag.Run(ctx, fullTask)
	return SubAgentResult{
		Summary:      lastAssistantText(ag.messages),
		FilesChanged: writtenFiles(ag.messages),
		Err:          err,
	}
}

// lastAssistantText returns the final non-empty text response from the assistant.
func lastAssistantText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			if s, ok := msgs[i].Content.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return "(task completed)"
}

// writtenFiles collects unique file paths from write_file and patch_file tool calls.
func writtenFiles(msgs []llm.Message) []string {
	seen := map[string]bool{}
	var files []string
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "write_file" && tc.Function.Name != "patch_file" {
				continue
			}
			var args map[string]any
			if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
				continue
			}
			if path, ok := args["path"].(string); ok && path != "" && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files
}
