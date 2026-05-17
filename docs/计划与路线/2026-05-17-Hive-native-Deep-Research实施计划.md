# Hive-native Deep Research 实施计划

> **日期**：2026-05-17  
> **状态**：设计与路线计划，尚未实施  
> **核心结论**：Deep Research 必须按 Hive-native 路径落地。供应商 Deep Research、搜索引擎、MCP、企业 KB、文件和数据库都只能作为可插拔数据源或研究 worker，不能绕过 Hive 的研究协议、证据账本、权限、回放和评测。  
> **执行口径**：计划一次性覆盖终局架构和阶段拆解，实施必须分阶段、小步验证。P0 只建立可信研究主干，不追求一次做完整产品。

---

## 1. 总结论

Hive 的 Deep Research 不应是“加一个联网搜索报告按钮”，也不应是“包一层 OpenAI / Perplexity / Gemini Deep Research API”。正确终局是：

**Hive-native Research OS：由 Hive 掌控研究协议、计划审批、数据源接入、证据账本、claim graph、报告合成、引用校验、回放审计和质量评测。**

外部能力可以接入，但只能位于边界内：

```text
Hive Research Orchestrator
  -> Web Search Provider
  -> Web Fetch / Browser Provider
  -> KB Provider
  -> File / Asset Provider
  -> MCP / Internal Data Provider
  -> Optional Vendor Deep Research Worker
  -> Evidence Ledger
  -> Claim Graph
  -> Report Writer
  -> Critic / Eval
```

这条路径工程量最大，但战略价值最高。原因：

- Hive 的现有优势是 Agent Runtime、SubAgent、KB evidence、权限、Replay、Quality Workbench 和运行治理，不是单次网页搜索。
- 企业客户真正需要的是“敢用于决策的可信研究产物”，不是一篇看起来流畅的长文。
- 公网资料、企业 KB、内部文件、MCP/数据库和历史会话必须进入统一证据层，不能在报告里混成不可追责的自然语言上下文。
- 供应商能力会变化，Hive 的研究 run、evidence、claim、citation、eval 标准必须保持平台主权。

一句话定义产品：

**Hive Deep Research 是一个可审计、可重跑、可评测的企业研究运行时。没有证据就不写结论，有争议就暴露不确定性，失败也要留下可复盘的研究轨迹。**

## 2. 当前源码事实

Hive 已经具备 Deep Research 的关键底座，但还没有一条一等公民级研究工作流。

### 2.1 已有底座

| 能力 | 当前事实 | Deep Research 复用方式 |
|---|---|---|
| Agent Runtime | README 定义 Hive 是 Agent Runtime + Agent Harness + Quality Control Plane + Ops Workbench | 研究任务作为长任务 run，复用会话、工具调用、HITL、上下文压缩和恢复 |
| Web Search | `internal/tools/websearch.go` 注册 `websearch`，默认 DuckDuckGo HTML endpoint，支持 allow/block domain、max results、strict 零结果策略 | 作为公网发现入口，不直接成为证据结论 |
| Web Fetch | `internal/tools/webfetch.go` 注册 `webfetch`，优先 agent-browser 渲染，失败降级 HTTP，并有 SSRF 防护 | 作为网页正文取证入口，抓取结果进入 evidence ledger |
| Browser | `internal/tools/browser.go` 注册 `browser_interact`，支持 navigate/snapshot/click/fill/eval/wait/screenshot | 用于少量动态网页和登录态场景，P0 不把它作为默认 crawler |
| KB | `internal/tools/kb.go` 已有 `kb.doc.meta`、`kb.doc.structure`、`kb.section.text`，README 明确 PageIndex-style tree-mode | 企业知识源必须通过 KB provider 进入证据账本 |
| Citation / Grounding | `internal/master/grounding.go` 能从工具输出收集 URL evidence，并阻止未验证 URL/citation | P0 扩展为 claim-level evidence 校验，不只校验 URL 是否出现 |
| SubAgent | `internal/tools/spawn_agent.go` 支持动态 Agent，Master 限定调用 | 研究 worker 可以被动态创建，但必须写 notes/evidence，不直接写最终结论 |
| Parallel Dispatch | `internal/tools/parallel_dispatch.go` 支持并行派发到 SubAgent | 并行处理研究子问题 |
| Quality | `internal/agentquality`、`internal/qualityworkbench` 已有样本、评估、回放和报告能力 | 研究质量门禁和回归评测直接复用，不另建平行质量系统 |
| Asset / Canvas | README 和现有计划中已有统一 asset、Canvas、artifact 路线 | 后续承载研究报告 HTML/PDF/DOCX，不作为 P0 阻塞项 |

### 2.2 关键缺口

当前系统还缺：

