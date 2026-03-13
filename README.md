# Codex - AI 代码助手 CLI 🚀

一个支持 OpenAI 兼容 API 的智能代码助手命令行工具。Codex 可以帮助你编写、编辑、理解和调试代码，支持多种 AI 提供商。✨

## ✨ 特性亮点

- 🤖 **多提供商支持**：DeepSeek、Qwen、Zhipu、Moonshot 等 OpenAI 兼容 API
- 🔧 **智能工具系统**：文件读写、命令执行、代码搜索等
- 💬 **两种使用模式**：单次命令和交互式 REPL
- 📁 **上下文感知**：自动探索代码库，理解项目结构
- 🎨 **美观输出**：彩色终端输出，工具调用可视化
- ⚙️ **灵活配置**：可配置 API 密钥、模型和工作目录
- 🚀 **快速响应**：实时代码生成和修改
- 🔍 **智能搜索**：强大的代码搜索和分析能力
- 🛡️ **安全执行**：可控的命令执行环境
- 📊 **进度显示**：实时显示任务执行进度

## 🚀 快速开始

### 📦 安装

1. **从源码构建** 🔨：
```bash
git clone <repository-url>
cd cli
go build -o codex
sudo mv codex /usr/local/bin/
```

2. **直接使用预编译二进制** ⚡：
```bash
# 下载对应平台的二进制文件
chmod +x codex
sudo mv codex /usr/local/bin/
```

### ⚙️ 配置

1. **查看可用提供商** 👀：
```bash
codex config list
```

2. **设置提供商** 🔧：
```bash
codex config set-provider deepseek
```

3. **设置 API 密钥** 🔑：
```bash
codex config set-key deepseek your-api-key-here
```

4. **验证配置** ✅：
```bash
codex config show
```

## 📖 使用方法

### 🎯 单次模式
```bash
# 编写代码 ✍️
codex "write a fibonacci function in Go"

# 重构代码 🔄
codex --provider deepseek "refactor this codebase"

# 指定工作目录 📂
codex --dir ./my-project "add error handling to main.go"

# 调试代码 🐛
codex "find and fix the bug in user_service.go"

# 添加测试 🧪
codex "add unit tests for the authentication module"
```

### 💬 交互式 REPL 模式
```bash
codex
```
进入交互模式后，你可以：
- 💭 输入自然语言请求
- 🔄 使用 `/reset` 清除对话历史
- ❓ 使用 `/help` 查看帮助
- 🚪 输入 `exit` 退出
- 📝 使用 `/save` 保存对话历史
- 📋 使用 `/load` 加载之前的对话

### ⚙️ 配置命令
```bash
# 列出所有提供商 📋
codex config list

# 设置当前提供商 🔧
codex config set-provider <name>

# 设置 API 密钥 🔑
codex config set-key <provider> <api-key>

# 设置模型 🤖
codex config set-model <provider> <model>

# 显示当前配置 👀
codex config show

# 重置配置 🔄
codex config reset

# 导出配置 📤
codex config export

# 导入配置 📥
codex config import <config-file>
```

## 🛠️ 支持的工具

Codex 可以调用以下工具来帮助你：🔧

| 工具 | 描述 | 示例用途 |
|------|------|----------|
| 📖 `read_file` | 读取文件内容 | 查看现有代码 |
| ✍️ `write_file` | 写入文件 | 创建或修改代码文件 |
| 📁 `list_files` | 列出文件和目录 | 探索项目结构 |
| 💻 `shell_exec` | 执行 shell 命令 | 运行测试、构建项目 |
| 🔍 `grep_files` | 搜索文件内容 | 查找特定代码模式 |
| 🔧 `patch_file` | 编辑文件内容 | 精确修改代码片段 |
| 📊 `search_files` | 高级文件搜索 | 复杂模式匹配 |

## 🔌 支持的提供商

### 🤖 内置提供商
- 🚀 **DeepSeek**: `deepseek-chat` 模型
- 🌟 **Qwen**: `qwen-max` 模型  
- 🔥 **Zhipu**: `glm-4` 模型
- 🌙 **Moonshot**: `moonshot-v1-8k` 模型
- ⚡ **OpenAI**: `gpt-4` 模型
- 🎯 **Anthropic**: `claude-3` 模型

### 🔧 自定义提供商
支持任何 OpenAI 兼容的 API 端点，只需在配置中指定：✨
```yaml
providers:
  my-provider:
    name: "my-provider"
    base_url: "https://api.example.com/v1"
    model: "custom-model"
```

## ⚙️ 配置详解

配置文件位于 `~/.codex/config.yaml`：📁

```yaml
# 🎯 当前使用的提供商
current_provider: deepseek

# 🔄 最大执行步骤（防止无限循环）
max_steps: 20

# 📂 默认工作目录
work_dir: ""

# 🎨 输出设置
output:
  color: true        # 🌈 彩色输出
  verbose: false     # 📋 详细模式
  progress: true     # 📊 显示进度条

# 🤖 提供商配置
providers:
  deepseek:
    name: deepseek
    base_url: https://api.deepseek.com/v1
    api_key: your-api-key-here
    model: deepseek-chat
    temperature: 0.7  # 🌡️ 温度参数
    max_tokens: 4096  # 🔢 最大 token 数
  # 其他提供商...
```

## 🎯 使用示例

### 🚀 示例 1：创建新项目
```bash
# 创建一个新的 Go 项目
codex "create a simple HTTP server in Go with graceful shutdown"

# Codex 会：✨
# 1. 📝 创建 main.go 文件
# 2. 📦 添加必要的依赖
# 3. 💻 编写完整的服务器代码
# 4. 🔧 运行 go mod init 和 go mod tidy
```

