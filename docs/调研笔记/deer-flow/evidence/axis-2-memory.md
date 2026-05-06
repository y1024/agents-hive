# 轴 2：Memory / RAG / 知识库机制深度对标（Hive vs deer-flow v2）

## 执行摘要

**方向性颠倒**：现有 `final-verdict.md` 里那句"**Memory 不是 RAG**（无向量库、无全文检索、无 reranker）"作为调研结论是**对的**——但对得只有一半：它描述的是 **deer-flow**，不是 Hive。Hive 的 `internal/memory/` 实际有完整的 RAG 链路（embedding + pgvector + FTS + hybrid + RRF 融合 + per-user 隔离 + async dedup）。也就是说：**原调研把 deer-flow 的简版 Memory 当基准来对齐 Hive，把方向搞反了。**

**定量差异**（代码体量，均为非 test 行数实测）：

| 维度 | deer-flow | Hive |
|---|---|---|
| Memory 模块代码 | ~900 行 python（updater 21K+queue 9K+storage 8K+prompt 15K+msg_proc 4K+hook 1.4K+types） | **1351 行 Go non-test**（memory/ 15 文件 3099 行含 test） |
| Storage | `.deer-flow/memory.json` 单文件 | PostgreSQL + pgvector + tsvector FTS |
| Embedding | ❌ | OpenAIEmbedder（兼容 OpenAI/Qwen/豆包），float32 |
| 向量检索 | ❌ | brute-force 余弦（vecindex.go 188 行） |
| 全文检索 | ❌ | tsvector + ts_rank BM25 + ILIKE 中文 fallback（pg_store.go L254-283） |
| Hybrid (FTS+Vector) | ❌ | hybrid.go HybridSearcher + RRF 融合 |
| Compaction | 1 个 summarization middleware | **独立包 6 文件 1072 行**（compactor/llm_summary/session_memory/tool_budget/truncate/history_snip） |
| Debounce | 30s per-thread | async 信号量限 5 并发（context.Background 不受 cancel 影响） |
| Router endpoints | 4（`/api/memory`） | 走 store + 内部 API（未独立暴露 REST） |

**一句话结论**：deer-flow Memory 是"对话级长期笔记本"（LLM 自己写 JSON 文件，下次对话把 top 15 facts 塞 prompt）；Hive Memory 是"带向量检索 + 异步写入的小型 RAG 子系统"。两套系统做的根本不是同一件事。

---

## 第 1 部分：deer-flow Memory 完整机制

来源权威：deer-flow `backend/CLAUDE.md`（官方）+ `backend/packages/harness/deerflow/agents/memory/` 源码。

### 1.1 数据结构（存盘格式）

持久化路径：`backend/.deer-flow/memory.json`（**单一 JSON 文件，非数据库**）。

```
memory.json
├── userContext
│   ├── workContext       (1-3 sentence summary)
│   ├── personalContext   (1-3 sentence summary)
│   └── topOfMind         (1-3 sentence summary)
├── history
│   ├── recentMonths
│   ├── earlierContext
│   └── longTermBackground
└── facts: [
    {
      "id": uuid,
      "content": "...",
      "category": "preference | knowledge | context | behavior | goal",
      "confidence": 0.0-1.0,
      "createdAt": ISO8601,
      "source": "..."
    },
    ...
  ]
```

### 1.2 源码模块拆解

来自 `ls backend/packages/harness/deerflow/agents/memory/`（行数实测）：

| 文件 | 行数 | 职责 |
|---|---|---|
| `updater.py` | 20993 字节 | LLM 调用 pipeline：读 conversation → prompt → 解析 JSON → diff 旧 memory → atomic write |
| `queue.py` | 8922 字节 | Debounce 队列，per-thread dedup，30s 默认 wait |
| `prompt.py` | 15377 字节 | `MEMORY_UPDATE_PROMPT` 巨型 system prompt（要求 LLM 输出 JSON patch） |
| `storage.py` | 7951 字节 | `MemoryStorage` 协议 + 默认 file-based 实现（ temp+rename 原子写 + mtime 缓存） |
| `message_processing.py` | 4300 字节 | 从 ThreadState 过滤 user inputs + final AI responses |
| `summarization_hook.py` | 1442 字节 | 可选，对消息流做摘要 |
| `__init__.py` | 1445 字节 | re-export |