- 没有 `research_run`：研究任务没有独立状态机、预算、进度、恢复和审计模型。
- 没有 `research_brief`：用户问题没有被结构化为目标、范围、时间、来源偏好、约束和不确定点。
- 没有 `research_plan`：长研究前没有用户可审批或可修改的计划。
- 没有统一 `evidence_ledger`：网页、KB、文件、MCP、vendor worker 输出没有统一证据账本。
- 没有 `claim graph`：最终报告中的关键结论没有逐条绑定 evidence。
- 没有研究 worker 协议：SubAgent 可以并行执行，但没有“只产出 notes/evidence、不得写最终结论”的契约。
- 没有 critic 门禁：报告生成后没有系统性检查弱证据、冲突、过期来源、无引用 claim 和单一来源依赖。
- 没有研究 UI：用户看不到 brief、计划、进度、来源、证据、报告和失败原因。
- 没有研究 eval：缺少 research golden cases、citation hit rate、claim support rate、source freshness、反证覆盖等指标。

## 3. 产品原则

### 3.1 不可妥协原则

1. **研究主权在 Hive**
   - 研究 run、计划、证据、claim、citation、报告、eval 的事实源必须在 Hive。
   - 外部 Deep Research provider 只能输出候选 evidence 或 worker notes，不能直接成为最终报告事实源。

2. **没有证据就不写结论**
   - 关键 claim 必须绑定 evidence。
   - 找不到证据时输出“证据不足”“未能确认”“需要人工验证”，不能补一个漂亮结论。

3. **先计划，再研究**
   - 长任务研究必须先生成 brief 和 plan。
   - 用户可以确认、修改、收窄或取消。
   - 对低风险短问题可走普通 Web/KB 问答，不自动升级 Deep Research。

4. **并行 worker 只找证据，不写最终结论**
   - worker 负责子问题、检索、摘录、初步判断和不确定性。
   - 最终报告由单一 synthesizer 统一合成，避免多 worker 报告拼接造成口径割裂。

5. **企业私有数据和公网数据统一入账**
   - KB、网页、文件、MCP、数据库、供应商 worker 都要映射为同一套 source/evidence/claim/citation。
   - 报告不能引用“模型记忆”或“上下文里某段话”作为不可追溯来源。

6. **可回放、可追责、可评测**
   - 每次检索 query、抓取 URL、抓取时间、worker 输出、claim 绑定和 critic 结果都可回放。
   - 失败样本必须能沉淀为 agentquality / qualityworkbench eval cases。

### 3.2 非目标

P0/P1 不做这些：

- 不做全网 crawler。
- 不做自动破解登录、验证码或付费墙。
- 不做无限制浏览器自动化。
- 不做几十个 Agent swarm。
- 不做每个普通问题自动 Deep Research。
- 不做供应商 Deep Research 直出最终报告。
- 不做高级报告编辑器、PPT/Word/PDF 全量导出。
- 不用 memory 代替 KB 或证据账本。
- 不把 citation 交给前端从 tool output 猜。

## 4. 术语和核心对象

| 名称 | 定义 |
|---|---|
| Research Run | 一次 Deep Research 长任务，包含状态、用户、session、预算、计划、证据、报告、评测和回放引用 |
| Research Brief | 用户问题的结构化研究说明，包括目标、范围、时间窗口、输出格式、约束、不确定点、来源偏好 |
| Research Plan | 研究步骤和子问题拆解，进入长任务前必须确认 |
| Research Source | 一个可引用来源，例如 URL、KB 文档版本、文件、MCP 资源、数据库查询快照 |
| Evidence Item | 从 source 提取出的可验证片段、表格、截图、节点、页范围或结构化事实 |
| Research Note | worker 对子问题的中间产物，只能引用 evidence，不是最终报告 |
| Claim | 最终报告中的关键事实性结论或建议依据 |
| Citation | claim 到 evidence item 的绑定，包含 span、source、抓取时间和验证状态 |
| Synthesizer | 统一报告合成器，把 notes/evidence 转换成报告和 claim graph |
| Critic | 质量门禁，检查无证据 claim、弱证据、冲突、过时来源、遗漏反证和格式问题 |
| Provider | 数据或研究能力提供者，例如 websearch、webfetch、KB、MCP、vendor deep research |

## 5. 目标架构

```text
User / Session
  |
  v
Research Intake
  -> classify need for deep research
  -> build research brief
  -> ask clarifying question when needed
  |
  v
Plan Builder
  -> decompose into subquestions
  -> source strategy
  -> budget and time estimate
  -> user approval / revision
  |
  v
Research Orchestrator
  -> create run
  -> spawn / dispatch workers
  -> enforce provider budget
  -> stream progress
  |
  +--> Web Provider
  +--> KB Provider
  +--> File / Asset Provider
  +--> MCP / Internal Data Provider
  +--> Optional Vendor Deep Research Worker
  |
  v
Evidence Ledger
  -> sources
  -> excerpts
  -> hashes
  -> query provenance
  -> access scope
  |
  v
Synthesizer
  -> report outline
  -> claim graph
  -> citation binding
  |
  v
Critic / Gate
  -> no unsupported claims
  -> conflicts
  -> source diversity
  -> freshness
  -> uncertainty
  |
  v
Final Report
  -> markdown/html artifact
  -> citations
  -> replay link
  -> quality record
```

