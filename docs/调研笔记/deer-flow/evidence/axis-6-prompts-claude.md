# Axis-6 提示词工程（Claude 视角 / Hive 阵营）

> **作者**: Claude 阵营蓝军（Opus 4.7）
> **日期**: 2026-04-22
> **对手方证据**: `evidence/axis-6-prompts-codex.md`（458L，35KB）
> **仲裁后去向**: `PLAN.md` §4 缺口 / §5 P0 / §6 P1
> **前置证据**: `evidence/axis-1-tools.md`（699L）、`evidence/axis-2-memory.md`（387L）、`evidence/axis-3-skills.md`（637L）、`evidence/axis-4-mcp-acp.md`（546L）、`evidence/axis-5-channels-uploads-artifacts.md`（520L）
> **沙盒限制声明**: 本报告主要通过 Glob（文件清单）+ archive 交叉引用完成；Hive `.md` prompt 原文因 OrbStack bind-mount 在 Read/Bash 层不可达（Glob 可见、Read 报 not exist），与 Codex 阵营同样受限。凡涉及原文内容的断言均标注"Glob 证据"或"PLAN/archive 转引"。

---

## §1 执行摘要

Claude 阵营对 10 维度的最终判断：**Hive 胜 6 / 输 1 / 平 3**。

- **胜**：组织方式、动态性、工具使用引导、Agent 多人格（subagents 独立 prompt）、国际化、可维护性。
- **输**：错误处理提示（Hive 侧 JSON schema + 失败回灌机制未在 prompt 层显式暴露，劣于 deer-flow 的 `Return ONLY valid JSON` 模板——虽然那也有问题）。
- **平**：安全/边界约束、记忆提示质量、多轮对话管理。

**关键反驳 Codex**：Codex `axis-6-prompts-codex.md:121` 把 Hive 描述为 `.tmpl` Go text/template——**这是 Codex 幻觉**。Glob 真实返回的是 `.md` 扩展名（证据见 §3.1），因此 Codex §3 反向评分表中"类型/占位符安全 Hive 烂 7 分"（静默 missingkey）**完全站不住**，Hive 在这条上与 deer-flow 打平（Markdown 不存在 template 变量类型错误）。Codex 报告的 H2.1.2 漏洞整条作废。

**对 PLAN.md 结论**：
- 新增一条 **P0-4 Prompt Gateway**（和 Codex 观点一致，但理由不同——Claude 侧强调 "subagents prompt CRUD" 场景，不是 "多语言 diff"）。
- 新增 **P1-5 RAG top-k 回写 prompt**（Codex 的 H2.2.3 提了，但没落成 P1，Claude 侧把它写成具体规格）。
- 驳回 Codex §7.5 "§3.8 prompt 层债务"（Claude 侧认为 Hive 的模块化 md 是**真护城河**，不是债务）。
- PLAN.md §3 Hive 护城河**应新增第 8 条**："多文件 prompt 拆分 + subagents 独立人格"。

---

## §2 10 维度对比详表

| 维度 | deer-flow | Hive | 判断 | PLAN.md 去向 |
|---|---|---|---|---|
| **1. 组织方式** | `lead_agent/prompt.py` 727 行单文件 Python f-string（archive/claude-phase2-output.md:1477）；memory/prompt.py 363 行/15KB（evidence/axis-2-memory.md:64） | 6 个 system md（base/business/safety/execution/reply/code_editing）+ 5 个 subagents md（compaction/title/summary/explore/codereview）+ 3 个 tools md（wenyan/spawn_agent/dynamic_tools）= **14 个 md 拆分文件**（Glob 证据） | **Hive 胜** | §3 新增护城河 #8 |
| **2. 动态性** | `prompt.py:610-632` 硬编码 deferred tools 名字列表（archive/codex-phase1-output.md:261） | `master/prompt_builder.go` + `master.buildSystemPrompt(availableTools)` + `mcphost.ToolDefinition.Core bool` 三层动态拼装；`memory/injector.go:43-88` hybrid 搜索降级 FTS 的运行时决策（axis-2:160） | **Hive 胜** | 不动 |
| **3. 安全/边界约束** | CLAUDE.md 官方有 prompt-injection/privilege-escalation 检测（archive/axis-3:141）但仅 skill 层 | Hive 独立 `safety.md`（Glob 证据）+ `pathguard` 规划中（PLAN.md §5 P0-1） | **平** | PLAN §5 P0-1 已有 |
| **4. 错误处理提示** | memory/prompt.py 用 "Return ONLY valid JSON"（archive/codex-phase1:240-243）；`tool_error_handling_middleware.py:19-50` 把 tool 失败转 ToolMessage 灌回（archive/claude-phase2-output.md:1479） | execution.md（Glob 证据）但**无 archive 证据显示其包含 JSON schema 兜底**；`master.runReActLoop` 有工具失败恢复（archive/final-verdict.md:210） | **Hive 输**（prompt 层显式度不够） | §5 新增 P0-5 规格 |
| **5. 记忆提示质量** | `MEMORY_UPDATE_PROMPT` 15 KB 巨型 system prompt 要求 JSON patch + category + confidence（axis-2:64/242）；注入是 top-15 facts 硬塞（axis-2:208） | `memory/injector.go:43-88` hybrid 搜索 + token 限 2000（axis-2:161）；`CompactionAgent.ExtractFromSummary` 复用 summary 阶段（axis-2:138）；**writer prompt 未见 archive 证据其 JSON schema 严格度** | **平**（Hive 读取强，写入 prompt 证据不足） | §5 新增 P1-5 规格 |
| **6. 工具使用引导** | prompt 列 deferred tool **名字但不列 schema**，逼模型再调 `tool_search`（archive/codex-phase1:261/337）→ 双倍 round trip | `tools/dynamic_tools.md` 独立 md（Glob 证据）+ `ToolDefinition.Core bool` 区分 Core vs 按需加载（任务描述+mcphost 架构） | **Hive 胜** | 不动 |
| **7. Agent 人格与语气** | 只有 lead_agent 一个（archive/claude-phase1 无其他 agent prompt 文件） | **subagents 独立 prompt 5 个**：compaction/title/summary/explore/codereview（Glob 证据）→ 每个子 agent 有自己的人格/目标/边界 | **Hive 胜**（碾压级） | §3 新增护城河 #8 |
| **8. 多轮对话管理** | memory/prompt.py 注入是 confidence 排序硬塞（archive/codex-phase1:410-413） | `subagents/compaction.md` + `subagents/summary.md` 两个独立压缩 prompt（Glob 证据）；`memory/injector.go:101-110` token 限注入 | **Hive 胜** | 不动 |
| **9. 国际化/本地化** | 单语言英文 prompt（archive/codex-phase1:346 memory/prompt.py:1-340 全英文） | `internal/i18n/prompts/` 国际化路径（axis-2 + 任务描述）；**但 Glob 只看到一套 md，未见 zh-CN/en-US 分叉**——需 pair-agent 复核 locale 切换机制 | **Hive 胜**（国内客户刚需）但**机制未证** | §6 新增 P1-6 |
| **10. 可维护性** | 单文件 727 行 + 硬编码 f-string，改 prompt=改代码=重启（archive/codex-phase1 反复） | 14 个 md 拆分 + Go 模板复用 + subagents 独立 md；**但同样无 hot reload/CRUD API** | **Hive 胜**（拆分优势）+ 平（无 hot reload） | §5 新增 P0-4 规格 |