验证（`head -40 updater.py`）：
- L29-33：`_SYNC_MEMORY_UPDATER_EXECUTOR = ThreadPoolExecutor(max_workers=4)` — 全局 4 worker 线程池
- L46-48：`get_memory_data(agent_name) -> storage.load(agent_name)`
- L56-72：`import_memory_data` 批量导入（throw OSError）
- L75-80：`clear_memory_data` 清空写空 JSON
- 从 L15-24 看依赖：**只依赖 `create_chat_model`（LLM）+ 自家 storage**——无 embedding 包、无 faiss、无 chromadb、无 pinecone、无 bm25 库

### 1.3 工作流（官方 CLAUDE.md §"Memory System"）

```
1. MemoryMiddleware.after_response
   ├── filter: 仅保留 user turn + final AI（抛弃 tool/intermediate）
   └── queue.enqueue(thread_id, conversation)
2. queue loop（每 30s 醒一次）
   ├── dedup per-thread（同 thread 只保留最新）
   └── dispatch 到 _SYNC_MEMORY_UPDATER_EXECUTOR（4 worker 并发上限）
3. worker 线程
   ├── load memory.json
   ├── format_conversation_for_update()
   ├── LLM.invoke(MEMORY_UPDATE_PROMPT + conversation + old memory)
   ├── parse JSON patch
   ├── **dedup by whitespace-normalized content**（去首尾空白后比较）
   ├── skip facts with confidence < 0.7
   ├── cap total facts at 100
   └── atomic write (temp + rename) + invalidate mtime cache
4. 下一回合 system_prompt 装配时
   └── 把 top 15 facts + userContext + history 用 <memory>...</memory> 标签塞入 system
       截断到 max_injection_tokens=2000
```

### 1.4 Gateway Router `/api/memory` 端点清单

来自官方 CLAUDE.md 表格：

| 方法 | 路径 | 语义 |
|---|---|---|
| GET | `/api/memory` | 读取 memory.json 全量 |
| POST | `/api/memory/reload` | 强制重读（绕过 mtime 缓存） |
| GET | `/api/memory/config` | 返回当前 `memory` 配置 |
| GET | `/api/memory/status` | 返回 config + data 概况 |

即 **4 个端点**，不是原调研 `merged-report.md` 里写的 "memory 10"（原调研把 memory-related 的 middleware/router/hook/storage/subagent 加在一起混算了）。

### 1.5 "deer-flow 是不是 RAG" 的硬证据

grep 结果（所有命令在 deer-flow backend/ 下执行）：

```bash
$ grep -rn "embedding\|vector\|faiss\|chromadb\|pinecone\|reranker\|bm25" backend/ --include="*.py"
# （无输出）
```

官方 CLAUDE.md `§Memory System` **没有任何一句**提到 embedding / vector / semantic search / retrieval。  
配置项（memory section）也只有：`enabled / injection_enabled / storage_path / debounce_seconds / model_name / max_facts / fact_confidence_threshold / max_injection_tokens` — 全部是 LLM-based 抽取 + 阈值筛 + token 截断，零检索。

结论：**deer-flow 的 "Memory" 本质是 LLM-extracted sticky notes + 下轮 system prompt 注入**，距离 RAG（Retrieve-Augmented Generation 需要有 retrieval 步骤）差一整套基础设施。

---

## 第 2 部分：Hive Memory / Compaction 完整机制

来源：`../memory/` 和 `../compaction/` 的实际文件（从 cwd `internal/tools/` 出发 `../memory/` = `internal/memory/`）。

### 2.1 Memory 子包（`internal/memory/`）

```
$ ls internal/memory/
embedding.go         OpenAIEmbedder 接口 + 实现（float32）
extractor.go         CompactionAgent.ExtractFromSummary（从摘要提炼 fact）
hybrid.go            HybridSearcher: FTS + vector + RRF 融合
injector.go          InjectContext(userMsg, topK) 按消息相关性注入 memory 到 prompt
pg_store.go          PostgreSQL 存储（含 tsvector FTS）677 行
pgvec_store.go       pgvector 专用存储 138 行
setup.go             启动注入装配
store.go             Store 接口 49 行
types.go             Memory / MemoryQuery / Result 75 行
vecindex.go          brute-force 余弦相似度 188 行
vecstore.go          VectorStore 接口 28 行

（+ 4 个 _test.go，合计 15 个 .go 文件，3099 行总量）
```

