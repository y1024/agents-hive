# 合并报告 · deer-flow 深度调研（双盲双份 + 蓝军验证）

日期：2026-04-21
合并来源：
- Claude Phase 1（议题 A-G）: `/tmp/deer-flow-analysis/claude-phase1-output.md`（1214 行）
- Claude Phase 2（议题 1-7）: `/tmp/deer-flow-analysis/claude-phase2-output.md`（1518 行）
- Codex Phase 1（议题 1-7）: `/tmp/deer-flow-analysis/codex-phase1-output.md`（759 行）
- Codex Phase 2（议题 A-G）: `/tmp/deer-flow-analysis/codex-phase2-output.md`（1100 行，已完成）

源码镜像：`/tmp/deer-flow-src/`（893 文件，26 MB，从 `/Users/guoss/workspace/company/vast/deer-flow` rsync）

---

## 合并流程

1. **并表**: 把 14 个议题（A-G + 1-7）分别提取两份独立报告里的核心断言 → 三类桶：**两份同意 / 仅 Claude / 仅 Codex**。
2. **蓝军**: 从"两份同意"里抽 3-5 条高价值断言，再从"仅一份"里抽 2-3 条疑点，逐一 grep 源码验证。
3. **结论**: 写面向 agents-hive 的借鉴清单，每条带文件锚点与已验证标记。

---

## 第 1 轮 · 断言并表（由合并步骤填入）

### 议题 A · 仓库顶层与部署形态
- **Claude Phase 1 A**: 4 个 docker-compose 服务（nginx:2026 / frontend / gateway:8001 / langgraph:2024）。**漏了 `provisioner:8002`**。
- **Codex Phase 2 A**: **5 个服务**：nginx / frontend / gateway / langgraph / **provisioner**（可选 K8s sandbox，端口 8002，nginx `/api/sandboxes` 路由）。证据 `docker/docker-compose.yaml:24-199`、`docker/nginx/nginx.conf:203-214`。
- **两份同意**: Docker socket 直接挂到 gateway 和 langgraph（DooD），`langgraph` 生产仍跑 `langgraph dev`（TODO 迁移到 langchain/langgraph-api 需要 license）。
- **仅 Codex 的关键风险**: `GATEWAY_WORKERS=4` 默认 + `RunManager`/`MemoryStreamBridge` 都是进程内状态 → **多 worker 下同一 run 不跨 worker 可见**（scale-out gap）。证据 `docker/docker-compose.yaml:77`、`runtime/runs/manager.py:40-45`、`runtime/stream_bridge/memory.py:32-35`。
- **并表结论**: 两份同意基础拓扑；仅 Codex 识别出 provisioner 服务和 4-worker/in-memory 规模化风险；Claude Phase 1 漏服务且未触及 worker 话题。

### 议题 B · Backend 分层
- **Claude Phase 1 B**: FastAPI 入口 `backend/app/gateway/app.py:77-147`；路由 13 个；`RunCreateRequest` 含 `multitask_strategy` 四态等。
- **Codex Phase 2 B**: 实测 **14 个 router 文件、合计 2888 行**（Claude 漏了 `runs.py` 的 stateless endpoints，只提到 thread_runs）。Endpoint 分布：threads 8 / thread_runs 8 / agents 8 / memory 10 / skills 10 / assistants_compat 4 / uploads 3 / models 2 / mcp 2 / channels 2 / suggestions 1 / artifacts 1 / runs 2。DI 是 `app.state` 单例（非 FastAPI Depends），由 `lifespan + AsyncExitStack` 初始化。
- **两份同意**: `multitask_strategy: reject|rollback|interrupt|enqueue` 四态；Pydantic `RunCreateRequest` 字段完整。
- **仅 Codex 的高价值发现 1 · command/checkpoint 半实现**: `RunCreateRequest.command/checkpoint/checkpoint_id` 在 Pydantic 声明，但 `services.start_run()` 实际只用 `body.input/config/context`（`services.py:311-325`）。agents-hive 若要做 resume，不能只看 schema，要看 runtime 是否真 plumb 到 `agent.astream()`。
- **仅 Codex 的高价值发现 2 · multitask_strategy="enqueue" 半实现**: Pydantic 允许 `enqueue`，但 `RunManager` 不支持，直接返回 501（`manager.py:148-152`）。四态只有 3 态真能走通。
- **Claude 漏 router count**: 13 → 14。

### 议题 C · Middleware 栈
- **两份同意（待蓝军）**:
  - Claude C1 表和 Codex 议题 6/7 都反复引用 `agents/lead_agent/agent.py:205-277` 和 `factory.py:278-289`：存在 14 个中间件、ClarificationMiddleware 必须在末尾。
  - 两份都提到 `ToolErrorHandlingMiddleware` 把异常转 `ToolMessage(status="error")`。
- **仅 Claude**:
  - Claude 声明"14 中间件 + 2 opt-in"的细分表（C1 表），codex 只写了 8 个 middleware 名字（没穷举）。
  - **蓝军动作**: `rg -n "AgentMiddleware\]" /tmp/deer-flow-src/backend/packages/harness/deerflow/agents/middlewares/` 穷举类数；对比 `factory.py` 的 `chain.append` 次数。

### 议题 D · LangGraph 编排
- **两份同意**: 3 种 checkpointer 后端（memory/sqlite/postgres），`provider.py` 里分支。
- **待蓝军**: reducer 函数 `merge_artifacts` / `merge_viewed_images` 的实际效果（空 dict 清空语义）。
- **蓝军动作**: 打开 `thread_state.py:21-45` 实读。

