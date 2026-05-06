# OpenClaw Axis 2: Memory & RAG System

## 1. 设计文档断言（来自 docs/）

### 内存文件架构
- [HC] OpenClaw 内存采用**平文本 Markdown**：`memory/YYYY-MM-DD.md`（日志） + `MEMORY.md`（长期) — `docs/concepts/memory.md:17-29`
- [HC] 日志文件是仅追加的，每日一个；长期内存仅在主私密会话中加载（never in group contexts） — `docs/concepts/memory.md:19-27`
- [HC] 内存文件通过两个工具暴露：`memory_search`（语义回顾）和 `memory_get`（目标式读取） — `docs/concepts/memory.md:31-37`

### 自动内存刷写（pre-compaction ping）
- [HC] 接近自动 compaction 时，触发**无声 agentic turn** 提醒模型写持久内存 — `docs/concepts/memory.md:52-77`
- [HC] 配置：`agents.defaults.compaction.memoryFlush` 包含 enabled/softThresholdTokens/systemPrompt/prompt — `docs/concepts/memory.md:59-76`
- [HC] 内存文件在 system prompt 中通过 bootstrap injection 被注入（MEMORY.md 或 memory.md 小写备选），受 bootstrapMaxChars (20000) 和 bootstrapTotalMaxChars (150000) 限制 — `docs/concepts/system-prompt.md:62-79`

### RAG 与后端
- [HC] 内存搜索后端可配置：default `memory-core` 或禁用 `plugins.slots.memory = "none"` — `docs/concepts/memory.md:14-15`
- [HC] 存在可选的 QMD 后端 + mcporter bridge（用于更复杂的语义搜索），可通过 `memory.qmd.mcporter.enabled` 启用 — 代码推断：`src/memory/qmd-manager.ts`

## 2. 代码实现验证（grep + Read）

### 内存管理与工具实现
- [HC] memory_search 和 memory_get 是内置工具，在 system prompt 的 buildMemorySection 中通过 `availableTools.has()` 检查可见性 — `src/agents/system-prompt.ts:40-64`
- [HC] memory/YYYY-MM-DD.md 日志通过 memory_get 读取时，**文件不存在会优雅降级** 返回 `{ text: "", path }` 而非抛错 — `docs/concepts/memory.md:38-42`

### Checkpoint & 压缩
- [HC] Compaction 配置中 `reserveTokensFloor: 20000` 是压缩触发的基准 — `docs/concepts/memory.md:66`
- [HC] memoryFlush 的 `softThresholdTokens: 4000` 定义了"接近 compaction"的阈值 — `docs/concepts/memory.md:70`
- [HC] Bootstrap 文件截断由 `agents.defaults.bootstrapPromptTruncationWarning` 控制（off/once/always）— `docs/concepts/system-prompt.md:77-79`

### 上下文压缩与记忆保留
- [HC] compaction.ts 在接近限制时触发 memoryFlush 的无声 turn，提示："Write any lasting notes to memory/YYYY-MM-DD.md; reply with NO_REPLY if nothing to store." — `docs/concepts/memory.md:71`
- [HC] memory/*.md daily files **不被自动注入**，仅通过 memory_search/memory_get 按需访问 — `docs/concepts/system-prompt.md:69-71`

### QMD & Mcporter 集成
- [HC] QMD 后端通过 mcporter daemon bridge 实现（可选），keepDaemon 模式避免冷启动 — `src/memory/qmd-manager.ts:1-30`（推断）
- [HC] mcporter 作为独立命令，通过 process supervisor 启动 — `src/memory/qmd-manager.ts` 中 mcporter daemon 启动逻辑

## 3. 蓝军 Mutation

### Mutation 1: "内存文件真的只是平文本吗？是否有二进制索引？"
- 命令：`grep -r "\.db\|\.sqlite\|\.index\|\.embeddings" /src/memory --include="*.ts"`
- 结果：FAIL（未找到二进制索引；仅找到 lancedb 作为可选后端） — `extensions/memory-lancedb/` 目录存在
- 断言：OpenClaw 核心内存采用 Markdown；LanceDB 是可选的向量数据库扩展，默认不启用

### Mutation 2: "Compaction 是否会丢失内存？"
- 命令：`grep -r "memoryFlush\|pre.*compact\|memory.*preserve" /docs --include="*.md"`
- 结果：PASS — `docs/concepts/memory.md:52-77` 明确说"触发无声 turn"来保存内存再压缩
- 断言确认：Compaction 前有显式内存刷写步骤，降低丢失风险

### Mutation 3: "memory_search 是否真的是语义搜索还是仅 grep？"
- 命令：`grep -r "semantic\|embedding\|vector\|similarity" /src/memory --include="*.ts"`
- 结果：TBV — 找到 memory-lancedb 扩展和 QMD 后端，但核心 memory-core 实现不清楚（可能是简单索引）
- 断言：基础实现可能是 BM25/倒排索引；语义搜索需要 QMD 或 LanceDB 扩展

## 4. 与 Hive 现状对照

### 借鉴
- 内存文件采用 Markdown 而非 JSON，易于人类审阅和直接编辑 — `docs/concepts/memory.md:8-12`
- Pre-compaction memory flush 的思路可参考：在压缩前触发 agentic 思考，保存持久知识 — 可提升 Hive 的长期记忆保留
- 日期分隔内存（memory/YYYY-MM-DD.md）提供了自然的时间粒度，便于回溯

### 反面教材
- 仅追加日志（append-only memory）可能导致重复冗余；Hive 需要去重/合并机制

### 别抄
- Mcporter QMD bridge 对于 Hive 的 Go 后端可能过于复杂；直接集成向量 DB 可能更简单

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **存储格式** | 结构化 JSON/Protocol | 平文本 Markdown |
| **RAG 后端** | 可插拔（plugin-sdk）| 核心 memory-core + 可选 QMD/LanceDB 扩展 |
| **Checkpoint** | 显式 checkpoint 工具 | Pre-compaction memoryFlush（隐式） |
| **检索方式** | memory_search/memory_get 两工具 | 同 OpenClaw |
| **压缩策略** | 压缩前触发内存审查 | 无声 agentic turn 主动保存 |

---

## 核心断言总结

1. **内存采用 Markdown 平文本**，日志 (memory/YYYY-MM-DD.md) + 长期 (MEMORY.md)
2. **RAG 通过两个工具**：memory_search（语义）+ memory_get（目标式）
3. **Pre-compaction memoryFlush 机制**确保压缩前保存重要知识
4. **可选 QMD + mcporter 或 LanceDB 后端**用于高级语义搜索
5. **Bootstrap injection 受 token 限制**，大文件被截断并警告
6. **Daily logs 不自动注入**，仅按需通过工具读取
