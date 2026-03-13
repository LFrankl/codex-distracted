# distracted-codex

一个极简的 AI 编程助手命令行工具，用 Go 编写，支持任何 OpenAI 兼容的 API——专为国产 AI 提供商（DeepSeek、Qwen、智谱、Moonshot）设计，同时也支持 OpenAI。

## 功能特性

- **ReAct 智能循环** — 思考、调用工具、观察结果，循环执行
- **流式输出** — 响应逐 token 流式返回，无需等待完整结果
- **两种工作模式** — 极简模式（严格按要求执行）和深度模式（探索、规划、验证）
- **21 个内置工具** — 文件读写/修改、Shell 命令、代码搜索、Git 完整工作流、HTTP/网页抓取、文件符号概览、并行子任务
- **BM25 本地代码搜索** — 开箱即用，零依赖，无需任何 API key；可选升级为向量语义搜索
- **子 Agent 并行执行** — 将独立任务分发给多个子 agent 同时运行
- **会话持久化** — 保存并跨次恢复对话
- **上下文压缩 + 自动恢复** — 自动摘要旧历史；超出上下文限制时自动压缩并重试，不崩溃
- **撤销栈** — 使用 `/undo` 撤销任意文件写入或修改
- **项目记忆** — 在项目根目录放置 `.codex.md`，每次会话自动注入
- **智能操作确认** — 只读命令（`ls`、`git status` 等）自动通过，写操作才需确认
- **带框输入框** — 简洁的终端 UI，历史记录灰显，支持方向键导航，支持多行粘贴
- **审批重定向** — 确认危险操作时可选「Other instructions →」直接注入新指令，无需取消后重新输入

## 在本地运行

### 环境要求

- **Go 1.21+**（`go version` 确认）
- **支持 ANSI 颜色的终端**：macOS Terminal / iTerm2 / Windows Terminal / 任何现代 Linux 终端均可
- **至少一个 AI 提供商的 API key**（见下方）

> **Windows 用户**：推荐在 Windows Terminal 或 WSL 中运行，cmd.exe 对 ANSI 颜色支持不完整。

---

### 第一步：克隆并编译

```bash
git clone https://github.com/LFrankl/codex-distracted
cd codex-distracted
go build -o distracted-codex .
```

将二进制放到 PATH：

```bash
# macOS / Linux
sudo mv distracted-codex /usr/local/bin/distracted-codex

# 或者放到用户目录（不需要 sudo）
mkdir -p ~/.local/bin
mv distracted-codex ~/.local/bin/
# 确保 ~/.local/bin 在 PATH 里：echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

验证安装：

```bash
distracted-codex --help
```

---

### 第二步：获取 API Key

选一个提供商即可开始使用。**推荐国内用户优先使用 DeepSeek**，性价比最高且延迟低。

| 提供商 | 注册地址 | 国内访问 | 支持 Embedding |
|--------|----------|----------|----------------|
| DeepSeek | https://platform.deepseek.com | ✅ | ❌（不支持）|
| 通义千问（Qwen） | https://dashscope.console.aliyun.com | ✅ | ✅ `text-embedding-v3` |
| 智谱 GLM | https://open.bigmodel.cn | ✅ | ✅ `embedding-3` |
| Moonshot | https://platform.moonshot.cn | ✅ | ❌ |
| OpenAI | https://platform.openai.com | ⚠️ 需代理 | ✅ `text-embedding-3-small` |

---

### 第三步：配置 API Key

```bash
# 以 DeepSeek 为例
distracted-codex config set-provider deepseek
distracted-codex config set-key deepseek sk-xxxxxxxxxxxxxxxxxxxxxxxx

# 验证配置
distracted-codex config show
```

配置会保存到 `~/.codex/config.yaml`，之后无需重复配置。

---

### 第四步：第一次运行

```bash
# 进入你的项目目录（很重要！agent 以此目录为工作根目录）
cd ~/my-project