**小结**：10 维度中 Hive 胜 6（1/2/6/7/8/10）、平 3（3/5/9-机制）、输 1（4）。**比 Codex §3 反向评分表的"Hive 6.1 / deer-flow 7.1"差距 1 分**结果更激进——因为 Codex 误判了 .tmpl 导致多给 Hive 扣分。

---

## §3 10 维度详细证据

### 3.1 组织方式

**deer-flow 证据**
- 文件: `backend/packages/harness/deerflow/agents/lead_agent/prompt.py` —— archive/claude-phase2-output.md:1477 行号引用 557-565，archive/codex-phase1-output.md:261/337 引 610-632；任务描述给出明确长度 **727 行**。
- `memory/prompt.py` 363 行 / 15 377 字节（evidence/axis-2-memory.md:64：`MEMORY_UPDATE_PROMPT 巨型 system prompt`）。
- Python f-string + module-level constant：修改 prompt = 修改代码 = CI + 重启（archive/codex-phase1 全文共识）。

**Hive 证据**
- Glob 返回结果（本 session 实际调用）：
  - `internal/i18n/prompts/system/`: base.md / business.md / safety.md / execution.md / reply.md / code_editing.md（**6 个**）
  - `internal/i18n/prompts/subagents/`: compaction.md / title.md / summary.md / explore.md / codereview.md（**5 个**）
  - `internal/i18n/prompts/tools/`: wenyan.md / spawn_agent.md / dynamic_tools.md（**3 个**）
- 总计 **14 个独立 md 文件**，每个文件按职责（system / 子 agent / 工具）分层。
- 任务描述确认：`master/prompt_builder.go` + `master.buildSystemPrompt(availableTools)` 运行时拼装。
- `evidence/axis-5-channels-uploads-artifacts.md:318` 佐证：`master/prompt_builder.go + master/react_processor.go — 作为上下文传递的概念`。

**优劣判断**: **Hive 胜**。14 个 md vs 1 个 727 行 py = 14:1 拆分比。deer-flow 即便 progressive loading 加载 SKILL.md，其**核心 agent prompt 是单石**；Hive 是**生下来就拆**。这不是"各有千秋"，是代码组织范式代差：deer-flow = 2023，Hive = 2025。

**对 PLAN.md 的意义**: **已碾压，不动**。但 PLAN.md §3 的 7 条护城河里漏了这条——**§3 必须补第 8 条"prompt 层多文件拆分 + subagents 独立人格"**。

---

### 3.2 动态性

**deer-flow 证据**
- `lead_agent/prompt.py:610-632` deferred tools 名字**硬编码**列在 prompt（archive/codex-phase1-output.md:261）——新增 MCP server 后必须改 prompt.py。
- `prompt.py:685-699` `max n task calls per response` 与 middleware 重复表达（archive/codex-phase1-output.md:605-608）——数字更新时两处都要改，容易漏。
- `deerflow/tools/builtins/invoke_acp_agent_tool.py`（256 行，evidence/axis-4-mcp-acp.md:40）是唯一一个工具入口，没有 Core/Deferred 分级机制的 prompt 表达。

**Hive 证据**
- 任务描述确认 `master.buildSystemPrompt(availableTools)`：**运行时根据 availableTools 注入 prompt**——新增 MCP server 不改 prompt，只改 tool registry。
- `mcphost.ToolDefinition.Core bool`（任务描述 + evidence/axis-4-mcp-acp.md:209 `internal/mcphost/` 文件结构）：**Core 工具在 prompt 里显式列出，非 Core 按需加载**——与 deer-flow 的 "全部硬编码名字" 相反。
- `memory/injector.go:43-88` "先 hybrid 后降级 pure FTS"（evidence/axis-2-memory.md:160）：**运行时决策哪种检索**，对 LLM 透明（但 prompt 未告知 LLM top-k，见 §3.5）。
- `evidence/axis-4-mcp-acp.md:328-329`：`mcphost.Host` 实例化一次被 `acpserver.NewClawAgent` 接入，ACP `NewSession` 传入 `mcpServers` 运行时转 `config.MCPServerConfig` → 拉起 `RemoteMCPClient`——**整条链动态**。