### 议题 E · Frontend 架构
- **Claude Phase 1 E**: Next.js 16.1.7；App Router；**`useSyncExternalStore` 模式 + Zustand store**（← 错！）；9 种 stream modes；Better Auth 鉴权。
- **Codex Phase 2 E** 关键矫正：
  - **没有 zustand**：grep `zustand|create\(` 在 `frontend/src` 只命中 `motion.create`，没有 store 创建。状态管理靠 React Query + `useStream`（LangGraph SDK）+ 组件 `useState`。
  - **没有手写 SSE/EventSource**：全靠 `@langchain/langgraph-sdk/react` 的 `useStream`。
  - **没有 Next.js error boundary 文件**：`error.tsx / global-error.tsx` 都不存在。
  - 断线重连：`reconnectOnMount` + `sessionStorage` 存 `lg:stream:*` key，SDK 内部 Last-Event-ID。
  - `QueryClient` 没有默认 retry/staleTime 配置，继承 TanStack 默认策略。
- **两份同意**: Next.js 16.1.7 + React 19 + App Router，LangGraph SDK。
- **并表结论**: Claude Phase 1 关于 "Zustand" 的断言错误；前端状态管理实际比 Claude 描述更轻。

### 议题 F · Observability
- **两份同意**: LangSmith、Langfuse 可选，env 开关 `LANGSMITH_TRACING` / `LANGFUSE_TRACING`；`test_tracing_factory.py` 覆盖 4 种场景。
- **仅 Claude**: Prometheus 缺失（蓝军 8 PASS）。
- **仅 Codex 的疑点**: `build_tracing_callbacks()` **生产调用点未找到**。静态 grep 只在 `tracing/factory.py`（定义）和 tests 出现，`make_lead_agent()` 和 `run_agent()` 都没显式合 callbacks 到 `RunnableConfig.callbacks`。这意味着 LangSmith/Langfuse 可能只是**半接入**，agents-hive 抄的时候要看 LangChain 是不是靠 env var + 全局 tracer 自动接入。
- **仅 Codex**: OTel 依赖存在（lockfile）但 runtime 未初始化；Gateway `logging.basicConfig` 是全局 INFO，没结构化 JSON、没 request-id；`traceparent`/`X-Request-ID` 跨 nginx/frontend/gateway/langgraph 无传播。
- **未解**: trace id 从哪里进入 LangChain callback trace context？`task_tool` 里 `[trace=...]` 只是日志前缀，不是 OTel trace id。

### 议题 G · 测试结构
- **Claude Phase 1 G**: Pytest 50+ 文件。
- **Codex Phase 2 G 矫正**: **实测 110 个 `test_*.py`**（Claude 严重低估）。前端 Vitest 6 个 unit + 6 个 E2E (498 行)。
- **两份同意**: 4 个 CI workflow（backend-unit / frontend-unit / e2e / lint-check）。
- **仅 Codex 的高价值发现**: Playwright E2E **全部 mock 后端 API**（`frontend/tests/e2e/utils/mock-api.ts:181-183` 拦截 `**/runs/stream`），能测 UI 行为但**测不了真实 Gateway/LangGraph/SSE 兼容**。无 coverage gate（grep `coverage|pytest-cov|vitest coverage|nyc|codecov` 全空）。
- **未解**: `test_client_live.py / test_create_deerflow_agent_live.py` 在默认 CI 是否真被 skip？需看具体 marker。

### 议题 1 · 工具集体系
- **两份同意**:
  - 工具配置驱动 (`config.tools[].use` 是 Python import path，用 `resolve_variable` 解析)。
  - DDG 空结果显式 JSON error 而非静默空列表（`ddg_search/tools.py:72-79`）。
  - 工具去重优先级 config > builtin > MCP > ACP（Claude Phase 2 议题 1.1）。
- **分歧**:
  - Claude Phase 2 说内置 "6 个"（present_file, ask_clarification, task, view_image, tool_search, skill_manage）；Codex Phase 1 说 "present_files, ask_clarification + view_image/task/skill_manage/tool_search/invoke_acp_agent"（7 个，多一个 `invoke_acp_agent`）。
  - **蓝军动作**: `rg -n "_add_" /tmp/deer-flow-src/backend/packages/harness/deerflow/tools/tools.py` 穷举条件 append 的工具。

### 议题 2 · LLM adapter 层
- **两份同意**: 支持多 provider（OpenAI/Anthropic/Vertex/Codex/DeepSeek/vLLM/MiniMax/Gemini），`factory.py` 用 `resolve_class`；Codex provider 支持 `reasoning_effort` (none/low/medium/high/xhigh)。
- **仅 Codex**: `patched_openai.py:120-132` 按 id / 位置匹配 tool_call 防丢失；`patched_deepseek.py` 保真 `reasoning_content`。
- **仅 Claude**: Claude provider OAuth 检测 + prompt cache + thinking budget。
- **蓝军动作**: 读 `client.py:620-668` 确认 streaming "messages mode" 是否同时处理 text + tool_calls + usage（对 agents-hive P0-C 参考价值高）。

### 议题 3 · MCP 集成
- **两份同意**: 
  - MultiServerMCPClient 支持 stdio / SSE / HTTP。
  - 工具前缀防冲突。
  - 异步工具由 ThreadPoolExecutor (3 worker) 同步包装（Claude Phase 2 议题 3 和 Codex 共同确认）。
  - 仅支持 tools，不支持 resources / prompts。
- **蓝军动作**: `rg -n "max_workers" /tmp/deer-flow-src/backend/packages/harness/deerflow/mcp/` 确认 worker=3。

### 议题 4 · RAG / retrieval
- **两份同意**: 无向量库、无全文检索、无 reranker、无 query rewriting。Memory 不是 RAG。Citation 是字符串约定 `[citation:Title](URL)`。
- **蓝军动作**: `rg -n "embed|vector|reranker|faiss|chroma|pgvector" /tmp/deer-flow-src/backend/` 确认没有隐藏实现。

### 议题 5 · 数据模型与存储
- **两份同意**:
  - ThreadState 包含 messages/sandbox/thread_data/artifacts/todos/viewed_images。
  - Checkpointer 三后端（已在议题 D 合并）。
  - RunStatus = pending/running/success/error/timeout/interrupted。
  - RunManager 是 in-memory，不跨重启。