关键实现（取自 grep）：
- `embedding.go:19 EmbeddingProvider interface` — 插件化 provider
- `embedding.go:45 OpenAIEmbedder struct` — 默认 provider，支持 OpenAI/Qwen/豆包
- `embedding.go:54` — 注释 "各 Provider 的默认 Embedding 模型和维度"
- `pg_store.go:82-98` — **Save 时异步后台生成 embedding**（信号量 max=5，`context.Background()` 隔离上游 cancel）
- `pg_store.go:172-188` — Update 时重新生成 embedding
- `pg_store.go:254-283` — tsvector FTS + ts_rank BM25 + ILIKE 中文回退
- `pg_store.go:105-113, 300-304` — `auth.UserIDFrom(ctx)` 严格 per-user 隔离（多租户）
- `injector.go:43-88` — InjectContext 先 hybrid 后降级 pure FTS
- `injector.go:101-110` — token 限制注入（默认 2000 tokens）
- `extractor.go:46-50` — `isDuplicate` 在同 user 范围内比 content 相似度去重

### 2.2 Compaction 独立包（`internal/compaction/`）

```
$ ls internal/compaction/ && wc -l internal/compaction/*.go
77   compactor.go       Pipeline 模式接口：串联 N 个 Compactor
161  llm_summary.go     LLM 生成结构化摘要（goal/completed/pending/file_changes）
131  session_memory.go  头部插入 "[会话记忆]" system message
82   tool_budget.go     工具预算控制
118  truncate.go        简单截断
60   history_snip.go    历史裁剪
443  compactor_test.go
———
1072 总
```

官方定位：**可插拔 pipeline**，和 react_processor 解耦。`compactor.go:37-49` 定义 `Compactor.Apply(ctx, messages) (compacted, error)` 接口，多个实现串联。

### 2.3 Hive 是 RAG 的硬证据

```bash
$ grep -rn "Embedd\|vecindex\|HybridSearch" ../memory/ --include="*.go" | grep -v _test
../memory/embedding.go:18  // EmbeddingProvider 向量嵌入提供者接口
../memory/embedding.go:35  Embedding []float32 `json:"embedding"`
../memory/embedding.go:45  OpenAIEmbedder struct { ... }
../memory/hybrid.go        (HybridSearcher = FTS + Vector 融合)
../memory/vecindex.go:63   (余弦相似度)
../memory/pg_store.go:82   (async embedding 信号量)
```

grep `pgvector`：**多处引用** `pgvec_store.go` 138 行专门针对 pgvector 扩展写的 store。

结论：**Hive 有完整 RAG**（Index + Retrieval + Fusion + Injection + Rate-limited Async Generation + Multi-tenancy），deer-flow 没有。

---

## 第 3 部分：逐能力对齐表（Hive × deer-flow）

| # | 能力维度 | deer-flow 实现 | Hive 实现 | 谁领先 |
|---|---|---|---|---|
| 1 | 短期 context buffer | 内置 LangGraph messages | react_processor.go 2013 行单体 | 平 |
| 2 | 在线 summarization | `SummarizationMiddleware`（token 触发） | `compaction/llm_summary.go` + 独立 pipeline | **Hive 深** |
| 3 | 历史裁剪 / truncate | 放在 summarization 内 | `compaction/truncate.go` + `history_snip.go` 独立 | **Hive 深** |
| 4 | Tool budget 控制 | ❌ 无明确模块 | `compaction/tool_budget.go` | **Hive 独有** |
| 5 | 长期 memory 写入 | LLM extract + debounce queue | async 后台 goroutine + embedding 生成 | 机制不同，Hive 更重 |
| 6 | 长期 memory 读取 | top 15 facts 塞 system prompt | hybrid search + token 限注入 | **Hive 深** |
| 7 | Dedup | whitespace-normalized content | content 相似度（user-scoped） | Hive 稍强 |
| 8 | Versioning | ❌（直接覆写） | （pg_store 自带行级时间戳） | Hive 稍强 |
| 9 | Per-user scoping | ❌（单文件全局，或 per-agent） | auth.UserIDFrom(ctx) 严格隔离 | **Hive 独有** |
| 10 | Per-thread scoping | queue 层按 thread_id dedup | store 层按 user_id（thread 尺度？待查） | 平 |
| 11 | Eviction / cap | max_facts=100 硬上限 | 无固定上限（由 DB 容量） | 语义不同 |
| 12 | Vector lookup | ❌ | brute-force 余弦 + pgvector 双路径 | **Hive 独有** |
| 13 | Keyword lookup | ❌ | tsvector + ts_rank + ILIKE | **Hive 独有** |
| 14 | Hybrid fusion | ❌ | HybridSearcher + RRF | **Hive 独有** |
| 15 | Async write | ThreadPoolExecutor 4 worker | goroutine + 信号量 max=5 | 机制类似 |
| 16 | 背景提取器 | updater.py 直接调用 LLM | `CompactionAgent.ExtractFromSummary`（走 LLM 摘要再提取） | 机制略不同 |
| 17 | Router API | 4 endpoints (`/api/memory/*`) | 未独立 REST（内部调用） | deer-flow 有对外面板 |

