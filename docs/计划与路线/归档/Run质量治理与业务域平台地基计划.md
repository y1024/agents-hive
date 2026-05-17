# Run 质量治理与业务域平台地基重构计划

> 状态：COMPLETED / ARCHIVED（2026-05-15）。
>
> **归档记录（2026-05-15）：** 本计划当前范围已完成并归档。已完成 Phase 0 redaction / CI guard、Phase 1 ADR 收口、Phase 2 execution identity / owner 字段、Phase 3 capability 归因与 RouteDecision 快照、Phase 4 回放面质量事件展示、Phase 5 Quality Workbench 业务域归因、Phase 6 memory owner/domain target 与 explain 隔离、Phase 7 业务域准入策略、Phase 8 客服试点准入条件。客服真实 KB/工具实现、产品全生命周期域和生产试点门禁属于下游业务域计划，不计入本地基计划交付范围。
>
> **验证记录（2026-05-15）：** `go test ./... -count=1`、`cd frontend && npm run lint`、`cd frontend && npm run build`、`bash scripts/check_run_quality_foundation_guard.sh` 已通过；归档后仅移动 docs 文件，不改变运行时代码。

> **当前口径**：2026-05-15，直接替代旧版 `Run质量治理与业务域平台地基计划.md`。
>
> **当前实现状态**：2026-05-15 当前工作区已完成并验收 Phase 0 redaction / CI guard、Phase 1 ADR 收口、Phase 2 execution identity / owner 字段、Phase 3 capability 归因与 RouteDecision 快照、Phase 4 回放面质量事件展示、Phase 5 Quality Workbench 业务域归因、Phase 6 memory owner/domain target 与 explain 隔离、Phase 7 业务域准入策略、Phase 8 客服试点准入条件。完整 `go test ./... -count=1`、`frontend npm run lint`、`frontend npm run build` 已通过。客服真实 KB/工具实现、产品全生命周期域和生产试点门禁属于下游业务域计划，不计入本地基计划交付范围。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` or an equivalent task-by-task implementation workflow when converting any phase below into code. This document is the platform foundation plan, not a single PR patch plan.
>
> **执行原则**：不得按旧计划新建一套平行的 `internal/run`、完整 DomainPack Registry 或业务域 Workbench。所有新增能力必须优先复用现有 `router`、`journal`、`trajectory`、`observability`、`agentquality`、`qualityworkbench`、`runtimepolicy`、`quota`、`memory`、`sessiontodo` 和 Admin surface。
>
> **安全前置**：`quality.Event` 进入 Journal / Log 前已通过 `internal/security.RedactJSON` 和 `master.redactQualityEventJSON` 脱敏；redaction 失败时写最小摘要并标记 `redaction_error=true`。后续不得绕过这一统一 helper 写 raw quality payload。

## 1. 重构结论

旧计划写作时，Hive 还没有现在这些能力：质量候选池、Quality Workbench、Replay jobs、Batch eval、Weekly report、自动优化建议、rollback、memory 生产治理、trajectory snapshot、session trace、journal replay、route eval gate、multi-agent/ACP 质量事件和 runtime policy。现在继续按旧计划施工，会重复造系统，并把业务域平台变成另一条质量旁路。

新的结论是：

- **Run 不再是新建运行时系统**：近期不创建独立 `internal/run` 包，也不把 ReAct 主循环包进一套新 RunRuntime。Run 先定义为治理视角里的 execution unit，由 `session_id + trace_id + quality_event + journal_event + trajectory_snapshot + optional run_id` 组合表达。
- **质量控制平面已经存在**：`agentquality`、`qualityworkbench`、`observability`、`journal`、`trajectory` 是地基，不是待建对象。本计划只补它们之间的共同身份、归因、排序、隔离和业务域准入语义。
- **业务域平台必须等准入层稳定**：客服业务域试点仍是下游计划，但阻塞条件从“Phase 8 新 Run 地基完成”改为“业务域准入门禁通过”。
- **Capability 继续以 `internal/router` 为唯一主线**：不得新建 `internal/capability` 或第二套授权配置。`tool_search` 只做发现，不能扩大 `RouteDecision.AllowedTools`。
- **Owner / User 隔离上提为平台合同**：所有质量、回放、业务域和产物 API 都必须能回答 owner 是谁、是否跨用户泄漏、是否把高基数字段写进通用 metrics。

## 2. 当前真实能力基线

### 2.1 已落地能力

| 能力面 | 当前入口 | 本计划处理方式 |
|---|---|---|
| Router typed capability | `internal/router/capability_entry.go`、`tool_profile.go`、`decision.go` | 继续扩展，不新建 catalog |
| Route eval / gate | `internal/agentquality/route_decision.go`、`cmd/agentquality` | 作为业务域准入门禁的一部分 |
| Quality event | `internal/agentquality/types.go`、`internal/master/quality_events.go` | 补 owner/domain/source/run 归因，保持 metrics 低基数 |
| Journal replay | `internal/journal`、`GET /api/v1/sessions/{id}/journal` | 作为用户可见执行时间线，不另建 RunEvent 首发 |
| Trace timeline | `internal/observability/trace_view.go`、`GET /api/v1/sessions/{id}/trace` | 作为跨质量事件和 span 的统一诊断视图 |
| Trajectory snapshot | `internal/trajectory`、`GET /api/v1/sessions/{id}/trajectory/{step}` | 作为可 fork / revert 的执行快照 |
| Quality Workbench | `internal/qualityworkbench`、`/api/v1/admin/quality-workbench/*` | 作为质量运营平面，不再规划全新质量 Workbench |
| Auto Optimization | `internal/agentquality/*suggestion*`、`/api/v1/admin/optimization/*` | 只允许建议、审批、人工 apply、rollback |
| Memory governance | `internal/memory`、`/api/v1/admin/memory/*` | 补业务域/owner 注入解释，不与 KB 混用 |
| Runtime limits / delegation breaker | `internal/runtimepolicy`、`internal/quota` | 只作为 timeout、并发、成本和 delegation circuit breaker；不得承担业务域授权语义 |
| Multi-agent / ACP | `internal/subagent`、`internal/acpclient`、`internal/acpserver` | 只纳入质量归因，不扩大生态产品面 |

### 2.2 旧计划已过时部分

这些内容不再按旧方案执行：

- 新建 `internal/run/types.go`、`store.go`、`envelope.go` 作为首发主线。
- 新增 `runs` / `run_events` 两张表作为所有复杂任务的唯一事实源。
- 新建 `RunDrawer`、`RunTimeline`、`runEventsStore` 作为 Chat UI 的首要改造。
- 把客服业务域试点阻塞在旧 Phase 8 的所有新建对象完成之后。
- 在第一阶段铺完整 DomainPack Registry、动态 Workbench Surface、Artifact public share 产品面。

旧计划保留的核心判断：

- Skill / MCP / builtin tool / custom tool / agent handoff 必须有真实 kind/source/domain 归因。
- `tool_search` 只能发现，不可授权。
- 业务域不得靠 prompt、`trigger_keywords` 或 `session.metadata.business_domain` 决定权限。
- 用户 A 不能读取用户 B 的事件、质量候选、产物、个人 Skill、MCP 配置或业务域数据。
- Quality Workbench、Replay、Batch eval、自动优化必须形成可审计闭环。

## 3. 新目标状态

### 3.1 用户能感知的变化

- “创建一个跟我打招呼的技能”稳定走 skill authoring，不误调 MCP builder。
- “基于 MCP 实现 greet server/tool”才允许 MCP 相关 capability。
- MCP tool 失败显示为 MCP capability 失败，不再被误归因为 skill 创建失败。
- Replay / Trace / Journal / Quality Workbench 能从同一个事件视角解释：意图是什么、路由允许了什么、实际调用了什么、失败归因是什么。
- 业务域试点必须能证明：工具授权来自 router capability，质量事件可回放，memory/KB/业务状态不混用，跨用户不可见。

### 3.2 平台能感知的变化

- `agentquality.Event`、route decision、journal decision、observability log/trace 之间有一致的 execution identity。
- 指标标签不引入 `run_id`、`session_id`、`user_id`、`owner_id` 这类高基数字段。
- Admin 和普通用户 API 对同一 session/trace/journal/trajectory 的 owner 检查语义一致。
- Quality Workbench 能按 domain/source/failure type 聚合业务域问题，而不需要为每个业务域新建一套工作台。

### 3.3 本计划暂不做

- 不重写 ReAct 主循环。
- 不引入新 vector DB、新 LLM SDK、新 auth 系统。
- 不做完整客服坐席 UI。
- 不做 `data_analysis`、`marketing`、`support`、`ops` 等多业务域铺货。
- 不把 `software_engineering` 作为业务域平台中心。
- 不允许自动优化静默 apply 生产变更。

## 4. 核心抽象调整

### 4.1 Execution Identity

新计划不要求每次执行都有独立 `runs` 表记录，但要求所有质量与回放事件都能携带一致的 execution identity。

推荐字段：

```go
type ExecutionRef struct {
    SessionID  string `json:"session_id,omitempty"`
    TraceID    string `json:"trace_id,omitempty"`
    SpanID     string `json:"span_id,omitempty"`
    RunID      string `json:"run_id,omitempty"` // 可选，先用于 batch/replay/scheduled task/业务域外部引用
    TurnID     string `json:"turn_id,omitempty"`
    DomainID   string `json:"domain_id,omitempty"`
    IntentKind string `json:"intent_kind,omitempty"`
}
```

规则：

- `session_id` 是当前线上事实源之一，但 quality metric 只能存 hash 或低基数字段。
- `trace_id/span_id` 用于诊断和 agent tree，不承担用户授权。
- `run_id` 暂时只在已有 run 概念中使用，例如 scheduled task run、quality replay/batch eval run、optimization eval diff、业务域外部对象。
- 如果未来证明必须有统一 `runs` 表，必须先写 ADR，说明为什么不能复用 journal/trace/trajectory/quality 组合。

### 4.2 OwnerRef

Owner 是业务域平台的硬合同，不是 UI 过滤条件。

```go
type OwnerScope string

const (
    OwnerScopeUser OwnerScope = "user"
)

type OwnerRef struct {
    Scope OwnerScope `json:"owner_scope"`
    ID    string     `json:"owner_id"`
}
```

近期规则：

- v1 只支持 `owner_scope=user`。
- Admin API 可以看全局数据，但每条业务对象和质量对象仍应保留 owner 或 session owner 可追溯性。
- 用户态 API 必须先校验 session/user ownership，再读 journal、trace、trajectory、quality-derived data。
- 业务域对象不得只靠 `created_by` 或 `session_id` 推断 owner。

### 4.3 DomainID

`DomainID` 是路由与质量归因字段，不是 prompt 文案、skill 名或 UI tab。

首批允许：

| domain_id | 用途 |
|---|---|
| `generic` | 普通 Web/API/IM 对话和通用任务 |
| `skill_authoring` | Skill 创建、修改、安装、调试 |
| `mcp_server_building` | MCP server/tool 构建与管理 |
| `quality_analysis` | 质量候选、replay、batch eval、报告、优化建议 |
| `memory_governance` | memory 注入、治理、迁移、prune、promotion |
| `customer_service` | 客服试点准入后启用 |

规则：

- `DomainID` 必须来自 host-side intent/capability decision 或后端显式 API，不得来自 LLM 自由文本。
- 旧字段 `business_domain` 只能作为兼容输入，进入后端后必须映射成 `DomainID` 并接受 router/policy 校验。
- 一个 capability 可以属于 domain，但授权仍由 `RouteDecision`、risk、runtime policy 和 HITL 共同决定。

### 4.4 Capability Entry

继续使用现有：

```go
router.CapabilityEntry
router.CapabilityKind
router.CapabilitySource
router.ToolProfile
router.RouteDecision
```

近期只允许补字段和派生快照，不允许新建平行包。建议扩展：

```go
type CapabilityEntry struct {
    Name            string
    Kind            CapabilityKind
    Domain          string
    Source          CapabilitySource
    Invocation      InvocationMode
    Risk            RiskLevel
    Capabilities    []Capability
    Description     string
    Version         string
    OwnerUserID     string
    Visibility      string // private | workspace | public | system
    PolicyProfile   string
    InputSchemaHash string
}
```

规则：

- `ToolProfile.Entry()` 必须复制新增审计字段。
- `RouteDecision` 可以保留 `AllowedTools` 兼容旧路径，但质量快照必须能看到允许的 capability kind/source/domain。
- MCP 默认 fail closed；可信 MCP 的 read-only 判断只能来自 host policy、annotation、schema/risk classifier，不来自 LLM rewrite。
- Skill 和 MCP 是不同来源，不允许因为描述相似而互相替代。

## 5. 新分期计划

### Phase 0：安全红线与计划防线

**目标**：先堵住质量事件原文进入 Journal 的泄漏风险，并用 CI 防止后续 worker 按旧计划新建平行系统。这个 Phase 是本计划的阻断前置，不是文档 polish。

**Files**

- Create: `internal/security/redaction.go` 或在现有安全包中新增等价 redaction helper
- Create: `internal/security/redaction_test.go`
- Modify: `internal/master/quality_events.go`
- Modify: `internal/master/quality_events_test.go`
- Create: `scripts/check_run_quality_foundation_guard.sh`
- Modify: CI 配置或现有检查入口（如果当前仓库没有 CI，只先提供脚本并在计划验收命令中调用）

**Required behavior**

- [x] 新增结构化 redaction helper，递归处理 `map[string]any`、`[]any`、`json.RawMessage`、JSON string 和普通 string。
- [x] 默认替换敏感 key：`api_key`、`apikey`、`token`、`access_token`、`refresh_token`、`secret`、`password`、`credential`、`context_token`、`authorization`、`cookie`、`set_cookie`、`private_key`、`client_secret`、`app_secret`。
- [x] `enqueueQualityJournalDecision` 不再把 raw `quality.Event` JSON 直接写入 `DecisionEntry.Reason`；必须写 redacted JSON。
- [x] `emitQualityEvent` 写 `hive_logs.attributes.quality_event` 前也使用同一个 redaction helper，避免 Log 与 Journal 口径不一致。
- [x] redaction 失败时 fail closed：写入最小事件摘要，不写 raw payload。
- [x] CI guard 禁止新增 `internal/run`、`internal/capability` 包，禁止 docs 中出现首发 `Create: internal/run` / `Create: internal/capability` 施工项，禁止 `DecisionEntry.Reason: string(raw)` 重新出现。

**Tests**

- [x] `TestRedactJSONRemovesNestedSecrets`
- [x] `TestEmitQualityEventRedactsLogAndJournal`
- [x] `TestQualityJournalDecisionDoesNotPersistRawToken`
- [x] `TestRunQualityFoundationGuardRejectsParallelRunPackage`

**Commands**

```bash
go test ./internal/security ./internal/master -run '(Redact|Quality|Journal)' -count=1
bash scripts/check_run_quality_foundation_guard.sh
```

### Phase 1：计划与 ADR 收口

**目标**：把“复用现有质量控制平面，不新建平行 Run 系统”的决策写清楚，防止后续 worker 按旧计划施工。

**Files**

- Modify: `docs/计划与路线/Run质量治理与业务域平台地基计划.md`
- Create/Modify: `docs/架构设计/Run质量治理地基.md`
- Modify: `DESIGN.md`
- Modify: `docs/计划与路线/客服业务域接入试点计划.md`

**Steps**

- [x] 在 ADR 中明确：`journal + trace + trajectory + quality event + route decision` 是近期 Run 治理事实源。
- [x] 写明不创建 `internal/run`，除非后续 ADR 证明现有事实源无法支撑。
- [x] 写明不创建 `internal/capability`，capability 主线仍是 `internal/router`。
- [x] 更新客服试点依赖：从旧 Phase 8 改成本文 Phase 8 业务域准入门禁。
- [x] 在 `DESIGN.md` 增加一条决策日志。

**Acceptance**

```bash
rg -n -e 'Create: \x60internal/(run|capability)' -e 'RunDrawer[[:space:]]+首发' -e 'DomainPack Registry[[:space:]]+首发' docs/计划与路线/Run质量治理与业务域平台地基计划.md
```

期望：不出现把这些对象作为首发必建项的语义。

### Phase 2：Execution Identity 与 Owner 合同

**目标**：让质量事件、trace、journal、trajectory 和业务域对象拥有一致的身份字段与 owner 语义。

**Files**

- Modify: `internal/agentquality/types.go`
- Modify: `internal/agentquality/route_decision.go`
- Modify: `internal/master/quality_events.go`
- Modify: `internal/observability/trace_view.go`
- Modify: `internal/journal/journal.go`
- Modify: `internal/api/session_handlers.go`
- Modify: `internal/api/session_trace_handlers.go` only if regression tests expose a gap
- Modify: `internal/api/session_trajectory_handlers.go` only if regression tests expose a gap
- Modify: `internal/store/postgres_migrate.go` only if persistence fields are necessary

**Required behavior**

- [x] `agentquality.Event` 增加低耦合身份字段：`RunID`、`TraceID`、`SpanID`、`TurnID`、`DomainID`、`SourceKind`、`SourceName`、`OwnerScope`、`OwnerID`、`UserID`。
- [x] `MetricLabels` 不输出 `run_id/session_id/user_id/owner_id/trace_id/span_id`。
- [x] `emitQualityEvent` 写 log/journal 时保留诊断字段，写 metric 时只保留低基数 label。
- [x] session journal/trace/trajectory API 保持已实现的 session owner 校验，并补齐 journal 的跨用户回归测试。
- [x] Admin 聚合 API 可以跨 owner，但 response 中不得泄漏 secrets/raw credentials。

**Tests**

- [x] `TestQualityEventCarriesExecutionRefWithoutMetricCardinalityExplosion`
- [x] `TestHandleGetSessionTraceUnauthorizedDoesNotCallReader`（覆盖 session trace cross-owner 回归）
- [x] `TestSessionJournalRejectsCrossOwner`
- [x] `TestTrajectoryRejectsCrossOwner`

**Commands**

```bash
go test ./internal/agentquality ./internal/master ./internal/observability ./internal/api -run '(Quality|Trace|Journal|Trajectory|Owner)' -count=1
```

### Phase 3：Capability 归因与 RouteDecision 快照

**目标**：Skill、MCP、builtin tool、custom tool、agent handoff 在路由、质量事件、tool_search 和 Workbench 中都能区分真实来源。

**Files**

- Modify: `internal/router/capability_entry.go`
- Modify: `internal/router/tool_profile.go`
- Modify: `internal/router/profile_infer.go`
- Modify: `internal/router/capability_registry.go`
- Modify: `internal/router/decision.go`
- Modify: `internal/router/decision_span.go`
- Modify: `internal/router/replay.go`
- Modify: `internal/tools/tool_search.go`
- Modify: `internal/agentquality/route_decision.go`
- Modify: `internal/router/*_test.go`
- Modify: `internal/tools/tool_search_test.go`

**Required behavior**

- [x] `CapabilityEntry` 补齐审计字段：version、owner、visibility、policy profile、input schema hash。
- [x] `RouteDecisionEvent` 输出 allowed/blocked capability snapshots，而不仅是 tool names。
- [x] `IntentCreateSkill` 只允许 `skill_authoring` 的 skill workflow。
- [x] `IntentManageTool` 或明确 MCP 请求才允许 MCP builder / MCP server building。
- [x] `tool_search` 返回 kind/source/domain/risk/visibility，但不能改变 `AllowedTools`。
- [x] route eval corpus 覆盖 skill/MCP 混淆、prompt injection、false match、cross-user private skill。

**Regression prompts**

```text
创建一个跟我打招呼的技能
创建一个技能，输入名字和语言，返回问候语
基于 MCP 实现一个 greet server
创建一个 MCP tool，用来返回问候语
搜索有没有能创建技能的工具
搜索有没有能创建 MCP server 的工具
```

**Tests**

- [x] `TestBuildRouteDecisionCreateSkillAllowsSkillAuthoringWorkflow`
- [x] `TestBuildRouteDecisionManageToolAllowsOnlyMCPBuilderWorkflow`
- [x] `TestRouteDecisionCarriesAllowedCapabilityEntries`
- [x] `TestToolSearchTypedMetadataPhase0Kinds` / `TestToolSearchResultsAreDiscoveryOnly`
- [x] `TestPersonalSkillHiddenFromOtherUser`

**Commands**

```bash
go test ./internal/router ./internal/tools ./internal/agentquality -run '(Skill|MCP|Capability|ToolSearch|RouteDecision)' -count=1
go test ./cmd/agentquality -count=1
```

### Phase 4：质量事件与现有回放面统一

**目标**：不再新建 RunEvent 首发链路，而是让 Journal、Trace、Trajectory、Quality Event 能组成稳定时间线。

**Files**

- Modify: `internal/journal/journal.go`
- Modify: `internal/journal/pg_journal.go`
- Modify: `internal/observability/trace_view.go`
- Modify: `internal/observability/pg_writer.go`
- Modify: `internal/master/quality_events.go`
- Modify: `frontend/src/types/journal.ts`
- Modify: `frontend/src/store/replay.ts`
- Modify: `frontend/src/pages/SessionReplay.tsx`
- Modify: `frontend/src/components/replay/*`

**Required behavior**

- [x] Replay timeline 能展示 quality event 的 domain/source/failure type。
- [x] Trace timeline 保持已实现的稳定排序，并扩展同 timestamp 的 trace/span/operation tie-break 回归。
- [x] Journal decision 只读取 Phase 0 已 redacted 的 quality JSON；本 Phase 不再引入 raw payload。
- [x] 前端 replay 对同一 quality event 有稳定 key，避免重复渲染。
- [x] Chat UI 不强行塞满工具日志；复杂诊断仍进入 Replay/Trace/Admin 视图。

**Tests**

- [x] `TestPgTracerGetSessionTimelineAggregatesQualityEvents`
- [x] `builds stable keys for quality timeline items from quality metadata`（`frontend/src/components/replay/traceViews.test.tsx`）
- [x] `TestSortTraceTimelineItemsUsesStableTieBreakers`

**Commands**

```bash
go test ./internal/journal ./internal/observability ./internal/master -run '(Journal|Trace|Quality|Redact)' -count=1
cd frontend && npm test -- --run src/store/__tests__/replay.test.ts
```

### Phase 5：Quality Workbench 业务域化

**目标**：把已有 Quality Workbench 从“候选失败运营面”升级为“可按 domain/source/owner 归因的质量控制平面”，而不是新建业务域工作台。

**Files**

- Modify: `internal/qualityworkbench/dashboard.go`
- Modify: `internal/qualityworkbench/cluster.go`
- Modify: `internal/qualityworkbench/replay.go`
- Modify: `internal/qualityworkbench/batch_eval.go`
- Modify: `internal/api/admin_workbench_handlers.go`
- Modify: `frontend/src/pages/admin/qualityworkbench/QualityWorkbench.tsx`
- Modify: `frontend/src/types/api.ts`

**Required behavior**

- [x] Clusters 支持按 domain/source_kind/source_name/failure_type 过滤，但不得直接把 domain 拼进现有 `ClusterKey` 导致历史 `cluster_id` 静默漂移。
- [x] 聚类 key 版本化：保留 v1 key/id 兼容旧 cluster；新增 v2 key/id 或 `GroupingRule.KeyVersion`，并提供 old/new 映射或 UI 合并展示策略。
- [x] Dashboard 可以按 domain 过滤和分面展示；默认 cluster 列表仍保持历史连续性。
- [x] Replay jobs 和 batch eval run 记录 source domain。
- [x] Dashboard snapshot/series 展示 domain 分布和 source 分布。
- [x] Weekly report 增加 domain/source 章节。
- [x] Admin API 仍保持 auth/admin guard；普通用户不暴露全局质量面。

**Tests**

- [x] `TestAdminQualityWorkbenchClustersFiltersByDomainSourceAndFailure`
- [x] `TestAggregateClusters_DefaultRuleGroupsSimilarFailures`（验证默认 v1 key 不被 domain/source 改写）
- [x] `TestClusterKeyV2IncludesVersionWithoutBreakingLegacyID`
- [x] `TestBuildDashboardSnapshotAggregatesWindowedCounts` / `TestBuildDashboardSeriesBucketsByDay`
- [x] `TestGenerateWeeklyReportIncludesSummaryAndKeyMarkdownContent`

**Commands**

```bash
go test ./internal/qualityworkbench ./internal/api -run '(Workbench|Dashboard|Report|Domain|Source)' -count=1
go test ./cmd/quality-weekly-report ./cmd/quality-batch-eval -count=1
cd frontend && npm run build
```

### Phase 6：Memory / KB / 业务上下文边界

**目标**：让业务域能使用 memory 和未来 KB，但不把 KB、用户记忆、质量候选和业务状态混成一张表或一个 prompt hint。

**Files**

- Modify: `internal/memory/target.go`
- Modify: `internal/memory/injector.go`
- Modify: `internal/memory/extractor.go`
- Modify: `internal/api/admin_memory_handlers.go`
- Modify: `internal/agentquality/types.go`
- Future downstream only: `internal/kb/*`（由独立 KB/证据层计划承接，不属于本地基计划交付范围）

**Required behavior**

- [x] Memory target 能表达 owner/domain/session/context source。
- [x] `InjectContext` / `InjectContextDetailed` 当前签名只有 `(ctx, userMessage, sessionID, userID)`，无法传 domain/owner/source；必须显式设计兼容迁移：新增 `InjectContextWithTarget(ctx, query, MemoryTarget)` 或 `InjectionRequest`，旧方法保留 wrapper，避免一次性 breaking change。
- [x] 更新所有调用点和测试，包括 `internal/master/react_processor.go`、`internal/memory/eval/runner.go`、`internal/memory/injector_test.go`。
- [x] `quality.context_build` 记录 domain、skip counts、source explanation。
- [x] Memory injection explain API 能显示 domain/owner 维度，但不泄漏其他用户内容。
- [x] KB 不混入 memory 的平台边界已在本计划中锁定；真实 `internal/kb` 独立模块由下游 KB/证据层计划承接。
- [x] KB chunk 的 namespace、doc_id、chunk_id、version、owner、引用回溯要求已从本地基计划移交到下游 KB/证据层计划。

**Tests**

- [x] `TestNormalizeMemoryRecordCarriesDomainAndSource`
- [x] `TestInjector_InjectContextDetailedLegacyWrapperUsesGenericDomain`
- [x] `TestInjector_InjectContextWithTargetCarriesDomainAndFiltersScope`
- [x] `TestMemoryInjectionExplainRejectsCrossOwner`
- [x] `TestContextBuildQualityEventCarriesMemoryDomainSourceAndOwner`

**Commands**

```bash
go test ./internal/memory ./internal/api ./internal/master -run '(Memory|ContextBuild|Domain|Owner)' -count=1
```

### Phase 7：业务域准入 API 与策略

**目标**：定义业务域进入 Hive 的统一准入，不让每个业务域私自接 prompt、keyword、tool 和 webhook。

**Files**

- Create: `docs/架构设计/业务域准入模型.md`
- Modify: `internal/router/intent.go`
- Modify: `internal/router/capability_registry.go`
- Modify/Create: `internal/domainpolicy/*` 或等价新包（用于业务域准入、风险和授权策略）
- Modify: `internal/runtimepolicy/policy.go` only for timeout/concurrency/cost profile references
- Modify: `internal/quota/*` only for delegation circuit breaker limits, not business quota
- Modify: `internal/api/routes.go` only when adding actual domain APIs

**准入清单**

每个业务域必须提供：

- `domain_id`
- capability entries
- allowed intent kinds
- risk model
- domain policy profile
- runtime limits profile（只包含 timeout、并发、成本）
- owner model
- memory/KB boundary
- quality event mapping
- eval corpus
- rollback / disable switch
- API owner check rules

**禁止项**

- 不允许 `trigger_keywords` 决定授权。
- 不允许 prompt 直接暴露未授权工具。
- 不允许 `business_domain` 绕过 router。
- 不允许业务域自建质量候选池。
- 不允许业务域自建危险操作审批。
- 不允许把业务域授权语义塞进 `runtimepolicy.Policy` 或 `quota.CircuitBreaker`。

**Tests**

- [x] `TestBusinessDomainCannotGrantToolByKeyword`
- [x] `TestBusinessDomainCapabilityRequiresDomainPolicy`
- [x] `TestRuntimePolicyDoesNotAuthorizeBusinessDomainTools`
- [x] `TestBusinessDomainRouteEvalGateFailsWithoutCorpus`

**Commands**

```bash
go test ./internal/router ./internal/domainpolicy ./internal/runtimepolicy ./internal/quota -run '(Domain|Capability|Policy|RouteEval)' -count=1
```

### Phase 8：客服业务域试点重开条件

**目标**：更新 `客服业务域接入试点计划.md`，让它作为第一个真实业务域验证，而不是平台地基本身。

客服试点可启动前，必须全部满足：

- [x] Skill/MCP route eval 通过，且新增 regression prompts 全部覆盖。
- [x] `agentquality.Event` 能携带 domain/source/execution identity，metrics 不含高基数字段。
- [x] Replay/Trace/Journal 能看到 route decision、quality event、tool call 和 trajectory 关键节点。
- [x] Quality Workbench 能按 domain/source/failure type 聚合。
- [x] Memory target 和 injection explain 支持 owner/domain。
- [x] 业务域准入模型文档完成。
- [x] Phase 0 redaction 和 CI guard 通过。
- [x] `tool_search` 只有发现能力，没有授权能力。
- [x] 跨用户 session/journal/trace/trajectory/quality-derived API 测试通过。
- [x] 客服计划中 `customer_service`、`kb_search`、`escalate_to_human`、`cancel_escalation` 均声明为 router capability。

客服试点仍然不做：

- 坐席 UI。
- 通用 DomainPack Registry。
- marketing/support/sales/hr/ops 等第二业务域。
- 把 KB 塞进 memory。
- 业务域自建质量工作台。

## 6. 数据与迁移策略

### 6.1 优先复用现有表

近期优先复用：

- `hive_traces`
- `hive_logs`
- `hive_metrics`
- `agentquality_candidates`
- `qualityworkbench_replay_jobs`
- `qualityworkbench_batch_eval_runs`
- `qualityworkbench_weekly_reports`
- `hive_step_snapshots`
- journal 相关表
- session/message/user/auth 相关表

只有在明确无法表达 owner/domain/source/execution identity 时，才允许加列。

### 6.2 不急于新增 `runs`

统一 `runs` 表不是当前默认方案。允许新增 `run_id` 字段作为跨对象关联，但不把 `runs` 表作为所有执行的唯一事实源。

如果未来要新增 `runs` 表，必须先满足：

- 至少两个现有事实源无法可靠 join。
- Replay/Trace/Journal/Quality Workbench 的查询复杂度或一致性出现真实问题。
- ADR 给出迁移路径、回填策略、owner 校验、索引策略和旧 API 兼容方案。

## 7. 风险与门禁

| 风险 | 门禁 |
|---|---|
| 重复造 RunRuntime | 禁止首发新建 `internal/run`；先复用 journal/trace/trajectory/quality |
| 又新增 capability 系统 | 禁止新建 `internal/capability`；只扩展 `internal/router` |
| 业务域绕过质量主线 | 每个业务域必须进入 agentquality/qualityworkbench |
| Skill/MCP 继续混淆 | route eval + source/kind/domain 归因必须通过 |
| tool_search 变授权绕过 | `tool_search` 只发现，不改变 `AllowedTools` |
| 指标爆炸 | Metric labels 禁止高基数字段 |
| 跨用户泄漏 | session owner + object owner 双重校验 |
| Memory / KB 混用 | KB 独立模块，Memory 只做用户/agent 记忆 |
| 自动优化越权 | suggestion 必须人工审批和 apply，支持 rollback |
| 客服试点偷跑 | 必须满足 Phase 8 准入条件 |

## 8. 总体验证命令

后端最小回归：

```bash
go test ./internal/agentquality ./internal/router ./internal/tools ./internal/master ./internal/observability ./internal/journal ./internal/trajectory ./internal/qualityworkbench ./internal/memory ./internal/api -count=1
bash scripts/check_run_quality_foundation_guard.sh
```

CLI 回归：

```bash
go test ./cmd/agentquality ./cmd/quality-weekly-report ./cmd/quality-batch-eval ./cmd/delegation-eval -count=1
```

前端回归：

```bash
cd frontend && npm test -- --run src/App.lazyRoutes.test.tsx src/store/__tests__/replay.test.ts src/pages/admin/QualityCandidates.test.ts
cd frontend && npm run build
```

完整回归：

```bash
go test ./... -v
cd frontend && npm run lint
cd frontend && npm run build
```

如果 Go cache 受 sandbox 影响：

```bash
env GOCACHE=/tmp/go-build go test ./internal/agentquality ./internal/router ./internal/tools ./internal/master ./internal/observability ./internal/journal ./internal/trajectory ./internal/qualityworkbench ./internal/memory ./internal/api -count=1
```

## 9. 后续文档动作

- [x] 更新 `docs/架构设计/Run质量治理地基.md`，记录本计划的 ADR。
- [x] 更新 `docs/计划与路线/客服业务域接入试点计划.md` 的 `depends_on` 和准入条件。
- [x] 更新 `DESIGN.md` 决策日志。
- [x] 若开始任一 Phase 的代码施工，另写具体 implementation plan，列出精确文件、测试和提交切片。

Implementation plan: `docs/计划与路线/2026-05-14-Run质量治理与业务域平台地基实施切片计划.md`。

## 10. 最终成功标准

- 创建 Skill 的请求不会调用 MCP builder。
- MCP builder 只在明确 MCP/tool management 意图下出现。
- RouteDecision、quality.Event、Journal、Trace、Replay 能显示一致的 kind/source/domain/execution identity。
- Quality Workbench 能按 domain/source/failure type 做聚合和报告。
- Memory 注入解释具备 owner/domain 语义，KB 不混入 memory。
- 业务域试点只能通过 router capability、runtime policy、quality event 和 eval corpus 进入 Hive。
- 没有新增平行 RunRuntime、Capability Catalog 或业务域质量工作台。