- **仅 Codex**: FileMemoryStorage mtime 缓存 + 无 TTL 清理（Claude 也提到）。
- **蓝军动作**: 读 `runtime/runs/manager.py:40-45` 确认 in-memory。

### 议题 6 · Subagent / Skills
- **两份同意**:
  - Subagent 暴露成 `task` 工具由 LLM 调用（不是编排器 spawn）。
  - `subagent_enabled=False` 防递归。
  - 线程池 3+3+3 硬编码。
  - SubagentLimitMiddleware `max_concurrent` 夹 [2,4]。
  - Skills 是 `SKILL.md` + progressive loading via `read_file`。
  - Skill 触发是 LLM 语义匹配（prompt 注入），没有强制规则。
- **蓝军动作**: 
  - `rg -n "max_workers|MAX_CONCURRENT_SUBAGENTS" /tmp/deer-flow-src/backend/packages/harness/deerflow/subagents/` 确认池容量 + middleware 边界。
  - 读 `skills/loader.py:11-79` 和 `skills/parser.py:12-76` 验证 frontmatter 机制。

### 议题 7 · 长任务调度
- **两份同意**:
  - 没有 Celery / arq / RQ / APScheduler。
  - Gateway run = `asyncio.Task` + in-memory `RunManager` + `StreamBridge`。
  - Subagent task = 全局 dict `_background_tasks` + ThreadPoolExecutor + 5s 轮询。
  - 取消是协作式（`asyncio.CancelledError`、`cancel_event`），长工具调用无法强杀。
  - 两种 cancel 语义：interrupt（保留检查点）/ rollback（恢复 pre-run checkpoint）。
  - Subagent timeout = `timeout_seconds + 60s` 轮询 safety net。
- **蓝军动作**: 
  - 读 `runtime/runs/worker.py:71-84` 确认 pre-run checkpoint snapshot。
  - 读 `runtime/runs/schemas.py:6-14` 确认 RunStatus 枚举值。
  - `rg -n "Celery|arq|RQ|APScheduler|rq_worker" /tmp/deer-flow-src/` 确认真无。

---

## 第 2 轮 · 蓝军 mutation 验证（已执行）

### 蓝军 1 · Middleware 类穷举 → **PARTIAL / 两份都 PARTIAL（共漏 4 个）**

**修正后的完整 middleware 装配清单**（按注册点）:

| # | middleware | 注册点 | 触发条件 | always / opt-in |
|---|---|---|---|---|
| 1 | ThreadDataMiddleware | `factory.py:201` | sandbox feature on | always（默认开） |
| 2 | UploadsMiddleware | `factory.py:202` | sandbox feature on | always（默认开） |
| 3 | SandboxMiddleware | `factory.py:203` | sandbox feature on | always（默认开） |
| 4 | DanglingToolCallMiddleware | `factory.py:206` | 无条件 | always |
| 5 | GuardrailMiddleware | `factory.py:211/213` | `feat.guardrail` | opt-in |
| 6 | ToolErrorHandlingMiddleware | `factory.py:216` | 无条件 | always |
| 7 | SummarizationMiddleware | `factory.py:221/223` | `feat.summarization` | opt-in |
| 8 | TodoMiddleware | `factory.py:229` | `plan_mode` | opt-in |
| 9 | TitleMiddleware | `factory.py:238` + `agent.py:244` | always on | always |
| 10 | MemoryMiddleware | `factory.py:247` + `agent.py:247` | always on | always |
| 11 | ViewImageMiddleware | `factory.py:256` + `agent.py:254` | model.supports_vision | conditional |
| 12 | **TokenUsageMiddleware** | `agent.py:241` | `token_usage.enabled` | conditional（两份漏） |
| 13 | **DeferredToolFilterMiddleware** | `agent.py:260` | `tool_search.enabled` | conditional（两份漏） |
| 14 | SubagentLimitMiddleware | `factory.py:268` + `agent.py:266` | `subagent_enabled` | opt-in |
| 15 | LoopDetectionMiddleware | `factory.py:276` + `agent.py:269` | 无条件 | always |
| 16 | ClarificationMiddleware | `factory.py:279` + `agent.py:276` | 无条件 + 保底末位 | always LAST |
| 17 | **LLMErrorHandlingMiddleware** | `tool_error_handling_middleware.py:94`（级联） | 在 `ToolErrorHandlingMiddleware.setup()` 时自动挂载 | always（两份漏） |
| 18 | **SandboxAuditMiddleware** | `tool_error_handling_middleware.py:123`（级联） | 同上 | always（两份漏） |

**关键发现 · 两条独立的 middleware 构建路径（两份报告都混为一谈）**:

- **路径 A** (lead agent, `lead_agent/agent.py:226 + :215-277`): 
  - 入口 `_build_middlewares(config, ...)` 先 `build_lead_runtime_middlewares(lazy_init=True)` → 调 `_build_runtime_middlewares(include_uploads=True, include_dangling_tool_call_patch=True)` 得到基础 **8 个**: ThreadData / Uploads / Sandbox / Dangling / **LLMErrorHandling** / Guardrail(条件) / **SandboxAudit** / ToolErrorHandling。(证据 `tool_error_handling_middleware.py:79-124`)
  - 然后 `_build_middlewares` 再追加: Summarization / Todo / **TokenUsage** / Title / Memory / ViewImage(条件) / **DeferredToolFilter**(条件) / SubagentLimit(条件) / LoopDetection / Clarification。
  - 合计可装配 **最多 18 个**。

- **路径 B** (通用 `create_deerflow_agent`, `factory.py:150-289`): 
  - 直接内建 14 次 `chain.append`: ThreadData / Uploads / Sandbox / Dangling / Guardrail / ToolErrorHandling / Summarization / Todo / Title / Memory / ViewImage / Subagent / Loop / Clarification。**不经过** `_build_runtime_middlewares()`，因此 **不自动挂载 LLMErrorHandling / SandboxAudit / TokenUsage / DeferredToolFilter**。