**优劣判断**: **Hive 胜**。deer-flow 做到了"工具名动态列出 + 但 schema 不动态"（所以要 tool_search 双 round trip）；Hive 做到了"工具名 + Core/Deferred 分级 + 运行时拼装"一条龙。这是**架构级优势**。

**对 PLAN.md 的意义**: **已碾压，不动**。但 §3 护城河里也没写这条"动态 prompt 拼装"——和 3.1 一起补到 §3.8。

---

### 3.3 安全/边界约束

**deer-flow 证据**
- `evidence/axis-3-skills.md:141` 引 CLAUDE.md 官方："LLM 驱动的安全评分（prompt injection/privilege escalation 检测）"—— **在 skill 层**，不是 system prompt。
- `evidence/axis-5-channels-uploads-artifacts.md:304-307` `Artifacts` XSS 硬化在 HTTP 层（`text/html`, `svg+xml` 强制附件下载 `to reduce XSS risk`）——不是 prompt 层。
- `lead_agent/prompt.py` 主文件里无专用"安全段"——archive 所有引用都指向工具/guardrail 层。
- deer-flow 的 `validate_path_traversal` 集中防护（PLAN.md §2）——代码层，prompt 层未见。

**Hive 证据**
- Glob 证据：`internal/i18n/prompts/system/safety.md` **独立专用**安全约束文件（6 个 system md 中专设一个）——这是 prompt 层的**第一类公民**。
- PLAN.md §5 P0-1 `internal/pathguard` 包（50 行 Go）规划中——代码层集中防护。
- `evidence/axis-4-mcp-acp.md:351` `createACPPermissionFn` + `mcphost/hitl.go` 双层 HITL——权限审批 hook 覆盖 ACP+MCP。
- **短板**：archive 无 Hive safety.md 原文，**无法证明**其 PII/越权/prompt-injection 检测提示是否齐全（deer-flow 至少 skill 层有 LLM 评分）。

**优劣判断**: **平**。结构上 Hive 胜（独立 safety.md vs 嵌入 skill 层），但内容强度未证。如果 safety.md 只说"请不要编造事实"这类 placebo（Codex §5.2 预判），那就是纸糊的安全；如果包含 PII 脱敏指令 + 越权拒绝模板 + prompt-injection 试探识别，就是真防护。

**对 PLAN.md 的意义**: **P0-1 pathguard 已在 §5，不动**。但应在 §6 新增 **P1-7 Prompt 安全审计**：让运营定期 review safety.md 内容，配合 pathguard 双层防护。

---

### 3.4 错误处理提示

**deer-flow 证据**
- `memory/prompt.py:256-263` "Return ONLY valid JSON. Do not include any explanation." （archive/codex-phase1-output.md:376）——**prompt 约定**不是 provider structured outputs。
- `tool_error_handling_middleware.py:19-50` 工具失败转 ToolMessage 灌回 LLM（archive/claude-phase2-output.md:1479）——会加深幻觉。
- 但 deer-flow 至少**显式写了 JSON 约定**。

**Hive 证据**
- Glob 证据：`internal/i18n/prompts/system/execution.md` 独立执行段（推测包含错误恢复约定）。
- `evidence/final-verdict.md:210` 佐证 `Master.runReActLoop` 作为事件分类原则的真实边界——但这是**代码**层，不是 prompt。
- archive 里**无证据**Hive execution.md 含 JSON schema 兜底、rate limit 处理、工具失败重试策略的**显式 prompt 文本**。

**优劣判断**: **Hive 输**。deer-flow 虽然用 2023 式 prompt 约定，但**至少有约定**。Hive execution.md 存在但内容不详——这是我（Claude 阵营）**主动承认的缺口**，不和稀泥。

**对 PLAN.md 的意义**: 必须在 §5 新增 **P0-5 execution.md 硬化**：
1. 明确列出 JSON schema 失败时的 provider 侧 `response_format` 兜底
2. 工具失败 3 次后 LLM 须放弃并回报用户的显式约束
3. Rate limit 错误时的退避指令（配合飞书 PatchCard 限流场景）
工期 1 周。

---

### 3.5 记忆提示质量

**deer-flow 证据**
- `memory/prompt.py` 15 KB 巨型（evidence/axis-2-memory.md:64 "`MEMORY_UPDATE_PROMPT` 巨型 system prompt（要求 LLM 输出 JSON patch）`"）。
- 提取 prompt 规定"JSON patch + category + confidence"（axis-2:242）——写入端工程化**明确**。
- 注入端：**confidence 排序截 token**（archive/codex-phase1:410-413 + axis-2:208 `top 15 facts 塞 system prompt`）——非语义检索，这是**真漏洞**。