# 单次提问（非交互）
distracted-codex "这个项目是做什么的？"

# 交互式 REPL（推荐日常使用）
distracted-codex
```

进入 REPL 后会看到：

```
Codex  ·  deepseek-chat  ·  /Users/you/my-project
Type your request · /help for commands
❯
```

---

### 注意事项

**工作目录**

agent 的所有文件操作都基于启动时的当前目录。建议在项目根目录下启动，而不是在 `~` 或 `/`：

```bash
cd ~/my-project && distracted-codex
```

**API Key 安全**

Key 明文存储在 `~/.codex/config.yaml`（权限 0600）。不要将此文件提交到 git。

**DeepSeek 不支持 Embedding**

如果你用 DeepSeek 并且想要代码语义搜索，需要额外配置一个支持 embedding 的提供商（如 Qwen）：

```yaml
# ~/.codex/config.yaml
current_provider: deepseek   # 对话仍用 DeepSeek
providers:
  deepseek:
    api_key: sk-xxx
    model: deepseek-chat
  qwen:
    api_key: sk-xxx
    embed_model: text-embedding-v3   # 只用于 /index，对话不用这个 provider
```

然后在对话中手动指定用 qwen 建索引：`distracted-codex --provider qwen /index`。或者直接用 BM25 本地搜索（无需任何 embedding，`/index` 默认会建）。

**多行粘贴**

终端支持 [Bracketed Paste Mode](https://en.wikipedia.org/wiki/Bracketed-paste) 时（绝大多数现代终端），可以直接粘贴多行文本（报错日志、代码片段）而不会提前提交。输入框会自动扩展高度。

**`--auto-approve` 的风险**

`-y` 会跳过所有确认（包括 `rm -rf`、`git push --force` 等）。只在完全信任 agent 输出时使用，建议先在交互模式下确认行为再切换。

**自定义 OpenAI 兼容接口**

任何支持 OpenAI API 格式的服务都可以用：

```bash
distracted-codex config set-provider myprovider
distracted-codex config set-key myprovider my-api-key
distracted-codex config set-model myprovider my-model-name
# 然后手动编辑 ~/.codex/config.yaml，补上 base_url 字段
```

---

### 推荐工作流

```bash
# 1. 进项目，建本地索引（一次性，之后自动加载）
cd ~/my-project
distracted-codex
/index

# 2. 日常使用
distracted-codex          # REPL 模式，支持多轮对话
distracted-codex "xxx"    # 单次执行，适合脚本

# 3. 复杂任务用深度模式
/thorough
❯ 帮我重构 auth 模块，把 JWT 换成 session

# 4. 保存重要会话
/save auth-refactor

# 5. 下次继续
distracted-codex --session auth-refactor
# 或者在 REPL 里 /load auth-refactor
```

---

## 安装（快速）

```bash
git clone https://github.com/LFrankl/codex-distracted
cd codex-distracted
go build -o distracted-codex .
sudo mv distracted-codex /usr/local/bin/
```

需要 Go 1.21+。详细说明见上方「在本地运行」。

## 快速开始

```bash
# 配置提供商
distracted-codex config set-provider deepseek
distracted-codex config set-key deepseek sk-xxxxxxxxxxxxxxxx

# 单次执行
distracted-codex "用 Go 写一个二分查找函数"

# 交互式 REPL
distracted-codex
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
| `/index [--force]` | 对当前项目建立（或更新）索引（BM25 全量 + 向量增量） |
| `/index-status` | 显示索引统计（BM25 chunk 数、向量索引维度等） |
| `/help` | 显示帮助 |
| `exit` / `Ctrl+D` | 退出（如有未保存内容会提示保存） |
| 连按两次 `Ctrl+C` | 立即退出 |

## 键盘快捷键

### 光标移动