架构边界：

- Orchestrator 不直接信任模型最终文本。
- Providers 不直接写最终报告。
- Evidence ledger 是所有可信引用的唯一入口。
- Synthesizer 只能从 evidence ledger 和 worker notes 取材。
- Critic 失败时，run 状态进入 `needs_revision` 或 `failed_quality_gate`，不能伪装成功。

## 6. 状态机

### 6.1 Research Run 状态

```text
draft
  -> brief_ready
  -> plan_ready
  -> awaiting_approval
  -> running
  -> synthesizing
  -> critic_review
  -> completed

terminal:
  canceled
  failed
  failed_quality_gate
  expired

recoverable:
  paused
  needs_revision
```

状态规则：

- `draft`：用户问题刚进入研究入口。
- `brief_ready`：brief 已生成，但还可能需要澄清。
- `plan_ready`：研究计划已生成。
- `awaiting_approval`：等待用户确认计划和预算。
- `running`：worker 执行中。
- `synthesizing`：汇总 notes/evidence，生成报告草稿和 claim graph。
- `critic_review`：执行质量门禁。
- `completed`：报告和 citations 完整落库。
- `failed_quality_gate`：critic 发现阻断问题，报告不得作为最终答案展示。
- `needs_revision`：需要补充搜索或用户收窄范围。

### 6.2 Worker 状态

```text
queued -> running -> completed
queued -> running -> failed
queued -> canceled
running -> timed_out
```

worker 完成条件：

- 必须输出 `notes`。
- 必须列出已使用 source/evidence。
- 必须标注未解决问题和不确定性。
- 如果没有 evidence，必须说明检索路径和失败原因。

## 7. 数据模型设计

### 7.1 表结构总览

建议新增独立 research schema 表，不复用 KB、memory 或 quality 表保存研究运行态。

```text
research_runs
research_briefs
research_plans
research_plan_items
research_workers
research_sources
research_evidence
research_notes
research_claims
research_citations
research_reports
research_quality_events
research_eval_cases
```

### 7.2 research_runs

核心字段：

```text
id
session_id
turn_id
trace_id
user_id
tenant_id
domain_id
status
title
original_question
research_mode              # standard | technical | business | kb_mixed
budget_max_tool_calls
budget_max_tokens
budget_max_cost_cents
deadline_at
started_at
completed_at
error_code
error_message
created_at
updated_at
```

设计口径：

- `session_id/user_id/tenant_id/domain_id` 用于权限和回放，不由模型传入。
- 成本预算必须在 run 层可见，不散落在工具调用日志里。
- `trace_id` 用于接入现有 Replay / Trace。

### 7.3 research_briefs

```text
id
run_id
version
goal
scope_in
scope_out
time_window
source_preferences
source_constraints
output_format
clarifying_questions
assumptions
created_by              # model | user
created_at
```

规则：

- 用户修改 brief 时新增 version，不覆盖旧版本。
- `scope_out` 必须显式记录，防止研究越界。

### 7.4 research_plans / research_plan_items

```text
research_plans:
id
run_id
brief_version
version
status                  # draft | approved | superseded
estimated_steps
estimated_tool_calls
estimated_cost_cents
created_at
approved_at
approved_by

research_plan_items:
id
plan_id
seq
kind                    # subquestion | source_scan | verification | synthesis
question
source_strategy
required_providers
success_criteria
max_tool_calls
status
```

规则：

- 计划审批后才能进入 `running`。
- 用户修改计划时旧计划 `superseded`，新计划重走审批。
- P0 可以先只支持“确认/取消”，P1 支持逐项编辑。

### 7.5 research_sources

```text
id
run_id
provider
source_type             # web_url | kb_document | file_asset | mcp_resource | vendor_report | db_snapshot
title
url
kb_namespace_id
kb_document_id
kb_document_version
asset_uri
mcp_server
mcp_resource_uri
retrieved_at
content_hash
access_scope
allowed_by
blocked_reason
metadata_json
created_at
```

规则：

- Source 是“可引用来源”的身份，不等于 evidence。
- 同一个 URL 多次抓取可产生多个 source version，必须按 `retrieved_at/content_hash` 区分。
- 对 KB source 必须记录 document version，禁止引用浮动 active 文档。

### 7.6 research_evidence

```text
id
run_id
source_id
worker_id
evidence_type           # excerpt | table | image | screenshot | kb_node | page_range | structured_fact
quote
summary
locator_json            # url span, page range, node id, line range, selector, table cell range
confidence
extracted_at
content_hash
metadata_json
created_at
```

规则：

- `quote` 必须控制长度，避免把整页正文复制进账本。
- `summary` 可以由模型生成，但不能替代 `quote/locator`。
- evidence item 必须能回到 source 和 locator。

### 7.7 research_notes

```text
id
run_id
worker_id
plan_item_id
subquestion
answer
evidence_ids
uncertainties
failed_queries
created_at
```

规则：