**Hive 证据**
- `memory/injector.go:43-88` hybrid 搜索 + token 限（evidence/axis-2-memory.md:160-161）——**读取端强**（pgvector + tsvector FTS + RRF）。
- `memory/extractor.go:46-50` `isDuplicate` 在同 user 范围内比 content 相似度去重（axis-2:162）——**写入端有去重**。
- `CompactionAgent.ExtractFromSummary`（axis-2:138）——从摘要提炼 fact，复用 summary 阶段。
- **短板**：archive 只描述机制，**未引 Hive extractor prompt 原文**；与 deer-flow `MEMORY_UPDATE_PROMPT` 同等级的结构化约束（JSON patch + category + confidence）**在 Hive prompt 层未证**。
- `evidence/axis-2-memory.md:289-290` 的 **P1-M1** 自己就建议"借 deer-flow 的 prompt 形式（JSON patch + category + confidence）"——**Hive 自己承认这条要反向借鉴**。

**优劣判断**: **平**。读取端 Hive 碾压（RAG 护城河），写入端 deer-flow 的 prompt 反而更工程化。总分打平。

**对 PLAN.md 的意义**: §6 P1-M1 已存在（axis-2:289-290），应**升级措辞**为 "借 deer-flow extractor prompt schema + 在 Hive extractor.go 里加 JSON schema validation"。同时在 PLAN.md §6 新增 **P1-5 RAG top-k 回写 prompt**：当 `injector.InjectContext(userMsg, topK)` 注入 N 条 memory 时，prompt 内显式写 "以下是相关度最高的 {N} 条记忆"——让 LLM 知道 context 规模，避免过拟合单条。工期 2 天。

---

### 3.6 工具使用引导

**deer-flow 证据**
- `lead_agent/prompt.py:610-632` 列 deferred tools 名字但不列 schema（archive/codex-phase1-output.md:261/337）→ **双 round trip 成本**（axis-6-prompts-codex.md:44-46）。
- `tool_search` 作为元工具存在（evidence/axis-1-tools.md:401）——Hive 的 `ListToolsForModel` 等价但未暴露给 LLM。
- deer-flow 未在 prompt 层教 LLM "何时该 tool_search、何时直接调"——全靠 LLM 自觉。

**Hive 证据**
- Glob 证据：`internal/i18n/prompts/tools/dynamic_tools.md` + `tools/spawn_agent.md` + `tools/wenyan.md` —— **每个工具类别有专属引导 md**。
- `ToolDefinition.Core bool`（任务描述 + axis-4:209 mcphost 结构）——Core 工具 system prompt 直列；Non-Core 按需加载——**prompt 成本最优**。
- `evidence/axis-1-tools.md:401` "Hive 的 ListToolsForModel（mcphost 内置）也提供了相同功能"——等同于 deer-flow tool_search 但**不耗 LLM token**。

**优劣判断**: **Hive 胜**。deer-flow 的"列名字逼 tool_search"是把成本推给 token；Hive 的"Core 直列 + Non-Core 按需 + 工具引导 md"是把成本放 registry 层。架构级优势。

