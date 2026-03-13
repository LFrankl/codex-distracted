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

## 2024-03 · 诊断完直接动手，不给用户确认机会

**问题**：agent 找到根本原因后立刻开始改代码，用户没有机会确认方向是否正确，也没有机会说"不用改这里"。导致大量未经确认的改动。

**根本原因**：提示词的调试流程没有"暂停确认"步骤，agent 完成诊断后默认直接进入实现阶段。

**解决方案**：两处提示词加入明确的暂停节点：① `systemPrompt` 调试规则新增 "DIAGNOSE FIRST, then PAUSE"——找到根本原因后停止工具调用，输出 "根本原因: X。修复方案: Y。是否继续？"，等待确认再 patch；② `thoroughPrompt` DEBUG 流程在第 4 步加入 PAUSE，PLAN 阶段改为"实现前必须确认，一个问题，确认后不再打断"。

---

## 2024-03 · 修完还在自言自语，停不下来

**问题**：修好了用户报告的 bug 之后，agent 继续读文件、发现新问题、继续修，循环不止，自言自语不停。

**根本原因**：提示词没有"完成即停止"的规则，LLM 的默认行为是看到问题就修，没有明确的终止条件。

**解决方案**：两处提示词加规则：① `systemPrompt` 新增第 11 条——用户请求满足后立即停止，不得推测性地修复未被提及的相关问题，不得叙述未来步骤；② `thoroughPrompt` Guardrails 末尾加同样规则——REPORT 阶段结束后等待下一条用户消息，不继续读写。

---

## 2024-03 · dev server 阻塞终端

**问题**：agent 执行 `go run main.go` / `npm run dev` 时不加 `&`，命令永久阻塞，会话卡死。

**根本原因**：`shell_exec` 工具描述和系统提示词均未提及后台执行，LLM 不知道需要加 `&`。

**解决方案**：三处同步修改：① `shell_exec` 工具 description 明确说明 `&` 是长期进程的**必选项**，并给出示例；② `systemPrompt` 加规则：启动 dev server / `go run` / `npm run dev` 等**必须**加 `&`，启动后用 `http_request` 验证；③ `subAgentPrompt` STEP 2 加同样规则。

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

## 2024-03 · 提示词分级重构

**问题**：系统提示词越加越长（~800 token），所有规则等权重堆在一起，LLM 注意力衰减，结尾的规则几乎被忽略，debug 规则和实现规则互相干扰。

**根本原因**：所有规则放在系统提示词里，既低效（每次都读）又无差异（不管什么任务都一样）。

**解决方案**：三层架构：

1. **Tier 1 — `corePrompt`（~80 token，始终生效）**
   只放真正的绝对规则：身份、不得自发 commit、不得创建多余文件、完成即停止。

2. **Tier 2 — 动态注入（每条用户消息前追加，注意力最高）**
   `detectIntent()` 检测意图 → 选择对应的 context block 追加到用户消息开头：
   - `debugContext`：报错/bug 消息 → 先问、定位、暂停、确认、最小修复
   - `planContext`：功能/修改消息 → 列文件、批量读、批量写、验证、停止
   - `thoroughAddendum`：thorough 模式追加，要求确认计划再动手
   - 子 agent 跳过注入（有自己的 `customPrompt`）

3. **Tier 3 — 工具描述（已有）**
   `shell_exec` 里的 `&` 规则、`patch_file` 里的 `patches[]` 规则。

**效果**：系统提示词缩小 90%；规则在需要时才出现，在最高注意力位置出现；debug 和实现协议不再互相干扰。

---

## 2024-03 · 多文件修改串行太慢

**问题**：agent 修改跨多个文件的功能（如给 Todo 加一个字段）时，逐个 read→patch→read→patch，N 个文件要 2N 次 LLM 往返，极慢。

**根本原因（多重）**：
1. LLM 每次 response 只输出一个 tool call，提示词没有明确的多文件批处理规范。
2. `patch_file` 不支持对同一文件多处修改，导致同文件也要多次往返。
3. Agent 执行层：只要有一个 serial 工具（如 `patch_file`），整个 response 里的所有工具全部串行，`read_file` 也跟着等。

**解决方案**：
1. **提示词**：两处 prompt 加入「多文件变更强制两步模式」——第一步一次性读所有文件，第二步一次性 patch 所有文件，禁止 read A→patch A→read B→patch B 模式。加了具体示例（4 文件 add field 场景）。同文件多处修改必须用 `patches[]` 数组。
2. **`patch_file` 新增 `patches` 数组参数**：一次调用对同一文件做多处字符串替换，所有 diff 一起展示，一次审批，一次写入。
3. **Agent 执行层三级分类**：
   - `parallel`（read_file/write_file/find_files 等）→ 始终并发
   - `patchOnly`（patch_file）→ 展示所有 diff，一次审批，并发写入
   - `serial`（shell_exec/git 操作/move/delete）→ 保持串行逐个审批

   混合 response（reads + patches）：reads 先并发，再批量审批 patches 并发执行。

**效果**：4 文件 add-field 任务从 ~10 次往返降至 ~2 次往返（全部读 → 全部 patch）。

---

## 2024-03 · agent 一股脑做，不向用户请示

**问题**：任务描述模糊时，agent 不问用户，直接猜测并大量读文件，走了很多弯路。用户其实一句话就能提供关键信息（错误文本、受影响页面、期望行为）。

**根本原因**：提示词没有"先澄清"步骤，LLM 默认行为是立刻展开工具调用。

**解决方案**：两处提示词加入「先澄清」规则：
- 任务模糊或缺少运行时信息 → 先问一个聚焦的问题，再动工具。
- Debug 流程加 Step 0：没有报错文本就问「能贴一下完整报错吗？」，需要命令输出就让用户跑并贴结果。
- Plan 流程：需求不明确时先问一个澄清问题，再开始实现。
- 强制规则：最多一个问题，不许列问题清单。

---

## 2024-03 · 命令审批只有 Yes/No，无法重定向 agent

**问题**：执行危险命令（shell_exec、patch_file、git_commit 等）时弹出确认框只有「是/否」，用户想改变 agent 方向只能取消再重新输入一条消息，体验割裂。

**根本原因**：`Approver` 类型原为 `func(kind, detail string) bool`，只能表达「批准/拒绝」，无法携带用户的新指令文本。

**解决方案**：
1. `Approver` 签名改为 `func(kind, detail string) (proceed bool, instruction string)`，`AutoApprover` 返回 `(true, "")`, `InteractiveApprover` 新增第 3 选项「Other instructions →」，选后读取一行文本作为新指令。
2. `ToolResult` 新增 `Instruction string` 字段；`shellExec`、`patchFile`、`gitCommit`、`gitPull`、`gitPush` 的审批路径在 `instruction != ""` 时返回 `ToolResult{Instruction: instr}`。
3. Agent 循环串行执行工具时，一旦某个结果的 `Instruction != ""`，立即将后续未执行的工具标记为 `cancelled`，所有工具结果写入消息历史后，追加一条 `role: user` 消息（内容为 instruction），然后 `continue` 进入下一步，让 LLM 基于新指令继续工作。

---