| 快捷键 | 功能 |
|--------|------|
| `←` / `→` | 单字符移动 |
| `Ctrl+←` / `Ctrl+→` | 按词跳转 |
| `Ctrl+A` / `Home` | 跳到行首 |
| `Ctrl+E` / `End` | 跳到行尾 |
| `Ctrl+B` / `Ctrl+F` | 单字符后退 / 前进（Emacs 风格） |

### 编辑

| 快捷键 | 功能 |
|--------|------|
| `Backspace` | 删除光标前一个字符 |
| `Delete` | 删除光标处字符 |
| `Ctrl+W` | 删除光标前一个词 |
| `Ctrl+K` | 删除光标到行尾 |
| `Ctrl+U` | 清空整行 |
| `Ctrl+L` | 清屏并重绘 |

### 历史与搜索

| 快捷键 | 功能 |
|--------|------|
| `↑` / `↓` | 浏览历史记录 |
| `Ctrl+R` | **逆向历史搜索** — 边输入边过滤，再按 `Ctrl+R` 跳到更早的匹配，`Enter` 接受，`ESC` 取消 |

### 补全

| 快捷键 | 功能 |
|--------|------|
| `Tab` | 自动补全 `/` 命令，连续按 `Tab` 循环切换匹配项 |

### 状态栏

底部边框实时显示当前模式和本次会话的 token 用量：

```
default  ·  session ↑2341 ↓876  ·  ↑↓ history  Ctrl+R search  Tab complete
```

thorough 模式时 `thorough` 显示为紫色。

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
| `git_branch` | 列出、创建、切换分支 |
| `git_pull` | 从远端拉取代码（merge 或 rebase，需确认） |
| `git_push` | 推送到远端（需确认；force push 使用 `--force-with-lease`） |
| `web_fetch` | 抓取任意 URL 并以纯文本返回（文档、GitHub Issue、API 规范等） |
| `file_outline` | 列出文件中所有符号（函数/类/类型）及其行号，无需读取全文 |
| `semantic_search` | 按语义/关键词搜索代码库（需先运行 `/index`） |
| `run_task` | 将独立任务分发给子 agent 并行执行 |

## web_fetch：抓取网页

`web_fetch` 让 agent 能直接读取外部 URL，无需任何 API key，纯标准库实现：

```
你：帮我看看 Go 1.23 的 release notes 有哪些新特性

agent → web_fetch("https://go.dev/doc/go1.23")
      → 解析 HTML，返回纯文本（自动去除 <script>/<style>/<nav>）
      → 总结新特性...
```

特性：
- 自动去除导航栏、脚本、样式等噪声，只保留正文
- 限制响应体 200 KB，避免 token 爆炸
- `max_lines` 参数可限制返回行数（默认 300）
- 非 HTML 内容（JSON、纯文本）直接返回原始内容

## file_outline：文件符号概览

`file_outline` 扫描文件，返回所有顶层符号（函数/类/类型/接口）及其行号，**无需读取全文内容**。在大文件中先定位、再精读，大幅节省 token。

支持语言：Go / Python / TypeScript / JavaScript / Rust / Java / Kotlin / Ruby / C / C++

```
你：agent.go 里 Run 方法在哪？

agent → file_outline("agent/agent.go")

agent/agent.go

Kind    Name             Line
─────────────────────────────
func    New              136
func    SetRAG           151
func    Run              158
func    step             203
func    printToolCall    401
func    toolDetail       411
func    printToolResult  461

→ 直接 read_file(agent.go, start_line=158, end_line=202)，无需读整个文件
```

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

深度模式下有专门的调试工作流：先陈述假设，再针对性读取证据，最小化修改后验证——避免一个一个盲目尝试读文件。

深度模式下 `❯` 提示符变为紫色。

## 代码搜索与索引

distracted-codex 的 `semantic_search` 工具背后有两种搜索后端，通过统一的 `Searcher` 接口抽象，可无缝切换：

