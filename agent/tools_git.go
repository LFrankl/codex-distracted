package agent

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"codex/llm"
)

// --- Tool definitions ---

func (r *ToolRegistry) defGitStatus() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "git_status",
			Description: "Show the working tree status (modified, staged, untracked files). Run this before committing.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (r *ToolRegistry) defGitDiff() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "git_diff",
			Description: "Show changes in the working tree or staging area. Use staged=true to see what will be committed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"staged": map[string]any{
						"type":        "boolean",
						"description": "If true, show staged changes (git diff --staged). Default false shows unstaged changes.",
					},
					"files": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Limit diff to these files (optional)",
					},
					"base": map[string]any{
						"type":        "string",
						"description": "Compare against this ref (e.g. HEAD~1, main). Overrides staged flag.",
					},
				},
			},
		},
	}
}

func (r *ToolRegistry) defGitLog() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "git_log",
			Description: "Show recent commit history.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"n": map[string]any{
						"type":        "integer",
						"description": "Number of commits to show (default 10)",
					},
					"file": map[string]any{
						"type":        "string",
						"description": "Limit to commits touching this file (optional)",
					},
				},
			},
		},
	}
}

func (r *ToolRegistry) defGitCommit() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "git_commit",
			Description: `Stage files and create a git commit.

Steps performed:
1. Stage the specified files (or all changes if files is empty)
2. Show a preview of what will be committed
3. Ask for user approval
4. Create the commit

Follow Conventional Commits format: feat/fix/refactor/docs/chore(scope): subject`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "Commit message (Conventional Commits format preferred)",
					},
					"files": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Files to stage. If empty, stages all changes (git add -A).",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

// --- Implementations ---

func (r *ToolRegistry) gitStatus(_ string) ToolResult {
	out, err := r.gitRun("status")
	if err != nil {
		return ToolResult{Content: out, IsError: true}
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "nothing to commit, working tree clean"}
	}
	return ToolResult{Content: out}
}

func (r *ToolRegistry) gitDiff(argsJSON string) ToolResult {
	var args struct {
		Staged bool     `json:"staged"`
		Files  []string `json:"files"`
		Base   string   `json:"base"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}

	gitArgs := []string{"diff"}
	if args.Base != "" {
		gitArgs = append(gitArgs, args.Base)
	} else if args.Staged {
		gitArgs = append(gitArgs, "--staged")
	}
	gitArgs = append(gitArgs, "--stat", "--patch")
	if len(args.Files) > 0 {
		gitArgs = append(gitArgs, "--")
		gitArgs = append(gitArgs, args.Files...)
	}

	out, err := r.gitRun(gitArgs...)
	if err != nil {
		return ToolResult{Content: out, IsError: true}
	}
	if strings.TrimSpace(out) == "" {
		label := "unstaged"
		if args.Staged {
			label = "staged"
		}
		return ToolResult{Content: fmt.Sprintf("No %s changes", label)}
	}
	return ToolResult{Content: out}
}

func (r *ToolRegistry) gitLog(argsJSON string) ToolResult {
	var args struct {
		N    int    `json:"n"`
		File string `json:"file"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.N <= 0 {
		args.N = 10
	}

	gitArgs := []string{"log", fmt.Sprintf("-n%d", args.N),
		"--oneline", "--decorate", "--graph"}
	if args.File != "" {
		gitArgs = append(gitArgs, "--", args.File)
	}

	out, err := r.gitRun(gitArgs...)
	if err != nil {
		return ToolResult{Content: out, IsError: true}
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "No commits yet"}
	}
	return ToolResult{Content: out}
}

func (r *ToolRegistry) gitCommit(argsJSON string) ToolResult {
	var args struct {
		Message string   `json:"message"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Message == "" {
		return ToolResult{Content: "commit message is required", IsError: true}
	}

	// Stage files
	if len(args.Files) > 0 {
		addArgs := append([]string{"add", "--"}, args.Files...)
		if out, err := r.gitRun(addArgs...); err != nil {
			return ToolResult{Content: fmt.Sprintf("git add failed: %s", out), IsError: true}
		}
	} else {
		if out, err := r.gitRun("add", "-A"); err != nil {
			return ToolResult{Content: fmt.Sprintf("git add -A failed: %s", out), IsError: true}
		}
	}

	// Show staged diff summary for approval
	staged, _ := r.gitRun("diff", "--staged", "--stat")
	if strings.TrimSpace(staged) == "" {
		return ToolResult{Content: "nothing staged to commit", IsError: true}
	}

	fmt.Printf("\n\033[2m%s\033[0m\n\033[1;33mMessage:\033[0m %s\n", staged, args.Message)

	if ok, instr := r.approver("git commit", args.Message); !ok {
		r.gitRun("reset", "HEAD")
		if instr != "" {
			return ToolResult{Instruction: instr}
		}
		return ToolResult{Content: "git_commit cancelled by user (changes unstaged)", IsError: true}
	}

	out, err := r.gitRun("commit", "-m", args.Message)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("commit failed: %s", out), IsError: true}
	}

	// Show short summary (first 3 lines)
	lines := strings.SplitN(out, "\n", 4)
	summary := strings.Join(lines[:min(3, len(lines))], "\n")
	return ToolResult{Content: summary}
}

func (r *ToolRegistry) defGitBranch() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "git_branch",
			Description: `List, create, or switch git branches.
- action "list" (default): show all local branches with current branch marked.
- action "create": create a new branch from an optional base ref, then check it out.
- action "checkout": switch to an existing branch.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"list", "create", "checkout"},
						"description": `"list" (default), "create", or "checkout"`,
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Branch name (required for create/checkout)",
					},
					"base": map[string]any{
						"type":        "string",
						"description": "Base ref for create (default HEAD). E.g. main, HEAD~3.",
					},
				},
			},
		},
	}
}

