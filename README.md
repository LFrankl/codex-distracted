# distracted-codex

一个极简的 AI 编程助手命令行工具，用 Go 编写，支持任何 OpenAI 兼容的 API——专为国产 AI 提供商（DeepSeek、Qwen、智谱、Moonshot）设计，同时也支持 OpenAI。

## 功能特性

- **ReAct 智能循环** — 思考、调用工具、观察结果，循环执行
- **流式输出** — 响应逐 token 流式返回，无需等待完整结果
- **两种工作模式** — 极简模式（严格按要求执行）和深度模式（探索、规划、验证）
- **16 个内置工具** — 文件读写/修改、Shell 命令、代码搜索、Git 操作、HTTP 测试、并行子任务
- **向量索引 / RAG** — 对代码库建立本地语义索引，支持自然语言搜索代码
- **子 Agent 并行执行** — 将独立任务分发给多个子 agent 同时运行
- **会话持久化** — 保存并跨次恢复对话
- **上下文压缩** — 自动摘要旧历史，保持在 token 限制内
- **撤销栈** — 使用 `/undo` 撤销任意文件写入或修改
- **项目记忆** — 在项目根目录放置 `.codex.md`，每次会话自动注入
- **智能操作确认** — 只读命令（`ls`、`git status` 等）自动通过，写操作才需确认
- **带框输入框** — 简洁的终端 UI，历史记录灰显，支持方向键导航

## 安装

```bash
git clone https://github.com/LFrankl/codex-distracted
cd codex-distracted
go build -o distracted-codex .
sudo mv distracted-codex /usr/local/bin/
```

需要 Go 1.21+。

## 快速开始

```bash
# 配置提供商
codex config set-provider deepseek
codex config set-key deepseek sk-xxxxxxxxxxxxxxxx

# 单次执行
codex "用 Go 写一个二分查找函数"

# 交互式 REPL
codex
```

## 支持的提供商

| 名称 | Base URL | 默认模型 |
|------|----------|----------|
| `deepseek` | `https://api.deepseek.com/v1` | `deepseek-chat` |
| `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-max` |
| `zhipu` | `https://open.bigmodel.cn/api/paas/v4` | `glm-4` |
| `moonshot` | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` |

支持任何 OpenAI 兼容的接口，通过 `codex config` 添加自定义提供商：

```bash
codex config set-provider myprovider
codex config set-key myprovider sk-xxx
codex config set-model myprovider my-model-name
```

然后编辑 `~/.codex/config.yaml` 设置 `base_url`。

## 命令行参数

```
codex [参数] [提示词]

参数：
  -p, --provider string    使用的提供商（覆盖配置）
  -m, --model string       使用的模型（覆盖提供商默认值）
  -d, --dir string         工作目录（默认为当前目录）
  -y, --auto-approve       跳过所有确认提示
  -s, --session string     通过 ID 恢复已保存的会话
      --save-as string     退出时自动以此名称保存会话
      --thorough           深度模式：探索代码库、规划、验证变更