```
semantic_search 工具
        │
        ▼
   Searcher 接口（Search / Kind）
   ┌─────────────────┬────────────────┐
   │                 │                │
   ▼                 ▼                ▼
BM25Index       VecSearcher     LocalVecSearcher
（本地，零依赖）  （向量 API）      （ONNX，规划中）
```

**优先级**：向量搜索 > BM25（两者都有时向量生效）。

### BM25 本地搜索（默认，开箱即用）

不需要 `embed_model`，不调用任何外部 API。启动时自动加载已有索引。

**工作原理**：BM25 是改进版 TF-IDF，是搜索引擎（Elasticsearch 等）的标准排序算法。对代码搜索效果好，因为标识符、函数名是高频且有区分性的词元。

分词器专门针对代码处理驼峰命名和下划线命名：

```
getUserById  →  getuserbyid, get, user, by, id
HTTPRequest  →  httprequest, http, request
user_profile →  user, profile
```

```bash
# 进入项目，建立 BM25 索引（无 API 调用，通常 < 5s）
cd ~/my-project
codex
/index

# 查看索引统计
/index-status
# 示例输出：
# BM25 index: 312 chunks (203 files)
# No vector index — run /index with embed_model configured to enable semantic search
```

索引持久化到 `~/.codex/index/<项目hash>/bm25.bin`，下次启动自动加载。

### 向量语义搜索（可选，需配置 embed_model）

向量搜索理解语义，能处理概念性查询（"在哪里处理错误"即使代码里没有"错误"这个词也能找到）。

在 `~/.codex/config.yaml` 中为你的提供商加上 `embed_model`：

```yaml
current_provider: qwen
providers:
  qwen:
    name: qwen
    base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
    api_key: sk-xxxxxxxxxxxxxxxx
    model: qwen-max
    embed_model: text-embedding-v3   # 加这一行
```

各提供商的 embedding 模型参考：

| 提供商 | embed_model 值 | 说明 |
|--------|----------------|------|
| Qwen（通义） | `text-embedding-v3` | 推荐，支持代码 |
| OpenAI | `text-embedding-3-small` | 通用 |
| Zhipu | `embedding-3` | |
| DeepSeek | ❌ 不支持 | 请使用其他提供商的 embedding |

配置后运行 `/index`，向量索引会在 BM25 之后建立并自动生效：

```bash
/index
# BM25: rebuilt 312 chunks
# embedding 203 files... done
# vector index: 312 chunks · 1024-dim · model: text-embedding-v3

/index-status
# BM25 index: 312 chunks (203 files)
# vector index: 312 chunks · 1024-dim · text-embedding-v3 (qwen)
# last updated: 2024-03-15 14:32:01
```

向量索引只在 `/index` 时对新建/变更文件调用 embedding API（增量更新），下次启动自动加载。

### 两种搜索后端对比

| | BM25（默认） | 向量搜索（可选） |
|---|---|---|
| 需要 embed_model | 否 | 是 |
| API 调用 | 无 | 仅 `/index` 时 |
| 查询速度 | 极快（纯本地） | 快（embed query 一次） |
| 适合查询 | 函数名、标识符、精确关键词 | 自然语言描述、概念性查询 |
| 典型用例 | "getUserById 在哪" | "在哪里处理认证失败" |

### 存储说明

所有索引存在 `~/.codex/index/` 下，按项目路径哈希隔离：

```
~/.codex/index/
└── a3f2b1c0/              ← SHA-256(workDir)[:8]
    ├── bm25.bin           ← BM25 索引（gob 编码）
    ├── chunks.bin         ← 向量索引（gob 编码，仅配置 embed_model 后存在）
    └── meta.json          ← 向量索引元数据（provider、model、文件 mtime 记录）
```

- 向量搜索完全本地，搜索时不发送任何数据到外部
- 每个项目按 workDir 哈希隔离，互不干扰

### 分块策略

