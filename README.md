# Codex

A minimal AI coding agent for the terminal. Built in Go, works with any OpenAI-compatible API — designed for Chinese domestic providers (DeepSeek, Qwen, Zhipu, Moonshot) as well as OpenAI itself.

## Features

- **ReAct agent loop** — thinks, calls tools, observes results, repeats
- **Streaming output** — responses stream token by token, no waiting for full reply
- **Two modes** — minimal (do exactly what's asked) and thorough (explore, plan, verify)
- **10 built-in tools** — file read/write/patch, shell exec, grep, git operations
- **Session persistence** — save and resume conversations across invocations
- **Context compression** — automatically summarizes old history to stay within token limits
- **Undo stack** — revert any file write or patch with `/undo`
- **Project memory** — place a `.codex.md` in your project root; it's injected into every session
- **Approval prompts** — shell commands and patches require confirmation (skip with `-y`)
- **Bordered input box** — clean terminal UI with dimmed history, arrow-key navigation

## Installation

```bash
git clone https://github.com/LFrankl/codex-distracted
cd codex-distracted
go build -o codex .
sudo mv codex /usr/local/bin/
```

Requires Go 1.21+.

## Quick start

```bash
# Set up a provider
codex config set-provider deepseek
codex config set-key deepseek sk-xxxxxxxxxxxxxxxx

# One-shot
codex "write a binary search function in Go"

# Interactive REPL
codex
```

## Supported providers

| Name | Base URL | Default model |
|------|----------|---------------|
| `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` |
| `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-max` |
| `zhipu` | `https://open.bigmodel.cn/api/paas/v4` | `glm-4` |
| `moonshot` | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` |

Any OpenAI-compatible endpoint works — add a custom provider via `codex config`:

```bash
codex config set-provider myprovider
codex config set-key myprovider sk-xxx
codex config set-model myprovider my-model-name
```

Then edit `~/.codex/config.yaml` to set `base_url` for the new provider.

## CLI flags

```
codex [flags] [prompt]

Flags:
  -p, --provider string    Provider to use (overrides config)
  -m, --model string       Model to use (overrides provider default)
  -d, --dir string         Working directory (defaults to current dir)
  -y, --auto-approve       Skip confirmation prompts
  -s, --session string     Resume a saved session by ID
      --save-as string     Auto-save session on exit with this name
      --thorough           Thorough mode: explore, plan, verify changes
```

## REPL commands

| Command | Description |
|---------|-------------|
| `/thorough` | Switch to thorough mode |
| `/default` | Switch back to minimal mode |
| `/mode` | Show current mode |
| `/reset` | Clear conversation history |
| `/undo` | Revert last file write or patch |
| `/save [name]` | Save current session |
| `/load <id>` | Load a saved session |
| `/sessions` | List saved sessions |
| `/help` | Show help |
| `exit` / `Ctrl+D` | Exit (prompts to save if unsaved) |
| `Ctrl+C` twice | Exit immediately |

## Tools

The agent has access to these tools:

| Tool | Description |
|------|-------------|
| `read_file` | Read a file, optionally specifying line range |
| `write_file` | Create or overwrite a file |
| `patch_file` | Replace an exact string or line range in a file (shows diff, requires approval) |
| `list_files` | List directory contents |
| `shell_exec` | Run a shell command (requires approval; trailing `&` runs in background) |
| `grep_files` | Search file contents with a pattern |
| `git_status` | Show working tree status |
| `git_diff` | Show staged or unstaged diff, or diff against a ref |
| `git_log` | Show recent commits |
| `git_commit` | Stage files and commit (shows staged diff, requires approval) |

## Modes

### Minimal (default)

Strict, task-focused. The agent does exactly what was asked — no speculative file exploration, no extra files, no unsolicited tests. If you say "ls", it runs `ls`. If you say "write a fibonacci function", it writes one file, done.

### Thorough (`--thorough` or `/thorough` in REPL)

Structured five-phase workflow:

1. **Understand** — read relevant files, check git history
2. **Plan** — state approach before touching anything
3. **Implement** — edit only necessary files, prefer `patch_file` over full rewrites
4. **Verify** — run tests or compile; fix failures before declaring done
5. **Report** — summarize what changed and why

The `❯` prompt turns purple in thorough mode.

## Context compression

When conversation history grows large (estimated >4000 tokens), older messages are automatically summarized and replaced with a compact summary. The summary preserves:

- Files created or modified and what changed
- Key decisions and their reasons
- Errors encountered and how they were resolved

Recent messages are always kept verbatim. Large shell output is truncated to 2000 characters in history (but displayed in full in the terminal).

## Project memory

Create a `.codex.md` file in your project root. It's automatically loaded and appended to the system prompt at the start of each session:

```markdown
# My Project

- Uses PostgreSQL, not SQLite
- API lives in `internal/api/`, handlers in `internal/handler/`
- Run tests with `make test`
- Never modify `generated/` files by hand
```

The file is only loaded from the exact working directory — not from parent directories.

## Session management

```bash
# Save current session
/save my-feature

# List all sessions
codex session list

# Resume a session
codex --session abc123

# Show session content
codex session show abc123

# Delete a session
codex session delete abc123
```

Sessions are stored as JSON in `~/.codex/sessions/`.

## Configuration file

`~/.codex/config.yaml`:

```yaml
current_provider: deepseek
max_steps: 10

providers:
  deepseek:
    name: deepseek
    base_url: https://api.deepseek.com/v1
    api_key: sk-xxxxxxxxxxxxxxxx
    model: deepseek-chat
  custom:
    name: custom
    base_url: https://my-api.example.com/v1
    api_key: my-key
    model: my-model
```

`work_dir` is intentionally never persisted — it's always resolved from the current directory at runtime.

## Project structure

```
.
├── main.go                 # Entry point
├── cmd/
│   ├── root.go             # Root command, REPL loop, flag definitions
│   ├── config.go           # `codex config` subcommand
│   ├── session.go          # `codex session` subcommand
│   └── liner.go            # Custom line editor (CJK-safe, bordered input box)
├── agent/
│   ├── agent.go            # Main agent loop, LLM streaming, tool dispatch
│   ├── tools.go            # Tool registry: read/write/patch/shell/grep/list
│   ├── tools_git.go        # Git tools: status/diff/log/commit
│   ├── compressor.go       # Context compression and token estimation
│   ├── session.go          # Session save/load/list/delete
│   ├── memory.go           # Project memory (.codex.md) loader
│   ├── approver.go         # Approval callbacks (interactive / auto)
│   ├── prompt.go           # Arrow-key menu for approval prompts
│   ├── prompt_tty.go       # Raw terminal mode helpers
│   ├── spinner.go          # Braille loading spinner
│   ├── diff.go             # Colored unified diff renderer
│   ├── undo.go             # In-memory undo stack (max 20 entries)
│   └── stats.go            # Per-turn and session token stats
├── llm/
│   └── client.go           # OpenAI-compatible streaming SSE client
└── config/
    └── config.go           # Config load/save, provider management
```

## Building for other platforms

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o codex-linux .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o codex-macos-arm64 .

# Windows
GOOS=windows GOARCH=amd64 go build -o codex.exe .
```

## License

MIT