- notes 是 worker 产物，不面向最终用户作为结论。
- notes 中的 factual statement 也应带 evidence id，至少在结构化字段里保留引用。

### 7.8 research_claims / research_citations

```text
research_claims:
id
run_id
report_id
claim_text
claim_type              # fact | comparison | recommendation | risk | uncertainty
importance              # critical | major | minor
support_status          # supported | weak | contradicted | unsupported | not_applicable
created_at

research_citations:
id
run_id
claim_id
evidence_id
source_id
span_start
span_end
verified
verification_error
created_at
```

规则：

- 最终报告中的 critical/major claim 必须有至少一个 verified citation。
- recommendation 类型 claim 必须引用事实依据或明确标注是模型推理建议。
- unsupported critical/major claim 阻断发布。

### 7.9 research_reports

```text
id
run_id
version
status                  # draft | final | blocked
format                  # markdown | html | artifact
content
summary
limitations
open_questions
artifact_uri
created_at
```

规则：

- critic 修改报告时新增 version。
- final report 必须能通过 API 读取 citations。

## 8. 研究协议

### 8.1 Intake 协议

用户消息进入 Deep Research 前先判断：

- 是否需要多来源、多步骤、时效性或证据链。
- 是否能用普通 KB/Web 问答直接回答。
- 是否需要澄清范围、时间窗口、地区、行业、输出格式。

触发 Deep Research 的典型信号：

- “调研”“深入研究”“竞品分析”“行业报告”“政策影响”“技术选型”“供应商评估”。
- 问题明显需要多个来源交叉验证。
- 结论可能用于决策，且用户要求引用或报告。
- 同时涉及企业 KB 和公网资料。

不触发：

- 单个事实查询。
- 简单代码解释。
- 已有 KB 中可直接定位的短答案。
- 用户明确要求快速回答。

### 8.2 Brief Builder 协议

Brief Builder 输出 JSON：

```json
{
  "goal": "研究目标",
  "scope_in": ["纳入范围"],
  "scope_out": ["不纳入范围"],
  "time_window": "资料时间要求",
  "source_preferences": ["优先来源"],
  "source_constraints": ["禁止或低可信来源"],
  "output_format": "报告结构",
  "clarifying_questions": [],
  "assumptions": []
}
```

如果 `clarifying_questions` 非空且问题会显著影响方向，必须先问用户。

### 8.3 Plan Builder 协议

Plan Builder 输出：

```json
{
  "plan_title": "研究计划标题",
  "estimated_duration": "预计耗时",
  "estimated_tool_calls": 30,
  "items": [
    {
      "seq": 1,
      "question": "子问题",
      "source_strategy": "使用哪些来源",
      "required_providers": ["web", "kb"],
      "success_criteria": "完成条件",
      "max_tool_calls": 8
    }
  ],
  "risks": ["可能缺数据的地方"],
  "needs_user_approval": true
}
```

计划质量要求：

- 每个子问题必须有清晰完成条件。
- 必须包含至少一个 verification/contradiction search 步骤。
- 必须显式列出 source strategy。
- 必须估算工具调用预算。

### 8.4 Worker 输出协议

Worker 只输出 notes，不写最终报告：

```json
{
  "subquestion": "子问题",
  "short_answer": "基于证据的简短结论",
  "evidence": [
    {
      "evidence_id": "ev_...",
      "why_relevant": "为什么支持该结论"
    }
  ],
  "contradictions": [],
  "uncertainties": [],
  "failed_queries": []
}
```

禁止：

- worker 输出“最终建议”。
- worker 引用未入账来源。
- worker 使用“据我所知”“常识上”等无来源表述支撑事实结论。

### 8.5 Synthesizer 输出协议

Synthesizer 输出报告草稿和 claim graph：

```json
{
  "report_markdown": "...",
  "claims": [
    {
      "claim_text": "关键结论",
      "claim_type": "fact",
      "importance": "major",
      "evidence_ids": ["ev_1", "ev_2"]
    }
  ],
  "limitations": [],
  "open_questions": []
}
```

规则：

- report markdown 中的 citation 标记必须能映射到 `claims -> citations`。
- claim graph 是事实源，报告正文只是展示。

### 8.6 Critic 协议

Critic 检查：

- unsupported critical/major claim。
- citation 指向不存在或未授权 evidence。
- 同一结论只有一个低可信来源。
- 过时来源支撑时效性结论。
- evidence 与 claim 语义不匹配。
- 互相矛盾的 evidence 未披露。
- 报告超出 brief scope。
- 未标注 limitations/open questions。

Critic 输出：

```json
{
  "passed": false,
  "blocking_issues": [
    {
      "code": "unsupported_major_claim",
      "claim_id": "cl_...",
      "message": "该关键结论没有证据支撑"
    }
  ],
  "warnings": [],
  "required_actions": ["补充检索", "删除结论", "降级为不确定性"]
}
```

P0 只需要 hard gate critical/major unsupported claim；P1 再完善弱证据和反证覆盖。

## 9. Provider 设计

### 9.1 统一接口