- **结论**: lead agent 实际运行时的 middleware 栈和 `factory.py` 文档里那个 14 项列表是两码事。两份报告都把 `factory.py` 当作真相源，结果漏了 4 个仅在 path A 生效的 middleware。agents-hive 若参考这部分架构，一定要认清"通用 factory 链"和"lead agent 运行时链"是两套。

命令:
```
ls backend/packages/harness/deerflow/agents/middlewares/ | grep -v __
rg -n "class \w+Middleware" backend/.../middlewares/
rg -n "chain.append\(|middlewares.append\(" backend/.../factory.py backend/.../lead_agent/agent.py backend/.../tool_error_handling_middleware.py
```

证据:
- `middlewares/` 目录共 **16 个文件**，定义 16 个 `*Middleware` class。
- `factory.py:194-289` 的 `build_lead_runtime_middlewares()` 中 `chain.append` **14 次**（含条件分支）。
- `lead_agent/agent.py:215-277` 的 `_build_middlewares()` 在基础链上再条件追加 4 个:
  - `TokenUsageMiddleware`（行 239-241，`token_usage.enabled`）
  - `DeferredToolFilterMiddleware`（行 256-260，`tool_search.enabled`）
  - `SubagentLimitMiddleware`（行 262-266，`subagent_enabled`）
  - `LoopDetectionMiddleware`（行 268-269，always）
  - `ClarificationMiddleware`（行 275-276，always LAST）
- 另外 `tool_error_handling_middleware.py:94` 和 `:123` 在 setup 时**级联注入** `LLMErrorHandlingMiddleware` 和 `SandboxAuditMiddleware`。

判定:
- **Claude Phase 1 C1 表写"14 + 2 opt-in"** → 缺 `TokenUsage / DeferredToolFilter / LLMErrorHandling / SandboxAudit`，实际至少 18 个可组装 middleware。
- **Codex Phase 1 只列 8 个** → 更不完整。
- **蓝军修正**: 正确 middleware 清单（按注册点）见下表，列在"合并后结论"。

### 蓝军 2 · ClarificationMiddleware 必最后 → **PASS**

命令:
```
sed -n '275,290p' backend/.../factory.py
```

证据:
```python
# factory.py:276-289
chain.append(ClarificationMiddleware())  # line 279
...
if extra_middleware:
    _insert_extra(chain, extra_middleware)
    clar_idx = next(i for i, m in enumerate(chain) if isinstance(m, ClarificationMiddleware))
    if clar_idx != len(chain) - 1:
        chain.append(chain.pop(clar_idx))
```

判定: 两份报告均对。factory.py 先 append 让它在初态就是最后，再在 extra_middleware 插入后"保底"把它移回末尾。这是个**不变量防御**，不是运行时 order check。

### 蓝军 3 · 内置工具 append 条件清单 → **Codex PASS / Claude Phase 2 PARTIAL（漏 invoke_acp_agent）**

命令:
```
rg -n "builtin_tools.append|builtin_tools.extend|acp_tools.append" backend/.../tools/tools.py
```

证据（`tools/tools.py`）:
| 工具名 | 触发条件 | 代码位置 |
|---|---|---|
| `present_files` | 始终 | （静态列表；未在 grep 出现，需确认） |
| `ask_clarification` | 始终（由 factory 追加到 extra_tools）| `factory.py:280` |
| `view_image_tool` | model_config.supports_vision | `tools.py:98-100` |
| `SUBAGENT_TOOLS`（含 `task`）| subagent_enabled | `tools.py:87-90` |
| `skill_manage_tool` | skill_evolution.enabled | `tools.py:83-85` |
| `tool_search_tool` | tool_search.enabled AND MCP tools | `tools.py:129-132` |
| `invoke_acp_agent` | ACP agents 配置 | `tools.py:138-147` |

判定: 7 个条件内置工具。Claude Phase 2 称 "6 个"漏了 `invoke_acp_agent`；Codex Phase 1 正确列出 7 个。

### 蓝军 4 · embedded client 流式 messages mode 同时处理 text/tool_calls/tool_message → **PASS**

命令: `Read client.py:615-680`

证据:
```python
# client.py:620-637（messages mode 分支）
if isinstance(msg_chunk, AIMessage):
    text = self._extract_text(msg_chunk.content)
    counted_usage = _account_usage(msg_id, msg_chunk.usage_metadata)
    if text:
        yield self._ai_text_event(...)
    if msg_chunk.tool_calls:
        yield self._ai_tool_calls_event(msg_id, msg_chunk.tool_calls)
elif isinstance(msg_chunk, ToolMessage):
    yield self._tool_message_event(msg_chunk)
```

判定: Codex Phase 1 断言正确。**这是 agents-hive P0-C（流式 tool_call 合并）最直接的参考实现**。

### 蓝军 5 · MCP 同步包装 worker 数 → **FAIL（两份共同错）**

命令:
```
rg -n "max_workers" backend/packages/harness/deerflow/
```

证据:
```
mcp/tools.py:19: _SYNC_TOOL_EXECUTOR = ThreadPoolExecutor(max_workers=10, thread_name_prefix="mcp-sync-tool")
subagents/executor.py:73,77,80: ThreadPoolExecutor(max_workers=3, ...)  x3
agents/memory/updater.py:30: ThreadPoolExecutor(max_workers=4, ...)
client.py:1083: ThreadPoolExecutor(max_workers=1, ...)
```

判定:
- **Claude Phase 2 和 Codex Phase 1 都说"MCP 同步包装 ThreadPoolExecutor (3 worker)"** → 错。
- 实际是 **max_workers=10**。两份报告撞同一个错。
- 这条在合并后报告里标红，提示蓝军辩论的价值。

### 蓝军 6 · Subagent 池容量 + SubagentLimitMiddleware clamp → **PASS**

