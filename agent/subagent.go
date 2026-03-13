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

STEP 0 — THINK before writing anything.
  Plan the complete file list and all imports/dependencies in your head.
  For Go: list every import each file needs. For TS/Vue: list every dependency in package.json.
  Do NOT start writing until the plan is complete. One correct write beats three broken ones.

STEP 1 — Write ALL files in a SINGLE response.
  Issue every write_file call at once. Do NOT write one file, wait, then write the next.
  write_file automatically creates parent directories — DO NOT run mkdir (it is silently ignored anyway).
  Each file must be complete and correct on the first write. No placeholder imports, no TODO stubs.

STEP 2 — Run build/install commands.
  After all files are written: npm install, go mod tidy, go build, etc.
  ALWAYS use absolute paths in shell commands. Shell state does not persist between calls.
  Correct:   shell_exec("cd /abs/path && go build ./...")
  Wrong:     shell_exec("cd backend") then shell_exec("go build ./...")

STEP 3 — Fix errors if any, then stop.
  Read the exact error. Patch only the broken lines. Re-run. Do not rewrite the whole file.

## ABSOLUTE RULES:

- ALWAYS use ABSOLUTE paths in write_file and shell_exec. Never relative paths or ~/paths.
- mkdir is silently ignored — do not waste a step on it. write_file creates all parent directories.
- NEVER run: ls, find, echo, pwd, or any exploratory/diagnostic command before writing.
  You know the file list from the task — write it directly.
- NEVER use interactive CLIs: npm create, yarn create, vite, create-react-app, cargo init, etc.
  They require stdin and hang forever. Write project files directly instead.
- NEVER create README, test files, or extra files unless explicitly requested.`

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