Provider 不直接暴露给模型，统一通过 Research Orchestrator 调用。

```text
ResearchProvider
  Name()
  Capabilities()
  Search(ctx, query, constraints) -> ProviderSearchResult[]
  Fetch(ctx, ref, constraints) -> ProviderDocument
  Extract(ctx, document, extractionGoal) -> EvidenceCandidate[]
```

P0 可以先不抽 Go interface，而是在 service 层保持同等边界；但文档和测试必须按 provider 概念组织。

### 9.2 Web Provider

复用：

- `websearch`：发现候选 URL。
- `webfetch`：抓取正文。
- `browser_interact`：只在明确需要动态渲染时使用。

P0 规则：

- 搜索结果不等于证据。
- 必须 fetch 成功并抽取 evidence 后才能被引用。
- 搜索 query、结果排名、抓取时间、抓取模式必须入账。
- 默认限制每个子问题搜索轮数和 fetch 数量。

### 9.3 KB Provider

复用：

- `kb.doc.meta`
- `kb.doc.structure`
- `kb.section.text`

P0 规则：

- KB provider 不接受模型传入 owner/domain/session 授权字段。
- 引用 KB 必须绑定 document version、node id 或 page range。
- KB evidence 与 Web evidence 同等进入 research_evidence。

### 9.4 File / Asset Provider

用于用户上传文档、已有 artifact、聊天附件。

P0 可以只支持文本/Markdown/PDF 已转文本的场景。图片、表格、PPT、Word 高级解析后置到 P2/P3，并复用统一 asset 和 fileconv。

### 9.5 MCP / Internal Data Provider

用于企业内部数据：

- 数据库查询。
- 工单系统。
- CRM。
- 文档系统。
- 指标平台。

规则：

- MCP 结果必须快照化为 source/evidence。
- 外部写入类工具不能进入 research provider 默认集合。
- 敏感字段需要脱敏或按权限过滤后才能入账。

### 9.6 Vendor Deep Research Worker

P2/P3 后接：

- OpenAI Deep Research。
- Perplexity Sonar Deep Research。
- Gemini Deep Research。
- 其他供应商或国产模型的研究能力。

定位：

- 只能作为 worker/provider，输出候选 sources、evidence、notes。
- 必须把来源和摘录回填 Hive evidence ledger。
- 不能绕过 Hive critic。
- 不能直接成为 final report。

## 10. API 设计

### 10.1 Research Run API

```text
POST   /api/v1/research/runs
GET    /api/v1/research/runs
GET    /api/v1/research/runs/{run_id}
POST   /api/v1/research/runs/{run_id}:cancel
POST   /api/v1/research/runs/{run_id}:resume
```

Create request：

```json
{
  "session_id": "sess_...",
  "question": "研究问题",
  "mode": "kb_mixed",
  "budget": {
    "max_tool_calls": 50,
    "max_cost_cents": 500
  }
}
```

### 10.2 Brief / Plan API

```text
GET    /api/v1/research/runs/{run_id}/brief
PATCH  /api/v1/research/runs/{run_id}/brief
GET    /api/v1/research/runs/{run_id}/plan
PATCH  /api/v1/research/runs/{run_id}/plan
POST   /api/v1/research/runs/{run_id}/plan:approve
POST   /api/v1/research/runs/{run_id}/plan:regenerate
```

### 10.3 Progress / Evidence / Report API

```text
GET    /api/v1/research/runs/{run_id}/events
GET    /api/v1/research/runs/{run_id}/sources
GET    /api/v1/research/runs/{run_id}/evidence
GET    /api/v1/research/runs/{run_id}/claims
GET    /api/v1/research/runs/{run_id}/report
GET    /api/v1/research/runs/{run_id}/quality
```

P0 前端可轮询，P1 接 WebSocket/SSE。

## 11. 前端体验

### 11.1 Chat 入口

Chat 中需要新增 Deep Research 任务卡：

```text
Research Card
  title
  status
  brief summary
  plan steps
  approve / edit / cancel
  progress
  sources count
  evidence count
  final report
```

P0 只做最小交互：

- 展示 brief。
- 展示 plan。
- 用户确认/取消。
- 展示进度和最终报告。
- 展示 citations。

### 11.2 Admin / Workbench

P1/P2 在质量工作台或新 Research 页面展示：

- run 列表。
- 状态、耗时、成本。
- source/evidence/claim/citation。
- critic 结果。
- replay link。
- 失败原因。
- eval case 生成入口。

### 11.3 报告展示

报告展示要求：

- citation 卡片来自服务端 claims/citations，不由前端从 markdown 猜。
- 鼠标悬停或点击 citation 能看到 evidence 摘录、source、抓取时间、worker。
- 报告必须展示 limitations/open questions。
- quality gate 失败时不展示为 final report，只展示“未通过原因”和可修复建议。

## 12. 权限、安全和治理

### 12.1 权限

