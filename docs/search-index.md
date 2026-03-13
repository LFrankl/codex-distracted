# 代码搜索索引

distracted-codex 的 `semantic_search` 工具背后的索引系统。支持纯本地 BM25 搜索和向量语义搜索，通过统一接口抽象，未来可无缝切换到本地向量化。

---

## 架构概览

```
semantic_search 工具
        │
        ▼
   Searcher 接口
  ┌──────────────────────────────┐
  │  Search(ctx, query, k)       │
  │  Kind() string               │
  └──────────────────────────────┘
        │
   ┌────┴────────────────┐
   │                     │
   ▼                     ▼
BM25Index          VecSearcher
(本地，零依赖)      (向量 API + VecIndex)
                         │
                    (预留占位)
                         │
                         ▼
                  LocalVecSearcher
                  (本地 ONNX，未实现)
```

启动时优先级：**VecSearcher > BM25Index**（哪个能加载就用哪个，都有时向量优先）。

---

## Searcher 接口

**文件**：`agent/searcher.go`

```go
type Searcher interface {
    Search(ctx context.Context, query string, k int) ([]CodeResult, error)
    Kind() string
}

type CodeResult struct {
    File    string
    StartLn int
    EndLn   int
    Text    string
    Score   float32
}
```

所有搜索后端都返回 `[]CodeResult`，`semantic_search` 工具只依赖这个接口，不感知底层实现。

---

## BM25（本地搜索，默认后端）

### 原理

BM25 是改进版 TF-IDF，是搜索引擎（Elasticsearch 等）的标准排序算法。对代码搜索效果好，因为代码中的标识符、函数名、关键词都是高频且有区分性的词元。

**打分公式**：

```
score(d, q) = Σ IDF(t) × tf(t,d) × (k1+1) / (tf(t,d) + k1×(1 - b + b×|d|/avgdl))

IDF(t) = ln((N - df(t) + 0.5) / (df(t) + 0.5) + 1)

参数：k1=1.5（词频饱和）, b=0.75（文档长度归一化）
```

### 分词策略

**文件**：`agent/bm25.go` — `tokenizeCode()`

代码分词需要处理驼峰命名和下划线命名：

```
getUserById  →  getuserbyid, get, user, by, id
HTTPRequest  →  httprequest, http, request
user_profile →  user, profile
db.Connect() →  db, connect
```

步骤：
1. 按非字母数字字符切分（处理 `_` `.` `(` 等）
2. 对每个词元进行驼峰拆分（`splitCamelCase`）
3. 保留原词元 + 所有拆分子词，均转小写
4. 过滤长度 < 2 的词元，用 map 去重

### 索引结构

**文件**：`agent/bm25.go`

```go
type BM25Index struct {
    Docs   []bm25Doc          // 所有文档块
    DF     map[string]int     // 词元 → 包含该词元的文档数
    AvgLen float64            // 文档平均词元数
}

type bm25Doc struct {
    File    string
    StartLn int
    EndLn   int
    Text    string
    Freq    map[string]int    // 词元 → 在本文档中出现次数
    Len     int               // 本文档总词元数
}
```

### 构建与持久化

**文件**：`agent/bm25indexer.go`

- 每次 `/index` 全量重建（纯内存计算，无 API 调用，大型项目通常 < 5s）
- 索引序列化为 gob 格式，存储在 `~/.codex/index/<hash>/bm25.bin`
- 启动时自动加载，无需手动操作

```go
type BM25Indexer struct {
    workDir  string
    indexDir string
    out      io.Writer
}
```

**Kind()** = `"bm25"`

---

## VecSearcher（向量语义搜索，可选）

### 原理

将查询和代码块都转换为高维浮点向量（embedding），通过余弦相似度找最近邻。语义理解比 BM25 更强，可以处理概念性查询（"在哪里处理错误"即使代码里没有"错误"这个词也能找到）。

### 组件

#### VecSearcher（搜索入口）

**文件**：`agent/vecsearcher.go`

```go
type VecSearcher struct {
    index      *VecIndex
    client     *llm.Client
    embedModel string
}
```

调用 provider 的 `/embeddings` API 将查询字符串转为向量，再用 `VecIndex.Search` 做余弦相似度搜索。

**Kind()** = `"vector-api"`

#### VecIndex（向量存储与检索）

**文件**：`agent/vecindex.go`

```go
type VecIndex struct {
    Chunks []Chunk    // 所有向量化的代码块
    Dim    int        // 向量维度
}

type Chunk struct {
    File    string
    StartLn int
    EndLn   int
    Text    string
    Vec     []float32  // embedding 向量
}
```

搜索使用暴力全量余弦相似度（适合 < 10 万 chunks 的项目）：

```go
func cosineSim(a, b []float32) float32 // float64 累加避免精度损失
```

#### Indexer（增量构建）

**文件**：`agent/indexer.go`

增量更新策略：
1. 扫描所有文件，记录 mtime
2. 只对 mtime 变化的文件重新调用 embedding API
3. 删除已从磁盘消失的文件的 chunks
4. 新 chunks 追加到现有索引

