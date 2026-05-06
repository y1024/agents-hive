# OpenClaw Axis 2: Memory — 主线程逐条核实

> **核实方法**：L3+L4 — Read docs/concepts/memory.md 全文 + grep 配置 schema + grep 代码层
> **日期**：2026-04-25

---

## 验证总览

| 类型 | 总数 | [VERIFIED] | [REVISED] | [FALSE] |
|---|---|---|---|---|
| 文档断言 (A6-A13) | 8 | 7 | 1 | 0 |
| 代码断言 (B12-B15) | 4 | 3 | 1 | 0 |
| 蓝军 mutation (M1-M3) | 3 | 3 | 0 | 0 |

---

## §1 关键 [VERIFIED]（不重述细节）

- **A6** 平文本 Markdown ✓ — `docs/concepts/memory.md:11-13` "memory is plain Markdown ... files are the source of truth"
- **A7** 日志仅追加 + 每日一个 ✓
- **A8** 两工具 memory_search + memory_get ✓
- **A9** ⭐ Pre-compaction memoryFlush ✓ — **三层证据全过**：
  - 文档：`docs/concepts/memory.md:52-91` 完整说明
  - Schema：`src/config/zod-schema.agent-defaults.ts:110-113`
  - 测试：`src/config/config.compaction-settings.test.ts:20-45`
- **A10** 配置字段 enabled/softThresholdTokens/systemPrompt/prompt ✓
  - **新发现**：还有 `forceFlushTranscriptBytes` 字段（schema.labels.ts:480 确认）
- **A11** bootstrapMaxChars=20000 / bootstrapTotalMaxChars=150000 ✓
- **A12** memory-core 默认 + plugins.slots.memory="none" 禁用 ✓
- **B12** memory_get 优雅降级返回 `{ text: "", path }` ✓
- **B13** reserveTokensFloor=20000 ✓
- **B14** softThresholdTokens=4000 ✓

---

## §2 [REVISED]

### A13 [REVISED] QMD + mcporter bridge

**原断言**："存在可选的 QMD 后端 + mcporter bridge（用于更复杂的语义搜索）"

**核实**：`src/memory/qmd-manager.ts` **未在 ls 输出中找到**。但 `src/memory/` 实际有 30+ 文件，是个庞大的 embedding 系统：
- batch-embedding-common.ts / batch-gemini.ts / batch-openai.ts / batch-voyage.ts / batch-runner.ts ...
- embedding-chunk-limits / embedding-input-limits / embedding-vectors / embeddings-debug ...

**修正后真相**：OpenClaw **memory 实际有完整 5-provider embedding 栈**（不是"平文本+可选 LanceDB"）：
- **5 个 embedding provider 自动选择**：local（node-llama-cpp）→ openai → gemini → voyage → mistral
- **sqlite-vec 加速向量搜索**（"Uses sqlite-vec (when available) to accelerate vector search inside SQLite"）
- **支持 ollama 自部署**
- **batch embedding** 系统（gemini/openai/voyage 各有 batch runner）

QMD 可能是 documentation 命名而代码中的实际实现叫别的，或已迁移。**关键事实**：OpenClaw 有完整向量栈。

### B15 [REVISED] compaction.ts 触发

**原断言**："compaction.ts 在接近限制时触发 memoryFlush 的无声 turn"

**核实**：grep 找到 schema + test，**但 compaction.ts 这个文件本身未直接 grep 到**。实际触发逻辑在何处需进一步定位。

**修正后真相**：memoryFlush 触发**机制存在**（schema + test 验证），但具体触发函数代码位置 [TBV]。这不影响"机制存在"的核心结论。

---

## §3 重大新发现（子 agent 漏报）

### F5 — OpenClaw 完整向量 memory 栈
**子 agent 漏说**：之前 axis-2 evidence 给的印象是"平文本 Markdown + 可选 LanceDB 扩展"，但实际 `src/memory/` 有 30+ embedding 相关文件，**完整的多 provider 向量栈**：

| Provider | 实现文件 |
|---|---|
| OpenAI | batch-openai.ts |
| Gemini | batch-gemini.ts |
| Voyage | batch-voyage.ts |
| Mistral | （文档提及）|
| Local (node-llama-cpp) | （文档提及）|
| Ollama | （文档提及，自选）|

**+ sqlite-vec 加速向量搜索**

### F6 — 自动 provider 选择优先级
`docs/concepts/memory.md:96-101`：
1. `local` if `memorySearch.local.modelPath` 配置
2. `openai` if OpenAI key 可解析
3. `gemini` if Gemini key 可解析
4. `voyage` if Voyage key 可解析
5. `mistral` if Mistral key 可解析
6. 否则 disabled

### F7 — Codex OAuth 不满足 embedding
特别警告："Codex OAuth only covers chat/completions and does **not** satisfy embeddings for memory search."

---

## §4 对 SYNTHESIS 的影响（关键）

### §0 #2 必须修正

**原 SYNTHESIS 断言**：
> "Hive 在 memory 维度领先 OpenClaw（OpenClaw 平文本 Markdown + 可选 LanceDB；Hive 有 pgvec + hybrid + extractor + injector 完整向量栈）"

**修正后**：
> "Hive 与 OpenClaw 在 memory 向量栈**势均力敌**：
> - 双方都有完整向量索引（Hive: pgvec; OpenClaw: sqlite-vec + 5 provider 自动选择）
> - 双方都有 chunking / batching 优化
> - **Hive 优势**：Postgres 持久化 + extractor/injector 完整流水线
> - **OpenClaw 优势**：5 provider 自动 fallback + sqlite-vec 嵌入式（无需独立 DB）+ pre-compaction memoryFlush（这是真借鉴机会）"

### §0 #5 加强

Pre-compaction memoryFlush 三层证据全过（文档 + schema + 测试），P0-4 根据非常扎实，**可信度 [VERIFIED-MAIN]**

### §3 P0-4 工期估算修正

发现 OpenClaw 自己实现 memoryFlush 还有 `forceFlushTranscriptBytes` 等额外细节，Hive 实施时要参考这个完整字段集（不是只有 4 个字段）。**工期可能延长到 5-7d**（不是原估 3-5d）。

---

## §5 仍待核实

- compaction.ts 实际触发函数代码位置（[TBV]，不影响结论）
- QMD 在 OpenClaw 当前版本的实际实现（可能已重命名/重构）

---

*— End of axis-2 主线程核实 —*