- Research run 继承 session/user/tenant/domain 权限。
- Provider 调用不能扩大用户权限。
- KB provider 必须走现有 KB binding resolver。
- MCP provider 必须走 MCP server 的工具权限和 runtime policy。
- 外部发送/写入工具默认不允许进入研究 worker。

### 12.2 安全

- Web fetch 继续使用 SSRF 防护。
- URL allow/block domain 在 run、plan item 和 provider 层都可配置。
- 私有地址默认禁止，除非企业内网 provider 明确注册。
- 抓取内容大小、时间、重定向次数必须有上限。
- Evidence quote 长度必须有上限。
- 截图、网页 HTML、文件内容进入 asset/evidence 时必须标注来源和权限。

### 12.3 成本治理

Run 级预算：

- max tool calls。
- max fetched URLs。
- max browser actions。
- max tokens。
- max cost。
- max wall-clock duration。

Worker 级预算：

- 每个子问题 max tool calls。
- 每个 provider max calls。
- 每个 query max results。

超过预算：

- run 进入 `needs_revision` 或 `failed`。
- 报告可以输出已有证据的部分结论，但必须标注未完成范围。

## 13. 质量评测

### 13.1 核心指标

| 指标 | 含义 |
|---|---|
| claim_support_rate | final report 中 critical/major claim 被 verified evidence 支撑的比例 |
| citation_validity_rate | citation 指向存在、授权、未过期 evidence 的比例 |
| source_freshness_rate | 时效性问题中来源满足时间窗口的比例 |
| contradiction_coverage_rate | 是否检索并披露反证或不同观点 |
| source_diversity_score | 关键结论是否过度依赖单一域名或单一文档 |
| answer_completeness_score | 是否覆盖 brief 和 plan 的子问题 |
| hallucination_rate | 无来源事实或来源不支持 claim 的比例 |
| cost_per_completed_run | 单次成功研究成本 |
| time_to_report | 从计划批准到报告完成耗时 |

### 13.2 Eval Case 类型

1. **Web-only research**
   - 给定问题和期望来源类型。
   - 检查是否找到权威来源、是否引用正确。

2. **KB-only research**
   - 给定绑定 namespace 和文档。
   - 检查是否命中期望 doc/node/page range。

3. **KB + Web mixed**
   - 内部文档给背景，公网资料给最新信息。
   - 检查报告是否区分内部事实和外部事实。

4. **Contradiction case**
   - 不同来源冲突。
   - 检查是否披露争议，而不是强行单一结论。

5. **Insufficient evidence case**
   - 故意提供找不到证据的问题。
   - 检查是否拒绝编造。

6. **Stale source case**
   - 旧资料和新资料冲突。
   - 检查是否按时间窗口筛选。

### 13.3 质量门禁

P0 hard gates：

- critical/major claim 没有 citation，阻断。
- citation 不在 evidence ledger，阻断。
- citation source 越权或已失效，阻断。
- 报告超出 brief scope 且未标注，阻断。

P1 gates：

- source freshness 不满足时间窗口，阻断或 warning。
- 单一低可信来源支撑重大建议，阻断或 warning。
- 未执行 contradiction search，warning。

P2 gates：

- 使用 LLM judge 或 rule + judge 混合评估 completeness/objectivity。
- 从失败 run 自动生成候选 eval case，进入人工审批。

## 14. 分阶段实施路线

### Phase 0：Research Spine，可信主干

目标：

建立 Hive-native Deep Research 的最小可信主干。可以跑一个简单研究 run，生成 brief、plan、用户确认、少量 Web/KB evidence、报告草稿、claim/citation 和 critic 结果。

做：

- 新增 research 数据模型和迁移。
- 新增 research service。
- 新增 brief/plan generation。
- 新增 run 状态机。
- 新增 evidence ledger。
- 新增 report/claim/citation 保存。
- 新增 critic 最小 hard gate。
- Chat 中展示最小 research card。

不做：

- 不做多 provider 抽象完全体。
- 不做 vendor deep research。
- 不做浏览器复杂交互。
- 不做报告导出。

验收：

- 用户发起研究，系统生成计划。
- 用户批准后执行。
- 至少能从 webfetch 或 KB 产生 evidence。
- 最终报告的 major claim 有 citation。
- 手工构造 unsupported claim 时 critic 阻断。
- run 可在后台列表或 API 中查看状态和 evidence。

### Phase 1：Parallel Workers 与报告质量

目标：

让研究具备并行子问题能力和较完整的质量门禁。

做：

- Research worker 协议。
- parallel_dispatch 集成。
- worker notes 入库。
- Synthesizer 基于 notes/evidence 写报告。
- Critic 检查弱证据、冲突、过时来源。
- 进度事件流。
- Research run 在 Replay/Trace 中可见。

验收：

- 一个计划拆成 3 个子问题并行执行。
- 每个 worker 只能输出 notes/evidence。
- 最终报告由单一 writer 生成。
- critic 能发现无证据和冲突来源。

### Phase 2：KB + Web 混合研究

目标：

把 Hive 差异化做出来：企业 KB 和公网资料统一研究。

做：

