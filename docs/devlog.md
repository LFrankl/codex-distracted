# 开发问题记录

每次遇到 bug 或设计缺陷时，在此追加记录。格式：问题描述 → 根本原因 → 解决方案。

---

## 2024-03 · 消息顺序校验错误

**问题**：API 返回 `"Messages with role 'tool' must be a response to a preceding message with 'tool_calls'"`，agent 崩溃。

**根本原因**：`maybeCompress` / `forceCompress` 将包含 `tool_calls` 的 assistant 消息压缩进摘要，但对应的 `tool` 结果消息留在了 `recent` 切片头部，造成消息序列非法。

**解决方案**：新增 `sanitizeRecent(msgs []llm.Message)`，在构建压缩历史前，从 `recent` 切片头部裁掉所有孤立的 `tool` 消息和纯 `tool_calls` 的 assistant 消息，直到遇到合法的 `user` 或带文本的 `assistant` 消息为止。同时在 `SetMessages()` 中也调用，修复从磁盘加载的历史会话。

---

## 2024-03 · 上下文超限崩溃

**问题**：长对话时 API 返回 `"This model's maximum context length is 131072 tokens"`，agent 直接报错退出。

**根本原因**：压缩阈值 `compressionThreshold = 4000` 过低，实际 token 估算不准，且超限错误没有被捕获处理。

**解决方案**：
1. 将 `compressionThreshold` 从 4000 提升到 20000，`keepRecentMessages` 从 4 提升到 6。
2. 新增 `isContextLengthError(err)` 识别超限错误（检查 `*llm.APIError` 字段及错误字符串）。
3. 在 `callLLM` 错误路径中检测超限 → 调用 `forceCompress` 强制压缩 → 重试，打印 `[context limit hit — compressing and retrying]`，不崩溃。

---

## 2024-03 · 子 agent 脚手架命令卡死

**问题**：子 agent 在创建新项目时调用 `npm create vue@latest`、`cargo init` 等交互式脚手架 CLI，命令等待 stdin 输入，永远卡住。

**根本原因**：子 agent 沿用主 agent 的惯用模式，不了解自己没有 stdin。

**解决方案**：在系统提示词和 `defRunTask()` 描述中明确禁止所有交互式 CLI，要求子 agent 直接用 `write_file` 创建 `package.json`、`vite.config.ts`、`go.mod` 等文件，再运行 `npm install`、`go mod tidy` 等非交互命令。

---

## 2024-03 · 子 agent 路径混乱 + 探索行为冗余

**问题**：子 agent 创建项目时大量运行 `ls`、`find`、`echo $HOME`、`mkdir` 等无意义命令，文件写到错误目录，整体行为像蜗牛。

**根本原因（多重）**：
1. 子 agent 使用主 agent 的提示词（为修改现有项目设计），不适合从零创建项目。
2. 子 agent 的 `workDir` 是 CLI 项目目录（`/dev/cli`），用相对路径会写错位置。
3. `shell_exec("cd foo && cmd")` 里的 `cd` 只对当次命令有效，下次 `shell_exec` 又回原目录。
4. `write_file` 已自动创建父目录，但子 agent 仍在执行多余的 `mkdir`。

**解决方案**：为子 agent 创建专用提示词 `subAgentPrompt`，强制执行三步模式：
- **STEP 1**：在单次响应中用绝对路径一次性写完所有文件（`write_file` 自动建目录，禁止 `mkdir`）
- **STEP 2**：用绝对路径运行构建命令
- **STEP 3**：有报错则修复，结束
- 禁止 `ls`、`find`、`echo $HOME` 等探索命令，禁止相对路径
- 主 agent 的 `run_task` 调用规则同步更新：传给子 agent 前先把 `~` 展开为真实绝对路径

---

## 2024-03 · DeepSeek 模型 ID 错误

**问题**：使用 `deepseek-v3` 模型 ID 报错 model not found。

**根本原因**：DeepSeek API 的实际模型 ID 是 `deepseek-chat`，不是 `deepseek-v3`。

**解决方案**：`codex config set-model deepseek deepseek-chat`。

---

## 2024-03 · 子 agent 仍在执行 mkdir + 写出破损代码

**问题**：专用 subAgentPrompt 加入后，子 agent 仍然：①用 `mkdir` 建目录（提示词规则被 LLM 忽略）；②写出缺少 import 的 Go 代码，再打两次补丁才能编译，每次都多一个 LLM 往返。

**根本原因**：
1. `mkdir` 禁令是软文本规则，DeepSeek 会无视。
2. 子 agent 没有被要求在写文件前先想清楚全部 import 和依赖，导致第一次写出不完整的代码。

**解决方案**：
1. `isMkdirOnly()` 函数识别纯 `mkdir` 命令（含 `&&` 和 `;` 链）；在 `shellExec` 里当 `depth > 0`（子 agent）时直接跳过，返回提示信息，代码层面彻底屏蔽。
2. `subAgentPrompt` 新增 STEP 0（写前思考）：在调用任何 `write_file` 之前，必须在脑中规划好完整文件列表和每个文件的所有 import；"一次正确的写入胜过三次修补"。

---

## 2024-03 · DeepSeek 不支持 embedding

**问题**：配置 `embed_model: deepseek-embedding` 后，`/index` 调用向量 API 报错。

**根本原因**：DeepSeek 没有 embedding 端点。

**解决方案**：去掉 `embed_model` 配置，使用 BM25 本地搜索（零 API 依赖）。向量搜索可通过配置 Qwen（`text-embedding-v3`）或 OpenAI（`text-embedding-3-small`）实现。

---