命令:
```
rg -n "MAX_CONCURRENT_SUBAGENTS|MIN_SUBAGENT_LIMIT|max_workers" backend/.../subagents/ backend/.../middlewares/subagent_limit_middleware.py
```

证据:
- `executor.py:73,77,80`: 3 个 ThreadPoolExecutor 全部 `max_workers=3`（scheduler / exec / isolated-loop）
- `executor.py:532`: `MAX_CONCURRENT_SUBAGENTS = 3`
- `subagent_limit_middleware.py:15`: `MIN_SUBAGENT_LIMIT = 2`
- `subagent_limit_middleware.py:33` docstring: "Clamped to [2, 4]"

判定: 两份报告均对。**这里有个实操细节**: 线程池硬上限 3，SubagentLimitMiddleware 在 LLM 输出层可以配置到 4。若 max_concurrent_subagents=4 但池只有 3，理论上第 4 个 task 会排队等。值得 agents-hive 注意。

### 蓝军 7 · 真无 Celery/arq/RQ/APScheduler → **PASS**

命令: `rg -n "Celery|arq\.|APScheduler|rq\.Queue|rq_worker" backend/`

证据: 0 matches.

判定: 长任务完全靠 `asyncio.Task + RunManager (in-memory dict)` + `ThreadPoolExecutor + _background_tasks dict`。进程重启丢所有 run/task。两份报告均对。

### 蓝军 8 · Prometheus 缺失 → **PASS**

命令: `rg -n "prometheus|Prometheus" /tmp/deer-flow-src/`

证据: 仅 3 匹配，全部在 `docker/provisioner/README.md` 和一个 demo thread json。**没有实际集成**。

判定: Claude Phase 1 F.3 断言正确。若生产需要指标，必须自己接入。

### 蓝军 9 · stream_mode "events" 前后端漂移 → **FAIL（Codex 发现的协议裂缝）**

命令:
```
sed -n '1,11p' frontend/src/core/api/stream-mode.ts
sed -n '55,70p' backend/packages/harness/deerflow/runtime/runs/worker.py
```

证据:
- 前端白名单 `SUPPORTED_RUN_STREAM_MODES` 包含 `"events"`（`frontend/src/core/api/stream-mode.ts:1-11`）。
- 后端 worker 显式 `continue` 跳过 `events`（`runtime/runs/worker.py:60-65`），注释说 Python public API 限制。

判定: 前端可以把 `events` 发给后端，但后端不会回任何 LangChain `on_*` 事件。前端 `onLangChainEvent(on_tool_end, ...)` 的监听器当前形同虚设。agents-hive 要么前端严格收窄白名单，要么后端实现 `astream_events()` 分支，绝不能让两边漂移。

### 蓝军 10 · multitask_strategy="enqueue" 半实现 → **FAIL（同 enqueue 的 501）**

命令:
```
rg -n "multitask_strategy|enqueue" backend/packages/harness/deerflow/runtime/runs/ backend/app/gateway/routers/thread_runs.py
```

证据:
- `thread_runs.py:52`: `multitask_strategy: Literal["reject", "rollback", "interrupt", "enqueue"] | None`
- `manager.py:148-152`: `enqueue` 未实现，`create_or_reject()` 返回 501（`NotImplementedError` 式拒绝）。

判定: API 层允许的 4 种 concurrency 策略，runtime 只实现 3 种。agents-hive 若抄此枚举，务必要么把 `enqueue` 去掉，要么真做 FIFO 队列，不要让客户端看到字段以为能用。

### 蓝军 11 · RunCreateRequest.command / checkpoint 半接入 → **FAIL（Codex 发现）**

命令:
```
sed -n '35,55p' backend/app/gateway/routers/thread_runs.py
sed -n '300,330p' backend/app/gateway/services.py
```

证据:
- `thread_runs.py:38-44` 声明了 `command: dict | None`、`checkpoint: dict | None`、`checkpoint_id: str | None`。
- `services.py:283-325` 的 `start_run()` 只用 `body.input/config/context`，**没有**把 `body.command` 传入 `agent.astream()`；亦未用 `checkpoint_id` 定位断点。
- LangGraph 标准的 `Command(resume=...)` resume flow 在 Gateway 路径下当前不落地；只有 cancel/interrupt/rollback 是真能用的状态转移。

判定: agents-hive 若要抄 LangGraph resume 语义，不能只抄 Pydantic schema。要么完整实现（把 command plumb 到 graph），要么从 schema 里砍掉 `command/checkpoint` 避免误导。这是"schema 看起来完整、runtime 半实现"的典型陷阱。

### 蓝军 13 · Frontend 真的没 zustand（Codex E 断言复核）→ **PASS（Claude Phase 1 错）**

命令:
```
Grep "zustand" /tmp/deer-flow-src/frontend/src  →  No matches
Grep "zustand" /tmp/deer-flow-src/frontend      →  仅 pnpm-lock.yaml 一处
cat frontend/package.json | grep zustand        →  0 命中
```

证据:
- `frontend/src/**` 0 处 zustand 引用（包括 `from 'zustand'`、`createStore`、`zustand/vanilla`）。
- `frontend/package.json` dependencies 没有 zustand。
- `pnpm-lock.yaml` 出现 zustand@4.5.7 和 5.0.12，但它们是**间接依赖**（某 React 生态库如 cmdk 或 @radix-ui 内部用），**不是前端自己的 store**。

判定: Claude Phase 1 E "useSyncExternalStore 模式 + Zustand store" **错**。前端状态管理实际栈：React Query (server state) + `useStream` from LangGraph SDK (stream state) + 组件本地 `useState` (ui state)。**Codex Phase 2 E 正确**。agents-hive 若参考前端架构，别引入 zustand 也能做一个干净的 thread UI。

### 蓝军 14 · 生产 compose 真是 5 服务（Codex A 断言复核）→ **PASS（Claude Phase 1 漏 provisioner）**

命令:
```
Grep "^[a-z]+:" docker/docker-compose.yaml  →  只有 2 行顶级 key：services:(24), networks:(200)
Read docker-compose.yaml line 24-199        →  services 块 5 个 service
```