- KB provider 正式化。
- KB evidence 绑定 document version/node/page range。
- Web source 和 KB source 混合 citation。
- Chat/Report UI 区分内部来源和外部来源。
- Research eval 接入 agentquality/qualityworkbench。
- 失败 run 生成 eval candidate。

验收：

- 同一报告同时引用 KB document version 和公网 URL。
- 用户无权限的 KB namespace 不会被研究 run 使用。
- citation card 能显示来源类型、抓取时间或文档版本。
- 至少 20 个 mixed golden cases 可跑。

### Phase 3：Internal Data Providers 与 Vendor Worker

目标：

让研究系统接入企业内部系统，并把外部 Deep Research 能力作为 worker/provider 使用。

做：

- MCP/Internal data provider。
- 数据快照 source/evidence。
- Vendor Deep Research worker adapter。
- Provider registry 和 per-provider budget。
- 更完整的 source trust policy。

验收：

- MCP 查询结果可以成为 evidence。
- Vendor worker 输出不能绕过 evidence ledger。
- Vendor worker 的最终文本不会直接作为 Hive final report。
- provider 级预算和失败可观测。

### Phase 4：Artifact、协作与生产治理

目标：

把 Deep Research 从能力变成企业生产工作流。

做：

- 报告 artifact：HTML/PDF/DOCX，复用统一 asset/canvas/report spec 路线。
- 研究模板：竞品分析、技术选型、政策研究、供应商评估。
- 多用户评论和审批。
- 配额、成本中心、租户级策略。
- 定时 research run。
- Canary 和质量趋势。

验收：

- 报告可下载且 citation 保留。
- 企业管理员能配置默认研究模板和来源策略。
- 定时研究可以生成报告并进入审批。
- Quality Workbench 能展示 Deep Research 趋势。

## 15. 建议文件结构

### 15.1 后端新增

| 文件 | 职责 |
|---|---|
| `internal/research/types.go` | Research run、brief、plan、source、evidence、claim、citation、report 类型 |
| `internal/research/service.go` | Research service 门面，负责创建 run、推进状态、读取报告 |
| `internal/research/store.go` | Store interface |
| `internal/research/pg_store.go` | PostgreSQL 实现 |
| `internal/research/state.go` | 状态机和状态转换校验 |
| `internal/research/brief.go` | Brief builder 协议和解析 |
| `internal/research/plan.go` | Plan builder 协议和解析 |
| `internal/research/orchestrator.go` | Run 编排器 |
| `internal/research/provider.go` | Provider 抽象 |
| `internal/research/provider_web.go` | Web search/fetch provider 适配 |
| `internal/research/provider_kb.go` | KB provider 适配 |
| `internal/research/worker.go` | Worker 协议和 notes 解析 |
| `internal/research/synthesizer.go` | 报告合成协议 |
| `internal/research/critic.go` | Critic 规则和门禁 |
| `internal/api/research_handlers.go` | Research HTTP API |
| `internal/api/research_handlers_test.go` | API 测试 |

### 15.2 后端修改

| 文件 | 修改 |
|---|---|
| `internal/store/postgres_migrate.go` | 新增 research 表迁移 |
| `internal/bootstrap/server.go` 或相关 bootstrap wiring | 初始化 research store/service/orchestrator |
| `internal/api/routes.go` | 注册 research API |
| `internal/master/session_loop.go` / `react_processor.go` | 将 Deep Research run 作为工具/任务事件接入会话 |
| `internal/master/grounding.go` | 扩展到 research claim/citation 校验 |
| `internal/tools/tools.go` | 后续可注册 `research.start` / `research.status` 工具 |
| `internal/agentquality` | 新增 research eval case 类型或复用现有 case attributes |
| `internal/qualityworkbench` | 聚合 research quality events |

### 15.3 前端新增/修改

| 文件 | 职责 |
|---|---|
| `frontend/src/types/api.ts` | Research 类型 |
| `frontend/src/api/node-client.ts` | Research API client |
| `frontend/src/components/chat/ResearchCard.tsx` | Chat 中展示 brief/plan/progress/report |
| `frontend/src/components/research/ResearchPlanEditor.tsx` | P1 计划编辑 |
| `frontend/src/components/research/ResearchEvidencePanel.tsx` | 来源和证据展示 |
| `frontend/src/components/research/ResearchReport.tsx` | 报告和 citation 展示 |
| `frontend/src/pages/admin/ResearchWorkbench.tsx` | P1/P2 管理台 |
| `frontend/src/i18n/locales/zh.json` | 文案 |
| `frontend/src/i18n/locales/en.json` | 文案 |

## 16. 工具与模型策略

### 16.1 Tool Choice

Deep Research 不应完全依赖模型自发选择工具。进入 research run 后，orchestrator 必须按计划调 provider。

模型负责：

- brief 生成。
- plan 生成。
- query 改写建议。
- evidence 摘录总结。
- notes 生成。
- synthesis。
- critic 辅助判断。

系统负责：