```

## REPL 命令

| 命令 | 描述 |
|------|------|
| `/thorough` | 切换到深度模式 |
| `/default` | 切换回极简模式 |
| `/mode` | 显示当前模式 |
| `/reset` | 清除对话历史 |
| `/undo` | 撤销最后一次文件写入或修改 |
| `/save [名称]` | 保存当前会话 |
| `/load <id>` | 加载已保存的会话 |
| `/sessions` | 列出所有已保存的会话 |
| `/index [--force]` | 对当前项目建立（或更新）向量索引 |
| `/index-status` | 显示向量索引统计信息 |
| `/help` | 显示帮助 |
| `exit` / `Ctrl+D` | 退出（如有未保存内容会提示保存） |
| 连按两次 `Ctrl+C` | 立即退出 |

## 工具列表

| 工具 | 描述 |
|------|------|
| `read_file` | 读取文件内容，可指定行范围 |
| `write_file` | 创建或覆盖文件 |
| `patch_file` | 精确替换文件中的字符串或行范围（显示 diff，需确认） |
| `list_files` | 列出目录内容 |
| `find_files` | 按 glob 模式查找文件，如 `**/*.go`、`src/*.ts` |
| `move_file` | 移动或重命名文件（支持 undo） |
| `delete_file` | 删除文件（内容备份到 undo 栈，可恢复） |
| `shell_exec` | 执行 Shell 命令（只读命令自动通过，写操作需确认；末尾加 `&` 后台运行） |
| `grep_files` | 在文件中搜索内容 |
| `http_request` | 发起 HTTP GET/POST 请求，方便调试本地 API |
| `git_status` | 显示工作区状态 |
| `git_diff` | 显示暂存或未暂存的差异，或与某个 ref 的差异 |
| `git_log` | 显示最近的提交记录 |
| `git_commit` | 暂存文件并提交（显示暂存 diff，需确认） |
| `semantic_search` | 按语义搜索代码库（需先运行 `/index`） |
| `run_task` | 将独立任务分发给子 agent 并行执行 |

## 工作模式

### 极简模式（默认）

严格、专注任务。完全按照要求执行，不进行推测性的文件探索，不创建额外文件，不自作主张运行测试。说"ls"就执行 `ls`，说"写一个 fibonacci 函数"就写一个文件，完毕。

### 深度模式（`--thorough` 或 REPL 中 `/thorough`）

结构化五阶段工作流：

1. **理解** — 读取相关文件，查看 git 历史
2. **规划** — 动手前先陈述方案
3. **实现** — 只修改必要文件，优先使用 `patch_file` 而非整体重写
4. **验证** — 运行测试或编译；发现失败先修复再结束
5. **汇报** — 总结改了什么、为什么

深度模式下 `❯` 提示符变为紫色。

## 向量索引 / RAG

向量索引让 agent 能够通过**自然语言语义**搜索代码库，而不是靠 grep 盲猜。在大型项目中尤其有用：不知道"认证逻辑在哪个文件"时，直接问就能找到。

### 开启方式

在 `~/.codex/config.yaml` 中为你的提供商加上 `embed_model`：

```yaml
current_provider: deepseek
providers:
  deepseek:
    name: deepseek
    base_url: https://api.deepseek.com/v1
    api_key: sk-xxxxxxxxxxxxxxxx
    model: deepseek-chat
    embed_model: deepseek-embedding   # 加这一行
```

各提供商的 embedding 模型参考：

| 提供商 | embed_model 值 |
|--------|----------------|
| DeepSeek | `deepseek-embedding` |
| Qwen（通义） | `text-embedding-v3` |
| OpenAI | `text-embedding-3-small` |
| Zhipu | `embedding-3` |

### 使用流程

```bash
# 进入你的项目目录
cd ~/my-project

# 启动 REPL
codex

# 第一次建立索引（只对新建/变更文件调用 embedding API）
/index

# 查看索引统计
/index-status
# 输出示例：
# index: 42 files · 187 chunks · 1536-dim · model: deepseek-embedding (deepseek)
# last updated: 2024-03-15 14:32:01

# 强制全量重建（比如切换了 embed_model）
/index --force
```

索引建好后，agent 会自动使用 `semantic_search` 工具搜索代码：

```
你：帮我找一下用户认证相关的代码在哪

agent → semantic_search("user authentication middleware")
      → auth/middleware.go  lines 12–45  (score 0.91)
      → auth/jwt.go         lines 1–30   (score 0.84)
      → ...
```

### 存储说明

| 数据 | 存储位置 | 说明 |
|------|----------|------|
| 向量索引 | `~/.codex/index/<项目hash>/chunks.bin` | gob 编码，本地文件 |
| 索引元数据 | `~/.codex/index/<项目hash>/meta.json` | 文件 mtime 记录 |

- **向量搜索完全本地**，不发送任何数据到外部
- 只有 `/index` 时，新增/变更的文件才会调用 embedding API
- 增量更新：只重新 embed 自上次索引后有变化的文件
- 每个项目按 workDir 路径的哈希隔离，互不干扰

### 分块策略

- 文件 ≤ 80 行：整文件作为一个 chunk
- 文件 > 80 行：每 60 行一个 chunk，相邻 chunk 重叠 15 行
- 自动跳过：`.git`、`node_modules`、`vendor`、`*.pb.go`、`*.min.js` 等

## 子 Agent 并行执行

对于可以并行的独立任务，agent 可以同时启动多个子 agent：

```
你：用 Vue3 + Go 写一个前后端项目，包含用户登录功能

主 agent 规划后，同时启动 3 个子 agent：

  ⟳ delegating to 3 sub-agents in parallel

  ◆ run_task  创建 Go 后端：gin 框架，JWT 认证，/api/login 接口...
  ◆ run_task  创建 Vue3 前端：登录页面，axios 请求，token 存储...
  ◆ run_task  写集成测试：登录流程端到端测试...

  ✓ Files: backend/main.go, backend/auth.go, backend/handler.go
  ✓ Files: frontend/src/views/Login.vue, frontend/src/api/auth.ts
  ✓ Files: tests/login_test.go
```

子 agent 的行为：

- **自动批准**所有文件操作，不弹确认框
- **完全隔离**，各自维护独立的对话历史
- **不能再次分发**子任务（防止无限递归）
- 父 agent 取消（Ctrl+C）会同时终止所有子 agent

适合使用 `run_task` 的场景：

- 前端 + 后端分开写（不同目录）
- 多个独立模块同时实现
- 为多个文件分别生成测试

**不适合**的场景：互相依赖的任务（B 需要 A 的输出）。

## 操作确认机制

| 操作类型 | 行为 |
|----------|------|
| `ls`、`pwd`、`cat`、`git status`、`git log`、`git diff`、`go version` 等只读命令 | **自动通过**，无需确认 |
| `patch_file`、`write_file` | 显示 diff 预览，需确认 |
| `shell_exec`（非只读） | 显示命令，需确认 |
| `git_commit` | 显示暂存 diff，需确认 |
| `move_file`、`delete_file` | 需确认 |

使用 `-y` / `--auto-approve` 跳过所有确认。

## 上下文压缩

当对话历史较长（估算超过 4000 token）时，旧消息会自动被摘要替换。摘要保留：

- 创建或修改的文件及变更内容
- 关键决策及原因
- 遇到的错误及解决方式

最近的消息始终完整保留。Shell 输出在历史中截断为 2000 字符（但在终端完整显示）。

## 项目记忆

在项目根目录创建 `.codex.md` 文件，每次会话开始时自动注入系统提示词：

```markdown
# 我的项目

- 使用 PostgreSQL，不是 SQLite
- API 在 `internal/api/`，Handler 在 `internal/handler/`
- 运行测试：`make test`
- 不要手动修改 `generated/` 目录下的文件
```

只从当前工作目录加载，不向上查找父目录。

## 会话管理

```bash
# 保存当前会话
/save my-feature

# 列出所有会话
codex session list

# 恢复会话
codex --session abc123

# 查看会话内容
codex session show abc123

# 删除会话
codex session delete abc123
```

会话以 JSON 格式存储在 `~/.codex/sessions/`。

## 配置文件

`~/.codex/config.yaml`：

```yaml
current_provider: deepseek
max_steps: 10

providers:
  deepseek:
    name: deepseek
    base_url: https://api.deepseek.com/v1
    api_key: sk-xxxxxxxxxxxxxxxx
    model: deepseek-chat
    embed_model: deepseek-embedding   # 可选，启用 RAG
  custom:
    name: custom
    base_url: https://my-api.example.com/v1
    api_key: my-key
    model: my-model
```

`work_dir` 故意不持久化——每次运行时从当前目录动态解析。

## 项目结构

```
.
├── main.go                 # 入口
├── cmd/
│   ├── root.go             # 根命令、REPL 循环、参数定义
│   ├── config.go           # `codex config` 子命令
│   ├── session.go          # `codex session` 子命令
│   └── liner.go            # 自定义行编辑器（CJK 安全、带框输入）
├── agent/
│   ├── agent.go            # 主循环、LLM 流式调用、工具分发
│   ├── tools.go            # 工具注册：读/写/修改/Shell/搜索/列表
│   ├── tools_git.go        # Git 工具：status/diff/log/commit
│   ├── tools_rag.go        # 语义搜索工具（semantic_search）
│   ├── tools_subagent.go   # 子 agent 工具（run_task）
│   ├── subagent.go         # 子 agent 运行器
│   ├── vecindex.go         # 本地向量索引（gob + 余弦相似度）
│   ├── chunker.go          # 文件分块策略
│   ├── indexer.go          # 增量项目索引器
│   ├── compressor.go       # 上下文压缩与 token 估算
│   ├── session.go          # 会话保存/加载/列表/删除
│   ├── memory.go           # 项目记忆（.codex.md）加载
│   ├── approver.go         # 确认回调（交互式 / 自动）
│   ├── prompt.go           # 方向键选择菜单
│   ├── prompt_tty.go       # 原始终端模式工具函数
│   ├── spinner.go          # Braille 加载动画
│   ├── diff.go             # 彩色 unified diff 渲染
│   ├── undo.go             # 内存撤销栈（最多 20 步）
│   └── stats.go            # 单轮和会话 token 统计
├── llm/
│   └── client.go           # OpenAI 兼容流式 SSE 客户端 + Embed()
└── config/
    └── config.go           # 配置加载/保存、提供商管理
```

## 跨平台构建

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o distracted-codex-linux .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o distracted-codex-macos-arm64 .

# Windows
GOOS=windows GOARCH=amd64 go build -o distracted-codex.exe .
```

## License

MIT