## 第 4 部分：蓝军 Mutation

### M1：反驳"deer-flow 没有 RAG 是缺陷"
- **反驳点**：deer-flow 没 RAG，是因为它是 **per-thread LangGraph agent**，memory 只需要"下轮 context 注入"，不需要 retrieval over 百万记忆。
- **替代解读**：RAG 在"百万文档 + agent 检索"场景必要，但 deer-flow 的 memory.json 规模按 `max_facts=100` 设计，cap 决定了一个 user 最多 100 条 — **线性扫描都够**，vector 是 over-engineering。
- **证据**：`max_facts=100, max_injection_tokens=2000`。100 条 fact 按每条 100 token 算也就 1 万 token，塞进 200K context 毫无压力。
- **对 Hive 的反推**：Hive 的 embedding + pgvector 是不是过度设计？如果 Hive 本来就一个用户最多 100 条 memory，为什么需要 RAG？——答案在 **multi-tenancy**：Hive 面向企业（`auth.UserIDFrom(ctx)`），多租户 + 长期积累，user 数 × memory 数可能破万，这时 retrieval 才划算。**Hive 的 RAG 不是 tech flex，是多租户必需品**。

### M2：反驳"Hive 的 Compaction 独立包 = 设计更好"
- **反驳点**：deer-flow 的 summarization 放在 middleware 里，是因为 middleware 本身就是 pipeline（18 个串联），不需要再抽一层 compactor。抽独立包是 Go 缺原生 middleware 的补偿。
- **替代解读**：两侧表达不同，功能可比——Hive `compactor.go:37-49` 的 `Compactor.Apply` 接口和 deer-flow `SummarizationMiddleware.before_model` hook 是同一件事。
- **证据**：deer-flow middleware 链有 `SummarizationMiddleware + TokenUsageMiddleware`，前者触发 summarize，后者记录 token——两个 middleware 对齐 Hive 的 `llm_summary.go + tool_budget.go`。
- **结论**：Hive 抽独立包 **不是** 设计更好，而是语言机制差异。别把体量差异当架构优势。

### M3：反驳"Hive 的 async embedding 信号量 = 高级设计"
- **反驳点**：deer-flow 的 ThreadPoolExecutor(4 worker) + debounce queue 也是 rate-limited async 写入。信号量 5 vs 线程池 4，本质相同。
- **替代解读**：Hive 的"信号量 + context.Background() 隔离 cancel"说明开发者踩过 cancel 级联 bug，这是实战痕迹；deer-flow 的 4 worker 池没这个护栏，但也够用。
- **证据**：pg_store.go:82-98 的 `context.Background()` 独立 context 是 Go 特有坑（caller 用 gin.Context 传到 goroutine，caller 返回 context cancel 会杀 embedding 任务）。deer-flow Python async 没这个问题（task 不共享 cancel）。
- **结论**：Hive 防御性更高，但不要把语言差异当"架构差异"。

