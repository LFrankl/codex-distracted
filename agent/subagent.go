package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"codex/llm"
)

const maxSubAgentSteps = 25

// subAgentPrompt is a lean, directive prompt for sub-agents.
// Sub-agents have one job: write a self-contained set of files and run build commands.
// They must NOT explore, must NOT use mkdir, must use absolute paths everywhere.
const subAgentPrompt = `You are a sub-agent. Complete the given task by writing files and running build commands.

Working directory: %s

Available tools: read_file, write_file, patch_file, shell_exec, find_files, grep_files, http_request.
(run_task is NOT available — you cannot spawn further sub-agents.)

## MANDATORY EXECUTION PATTERN — follow this exactly:

STEP 1 — Write ALL files in a SINGLE response.
  Issue every write_file call at once. Do NOT write one file, wait, then write the next.
  write_file automatically creates parent directories. NEVER call mkdir or shell_exec for directory creation.

STEP 2 — Run build/install commands.
  After all files are written, run: npm install, go mod tidy, go build, etc.
  Use ABSOLUTE paths in every shell command. NEVER use "cd dir && cmd" — it does not persist.
  Correct:   shell_exec("cd /abs/path && go build ./...")
  Wrong:     shell_exec("cd backend") then shell_exec("go build ./...")

STEP 3 — Fix errors if any, then stop.

## ABSOLUTE RULES:

- ALWAYS use ABSOLUTE paths in write_file and shell_exec. Never use relative paths.
  If the task says "~/foo", expand it to the full path (e.g. /Users/username/foo).
- write_file creates parent dirs automatically. NEVER run mkdir.
- NEVER run: ls, find, echo $HOME, pwd, or any exploratory command.
  You already know what files to create from the task description — just create them.
- NEVER use interactive CLIs: npm create, yarn create, vite, create-react-app, cargo init, etc.
  They require stdin and hang forever. Write project files directly instead.
- NEVER create README, test files, or extra files unless explicitly requested.
- If a build command fails, read the error, patch the specific file, re-run. Do not re-create everything.`

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
		prompt:   subAgentPrompt,
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