### 🐛 示例 2：调试代码
```bash
# 在项目目录中
codex "find and fix the bug in user_service.go"

# Codex 会：🔍
# 1. 📖 读取相关文件
# 2. 🧠 分析代码逻辑
# 3. 🧪 运行测试查找问题
# 4. 🔧 提出修复方案并实施
```

### 🔄 示例 3：重构代码
```bash
codex "refactor the authentication module to use JWT tokens"

# Codex 会：🔄
# 1. 🧠 理解现有认证逻辑
# 2. 🎨 设计新的 JWT 实现
# 3. 📝 逐步迁移代码
# 4. ✅ 确保向后兼容
```

### 📚 示例 4：学习新语言
```bash
# 学习 Rust 编程
codex "teach me Rust by creating a simple CLI tool"

# Codex 会：📚
# 1. 📝 创建 Rust 项目结构
# 2. 💡 解释 Rust 语法和概念
# 3. 🔧 编写示例代码
# 4. 🧪 添加测试和文档
```

### 🧪 示例 5：添加测试
```bash
# 为现有代码添加测试
codex "add comprehensive unit tests for the database layer"

# Codex 会：🧪
# 1. 📖 分析现有代码结构
# 2. 🎯 设计测试用例
# 3. 📝 编写测试代码
# 4. ✅ 运行测试确保通过
```

## 🏗️ 项目结构

```
.
├── 📁 agent/           # 代理核心逻辑
│   ├── 🤖 agent.go     # 代理主循环和工具调用
│   └── 🔧 tools.go     # 工具函数实现
├── 📁 cmd/             # CLI 命令
│   ├── ⚙️ config.go    # 配置管理命令
│   └── 🎯 root.go      # 根命令和 REPL 模式
├── 📁 config/          # 配置管理
│   └── ⚙️ config.go    # 配置加载和保存
├── 📁 llm/             # LLM 客户端
│   └── 🤖 client.go    # OpenAI 兼容 API 客户端
├── 🚀 main.go          # 程序入口
└── 📖 README.md        # 本文档
```

## 🔧 开发指南

### 🏗️ 构建项目
```bash
# 开发构建 🔨
go build -o codex

# 发布构建（优化）⚡
go build -ldflags="-s -w" -o codex

# 交叉编译（Linux）🐧
GOOS=linux GOARCH=amd64 go build -o codex-linux

# 交叉编译（macOS）🍎
GOOS=darwin GOARCH=arm64 go build -o codex-macos

# 交叉编译（Windows）🪟
GOOS=windows GOARCH=amd64 go build -o codex.exe
```

### 🔧 添加新工具
1. 📝 在 `agent/tools.go` 中添加工具定义
2. 💻 实现工具处理函数
3. 📋 在 `NewToolRegistry` 中注册工具

### 🤖 添加新提供商
1. ⚙️ 在 `config/config.go` 的 `builtinProviders` 中添加
2. 🔧 或通过配置文件自定义

### 🧪 运行测试
```bash
# 运行所有测试 ✅
go test ./...

# 运行特定包测试 🎯
go test ./agent

# 运行测试并显示覆盖率 📊
go test -cover ./...

# 运行基准测试 ⚡
go test -bench=. ./...
```

## 📝 最佳实践

1. 🎯 **明确指令**：提供具体的需求和上下文
2. 🔄 **逐步迭代**：复杂任务分步骤进行
3. ✅ **验证结果**：总是检查生成的代码
4. 📚 **使用版本控制**：Codex 修改前确保有备份
5. 🧪 **测试驱动**：先写测试，再让 Codex 实现
6. 📖 **文档优先**：让 Codex 先写文档，再写代码
7. 🔍 **代码审查**：仔细审查 Codex 生成的代码
8. 🚀 **从小开始**：从简单任务开始，逐步增加复杂度
9. 💡 **提供示例**：给 Codex 提供示例代码作为参考
10. 🔄 **反馈循环**：根据结果调整指令，优化输出

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！🎉

1. 🍴 Fork 项目
2. 🌿 创建功能分支
3. 📝 提交更改
4. 📤 推送到分支
5. 🔄 创建 Pull Request

### 🐛 报告问题
- 📋 使用 Issue 模板
- 🔍 提供复现步骤
- 📊 包含环境信息
- 💡 建议解决方案

### 🔧 提交代码
- ✅ 通过所有测试
- 📝 更新相关文档
- 🎨 遵循代码风格
- 🔍 添加适当的测试

## 📄 许可证

📜 MIT License

## 🙏 致谢

- 🤖 感谢所有支持的 AI 提供商
- 🌟 感谢开源社区的贡献
- 🚀 特别感谢 OpenAI 的 API 标准
- 💻 感谢所有使用者和贡献者
- 🎯 感谢测试和反馈的用户

## ❓ 常见问题

### 🤔 Codex 安全吗？
✅ Codex 在受控环境中执行命令，但请始终审查生成的代码。

### 💰 需要付费吗？
🆓 Codex 本身是免费的，但需要 API 密钥访问 AI 服务。

### 🚀 如何提高效率？
🎯 提供清晰的指令，分步骤进行复杂任务。

### 🔧 遇到问题怎么办？
📋 查看 Issue 页面或提交新的 Issue。

---

**提示**：Codex 是一个强大的工具，但请始终审查生成的代码，特别是在生产环境中使用前。🔍✅