证据（services 块 24-199 行内的 service 定义）:
1. `nginx:`（24-45） — 反向代理，端口 2026
2. `frontend:`（47-66） — Next.js prod server
3. `gateway:`（67-117） — FastAPI Gateway，`--workers 4`
4. `langgraph:`（118-167） — LangGraph dev server
5. `provisioner:`（169-199） — K8s sandbox lifecycle，端口 8002

（第 200 行是 `networks:`，`deer-flow:` 是 network 名不是 service）

判定: **生产就是 5 个 service**，Claude Phase 1 A 的 "4 服务" 是真漏（漏掉 provisioner）。Codex Phase 2 A 正确。agents-hive 评估 DeerFlow 部署复杂度时必须算上 provisioner 的可选性（K8s sandbox 是另一个独立 deployment unit）。

### 蓝军 15 · Backend 测试真是 110 个（Codex G 断言复核）→ **PASS（Claude "50+" 严重低估）**

命令:
```
ls /tmp/deer-flow-src/backend/tests/test_*.py | wc -l  →  110
```

证据: `backend/tests/test_*.py` **精确 110 个文件**。

判定: Claude Phase 1 G "50+" 低估约 60 个文件（实际翻倍），显示 Claude 没对 tests 目录做实测 count。Codex Phase 2 G 实测数字准确。agents-hive 判断 deer-flow 测试成熟度时应参考真实数字 —— **backend 测试覆盖相当密集**（中间件、runtime、router、sandbox、skills、tracing 各条线都有），这也反过来证明了"middleware 顺序 / rollback / stream" 等不变量是被 test 锁住的，抄中间件模式时可以信任其 behavior contract。

### 蓝军 16 · multitask_strategy="enqueue" 真返回 HTTP 501（Codex B/D 断言复核）→ **PASS（schema/runtime 漂移确证）**

命令:
```
Read manager.py:125-195
Grep "UnsupportedStrategyError" → 定位 services.py:274-275
```

证据（三点链路完整）:
1. **Pydantic 声明 4 态**: `thread_runs.py:52` `multitask_strategy: Literal["reject","rollback","interrupt","enqueue"]` — 客户端发 `enqueue` 能通过 request validation。
2. **Runtime 硬编码 3 态白名单**: `manager.py:148` `_supported_strategies = ("reject", "interrupt", "rollback")`，`manager.py:151-152` 若 `multitask_strategy not in _supported_strategies` 直接 `raise UnsupportedStrategyError(...)`。
3. **Gateway 映射 501**: `services.py:274-275` `except UnsupportedStrategyError as exc: raise HTTPException(status_code=501, detail=str(exc))`。

判定: Codex Phase 2 D 的 "multitask_strategy=enqueue 返回 501" **完全精确**。这是 schema-runtime drift 的教科书案例：

- Pydantic **允许** → 客户端 UI 以为能用
- Runtime **拒绝** → 实际 501

**agents-hive 必须警惕这种模式**。若抄 deer-flow 的 `RunCreateRequest`，立即要么：(a) 从 Literal 里去掉 `enqueue`，避免误导；(b) 真实现 FIFO enqueue 语义；(c) 至少给 Pydantic 加 `@validator` 挡住未实现值，让 422 在 request 层就拒绝，而不是 501 落到 runtime。同样模式也发生在 `RunCreateRequest.command/checkpoint`（蓝军 11）和 `stream_mode="events"`（蓝军 9）。

---

### 蓝军 12 · build_tracing_callbacks 生产调用点缺失 → **PARTIAL（值得追查）**

命令:
```
rg -n "build_tracing_callbacks" /tmp/deer-flow-src/backend/
```

证据:
- `tracing/factory.py:32-53` 定义 `build_tracing_callbacks()`。
- 其他命中仅在 tests 目录（`test_tracing_factory.py`）。
- `agents/lead_agent/agent.py:323-337` 只写 metadata（agent_name / model_name 等），**未调用** `build_tracing_callbacks()` 合入 `config["callbacks"]`。
- `runtime/runs/worker.py` 的 `run_agent()` 也没看到显式调用。

判定: 若 LangSmith 真能工作，依赖的一定是 LangChain 的全局 tracer env var 自动接入（`LANGSMITH_TRACING=true` → LangChain 内部 `tracing_v2_enabled()`）。Langfuse 则需要显式 callback handler 合入 config，但代码里找不到合入点。**agents-hive 若抄这里，要么用 LangChain global tracer pattern，要么在 `_build_middlewares` 外显式把 callbacks 合入**。

---

## 第 3 轮 · agents-hive 借鉴清单（映射到现有 P0-A/B/C/D 施工状态）

**前置上下文**: agents-hive 现状（`docs/agent-quality-remediation-plan.md` 与近期提交）:
- P0-A（tool_choice enforcement）: **已落地**。`tool_choice_detector.go` + `react_processor.go` 多点 guard (245/331/340/450/653-675)。
- P0-B（websearch strict）: **已落地**。`websearch.go:54,165` 严格模式。
- P0-C（stream tool_call）: **未落地**。主回调仍只看文本。
- P0-D（grounding validator）: **未落地**。

### 3.1 直接可抄（高优先级，每条带 deer-flow 锚点）