func (r *ToolRegistry) defGitPull() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "git_pull",
			Description: "Fetch and merge (or rebase) remote changes into the current branch.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"remote": map[string]any{
						"type":        "string",
						"description": "Remote name (default: origin)",
					},
					"branch": map[string]any{
						"type":        "string",
						"description": "Remote branch to pull (default: tracking branch)",
					},
					"rebase": map[string]any{
						"type":        "boolean",
						"description": "Use --rebase instead of merge (default false)",
					},
				},
			},
		},
	}
}

func (r *ToolRegistry) defGitPush() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "git_push",
			Description: "Push local commits to a remote. Always asks for user confirmation. Force push asks twice.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"remote": map[string]any{
						"type":        "string",
						"description": "Remote name (default: origin)",
					},
					"branch": map[string]any{
						"type":        "string",
						"description": "Branch to push (default: current branch)",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Force push (--force-with-lease). Requires explicit user approval.",
					},
					"set_upstream": map[string]any{
						"type":        "boolean",
						"description": "Set upstream tracking (-u). Use on first push of a new branch.",
					},
				},
			},
		},
	}
}

// --- git_branch / git_pull / git_push implementations ---

func (r *ToolRegistry) gitBranch(argsJSON string) ToolResult {
	var args struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Base   string `json:"base"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Action == "" {
		args.Action = "list"
	}

	switch args.Action {
	case "list":
		out, err := r.gitRun("branch", "-v")
		if err != nil {
			return ToolResult{Content: out, IsError: true}
		}
		if strings.TrimSpace(out) == "" {
			return ToolResult{Content: "no branches yet"}
		}
		return ToolResult{Content: out}

	case "create":
		if args.Name == "" {
			return ToolResult{Content: "name is required for create", IsError: true}
		}
		gitArgs := []string{"checkout", "-b", args.Name}
		if args.Base != "" {
			gitArgs = append(gitArgs, args.Base)
		}
		out, err := r.gitRun(gitArgs...)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("create branch failed: %s", out), IsError: true}
		}
		return ToolResult{Content: fmt.Sprintf("created and switched to branch '%s'\n%s", args.Name, out)}

	case "checkout":
		if args.Name == "" {
			return ToolResult{Content: "name is required for checkout", IsError: true}
		}
		out, err := r.gitRun("checkout", args.Name)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("checkout failed: %s", out), IsError: true}
		}
		return ToolResult{Content: out}

	default:
		return ToolResult{Content: fmt.Sprintf("unknown action: %s (use list/create/checkout)", args.Action), IsError: true}
	}
}

func (r *ToolRegistry) gitPull(argsJSON string) ToolResult {
	var args struct {
		Remote string `json:"remote"`
		Branch string `json:"branch"`
		Rebase bool   `json:"rebase"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Remote == "" {
		args.Remote = "origin"
	}

	if ok, instr := r.approver("git pull", fmt.Sprintf("%s %s", args.Remote, args.Branch)); !ok {
		if instr != "" {
			return ToolResult{Instruction: instr}
		}
		return ToolResult{Content: "git_pull cancelled by user", IsError: true}
	}

	gitArgs := []string{"pull"}
	if args.Rebase {
		gitArgs = append(gitArgs, "--rebase")
	}
	gitArgs = append(gitArgs, args.Remote)
	if args.Branch != "" {
		gitArgs = append(gitArgs, args.Branch)
	}

	out, err := r.gitRun(gitArgs...)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("pull failed: %s", out), IsError: true}
	}
	return ToolResult{Content: out}
}

func (r *ToolRegistry) gitPush(argsJSON string) ToolResult {
	var args struct {
		Remote      string `json:"remote"`
		Branch      string `json:"branch"`
		Force       bool   `json:"force"`
		SetUpstream bool   `json:"set_upstream"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Remote == "" {
		args.Remote = "origin"
	}

	desc := fmt.Sprintf("%s %s", args.Remote, args.Branch)
	if args.Force {
		desc += " (FORCE)"
	}
	if ok, instr := r.approver("git push", desc); !ok {
		if instr != "" {
			return ToolResult{Instruction: instr}
		}
		return ToolResult{Content: "git_push cancelled by user", IsError: true}
	}

	gitArgs := []string{"push"}
	if args.Force {
		gitArgs = append(gitArgs, "--force-with-lease")
	}
	if args.SetUpstream {
		gitArgs = append(gitArgs, "-u")
	}
	gitArgs = append(gitArgs, args.Remote)
	if args.Branch != "" {
		gitArgs = append(gitArgs, args.Branch)
	}

	out, err := r.gitRun(gitArgs...)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("push failed: %s", out), IsError: true}
	}
	return ToolResult{Content: out}
}

// --- helpers ---

func (r *ToolRegistry) gitRun(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