embed 时为每个 chunk 前置文件名作为上下文：
```
# auth/middleware.go
func validateToken(token string) (*Claims, error) {
    ...
}
```

批次大小：20 个 chunk/次 API 调用。

```go
type IndexMeta struct {
    Provider   string
    EmbedModel string
    Dim        int
    FileMtimes map[string]int64  // relPath → unix mtime，用于增量判断
    IndexedAt  time.Time
}
```

**Kind()** = `"vector-api"`

---

## LocalVecSearcher（占位，未实现）

**文件**：`agent/vecsearcher.go`

```go
type localVecSearcher struct {
    index     *VecIndex
    modelPath string   // .onnx 模型路径
}
```

当前调用 `Search` 会返回错误。实现路径：

1. 引入 ONNX Runtime Go 绑定（如 `github.com/yalue/onnxruntime_go`）
2. 下载小型 embedding 模型（`all-MiniLM-L6-v2` ≈ 22MB，`nomic-embed-code` 更适合代码）
3. 首次 `/index` 时自动下载模型到 `~/.codex/models/`
4. 替换 `VecSearcher` 中的 API 调用为本地推理
5. 将 `localVecSearcher` 导出为 `LocalVecSearcher`，在 `cmd/root.go` 中接入

**Kind()** = `"vector-local"`（规划中）

---

## 文件分块策略

**文件**：`agent/chunker.go`

| 文件大小 | 策略 |
|---------|------|
| ≤ 80 行 | 整文件作为一个 chunk |
| > 80 行 | 每 60 行一个 chunk，相邻重叠 15 行 |

重叠的目的：防止跨 chunk 边界的函数/逻辑被截断搜不到。

**跳过规则** — 不索引以下内容：
- 隐藏路径（`.git`、`.cache` 等）
- 生成文件（`*.pb.go`、`*_gen.go`、`*.generated.go`）
- 压缩资源（`*.min.js`、`*.min.css`）
- 目录：`node_modules`、`vendor`、`dist`、`build`、`__pycache__`、`.next`

**支持的扩展名**：`.go .ts .tsx .js .jsx .py .rb .java .kt .swift .c .cpp .h .hpp .rs .cs .php .scala .clj .sh .bash .yaml .yml .json .toml .ini .xml .html .css .scss .sass .md .txt .rst .sql .graphql .proto .vue .svelte` 以及无扩展名文件（Makefile、Dockerfile 等）

---

## 存储布局

所有索引数据存在 `~/.codex/index/` 下，按项目路径哈希隔离：

```
~/.codex/index/
└── a3f2b1c0/              ← SHA-256(workDir)[:8]
    ├── bm25.bin           ← BM25 索引（gob 编码）
    ├── chunks.bin         ← 向量索引（gob 编码，仅在配置 embed_model 后存在）
    └── meta.json          ← 向量索引元数据（provider、model、文件 mtime 记录）
```

不同项目互不干扰，同一项目的所有会话共享同一份索引。

---

## 生命周期

### 启动时

```
cmd/root.go
│
├── NewBM25Indexer(workDir)
│     HasIndex? → LoadIndex → ag.SetBM25(bi)
│     [打印: BM25 index loaded — N chunks]
│
└── (若 embed_model 已配置) NewIndexer(workDir, ...)
      HasIndex? → LoadIndex → ag.SetRAG(vi, embedModel)
      [打印: vector index loaded — N chunks]
      注：向量索引会覆盖 BM25，成为活跃 Searcher
```

### 运行 `/index` 时

```
BM25Indexer.Run(ctx)         ← 总是执行，全量重建，无 API 调用
  → 扫描文件 → 分词 → buildBM25 → 保存 bm25.bin
  → ag.SetBM25(bi)

(若 embed_model 已配置)
Indexer.Run(ctx, force)      ← 增量，只 embed 变更文件
  → 扫描 mtime → 调 /embeddings API → 更新 chunks.bin + meta.json
  → ag.SetRAG(vi, embedModel)  ← 覆盖 BM25，向量生效
```

### `semantic_search` 工具被调用时

```
ToolRegistry.semanticSearch(argsJSON)
  → r.rag.searcher.Search(ctx, query, k)
       │
       ├── BM25Index.Search  → tokenizeCode(query) → BM25 打分 → []CodeResult
       └── VecSearcher.Search → client.Embed(query) → cosineSim → []CodeResult
  → 格式化输出：### file  lines X–Y  (score N.NN)\n\ntext
```

---

## 接入新 Searcher 后端

只需实现两个方法：

```go
type MySearcher struct { ... }

func (s *MySearcher) Search(ctx context.Context, query string, k int) ([]CodeResult, error) {
    // 任意搜索逻辑
}

func (s *MySearcher) Kind() string { return "my-backend" }
```

然后在 `cmd/root.go` 中：

```go
ag.SetSearcher(agent.NewMySearcher(...))
```

`semantic_search` 工具和 agent 的其他部分完全不需要改动。
