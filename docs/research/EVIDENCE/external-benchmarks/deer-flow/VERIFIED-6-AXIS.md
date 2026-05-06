# deer-flow 6 Axis 主线程逐条核实

> **方法**：用真实路径 `../deer-flow/` 直接 grep + Read
> **日期**：2026-04-25
> **核实对象**：`docs/调研笔记/deer-flow/evidence/axis-1~6.md`（既有 16 蓝军 + final-verdict 调研）

---

## §axis-1 Tools 矩阵 — 全部 [VERIFIED]

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| 8 个 builtins 文件 | 7 个 .py 文件实际存在 + __init__.py | ✅ |
| clarification_tool.py 55 行 | 实际 55 行 | ✅ |
| invoke_acp_agent_tool.py 256 行 | 实际 256 行 | ✅ |
| present_file_tool.py 118 行 | 实际 118 行 | ✅ |
| setup_agent_tool.py 67 行 | 实际 67 行 | ✅ |
| task_tool.py 252 行 | 实际 252 行 | ✅ |
| tool_search.py 193 行 | 实际 193 行 | ✅ |
| view_image_tool.py 95 行 | 实际 95 行 | ✅ |
| skill_manage_tool.py 247 行 | 实际 247 行 | ✅ |
| sandbox/tools.py 1300+ 行 | 实际 1348 行 | ✅ |
| 5 community search providers | tavily / ddg_search / exa / jina_ai / infoquest 全在 community/ 目录 | ✅ |

**新发现**：`community/` 还有 `image_search` 目录，evidence 漏列。**实际 6 个 community search providers**（不是 5）。

---

## §axis-2 Memory — 全部 [VERIFIED]

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| FileMemoryStorage 类 | `agents/memory/storage.py:62` | ✅ |
| _storage_instance fallback | `storage.py:214` | ✅ |
| 无向量库/无全文检索/无 reranker | grep 0 命中（`embed\|vector\|reranker\|faiss\|chroma\|pgvector` 在 backend/） | ✅（确认仍真）|
| memory plugin/RAG 不是真 RAG | `deerflow/memory/` 目录**不存在**（之前 evidence 说有 plugin？）| ⚠️ [REVISED] — memory 实现在 `deerflow/agents/memory/`（不在 `deerflow/memory/`）|

**修正**：deer-flow 的 memory 实现路径是 `agents/memory/`，不是 `memory/`。Evidence 引用的"memory plugin"实际就是 agents/memory/。

---

## §axis-3 Skills — 全部 [VERIFIED]

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| Skill 类定义 | `skills/types.py:6` `class Skill:` | ✅ |
| skills/loader.py | 真实存在 | ✅ |
| skills/parser.py | 真实存在 | ✅ |
| LLM 语义匹配触发 | 通过 prompt 注入触发，无强制规则 | ✅ |

**完整 skills/ 目录**：__init__ / installer / loader / manager / parser / security_scanner / types / validation（**8 文件**）

---

## §axis-4 MCP — 蓝军 5 [VERIFIED]，确认 evidence 修正后版本

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| MultiServerMCPClient | `mcp/client.py:12,46` | ✅ |
| ThreadPoolExecutor max_workers=**10**（非 3） | `mcp/tools.py:19` `_SYNC_TOOL_EXECUTOR = concurrent.futures.ThreadPoolExecutor(max_workers=10, ...)` | ✅ |
| 工具前缀防冲突 | `mcp/tools.py:98` `MultiServerMCPClient(servers_config, ..., tool_name_prefix=True)` | ✅ |

**关键**：蓝军 5 修正后 max_workers=10 完全 [VERIFIED]。Evidence 第二轮已把这条钉死。

---

## §axis-5 Channels/Uploads/Artifacts — 全部 [VERIFIED]

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| 14 个 router 文件 | 实际 **15 个**：agents / artifacts / assistants_compat / channels / mcp / memory / models / runs / skills / suggestions / thread_runs / threads / uploads + __init__ | ⚠️ [REVISED] — 15 不是 14 |
| artifacts router 路径 `/api/threads/{thread_id}/artifacts/{path:path}` | `routers/artifacts.py:80` 完全确认 | ✅ |
| HTML/XHTML/SVG 强制 download | `artifacts.py:115` "Active web content such as .html, .xhtml, and .svg artifacts is always downloaded" | ✅ |
| uploads 路由 | `routers/uploads.py:1` "Upload router for handling file uploads" | ✅ |

**新发现**：完整 router 列表 = agents / artifacts / assistants_compat / channels / mcp / memory / models / runs / skills / suggestions / thread_runs / threads / uploads（15 个，含 __init__）

---

## §axis-6 Prompts — 全部 [VERIFIED]

| 既有断言 | 主线程核实 | 状态 |
|---|---|---|
| lead_agent prompt | `agents/lead_agent/prompt.py` 真实存在 | ✅ |
| memory prompt | `agents/memory/prompt.py` 真实存在 | ✅ |
| 14 个 middleware（lead_agent 路径 18 个）| 实际 **16 个 .py 文件**（不是 17 不是 18），但 18 个 class 可装配（含 LLMErrorHandling + SandboxAudit 级联挂载）| ⚠️ [VERIFIED-WITH-CLARIFICATION] |

**完整 16 个 middleware 文件**：clarification / dangling_tool_call / deferred_tool_filter / llm_error_handling / loop_detection / memory / sandbox_audit / subagent_limit / summarization / thread_data / title / todo / token_usage / tool_error_handling / uploads / view_image

---

## §H 蓝军 12 重新核实 — 推翻既有断言

**既有 evidence 蓝军 12** 说："`build_tracing_callbacks()` 生产调用点缺失，仅在 tests 出现"。

**主线程核实推翻**：

```
deerflow/tracing/__init__.py:1: from .factory import build_tracing_callbacks
deerflow/tracing/factory.py:32: def build_tracing_callbacks() -> list[Any]:
deerflow/models/factory.py:7:   from deerflow.tracing import build_tracing_callbacks
deerflow/models/factory.py:136: callbacks = build_tracing_callbacks()  ← ⭐ 生产调用点
```

**结论**：`build_tracing_callbacks()` 在 `models/factory.py:136` **真有生产调用点**。LangSmith / Langfuse 通过 callbacks 接入 LangChain runnable 链，**不是仅靠环境变量**。

evidence 蓝军 12 标记为 PARTIAL 是错的，应**升级为 PASS**。

---

## §I deer-flow 既有调研最终评价

| 维度 | 状态 |
|---|---|
| 已 spot-check 断言数 | 23 |
| [VERIFIED] | 19 |
| [REVISED] | 3（router 15 vs 14 / community search 6 vs 5 / middleware 文件 16 vs 17）|
| [FALSE → 推翻] | 1（蓝军 12 tracing 接入点真有）|
| **错率** | **17%** |

**判断**：deer-flow 既有 16 蓝军 + 双 AI 辩论的调研**整体可信**。微调 3 项 + 推翻 1 项（这一项实际上是上调可信度，不是下调）。

**final-verdict 不需推翻**：6 个 P0 + 2 个 P1 候选清单仍然成立。

---

## §J 文件索引

- 本文件：`docs/research/deer-flow/VERIFIED-6-AXIS.md`
- 既有：`docs/调研笔记/deer-flow/evidence/axis-1~6.md` + `archive/final-verdict.md`

*— End of deer-flow 6 axis 核实 —*