- 文件 ≤ 80 行：整文件作为一个 chunk
- 文件 > 80 行：每 60 行一个 chunk，相邻重叠 15 行（防止函数被截断）
- 自动跳过：`.git`、`node_modules`、`vendor`、`dist`、`build`、`*.pb.go`、`*.min.js` 等

详细的搜索架构文档见 [`docs/search-index.md`](docs/search-index.md)。

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

**重要**：子 agent 不能使用交互式脚手架 CLI（`npm create`、`yarn create`、`vite`、`create-react-app`、`cargo init`、`django-admin startproject` 等），因为它们需要 stdin 输入会永久阻塞。子 agent 会直接用 `write_file` 创建 `package.json`、`vite.config.ts` 等项目文件，再运行非交互式命令（`npm install`、`go mod tidy`）。

适合使用 `run_task` 的场景：

- 前端 + 后端分开写（不同目录）
- 多个独立模块同时实现
- 为多个文件分别生成测试

**不适合**的场景：互相依赖的任务（B 需要 A 的输出）。

## 操作确认机制

| 操作类型 | 行为 |
|----------|------|
| `ls`、`pwd`、`cat`、`git status`、`git log`、`git diff`、`go version` 等只读命令 | **自动通过**，无需确认 |
| `git_branch list` | **自动通过**，只读操作 |
| `patch_file`、`write_file` | 显示 diff 预览，需确认 |
| `shell_exec`（非只读） | 显示命令，需确认 |
| `git_commit` | 显示暂存 diff，需确认 |
| `git_pull` | 显示拉取目标，需确认 |
| `git_push` | 显示推送目标，需确认；force push 额外标注 `(FORCE)` |
| `move_file`、`delete_file` | 需确认 |

使用 `-y` / `--auto-approve` 跳过所有确认。

## 上下文压缩

当对话历史较长（估算超过 20000 token）时，旧消息会自动被摘要替换。摘要保留：

- 创建或修改的文件及变更内容
- 关键决策及原因
- 遇到的错误及解决方式

最近 6 条消息始终完整保留。Shell 输出在历史中截断为 2000 字符（但在终端完整显示）。

**自动恢复**：如果 API 返回上下文超限错误（`context length exceeded`），agent 会立即触发强制压缩并重试，不崩溃、不中断对话。终端会显示：

```
[context limit hit — compressing and retrying]
```

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
    # embed_model 不填则使用 BM25 本地搜索（推荐）
    # DeepSeek 暂不支持 embedding，如需向量搜索请配置 qwen/openai
  qwen:
    name: qwen
    base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
    api_key: sk-xxxxxxxxxxxxxxxx
    model: qwen-max
    embed_model: text-embedding-v3   # 可选，启用向量语义搜索
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
│   ├── tools_git.go        # Git 工具：status/diff/log/commit/branch/pull/push
│   ├── tools_fetch.go      # 网页抓取工具（web_fetch）
│   ├── tools_outline.go    # 文件符号概览工具（file_outline）
│   ├── tools_rag.go        # 语义搜索工具（semantic_search）、Searcher 接口连接
│   ├── tools_subagent.go   # 子 agent 工具（run_task）
│   ├── subagent.go         # 子 agent 运行器
│   ├── searcher.go         # Searcher 接口定义 + CodeResult 类型
│   ├── bm25.go             # BM25 索引实现（打分、分词、驼峰拆分）
│   ├── bm25indexer.go      # BM25 全量重建索引器
│   ├── vecsearcher.go      # VecSearcher（向量 API）+ LocalVecSearcher（ONNX 占位）
│   ├── vecindex.go         # 本地向量索引（gob + 余弦相似度）
│   ├── chunker.go          # 文件分块策略
│   ├── indexer.go          # 向量增量索引器
│   ├── compressor.go       # 上下文压缩、token 估算、消息序列修复
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
├── config/
│   └── config.go           # 配置加载/保存、提供商管理
└── docs/
    └── search-index.md     # 搜索/索引子系统详细架构文档
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