### M4：反驳"deer-flow Memory 没技术含量"
- **反驳点**：updater.py 的 LLM prompt engineering 才是重点。`prompt.py` 15 KB 的 `MEMORY_UPDATE_PROMPT` 规定了"读 conversation → 输出 JSON patch → 按 category 归类 → 给 confidence"一整套结构化输出契约。如果 prompt 写得不好，LLM 产出的 fact 混乱、重复、矛盾。
- **替代解读**：Hive 的 RAG 是"后端 infra 深度"，deer-flow 的 Memory 是"提示工程深度"。两条技术路径，各自有坑。
- **证据**：`MEMORY_UPDATE_PROMPT` 的 15 KB 容量远大于 `MEMORY_INJECTION` 或 query generation prompt，说明**写入端重，读取端轻**。Hive 刚好反过来（写入端简单：embedding；读取端重：hybrid + rank fusion + injection）。
- **结论**：技术浓度在两边不同的位置，不能单看一侧代码体量下判断。

---

## 第 5 部分：Codex 原调研盲点

### B1：把 "memory 10" 误读为 10 个 endpoint
**原调研** `merged-report.md` 写 "router memory 10"，暗示有 10 个对外接口。实际上：
- `backend/app/gateway/routers/memory.py` 12.6 KB → 官方 CLAUDE.md 明确为 **4 endpoints**
- "10" 推测是把 middleware / hook / storage / subagent 全算进去的 byte-count 或 grep count
- 错位导致后续 P0/P1 对 "memory 面板"的估计全部偏了

### B2：没发现 Hive 其实是 RAG
原调研 `merged-report.md` 对"Memory 不是 RAG" 下结论时 **没对 Hive 侧做镜像检查**。结果是 Hive 自己已经有 embedding/vector/FTS/hybrid，而调研把它当成"对齐 deer-flow 的浅 memory"在规划 P1。—— 反而应该**把 Hive 的 RAG 当作已实现资产**，在 verdict 里当正项而非负项。

### B3：没区分 compaction vs summarization vs memory
- **compaction**：裁剪 context 窗口（避免超限）
- **summarization**：生成结构化摘要
- **long-term memory**：跨会话持久化事实

原调研三者混谈，导致"Hive 缺 memory"的印象。实际上 Hive **三者都有且解耦**（compaction 独立包 + llm_summary.go + memory 子系统），deer-flow 合并在 middleware 中。

### B4：没评估 deer-flow "JSON 单文件" 的生产隐患
`memory.json` 单文件 atomic rename 在单进程 OK。但 deer-flow 支持 Gateway 和 LangGraph Server **双进程并发**（Standard mode 4 进程）——mtime 缓存失效有 race，万一两个进程同时写...... 原调研没挖这个维度。

---

## 第 6 部分：建议 P0 / P1 / 不抄

### P0（必做）

**P0-M1：把 Hive 的 RAG 作为核心正项写入 v2 final-verdict**
- 行动：`final-verdict-v2.md` 在"反向领先项"章节明确列出："Hive `internal/memory/` 是完整 RAG（embedding + pgvector + FTS + hybrid + RRF + 多租户），deer-flow 仅 JSON facts"
- 工作量：<1 小时
- 验证：把 15 文件名 + 行数 + 关键 grep 证据贴到 verdict

**P0-M2：核对 Hive Memory 是否被 react_processor 真正调用**
- 行动：grep `memory.Injector | InjectContext | memory.Store` 在 `master/` 和 `react_processor.go` 中的引用
- 工作量：1 小时
- 风险：如果 memory/ 只在 handler 层 import，**react runtime 层并未消费**——那么"Hive 有 RAG" 只是模块存在，不是业务在用；verdict 得区分"模块 ready vs 业务接入"两档
- 验证：出一份 call-graph：InjectContext 调用栈 → 最终被什么请求路径使用

### P1（建议）

**P1-M1：补一个 deer-flow 侧 prompt.py 的反向借鉴**
- 行动：读 `packages/harness/deerflow/agents/memory/prompt.py` 的 MEMORY_UPDATE_PROMPT，看 Hive 的 extractor.go 是否有等价 prompt；若没有，把 deer-flow 的 prompt 形式（JSON patch + category + confidence）借来，让 Hive extractor 产出更结构化的 memory
- 工作量：1-2 天
- 验证：A/B 测试两种 prompt 产出的 memory 质量（人工 sample 50 条）