| 目标 P0 | 抄什么 | deer-flow 锚点 | 改造成本 |
|---|---|---|---|
| P0-C | embedded client "messages mode" 同时 yield text + tool_calls + tool_message（见蓝军 4） | `client.py:615-680` | 中。Go 侧需要对 stream event router 改造，把 tool_call chunk 也抛给主回调 |
| P0-B 增强 | DDG 空结果显式 JSON error（`{"error":"No results found", ...}`），不是静默空数组 | `ddg_search/tools.py:72-79` | 低。Go 侧已有 strict 模式，把"空结果"也编码为 error envelope |
| P0-D | ToolErrorHandlingMiddleware 把异常转 `ToolMessage(status="error")`，不让异常穿透到 model | `tool_error_handling_middleware.py:19-65` | 中。Go 侧在 tool execution loop 加 recover + 显式 error message |
| 新增 guard | `SubagentLimitMiddleware` 在 after_model 截断超额 task tool_calls（LLM 输出层硬截，不靠 prompt） | `subagent_limit_middleware.py:36-63` | 低。agents-hive 若引入 subagent dispatch，这层防御不要少 |
| 新增 guard | ClarificationMiddleware 必最后的"防御式重排"（不信任插件顺序） | `factory.py:278-289` | 低。中间件/拦截器链序不变量用 invariant check，不靠文档 |

### 3.2 结构可借鉴（不直接抄代码，但设计值得参考）

| 设计 | 锚点 | agents-hive 映射 |
|---|---|---|
| 工具注册配置化: `config.tools[].use` 是 Python import path + `resolve_variable` 动态解析 | `config.example.yaml:345-461` + `tools/tools.py:55-63` | 可为 agents-hive 的 tool registry 加 YAML-driven 注册，避免硬编码 |
| 工具可见性三层过滤: `tool_groups` + `host_bash_allowed` + `subagent_enabled` 控制 LLM 可见工具数 | `tools/tools.py:35-63` + `lead_agent/agent.py:350-356` | 对应 agents-hive"每轮全量塞工具无 tool_choice"的降噪手段（P0-A 已做 tool_choice，这是另一维） |
| Checkpointer 三后端工厂 + context manager 清理 | `agents/checkpointer/provider.py:48-92` | 若 agents-hive 要做 thread 持久化，用同样结构 |
| ThreadState reducer（`merge_artifacts` dedupe，`merge_viewed_images` 空 dict 清空语义） | `agents/thread_state.py:21-45` | 若 agents-hive 扩 State，抄 reducer 模式而不是每次全量覆盖 |
| 流式 `RunStatus` 枚举 `pending/running/success/error/timeout/interrupted` + interrupt vs rollback 两种 cancel 语义 | `runtime/runs/schemas.py:6-14` + `runtime/runs/manager.py:111-123` + `runtime/runs/worker.py:67-223` | agents-hive 若要长任务，状态机参考此；不用抄 in-memory 存储 |

### 3.3 **别抄**（坑，原因在锚点）

| 别抄什么 | 原因 | 锚点 |
|---|---|---|
| Celery/arq 替代方案: `RunManager` + 全局 dict `_background_tasks` | 进程重启丢 run/task；cancel 协作式无法强杀工具调用；scale-out 不支持 | `runtime/runs/manager.py:40-45`（in-memory dict） + `subagents/executor.py:68-80` |
| Memory update queue 当 RAG | 当前只是 JSON summaries + confidence，不是真正的 retrieval；deer-flow 文档自己承认 TF-IDF 还没合并 | `agents/memory/queue.py` + `MEMORY_IMPROVEMENTS.md:13-18` |
| Skill 触发靠 LLM 语义匹配（prompt 注入） | 没有强制规则，LLM 可忽略；skill 是 SKILL.md + read_file progressive loading，不是宿主 pre-load | `agents/lead_agent/prompt.py:560-599` + `skills/loader.py:11-79` |
| Subagent 线程池容量硬编码 3+3+3 | 不可配；与 SubagentLimitMiddleware 的 `max_concurrent_subagents` 解耦，高并发时前者变成隐藏瓶颈 | `subagents/executor.py:73,77,80,532` |
| MCP 同步包装 worker 数 | deer-flow 设到 10（两份调研都误写 3）。如果 agents-hive 要接 MCP，要么独立 event loop，要么 worker 数做成可配 | `mcp/tools.py:19`（max_workers=10） |

---

## 第 4 轮 · 未解开的疑问（合并 4 份报告）

1. **TokenUsageMiddleware 的实际挂载条件与 usage 上报路径**: Claude Phase 1 没提，Codex 也没提。`lead_agent/agent.py:239-241` 条件 `token_usage.enabled`，需追 `token_usage_middleware.py` 确认上报到 LangSmith/Langfuse 还是本地日志。
2. **DeferredToolFilterMiddleware 的工作原理**: `tool_search.enabled` 时挂载，两份报告都没细讲 schema 延迟注入如何不影响 tool_call 解析。需读 `deferred_tool_filter_middleware.py` + `tool_search.py:58,189`。
3. **LLMErrorHandlingMiddleware vs ToolErrorHandlingMiddleware 的边界**: 前者被 `tool_error_handling_middleware.py:94` 级联注入，两份报告都当成同一个东西。需分别读两个文件对比。
4. **SandboxAuditMiddleware 的审计策略**: 文件头说"bash command security auditing"，`:197` 定义类，`tool_error_handling_middleware.py:123` 注入。具体审什么、放/挡哪些命令、是否有白名单配置？
5. **前端 SSE 断线重连协议**: Codex Phase 2 已部分展开。`runtime/stream_bridge/memory.py:51-64` 有 replay buffer，前端 `useStream` + sessionStorage `lg:stream:*` + `reconnectOnMount`，但 Last-Event-ID 格式与 SDK 内部协议仍需真实流中观察。
6. **Postgres checkpointer 的生产就绪度**: `provider.py:48-92` 只看到 `.setup()` 和 `.from_conn_string()`，没看到迁移、连接池配置、并发写测试。两份报告都没实测并发写场景。
7. **`invoke_acp_agent` 与 ACP 协议**: 只在 Codex Phase 1 闪现，两份都没深挖。ACP 是 Agent Communication Protocol 相关协议，值得单独调研。
8. **工具去重优先级表的最终仲裁代码**: Claude Phase 2 称顺序"config > builtin > MCP > ACP"，需读 `tools.py:153-168` 的去重算法（based on tool name），验证冲突时谁赢。
9. **两条 middleware 构建路径的用途边界**（重要）: `lead_agent/agent.py:_build_middlewares()` 与 `agents/factory.py:create_deerflow_agent()` 为何并存？是否有 subagent 走 path B？两份调研都没分清，值得单独挖。
10. **`build_tracing_callbacks()` 生产接入点**（Codex Phase 2 提出）: 调用点在 factory/tests 以外找不到，LangSmith 是否靠 LangChain 环境变量 global tracer 自动接入？Langfuse 是否真能工作？
11. **GATEWAY_WORKERS=4 + in-memory RunManager 在生产怎么跑**（Codex Phase 2 提出）: 若真有 4 worker，同一 run 的 SSE join、cancel、metadata 跨不到别的 worker。是否靠 nginx sticky session？还是单实例部署？
12. **K8s provisioner 的 sandbox 生命周期**: 生产 compose 里 provisioner:8002 是 optional，`/api/sandboxes` nginx 路由能落到 provisioner。两份报告都没深挖 K8s sandbox 的 pod 生命周期、TTL、清理策略。

