package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex/llm"
)

func (r *ToolRegistry) defRunTask() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "run_task",
			Description: `Spawn an independent sub-agent to complete a self-contained task.

Multiple run_task calls in a SINGLE response run in PARALLEL — use this for
genuinely independent work (e.g. separate frontend/backend/test directories).

Rules:
- Include ALL necessary context in 'task'; the sub-agent has no conversation history.
- Sub-agents auto-approve all file operations (no user prompts).
- Sub-agents cannot spawn further agents.
- Do NOT use for tasks that depend on each other's output.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "Complete, self-contained task. Include file paths, tech stack, and all requirements.",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Brief shared context: architecture decisions, naming conventions, port numbers, etc.",
					},
				},
				"required": []string{"task"},
			},
		},
	}
}

func (r *ToolRegistry) runTask(ctx context.Context, argsJSON string) ToolResult {
	if r.depth > 0 {
		return ToolResult{Content: "sub-agents cannot spawn further agents", IsError: true}
	}

	var args struct {
		Task    string `json:"task"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Task == "" {
		return ToolResult{Content: "task is required", IsError: true}
	}

	result := runSubAgent(ctx, args.Task, args.Context, r.client, r.workDir)

	if result.Err != nil {
		if result.Err == context.Canceled {
			return ToolResult{Content: "sub-agent cancelled", IsError: true}
		}
		msg := fmt.Sprintf("sub-agent error: %v", result.Err)
		if result.Summary != "" && result.Summary != "(task completed)" {
			msg += "\n\nPartial output:\n" + result.Summary
		}
		return ToolResult{Content: msg, IsError: true}
	}

	var sb strings.Builder
	if len(result.FilesChanged) > 0 {
		fmt.Fprintf(&sb, "Files: %s\n\n", strings.Join(result.FilesChanged, ", "))
	}
	sb.WriteString(result.Summary)
	return ToolResult{Content: sb.String()}
}