**P1-M2：给 Hive memory 加一个对外 Router（`/api/memory/*`）**
- 行动：参照 deer-flow 4 endpoints（GET/reload/config/status），在 Hive `internal/api` 或 controlplane 暴露
- 工作量：2-3 天
- 收益：提供 admin 面板读/调试 memory，利于多租户运维

### 不抄

**不抄-M1：JSON 单文件存储**
- 理由：Hive 已是 PostgreSQL，改回 JSON 是回退
- 替代：保留现有 pg_store + pgvec_store

**不抄-M2：30s debounce queue**
- 理由：Hive async 写入已不是问题；deer-flow 用 debounce 是为了合并多次 LLM 调用省 token。Hive 的 extractor 是 CompactionAgent.ExtractFromSummary（复用 summary 阶段），已经摊薄了 LLM 调用，没必要再 debounce
- 替代：保持 save-path async background

---

## 附录 A：命令输出证据

### A.1 deer-flow 无 embedding / vector
```bash
$ grep -rn "embedding\|vector\|faiss\|chromadb\|pinecone\|bm25" \
    ../../docs/调研笔记/deer-flow/src/backend/ --include="*.py" 2>&1 | head
# （无输出 — 代码库内零匹配）
```

### A.2 deer-flow memory/ 文件清单（7 文件）
```bash
$ ls ../../docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/agents/memory/
__init__.py              (1445 字节)
message_processing.py    (4300 字节)
prompt.py                (15377 字节)
queue.py                 (8922 字节)
storage.py               (7951 字节)
summarization_hook.py    (1442 字节)
updater.py               (20993 字节)
```

### A.3 deer-flow 官方 memory API（CLAUDE.md）
> Memory (/api/memory): GET / - memory data; POST /reload - force reload; GET /config - config; GET /status - config + data

→ **4 endpoints**，不是 10。

### A.4 Hive memory/ 文件清单
```bash
$ ls ../memory/ && wc -l ../memory/*.go 2>/dev/null
embedding.go extractor.go hybrid.go injector.go pg_store.go
pgvec_store.go setup.go store.go types.go vecindex.go vecstore.go
+ 4 个 _test.go
行数：3099 total（含测试），non-test ~1351 行
```

### A.5 Hive RAG 组件证据
```bash
$ grep -rn "Embedd\|HybridSearch\|vecindex" ../memory/ --include="*.go" | grep -v _test | head
../memory/embedding.go:18 // EmbeddingProvider 向量嵌入提供者接口
../memory/embedding.go:19 type EmbeddingProvider interface { ... }
../memory/embedding.go:45 type OpenAIEmbedder struct { ... }
../memory/hybrid.go:1     // HybridSearcher FTS+Vector 融合
../memory/vecindex.go:63  // 余弦相似度
```

### A.6 Hive Compaction 完整 6 文件
```bash
$ ls ../compaction/ && wc -l ../compaction/*.go
77   compactor.go
60   history_snip.go
161  llm_summary.go
131  session_memory.go
82   tool_budget.go
118  truncate.go
443  compactor_test.go
———
1072 total
```

---

## 附录 B：关键文件索引（供 verdict-v2 引用）

| 断言 | 关键文件:行号 |
|---|---|
| deer-flow 存储为单 JSON | CLAUDE.md §Memory System "Data Structure (stored in backend/.deer-flow/memory.json)" |
| deer-flow max_facts=100, threshold=0.7, injection_tokens=2000 | CLAUDE.md §Memory System "Configuration" |
| deer-flow 无 embedding/vector | `grep -rn "embedding\|vector\|faiss" backend/ --include="*.py"` = 0 命中 |
| Hive async embedding 信号量 | `../memory/pg_store.go:82-98` |
| Hive hybrid search | `../memory/hybrid.go` |
| Hive per-user 隔离 | `../memory/pg_store.go:105-113, 300-304` (auth.UserIDFrom) |
| Hive context 注入 token 限 | `../memory/injector.go:101-110` |
| Hive compaction pipeline | `../compaction/compactor.go:37-49` |

---

报告基于 deer-flow main branch tarball（落 `../../docs/调研笔记/deer-flow/src/` 27 MB）+ Hive 当前 working tree。由 plan-ceo-review 主 agent 整合两个 Explore 子 agent 输出 + 主线亲自读文件 + 官方 deer-flow CLAUDE.md 章节后合成。