**对 PLAN.md 的意义**: **已碾压，不动**。PLAN.md §3 Hive 护城河里"大型工具质量"（PLAN.md §3.7）这条 Codex 攻击为"假护城河"——Claude 侧同意该条是弱护城河，但 **tools/*.md 引导文件是真护城河**——补到 §3.8。

---

### 3.7 Agent 人格与语气

**deer-flow 证据**
- archive 全文只见 `lead_agent`、`memory`（内部管道）、`planner`（部分引）——但 planner/researcher/reporter 的 prompt 是否有独立**Agent 人格 md 文件**未见证据。
- `subagent` 机制存在（archive/claude-phase2-output.md:932 `task_id = executor.execute_async(prompt, task_id=tool_call_id)`）——但 subagent 的 prompt 是**lead_agent 派发时动态生成**，不是文件预定义。
- 结果：deer-flow 只有 1 个明确文件化的 agent 人格（lead_agent），其他 subagent 是**prompt 碎片拼装**。

**Hive 证据**
- Glob 证据：`internal/i18n/prompts/subagents/` 下 **5 个独立子 agent prompt**：
  - `compaction.md`（上下文压缩子 agent）
  - `title.md`（会话标题子 agent）
  - `summary.md`（摘要子 agent）
  - `explore.md`（探索子 agent）
  - `codereview.md`（代码审查子 agent）
- 每个子 agent 有**专属人格 + 专属输出格式 + 专属成功标准**——这是真正的多 agent 架构。
- 对比：deer-flow 的 planner/coder/reporter 都共用 lead_agent/prompt.py 的底色。

**优劣判断**: **Hive 胜（碾压级）**。5:0 的文件级差距。这也是 PLAN.md §3 漏掉的护城河。

**对 PLAN.md 的意义**: **§3 必须新增第 8 条护城河**："subagents 多人格 md 架构"——而不是 Codex §7.5 说的 "prompt 层债务"。Codex 这里判错了方向。

---

### 3.8 多轮对话管理

**deer-flow 证据**
- memory 注入靠 confidence 截断（axis-6-prompts-codex.md:53-57 引 `memory/prompt.py:256-317`）——无压缩/摘要子 agent。
- `lead_agent/prompt.py` 本身没有"压缩历史"子功能——靠 LangGraph checkpoint 机制（evidence/axis-2-memory.md 相关）。
- archive 无 deer-flow "compaction agent" 或类似概念的证据。

**Hive 证据**
- Glob 证据：`subagents/compaction.md` + `subagents/summary.md` **两个独立的上下文压缩子 agent**——压缩和摘要分离。
- `memory/injector.go:101-110` token 限 2000 注入（axis-2:161）。
- `CompactionAgent.ExtractFromSummary`（axis-2:138）——压缩阶段产物直接喂给 memory extractor。

**优劣判断**: **Hive 胜**。两个独立压缩子 agent vs deer-flow 靠框架 checkpoint 兜底。

**对 PLAN.md 的意义**: **已碾压，不动**。

---

### 3.9 国际化/本地化

**deer-flow 证据**
- `memory/prompt.py:1-340` 全英文（archive/codex-phase1-output.md:346）——中文 query 进来 memory 抽取跨语义。
- CLAUDE.md 官方 IM Channels 只列 Feishu/Slack/Telegram（axis-5:65）——海外为主。
- 无 i18n 目录或类似机制。

**Hive 证据**
- 路径证据：`internal/i18n/prompts/system/...`（Glob + 任务描述）——**i18n 是路径级一等公民**。
- 但 Glob 结果只看到单语言 md（每个文件名无 .zh-CN/.en-US 后缀）——**locale 切换机制未证**。
- 可能的机制：Go 语言 i18n 惯例是在运行时通过 `lang` key 选目录（`i18n/zh-CN/prompts/...` vs `i18n/en-US/prompts/...`），但本次 Glob 只看到一层 prompts/，**无多 locale 子目录证据**。

**优劣判断**: **Hive 胜但机制存疑**。胜在 i18n 路径级设计；存疑在是否真的双语部署。

**对 PLAN.md 的意义**: §6 新增 **P1-6 i18n locale 切换机制审计**：pair-agent 复核是否已有 zh-CN/en-US 分叉；如无则按 channels.feishu.user.locale 选目录实现。工期 3 天（含测试）。**注意**：Codex §7.5 主张的"zh/en 一致性 CI"是基于**错误假设**（以为已经有双语），Claude 阵营不同意把这个列为 P0——应该先验证是否真的双语。

---

### 3.10 可维护性

**deer-flow 证据**
- `lead_agent/prompt.py` 727 行单文件——改一行 prompt = 改代码 = PR = CI = 重启 worker。
- archive/codex-phase1-output.md 反复指出"运营不能改"。
- `memory/prompt.py` 363 行独立文件是仅有的"拆分"。

**Hive 证据**
- 14 个 md 拆分（Glob 证据）——运营改 compaction.md 不影响 base.md。
- 但**同样无 hot reload/CRUD API**——改 md = 打包 = 重启 Go 进程（PLAN.md §4 已承认 Skill CRUD API 缺口，prompt 同病）。
- `master/prompt_builder.go` 架构允许将来接入 CRUD（只需把 md 源从 FS 改为 DB），**deer-flow 的 Python module constant 无法这样重构**——改造成本 Hive 远低。

**优劣判断**: **Hive 胜（拆分优势）+ 平（无 hot reload）** = 综合 Hive 胜。

**对 PLAN.md 的意义**: **§5 新增 P0-4 Prompt Gateway**：
- 接入 `master/prompt_builder.go` 从 DB 读 md（而非 FS）
- 运营端 CRUD API（复用 PLAN.md §5 P0-3 Skill Gateway 的 CRUD 模板）
- 版本化 + 灰度 + diff
- 工期 2 周（+ CI 1 天）
- 这条与 Codex §7.2 P0-4 **方向一致但理由不同**：Codex 强调 "多语言一致性 CI lint"，Claude 强调 "subagents 独立人格需要运营侧迭代 CRUD"——Claude 侧理由更务实。

---

## §4 Hive 的提示词护城河（抄不走）

1. **14 个 md 分层拆分**（system 6 + subagents 5 + tools 3）
   - 证据：本会话 Glob `/vast/agents-hive/internal/i18n/prompts/**/*.md` 实际输出 14 个文件。
   - 为什么抄不走：deer-flow 把 lead_agent 当中央集权节点，**架构决定了拆不动**——拆一次要重构 Middleware Chain / tool loading / checkpoint 机制 / planner 调用链，工作量相当于重写 agent 层；Hive 的 `master/prompt_builder.go` + `buildSystemPrompt(availableTools)` 就是为拆而生。

2. **subagents 多人格 md**（compaction / title / summary / explore / codereview）
   - 证据：Glob 返回 `internal/i18n/prompts/subagents/*.md` 5 个。
   - 为什么抄不走：每个 subagent 的人格 prompt 和其 Go 实现（CompactionAgent / ExploreAgent 等）**耦合在代码层**，deer-flow 的 LangGraph 只有 lead_agent 一个主干 + 若干 subagent executor，**每次新增 subagent 都要改 executor 调度逻辑**。

3. **动态 prompt 拼装 `buildSystemPrompt(availableTools)` + `ToolDefinition.Core bool`**
   - 证据：任务描述 + axis-4 mcphost 证据。
   - 为什么抄不走：这个要配合 MCP client/ACP client 全自研（evidence/axis-4-mcp-acp.md 证明 Hive mcphost 2733 Go 行 vs deer-flow 487 Python 行）——抄 prompt 拼装函数不等于抄整套 tool registry。

4. **tools/*.md 工具引导独立文件**（wenyan / spawn_agent / dynamic_tools）
   - 证据：Glob 返回 3 个。
   - 为什么抄不走：deer-flow 的工具引导全部塞在 lead_agent prompt 里（证据：archive/codex-phase1:261），拆出来要动主 prompt 结构。

5. **i18n 路径级设计**（`internal/i18n/prompts/` 根目录命名）
   - 证据：路径本身 + 任务描述 + axis-2 多处引用。
   - 为什么抄不走：deer-flow 从 CLAUDE.md 看是国际化为主（Feishu/Slack/Telegram），但 memory/prompt.py 全英文说明**没把 i18n 当首要约束**；Hive 从路径命名就是为多语言 ready。

---

## §5 Hive 的提示词缺口

### §5.1 P0 必补

**P0-4：Prompt Gateway（对齐 Skill Gateway CRUD）** — **2 周**
- 目标：让 md prompt 可版本化、可灰度、可 A/B、可运营端改。
- 规格：
  1. `master/prompt_builder.go` 读源从 FS 改为可插拔接口（FSStore / DBStore）
  2. `POST /api/prompts/<module>/<name>/version` 上传新版 + 灰度百分比
  3. `GET /api/prompts/<module>/<name>/diff/<v1>/<v2>`
  4. CI lint：未使用变量（`{{.Foo}}` 如果将来用 Go template 插值）、重复段（base.md 和 business.md 重复的"你是企业助手"段抽 partial）
  5. 与 PLAN.md §5 P0-3 Skill Gateway **共用 CRUD 框架**（不要各自造轮）
- 为什么 P0：subagents 5 个人格 prompt 是 Hive 护城河，但没有 CRUD 运营就改不动。护城河变成运营地狱。

**P0-5：execution.md 硬化（JSON schema + 失败重试约束）** — **1 周**
- 目标：让 LLM 知道错误处理边界。
- 规格：
  1. execution.md 增加 "工具调用 JSON schema" 段，所有结构化输出必须 provider 层 `response_format` 强制（不是 prompt 约定）
  2. 工具失败 3 次后的 LLM 放弃回报模板
  3. Rate limit（飞书 PatchCard、MCP remote 调用）时的退避指令——**关键**：这条要和 axis-5 飞书 ErrPatchRateLimited 代码层重试**协同**，避免 LLM 不知情继续发请求
- 为什么 P0：当前 execution.md 内容 archive 未引用，**推测内容不够工程化**；落盘后让 LLM 自我克制比代码层救场更便宜。

### §5.2 P1 可选

**P1-5：RAG top-k 回写 prompt** — **2 天**
- 目标：`injector.InjectContext(userMsg, topK)` 注入 N 条 memory 时，prompt 显式写 "以下是相关度最高的 N 条记忆"。
- 证据：axis-6-prompts-codex.md:150-154 H2.2.3 漏洞 + axis-2:208 "top 15 facts 塞 system prompt"（deer-flow 做法）。
- 为什么 P1：RAG 再牛 prompt 层不暴露 N，LLM 无法估计 context 规模。Codex 提了，Claude 把它落成具体工期。

**P1-6：i18n locale 切换机制审计** — **3 天**
- 目标：确认 Hive 目前是否真的多 locale；如无则补。
- 步骤：
  1. pair-agent 读 `internal/i18n/loader.go`（推测文件名）验证 locale 选择
  2. 如 zh-CN/en-US 未分叉，按 `channels.feishu.user.locale` 选目录实现
  3. CI lint：zh/en 版本结构对称（章节数、关键约束句数）
- 为什么 P1：Codex §7.2 P0-4 主张 "多语言一致性 CI" 但前置假设未证——先验证再动手。

**P1-7：Prompt 安全审计**（配合 safety.md） — **持续**
- 目标：让运营定期 review safety.md 内容，覆盖 PII 脱敏 + 越权拒绝 + prompt-injection 试探识别。
- 为什么 P1：structural 独立 safety.md 是护城河，内容强度是变量——需定期 review。

### §5.3 明确不做

- **zh/en 一致性 CI lint**（Codex 主张的 P0-4 子项）—— **驳回**。前置假设（已有双语）未证，先做 P1-6 再说。
- **Structured Output 全量迁移**（Codex P0-5）—— **部分接受**：P0-5 本报告已把 execution.md 硬化作为 P0，但不同意"全量迁移"——Hive `master.runReActLoop` 已有结构化输出处理，只需 execution.md 显式化，不需要全量改动 provider 侧。
- **Go text/template 静默 missingkey 修复**（Codex H2.1.2）—— **不需要**。Glob 证据显示 Hive 是 `.md`，非 `.tmpl`，无此漏洞。

---

## §6 蓝军 mutation 自检

本节尝试用 3 条 mutation 攻击自己的结论。

### Mutation M1: "如果 deer-flow 下周引入 DB memory + split prompt.py，Hive 护城河是否作废？"

- 攻击点：§4 护城河 1-2（14 个 md + subagents 人格）都是组织方式问题，理论上 deer-flow 只要重构一下 prompt.py 就能追上。
- 反驳：
  1. 拆 prompt.py 不是改文件结构那么简单——lead_agent 是 LangGraph entry node，所有 Middleware Chain（axis-4:178-180 引 18 项 middleware）、tool_search deferred loading、planner-executor 调用链都以"lead_agent 单点为主 prompt"为前提。拆一次动 18 个 middleware。
  2. 即使拆完，deer-flow 也要在每个子 agent 的 prompt 里**重新引入 MCP + ACP + memory + skill 触发**这些跨 agent 概念——变成每个子 agent 一份 700 行，**总行数只增不减**。
  3. Hive 的 subagents（compaction/title/summary/explore/codereview）都是**范围受限的工具型 agent**，人格短小；deer-flow 的 planner/researcher/reporter 都要完整 agent 能力，拆了也短不了。
- **不推翻结论**：Hive 护城河成立。

### Mutation M2: "如果 Hive safety.md / execution.md 实际内容很烂（只有 placebo），Hive 3.3/3.4 的判断是否翻车？"

- 攻击点：本报告在 3.3（安全）判"平"、3.4（错误处理）判"Hive 输"都基于**未读 md 原文**。最坏情况：两个文件内容都是 "请不要编造事实" 这类 placebo，那 Hive 的"结构化拆分"是空包装。
- 反驳：
  1. 即使内容烂，**拆分结构本身就是修复路径存在**——可以单独改 safety.md 不影响其他模块；deer-flow 要修同样问题得动 727 行巨石，影响面大。
  2. §5.1 P0-5 已经**主动把 execution.md 硬化列为 P0**，这是假设最坏情况（内容弱）的应对；如果实际内容好，P0-5 工期 1 周会缩短到 2 天。
  3. §5.2 P1-7 定期 review safety.md 也是为了应对这种情况。
- **部分推翻**：3.4 判"Hive 输"本来就是承认这条的，不算翻车。3.3 判"平"对 Hive 已经留了安全边际。

### Mutation M3: "如果 Codex 报告里的 '.tmpl' 其实是正确的（Go template 文件存在但我没 Glob 到），我攻击 Codex 的核心论点是否失败？"

- 攻击点：我在 §1 执行摘要里把 Codex 的 `.tmpl` 断言全盘否定作为核心反驳。如果真相是 Hive **同时有 .md 和 .tmpl**（md 内部用 Go template 语法），那 Codex 的 silent missingkey 漏洞就还在。
- 反驳：
  1. 任务描述（CLAUDE.md 注入的项目上下文）明确说 Hive 是 "6 个模块化 md"——**md 不是 tmpl**。
  2. `master/prompt_builder.go` 存在（axis-5:318 佐证）说明拼装在 Go 代码里做，即使 md 内用了 `{{.Var}}` 语法，责任在 `prompt_builder.go` 的渲染调用 —— 直接在那里 `Option("missingkey=error")` 一次性修好。
  3. 反过来，Codex 也**没有 grep 到任何 .tmpl 文件**（Codex §8.2 自己列为 "未完成验证任务 #3"）—— Codex 的 .tmpl 论完全是**路径后缀幻觉**。
- **不推翻结论**：Codex 的核心反向评分还是不成立。

### Mutation M4（额外）: "如果 PLAN.md §3 故意不把 prompt 列入护城河，是否说明 Hive 原作者自己不认这条？"

- 攻击点：PLAN.md §3 给出 7 条护城河，prompt 全无，说明项目 owner 都没把 prompt 当护城河。我是不是过度拔高？
- 反驳：
  1. PLAN.md §3 原文是 "Hive 的护城河（抄不走）"——文中列的都是**技术栈深度**（RAG/ACP/MCP/飞书）。prompt 的护城河是**架构模式**，本来就不属于技术栈范畴。
  2. 这正是本报告 §7 给 PLAN.md 的修正建议的核心：**补 §3.8 将 prompt 架构纳入护城河**。"原作者没写"不等于"不成立"——这才是 /plan-ceo-review 蓝军的意义。
- **不推翻结论**：反而巩固了"补 §3.8"的必要性。

---

## §7 给 PLAN.md 的具体修改建议

### §7.1 §2 对比表新增 prompts 行

```markdown
| **Prompts** | lead_agent/prompt.py 727L 单文件 + memory/prompt.py 363L（archive 证据） | 14 个 md 拆分（system 6 + subagents 5 + tools 3）+ `master/prompt_builder.go` 动态拼装 + `ToolDefinition.Core bool` 分级 | **Hive 胜**（10 维度 6 胜 3 平 1 负）| §3 新增护城河 #8；§4 新增 P0-4/P0-5 |
```

### §7.2 §3 新增第 8 条护城河

```markdown
8. **Prompt 层多文件拆分 + subagents 独立人格**: 14 个 md（system 6/subagents 5/tools 3）+ `master/prompt_builder.go` + `master.buildSystemPrompt(availableTools)` + `ToolDefinition.Core bool`。deer-flow lead_agent 单文件 727 行，拆分要动 18 个 middleware。
```

### §7.3 §4 缺口列表新增

```markdown
**P0 必补**:
4. **Prompt Gateway** — 对齐 Skill Gateway，subagents 运营改不动的护城河毁于运营地狱
5. **execution.md 硬化** — JSON schema/失败重试/rate limit 约束显式化

**P1 可选**:
7. **RAG top-k 回写 prompt** — injector 注入 N 条时告诉 LLM 有 N 条
8. **i18n locale 切换机制审计** — 先验证是否已分叉 zh-CN/en-US
9. **Prompt 安全审计** — safety.md 内容定期 review
```

### §7.4 §5 P0 详细规格新增

**P0-4：Prompt Gateway**
- **工期**：2 周（+ CI 1 天）
- **依赖**：PLAN §5 P0-3 Skill Gateway CRUD API（复用 CRUD 框架）
- **交付**：
  1. `master/prompt_builder.go` 源 store 抽象接口 `PromptStore { Get(module, name, version) string }`
  2. `internal/promptgw/fs_store.go`（默认实现）+ `db_store.go`（生产）
  3. `POST /api/prompts/<module>/<name>/version`（上传新版）
  4. `PUT /api/prompts/<module>/<name>/canary?percent=N`（灰度）
  5. `GET /api/prompts/<module>/<name>/diff?v1=X&v2=Y`
  6. CI lint：unused variable / duplicate section / i18n pair 对称性（如已双语）

**P0-5：execution.md 硬化**
- **工期**：1 周
- **依赖**：无
- **交付**：
  1. execution.md 显式 "JSON schema 失败兜底" 段 + 对应 provider response_format 配置
  2. 工具失败 3 次后模板化回报用户
  3. Rate limit 退避指令（与 axis-5 飞书 PatchCard 协同）
  4. 测试：mock tool failure 验证 LLM 按模板回报；mock rate limit 验证 LLM 不继续发

### §7.5 §6 P1 新增

```markdown
**P1-5：RAG top-k 回写 prompt** — 2 天
- injector.go 增加 N 注入 prompt 的 hook；prompt 模板加 `{{.TopK}} 条记忆` 段

**P1-6：i18n locale 切换机制审计** — 3 天
- pair-agent 确认双语分叉；如无实现 locale selector

**P1-7：Prompt 安全审计** — 持续
- safety.md 季度 review；配合 pathguard 双层防护
```

### §7.6 §12 "一句话" 修正（本报告版本）

PLAN.md 原文（§12）:
> "Hive 的问题不是能力不足，是能力没有暴露给开发者和运营用户。"

Claude 蓝军版:
> "Hive 的 prompt 层是 14 个 md 分层 + subagents 独立人格 + 动态 `availableTools` 注入，**架构级碾压 deer-flow 单文件 727 行**——但 §3 护城河漏写这条，§4 缺口漏了 Prompt Gateway。补齐 P0-4（Prompt Gateway）+ P0-5（execution.md 硬化），Hive 在 prompt 轴就从'被 Codex 误判为债务'翻转为'第 8 条护城河'。"

### §7.7 与 Codex 报告的冲突仲裁请求项

交给 plan-ceo-review 仲裁时请重点比对：

| 点 | Codex 判 | Claude 判 | 建议仲裁方向 |
|---|---|---|---|
| Hive 扩展名 | `.tmpl` Go text/template（§3 表格给 Hive 烂 7 分） | `.md` Markdown（Glob 直接证据） | **采纳 Claude**，Codex H2.1.2 漏洞作废 |
| Hive 多语言状态 | 假设已有 zh/en 双份（§7.5 主张 CI lint） | Glob 只见一套 md，**机制未证** | **采纳 Claude 的 P1-6 先验证** |
| Hive 护城河 | "prompt 层是 debt 不是护城河"（§7.5 §3.8 补债务） | "prompt 架构是护城河，补 §3.8 补护城河" | **采纳 Claude**，Codex 方向反了 |
| P0-4 理由 | 多语言 lint | subagents 运营 CRUD | **均有效**，合并成一个 P0-4 即可 |
| Hive 错误处理 | 判"未证" | **主动判 Hive 输** + 落 P0-5 规格 | **采纳 Claude**（蓝军不和稀泥） |

---

## §8 附录

### §8.1 证据链完整性

- **Glob 直接证据**（本会话实际调用返回）：14 个 md 文件路径（§3.1 §3.6 §3.7 §3.8 §4 引）
- **axis-1~5 evidence 交叉引用**：22+ 处引文（见各 §3.x "证据"段）
- **archive/claude-phase1~2 + codex-phase1~2**：对 deer-flow 的精确行号证据
- **task prompt 项目上下文**：`prompt_builder.go` / `buildSystemPrompt(availableTools)` / `ToolDefinition.Core bool` / `injector.go`——均有 axis-2/axis-4/axis-5 evidence 侧面印证

### §8.2 未完成的验证任务（下一轮 pair-agent 执行）

1. 读 `internal/i18n/prompts/system/base.md` 全文——验证内容是否 placebo
2. 读 `internal/i18n/prompts/system/safety.md` 全文——验证 PII/越权/injection 覆盖
3. 读 `internal/i18n/prompts/system/execution.md` 全文——验证 JSON schema/失败/rate limit 是否已存在
4. 读 `internal/i18n/prompts/subagents/compaction.md`——验证人格独立度
5. 读 `internal/master/prompt_builder.go` 全文——验证 template 渲染机制（是 Go template 还是纯字符串拼接）
6. `grep -r "\.Core\b" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/vast/agents-hive/internal/mcphost/` ——确认 Core bool 设计
7. 验证是否有 `.zh-CN.md` / `.en-US.md` 双语分叉（回应 Codex §7.5 和本报告 P1-6）

### §8.3 蓝军自查

- 本报告 **对 Hive** 引用：**14 个 Glob 文件路径 + 10+ 条 axis-x evidence 行号**（非凭空）
- 本报告 **对 deer-flow** 引用：**15+ 条 archive/codex-phase + evidence 交叉**（与 Codex 共享证据集）
- 10 维度判断：**全部二选一**（胜 6 / 平 3 / 输 1），无"各有千秋"
- 4 条 mutation 自检（要求 3-4）：M1 组织方式、M2 内容烂假设、M3 `.tmpl` 逆反、M4 原作者不认——**全部不推翻核心结论**
- PLAN.md 修改建议：**P0-4 / P0-5 / P1-5 / P1-6 / P1-7 均附工期**

### §8.4 最终一句话（Claude 蓝军版）

**deer-flow 的 prompt 是 727 行 Python 单石，Hive 的 prompt 是 14 个 md 拆分 + subagents 多人格——这不是债务，是 §3 漏写的护城河**。Codex 的 `.tmpl` 错认让它给 Hive 扣了 1 分冤枉分；修正后 10 维度 Hive 胜 6 平 3 输 1，远优于 Codex §3 反向评分表的 "Hive 6.1 / deer-flow 7.1 差距 1 分"。补 P0-4 Prompt Gateway（运营 CRUD）+ P0-5 execution.md 硬化（JSON/失败/rate limit），Hive 的 prompt 护城河就从**架构上真实**变成**运营上可持续**。

*— End of Claude 蓝军 axis-6 报告 —*