- 哪些 provider 可用。
- 调用预算。
- ACL。
- source/evidence 入账。
- 状态机。
- citation 验证。
- quality gate。

### 16.2 模型选择

不同阶段可用不同模型：

- Brief/Plan：强推理模型。
- Query rewrite：低成本模型。
- Evidence extraction：低成本或结构化输出模型。
- Synthesis：强推理/长上下文模型。
- Critic：强推理模型，必要时多模型交叉。

模型选择必须走现有 LLM provider/runtime config，不在 research 包里直连某供应商 SDK。

## 17. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 报告看起来很好但证据不支持 | 最大风险，破坏可信定位 | claim graph + critic hard gate |
| P0 范围过大 | 延期且核心做不扎实 | P0 只做 spine，不做高级 provider 和导出 |
| vendor API 绕过 Hive | 失去平台主权 | vendor 只能作为 worker，必须回填 evidence |
| worker 并行导致结论割裂 | 报告质量差 | worker 只写 notes，统一 synthesizer 写报告 |
| 浏览器自动化失控 | 成本高、不稳定、安全风险 | P0 默认不用 browser crawler，只做明确需要的动态 fetch |
| evidence ledger 过大 | 存储和 UI 压力 | quote 长度限制、source hash、按需读取、GC |
| 引用前端推断 | citation 不可信 | citation 由服务端 claims/citations API 输出 |
| KB 权限泄漏 | 高危安全问题 | provider/store 双重 ACL，namespace 只 narrowing |
| eval 空转 | 报告质量无法提升 | P0 同步建立最小 golden cases |

## 18. 验收定义

Deep Research 不能以“能生成长报告”作为验收。最小合格标准：

1. 用户能看到并确认研究计划。
2. run 状态可追踪、可取消、可恢复或明确失败。
3. 每个 source 有 provider、抓取时间、访问范围和 hash。
4. 每个 major claim 有 verified citation。
5. unsupported major claim 会被 critic 阻断。
6. 报告展示 limitations 和 open questions。
7. 研究过程可在 Replay/Trace 或 API 中复查。
8. 至少有一组 eval cases 覆盖成功、证据不足、冲突、KB 权限。
9. 成本和工具调用次数可见。
10. P0 不依赖任何单一供应商 Deep Research API。

## 19. 参考来源

这些外部资料只用于行业对齐，不决定 Hive 的内部事实源：

- OpenAI Deep Research API：`https://platform.openai.com/docs/guides/deep-research`
- OpenAI BrowseComp：`https://openai.com/index/browsecomp/`
- Gemini Deep Research：`https://blog.google/products-and-platforms/products/gemini/google-gemini-deep-research/`
- Gemini Deep Research Max：`https://blog.google/innovation-and-ai/models-and-research/gemini-models/next-generation-gemini-deep-research`
- Anthropic Web Search Tool：`https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/web-search-tool`
- Perplexity Deep Research：`https://www.perplexity.ai/hub/blog/introducing-perplexity-deep-research`
- Perplexity Sonar Deep Research：`https://docs.perplexity.ai/docs/sonar/models/sonar-deep-research`
- Microsoft 365 Copilot Researcher：`https://learn.microsoft.com/en-us/microsoft-365/copilot/researcher-agent`
- LangChain Open Deep Research：`https://www.langchain.com/blog/open-deep-research`
- DRACO benchmark：`https://arxiv.org/abs/2602.11685`

## 20. 待讨论决策

以下决策建议在正式实施前拍板：

1. **Deep Research 入口形态**
   - 推荐：Chat 中显式按钮/模式 + 自动建议，不默认自动触发。

2. **P0 是否支持 KB + Web 混合**
   - 推荐：P0 支持最小混合，但只支持现有 KB text evidence 和 webfetch evidence。

3. **Research run 是否作为工具暴露给模型**
   - 推荐：P0 先走 host/orchestrator API，不把 `research.start` 作为普通模型工具随意开放。P1 再考虑工具化。

4. **报告首发格式**
   - 推荐：Markdown + 服务端 citations。HTML/PDF/DOCX 后置。

5. **计划审批粒度**
   - 推荐：P0 只支持确认/取消；P1 支持编辑 plan item。

6. **Vendor Deep Research 接入时机**
   - 推荐：P3。必须等 evidence ledger 和 critic 稳定后再接，否则会把供应商文本当事实源。

## 21. 推荐实施顺序

1. 先落 `internal/research` 数据模型、store 和状态机。
2. 落 brief/plan API 和最小 Chat Research Card。
3. 落 evidence ledger 和 web provider 的最小路径。
4. 落 KB provider 的最小路径。
5. 落 synthesizer 和 claim/citation。
6. 落 critic hard gate。
7. 接 Replay/Quality 事件。
8. 补 eval cases。
9. 做 parallel workers。
10. 做 Research Workbench。
11. 接 MCP/Internal provider。
12. 接 vendor deep research worker。

这一路线的核心纪律是：

**先让一条小研究链路可信，再让它更强。不要先追求多源、多 worker、多报告格式和供应商能力。**