---

## 第 5 轮 · 汇总度量

| 指标 | 值 | 备注 |
|---|---|---|
| 源文件总覆盖 | ≥ 80 个独立文件引用 | Claude Phase1 33+ / Claude Phase2 30+ / Codex Phase1 30+ / Codex Phase2 30+ |
| 双份都命中的高价值断言 | 12 个 | 信度高 |
| 蓝军 PASS（锁定正确） | 9 / 16 | 2/4/6/7/8/13/14/15/16 |
| 蓝军 PARTIAL（一份漏） | 3 / 16 | 1（middleware 漏 4 个）/ 3（Claude 漏 invoke_acp_agent）/ 12（tracing 接入点未找到）|
| 蓝军 FAIL（单份错 / 两份共错） | 4 / 16 | 5（MCP=10 不是 3）/ 9（events mode 漂移）/ 10（enqueue 501）/ 11（command 半接入）|
| 未解疑问 | 12 条 | 需要再派调研 |
| 本轮新跑蓝军（mutation 级）| 13-16 | 真实 ls/grep/Read 验证，非引用转述 |
| Claude 专漏项（经蓝军确证）| provisioner 服务（漏 1/5）、zustand 误判（前端实际无）、error boundary 误判、110 test 低估为 50+、command/checkpoint 半接入、events drift | 双盲直接暴露 Phase 1 盲点 |
| Codex 专漏项 | Claude provider OAuth/thinking budget、Reducer 行为细节 | Phase 1 codex 不如 Phase 2 codex 细 |

---

## 第 6 轮 · 面向 agents-hive 的最终 takeaway

**立刻动手的 3 件事**（按 ROI 排序）:

1. **P0-C 流式 tool_call**：抄 `client.py:615-680` 的 messages-mode 分支 — 一个循环里同时 `yield` AIMessage.text + AIMessage.tool_calls + ToolMessage，别让 tool_call chunk 只走文本回调。
2. **P0-D grounding validator**：抄 `tool_error_handling_middleware.py:19-65` 的套路 — 工具调用出异常时，`wrap_tool_call` 把异常转成 `ToolMessage(status="error")`，且保留 `GraphBubbleUp` 的控制流不被吞掉。grounding validator 本质就是另一个 wrap_tool_call。
3. **P0-B 增强**：把 "空结果" 提升到"error envelope"层次。照 `ddg_search/tools.py:72-79`：返回 `{"error": "No results found", "query": ...}` 而不是空数组；LLM 在看到 error envelope 时会重试或降级。

**架构设计不要抄**（deer-flow 自己都没做好）:

1. **`RunManager` + `MemoryStreamBridge` 进程内状态 × 多 worker 组合**（`GATEWAY_WORKERS=4` + `manager.py:40-45` + `stream_bridge/memory.py:32-35`）：除非单实例部署或有 sticky session，否则 run join、cancel、SSE replay 全部跨不了 worker。agents-hive 若多副本部署，必须把 RunManager/StreamBridge 做成 Redis/Postgres-backed。
2. **API schema 写得完整、runtime 半实现**（`RunCreateRequest.command/checkpoint` 未 plumb、`multitask_strategy="enqueue"` 直接 501、stream_mode "events" 前端允许后端跳过）：这是三个独立的"schema 领先 runtime"陷阱，agents-hive 应把 Pydantic model 和 runtime 支持度做对齐 lint。
3. **硬编码线程池 3+3+3**（`subagents/executor.py:73,77,80,532`）：与 `SubagentLimitMiddleware` 的 [2,4] clamp 不解耦，高并发时前者变瓶颈。agents-hive 若引入 subagent dispatch，池容量要跟 limit 从同一 config 读。

**抄了要小心**（有价值但要改造）:

1. **Middleware 固定顺序 + last-invariant 防御**（`factory.py:278-289` 和 `lead_agent/agent.py:275-276`）：ClarificationMiddleware 在 extra_middleware 插入后再强制回到末尾。agents-hive 抄时要把"必在最后 / 必在最前"做成 invariant check，不靠人看文档。
2. **Two middleware build paths**: `_build_middlewares()`（lead agent 18-middleware 链） vs `create_deerflow_agent()`（factory 14-middleware 链，SDK feature 默认 off）。抄前要想清楚产品 UI 和外部 SDK 用户谁走哪条。别把 "factory 链" 当文档。
3. **工具注册 YAML-driven**（`config.tools[].use` + `resolve_variable`）：减少硬编码很香，但 import path 是生产风险，`resolve_variable` 要有白名单 / 沙箱。

---

## 合并结论一句话

**deer-flow 是"LangGraph + create_agent() + AgentMiddleware + FastAPI + 进程内 RunManager"范式的最完整参考，代码质量不错但多处 schema-runtime 漂移与规模化短板，agents-hive 抄中间件 + 流式 tool_call 路径即可，Gateway 的进程内状态、enqueue/command 半实现、tracing 未接入这几条必须绕过或补全。**
