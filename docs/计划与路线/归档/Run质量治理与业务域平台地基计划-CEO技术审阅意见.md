# Run 质量治理与业务域平台地基计划 — CEO + 顶级技术专家深度审阅意见

> 状态：ARCHIVED（2026-05-15）。
>
> **归档记录（2026-05-15）：** 本审阅文档作为主计划的技术审阅输入已完成归档，保留为决策记录。主计划已按审阅意见完成 redaction、domain 归因、Workbench 业务域化、memory 边界和准入门禁收口；后续 KB / 客服 / 产品全生命周期域由下游独立计划承接。

**审阅对象**：`docs/计划与路线/Run质量治理与业务域平台地基计划.md`
**审阅日期**：2026-05-14
**审阅视角**：CEO 战略判断 + 顶级技术专家工程把关
**审阅人**：Claude (Opus 4.7, 1M context)
**审阅基础**：实地阅读 12 个核心文件，覆盖 9 个 Phase 中 7 个的关键代码（agentquality/types.go、route_decision.go、master/quality_events.go、journal/journal.go、observability/trace_view.go、qualityworkbench/cluster.go、qualityworkbench/dashboard.go、memory/target.go、memory/injector.go、router/capability_entry.go、runtimepolicy/policy.go、api/session_trace_handlers.go），加 ~15 处 grep 验证

> **作者说明**：本审阅在第一稿写作时仅基于 6 个文件就妄下"现状清楚"的结论，被用户当场质疑。第二稿在补读 12 个核心文件后重写，所有结论附 `文件:行号` 证据。Phase 2/6 的 quota/intent 部分仍有 ~20% 代码未读，相关结论在文中显式标注「需进一步验证」。

---

## TL;DR（30 秒读完）

**核心立场对、纪律对、但工程现状摸得不准。**

- 「反对重建 RunRuntime/Capability Catalog」的立场是这个计划最值钱的产物。代码层面 `internal/run/` `internal/capability/` 当前都不存在 — 立场必须靠 ADR + 路由器审查锁死，否则未来 worker 还是会按旧计划起炉灶
- **Phase 3 的 raw quality JSON 进 Journal 当前完全没有 redaction**（`master/quality_events.go:242` 直接 `Reason: string(raw)`），这是 P0 安全风险，不是「补强」级别
- **Phase 6 的 `runtimepolicy.Policy` 是纯 resource limit（timeout/并发/成本）**，把它扩成"业务域工具执行边界"是把两个语义层塞一个包 — 计划完全没识别。`internal/quota/` 只有 `circuit_breaker.go`，根本不是配额系统
- **Phase 5 的 memory injector 必须改接口签名**（`InjectContext(ctx, msg, sessionID, userID)` → 加 domain）才能按 domain 过滤，这是隐藏 breaking change
- **Phase 4 改 Cluster 加 domain 会改变 ClusterKey hash**，历史 cluster 连续性会断 — 计划没说迁移
- 多处「现状已实现」被错列在 required behavior（`SortTraceTimelineItems` 稳定排序、Web API owner check）— 工作量估算偏高
- Phase 1/2/3 的字段变更对 `qualityworkbench.Cluster` `BuildAgentTraceTree` 等下游有强 ripple 依赖，但计划没显式画依赖图

---

## 一、CEO 视角：根问题诊断

### 1.1 这个计划最值钱的部分是「不做什么」

旧计划要造的 `internal/run` / `runs` / `run_events` / `RunDrawer` / DomainPack Registry / 全新 Workbench Surface 这些对象，**当前代码库一个都不存在**：

```
$ ls internal/run/        # 不存在
$ ls internal/capability/ # 不存在
```

新计划的核心贡献是**用 ADR 把它们永久锁在"不建"状态**。这件事的杠杆比任何字段变更大一个数量级 — 一次错误的"建新包"决策会拖出 1-2 个月废代码。

**风险**：ADR 只是文档，未来 worker 仍可能在不读 ADR 的情况下按旧计划起炉灶。Phase 0 的 acceptance（rg 检查不出现某些字符串）是必要但不充分。**真正的护栏是 CI**：

**建议补**：
```bash
# scripts/check_no_parallel_run_system.sh
test ! -d internal/run/ || { echo "PROHIBITED: internal/run/ exists"; exit 1; }
test ! -d internal/capability/ || { echo "PROHIBITED: internal/capability/ exists"; exit 1; }
```

把这个脚本接到 CI，比 doc 防御强 10 倍。

### 1.2 「Run 不是新建系统」立场正确，但 ExecutionRef 设计有逻辑漏洞

`ExecutionRef` 设计（计划 4.1）包含 9 个字段：`SessionID/TraceID/SpanID/RunID/TurnID/DomainID/IntentKind`。

**但当前 `agentquality.ToolRecall` 已经在子结构里有 `TurnID`/`TraceID`**（`types.go:67-68`），`Delegation` 子结构有 `ParentTraceID/ChildTraceID/GroupID`（`types.go:84-90`）。这是**嵌套式归因**，新计划要做的是**顶层归因**。两套并存会有：

- 同一 trace_id 出现在 Event 顶层和子结构里，反序列化 consumer 如何选？
- `BuildAgentTraceTree`（`trace_view.go:69-122`）当前反向解析 `Delegation.ParentTraceID/ChildTraceID`，**这是 observability 包对 agentquality 包结构的硬耦合**。Phase 1 给 Event 加顶层 TraceID 后，是否应该把 Delegation 子结构精简？计划没说

**修法**：Phase 1 必须先决定**字段归属层级**——是只在 Event 顶层、只在子结构、还是顶层是 canonical 子结构是兼容遗留。然后写 ADR 说明，否则未来必出 drift。

### 1.3 Phase 4 工作量被严重低估

计划 Phase 4 列了 7 个 file modify，加 3 个 test。实际现状：

```
$ ls internal/qualityworkbench/
batch_eval.go cluster.go dashboard.go grouping_preview.go grouping_store.go
latency.go pg_store.go replay.go replay_fanout.go replay_runner.go
report.go version_diff.go
```

12 个 .go 文件 + 对应测试。加 domain/source 字段意味着：

- `Cluster` 结构加 2 字段（`cluster.go:39-54`）
- `ComputeClusterKey` 的 hash 输入加 domain（`cluster.go:95+`）→ **历史 cluster 数据 key 全变**
- `DashboardSnapshot` 加 `DomainBreakdown` map（`dashboard.go:16-23`）
- `DashboardSeriesPoint` 同上
- `pg_store.go` schema 加列 + 索引 + 迁移 + 双写
- `replay_runner.go` / `batch_eval.go` job 表加 domain
- `report.go` weekly report 章节生成器
- `admin_workbench_handlers.go` 加 filter query param
- 前端 `QualityWorkbench.tsx` 加 filter UI
- ~20 个测试

**真实工作量**：3-5 天人工 / 1 天 CC+gstack。计划 Phase 4 写法暗示 1 天就能搞定，会让 worker 低估并跳过测试。

---

## 二、顶级技术专家视角：按 Severity 排序的发现

### CRITICAL-1：`master/quality_events.go:242` Journal redaction 完全没做

```go
// internal/master/quality_events.go:235-249
func (m *Master) enqueueQualityJournalDecision(sessionID string, ev agentquality.Event, raw []byte) {
    if m.journal == nil || m.journalCh == nil || sessionID == "" {
        return
    }
    entry := journal.DecisionEntry{
        SessionID: sessionID,
        Decision:  string(ev.Name),
        Reason:    string(raw),           // ← 整个 raw quality event JSON 直接当 Reason 字符串塞进 journal
        AgentID:   "quality",
        Timestamp: ev.Ts,
    }
    ...
}
```

`raw` 是整条 `agentquality.Event` 的 JSON marshal 输出，**没有任何过滤**。`Event.Attributes map[string]any` 允许放任意字段（`types.go:144`）。Tool 调用参数、错误信息、用户消息片段都可能进 Attributes → 整条进 Journal Decision。

`grep -n "redact|Redact" internal/master/*.go internal/journal/*.go` 返回零结果。**整个项目当前没有 redaction 工具**。

计划 Phase 3.3 列了「Journal decision 中的 quality raw JSON 必须 redaction 后入库」，但没说现状是**完全裸奔**，把它当「补强」描述。这是 P0：

- secret/credential/token 一旦进了 Attributes 就直入 journal
- Journal 持久化到 Postgres，泄漏面持续放大
- Replay 时前端会拉出来渲染

**修法**：Phase 3 应升级为 P0，**前置到 Phase 1 同期完成**：

1. 新建 `internal/agentquality/redact.go`：实现 `RedactEvent(ev Event) Event`，按字段白名单清洗 Attributes
2. `emitQualityEvent`（`quality_events.go:61`）所有三处出口（metric/log/journal）**都用 redacted version**，不只 journal
3. 加测试 `TestQualityEventRedactsSecretsBeforeJournal`、`TestQualityEventRedactsSecretsBeforeLogAttributes`
4. 跑一次 staging 历史 journal 数据扫描，看是否已经有 secret 进库 → 若有，紧急清理

### CRITICAL-2：`enqueueLog` 把 raw quality event 直塞 log Attributes

```go
// internal/master/quality_events.go:79-90
m.enqueueLog(observability.LogEntry{
    Level:     "info",
    Message:   string(ev.Name),
    TraceID:   traceID,
    SpanID:    spanID,
    SessionID: sessionID,                       // ← SessionID 当 log 字段，若接 loki 当 label 用是高基数
    Attributes: map[string]any{
        "quality_event": json.RawMessage(raw),  // ← raw 同上未脱敏
    },
    Ts: ev.Ts,
})
```

计划 Phase 1 「`MetricLabels` 不输出 `run_id/session_id/user_id`」是对的，但**`LogEntry.SessionID` 字段同样会被 loki/elasticsearch 索引**。如果下游 log pipeline 把这个字段当 label 用，基数爆炸；如果只当 doc field 用，OK 但 raw event 仍未脱敏。

**修法**：
1. 同 CRITICAL-1，`enqueueLog` 也走 redacted event
2. 文档明确：`LogEntry.SessionID` 是 doc field 不是 label，下游若需聚合用 `session_id_hash`（已存在）

### CRITICAL-3：Phase 6 `runtimepolicy` 与 `quota` 包语义错配

**`runtimepolicy.Policy`** 当前是纯 resource limit：

```go
// internal/runtimepolicy/policy.go:9-21
type Policy struct {
    LLMCallTimeout      time.Duration
    ToolTimeout         time.Duration
    TaskTimeout         time.Duration
    SpawnAgentTimeout   time.Duration
    ACPPromptTimeout    time.Duration
    ACPReconnectTimeout time.Duration
    SubagentMaxTurns    int
    SubagentMaxDepth    int
    PerSessionParallel  int
    GlobalWorkers       int
    MaxSessionCostUSD   float64
}
```

**全部 11 个字段都是「多久超时 / 多大并发 / 多少美金」**，没有任何 capability/domain/tool/risk 概念。计划 Phase 6 想把它扩成「业务域工具执行边界」：

> Modify: `internal/runtimepolicy/policy.go`
> Modify: `internal/quota/*`
> ...
> `TestBusinessDomainCapabilityRequiresRuntimePolicy`

这是**把两个不同语义层（资源限额 vs 能力授权）塞进同一个包**。后果：

- 类型膨胀：Policy 既要表达 "ToolTimeout=2min"，又要表达 "domain=customer_service 允许 kb_search 工具"
- 测试维度爆炸：现有 11 个字段 × 新增 N 个 capability profile
- 配置文件爆炸：每个业务域都要复制一份 timeout 默认值

**`internal/quota/`** 现状更尴尬：

```
$ ls internal/quota/
circuit_breaker.go circuit_breaker_test.go
```

**只有熔断器，不是配额包**。Phase 6「Modify `internal/quota/*`」期望从这里加业务域 quota 字段，但当前包根本不在这个语义层。

**修法**：

1. **不要扩 `runtimepolicy`**。新建 `internal/router/domain_policy.go`（仍在 router 包，符合"capability 主线只在 router"原则）表达 domain × capability × tool 边界
2. `runtimepolicy` 保持纯 resource limit 含义
3. `internal/quota/` 重命名或文档说明它是 circuit breaker，配额功能未来再说
4. Phase 6 acceptance 里的 `TestBusinessDomainCapabilityRequiresRuntimePolicy` 改名 `TestBusinessDomainCapabilityRequiresRouterDomainPolicy`

### HIGH-1：Memory Injector 接口要改签名，是隐藏 breaking change

```go
// internal/memory/injector.go:144
func (inj *Injector) InjectContext(ctx context.Context, userMessage string, sessionID string, userID string) (string, error)
```

计划 Phase 5「Memory target 能表达 owner/domain/session/context source」如果只改 `MemoryTarget` 字段（target.go:30-43 加一个 `DomainID`），**injector 没法在注入时按 domain 过滤** — 它根本拿不到当前请求的 domain。

要按 domain 过滤，**InjectContext 必须改签名**（加 `domainID` 或 `runtimeContext` 参数）。这是 breaking change，所有调用方要改。

`grep -n "InjectContext" internal/` 调用点（需进一步验证，至少 master/react_processor 用）必须同步改。

**修法**：
1. Phase 5 加 step：`InjectContext(ctx, userMessage, sessionID, userID, domainID string)` 或更优雅地传 `RuntimeContext` struct（`internal/memory/runtime_context.go` 似乎已存在，需核实）
2. 加 breaking change 声明 + 调用方 migration list
3. 加测试 `TestInjectorFiltersOutCrossDomainMemoryWhenDomainProvided`

### HIGH-2：`MemoryTarget` 现状已经很丰富，Phase 5 工作量被高估

```go
// internal/memory/target.go:30-43
type MemoryTarget struct {
    Scope       TargetScope
    ID          string
    Visibility  TargetVisibility
    UserID      string
    TenantID    string
    WorkspaceID string
    ProjectID   string
    RepoID      string
    SessionID   string
    AgentName   string
    SkillName   string
}
```

已有 11 字段，覆盖 owner（UserID/TenantID）、session（SessionID）、context source（AgentName/SkillName/SkillName）。**缺的只有 `DomainID` 一个字段**。

Phase 5 写法暗示要重做 target 模型，实际只需加一个字段 + scope 枚举加 `TargetScopeDomain`。**真实工作量比计划描述小 60%**。

但 HIGH-1 的接口改动反而被低估。**总体工作量持平，但任务分配错位**。

### HIGH-3：`recordRouteDecisionSpan` 和 `RouteDecisionEvent` 是两条路，Phase 2 没收敛

```go
// internal/master/quality_events.go:127-146
func (m *Master) recordRouteDecisionSpan(traceID, spanID string, session *SessionState, span router.DecisionSpan) {
    ...
    m.enqueueLog(observability.LogEntry{
        Level:     "info",
        Message:   "quality.route_decision.span",
        ...
        Attributes: map[string]any{
            "route_decision_span": span,  // ← 走 log 路径
        },
        ...
    })
}

// 对照 recordRouteDecision (line 110-125)
m.emitQualityEvent(..., agentquality.Event{
    Name:          agentquality.EventRouteDecision,    // ← 走 quality event 路径
    ...
    RouteDecision: ev,
})
```

**两个 router 输出走不同管道**：
- `DecisionSpan`（更细的 router-internal 调试数据）→ log only
- `RouteDecisionEvent`（聚合后的快照）→ metric + log + journal

Phase 2「`RouteDecisionEvent` 输出 allowed/blocked capability snapshots」隐含让 RouteDecisionEvent 变细，但**没说 DecisionSpan 是合并、保留还是废弃**。如果合并，需要决定 schema；如果保留，下游消费者需要知道两条路区别。

**修法**：Phase 2 加决策：
- 选项 A：DecisionSpan 合并进 RouteDecisionEvent，统一一个 schema
- 选项 B：保留两条路，文档明确 DecisionSpan 用于 debug、RouteDecisionEvent 用于审计
- 写 ADR

### HIGH-4：`SortTraceTimelineItems` 稳定排序已实现，Phase 3 重复列在 required behavior

```go
// internal/observability/trace_view.go:54-67
func SortTraceTimelineItems(items []TraceTimelineItem) {
    sort.SliceStable(items, func(i, j int) bool {
        if !items[i].Timestamp.Equal(items[j].Timestamp) {
            return items[i].Timestamp.Before(items[j].Timestamp)
        }
        if items[i].TraceID != items[j].TraceID {
            return items[i].TraceID < items[j].TraceID
        }
        if items[i].SpanID != items[j].SpanID {
            return items[i].SpanID < items[j].SpanID
        }
        return items[i].Operation < items[j].Operation
    })
}
```

排序逻辑完全符合 Phase 3「Trace timeline 能按 timestamp 稳定排序；同一 timestamp 使用 trace/span/operation 稳定 tie-break」描述 — **已经实现**。

但计划仍把它列在 Phase 3 required behavior。说明计划写作时没核对现状。

**修法**：Phase 3 删此条；同时在该条位置加「保留现有 tie-break，新增 capability event 时不得改变现有排序契约」契约保护。

### HIGH-5：Web API owner check 已落地，Phase 1 测试是补回归不是建机制

```go
// internal/api/session_trace_handlers.go:18-28
func (s *Server) loadAuthorizedSession(w http.ResponseWriter, r *http.Request, sessionID string) (*store.SessionRecord, bool) {
    session, err := s.master.GetSessionByID(r.Context(), sessionID)
    if err != nil {
        writeJSON(w, http.StatusNotFound, ...)
        return nil, false
    }
    if !s.checkSessionOwnership(w, r, session) {
        return nil, false
    }
    return session, true
}
```

`grep -n checkSessionOwnership internal/api/session_handlers.go` 找到 11 处调用，覆盖 Journal/Trace/Trajectory 三类 handler。

**真正的 gap 是 Admin API**：
- `internal/api/admin_workbench_handlers.go`
- `internal/api/admin_quality_handlers.go`
- `internal/api/admin_memory_handlers.go`

跨 session 聚合时是否对每条结果做 owner 过滤或 redaction？计划 Phase 1「Admin 聚合 API 可以跨 owner，但 response 中不得泄漏 secrets/raw credentials」措辞太软，没列具体保护机制。

**修法**：Phase 1 加：
1. 加 ADR：Admin API 跨 owner 的 read-only 聚合允许，但 raw event payload 必须先 redaction
2. 测试 `TestAdminWorkbenchClustersDoNotLeakRawSecrets`、`TestAdminMemoryListRedactsCredentials`
3. 用 CRITICAL-1 同一个 redactor

### HIGH-6：`Cluster` 加 domain 字段会改变 ClusterKey hash，历史 cluster 连续性会断

```go
// internal/qualityworkbench/cluster.go:95-?
func ComputeClusterKey(rule GroupingRule, rec agentquality.CandidateRecord) ClusterKey {
    ev := rec.SourceEvent
    parts := make([]string, 0, len(rule.KeyFields))
    for _, f := range rule.KeyFields {
        switch f {
        case "failure_type":
            ...
        // 当前 key 由 failure_type / tool_or_skill / prompt_key / error_digest 组成
    }
}
```

`DefaultGroupingRule` 的 `KeyFields = ["failure_type", "tool_or_skill", "prompt_key", "error_digest"]`（`cluster.go:67-72`），不含 domain。

Phase 4 「Clusters 支持按 domain/source_kind/source_name/failure_type 过滤」如果把 domain 加入 KeyFields 默认列表，**所有历史 cluster 的 key hash 会全部变化**：
- 历史 candidate 重新聚类
- 既有 cluster 的 OpenCount/Size/CandidateIDs 失真
- 周报 trend 出现断点

**修法**：Phase 4 加迁移策略：
- 选项 A：保持默认 KeyFields 不变，只允许通过新建 GroupingRule 显式启用 domain
- 选项 B：版本化 cluster_key（加 `key_version` 字段），历史 cluster 标记 v1，新 cluster 用 v2
- 选项 C：一次性 rebuild + 周报标注断点

推荐 B。但计划没提，落地时会被踩坑。

### MEDIUM-1：`agentquality.Event` 子结构与计划的顶层归因有冲突

`ToolRecall.TurnID/TraceID`、`Delegation.ParentTraceID/ChildTraceID/AgentID/GroupID` 已经在子结构里（`types.go:67-93`）。

Phase 1 把同名字段加到 Event 顶层：
- 写入侧：emit 时填哪个？
- 消费侧：读取时按哪个？
- `BuildAgentTraceTree`（`trace_view.go:69-122`）当前从 `Delegation` 子结构反解析

**修法**：Phase 1 加决策矩阵：

| 字段 | 顶层 | 子结构 | 关系 |
|---|---|---|---|
| TraceID | ✓ | ToolRecall/Delegation | 顶层 canonical；子结构保留兼容（emit 时同步填充，读取时优先顶层） |
| TurnID | ✓ | ToolRecall | 同上 |
| AgentID | - | Delegation | 仅 delegation 才有意义，保留在子结构 |
| ParentTraceID | - | Delegation | 同上 |
| GroupID | - | Delegation | 同上 |

写 ADR。

### MEDIUM-2：`IntentCreateSkill` 与 `skill_authoring` domain 的方向反

```go
// internal/router/capability_registry.go:55-65
var skillDomainRules = map[string]SkillDomainRule{
    "skill_authoring": {
        Capabilities:       []Capability{CapabilityMetaSkillCreate, CapabilityMetaSkillModify},
        AllowedIntentKinds: []IntentKind{IntentCreateSkill, IntentModifySkill},
        CallableTool:       "skill",
    },
    "mcp_server_building": {
        Capabilities:       []Capability{CapabilityMetaToolRegister},
        AllowedIntentKinds: []IntentKind{IntentManageTool},
        CallableTool:       "skill",
    },
}
```

当前**方向是 domain → 声明它接受哪些 intent**（domain 视角）。计划 Phase 2「`IntentCreateSkill` 只允许 `skill_authoring` 的 skill workflow」是**intent → 声明它接受哪些 domain**（intent 视角）。两个方向都对，但**第一个已存在，第二个要新建**。

**修法**：Phase 2 改写为「校验 `EvaluateToolPolicy` 在 IntentCreateSkill 下，从 candidates 中只放行 domain=skill_authoring 的 skill workflow profile」 — 用现有 `skillDomainRule(domain).AllowedIntentKinds` 反查即可，**不需要新增数据结构**。

### MEDIUM-3：`enqueueLog.SessionID` vs Phase 1 高基数禁令的关系不清

Phase 1 写「`MetricLabels` 不输出 `run_id/session_id/user_id/owner_id/trace_id/span_id`」，但 LogEntry 同样持久化到 hive_logs 表，并通过 trace_view 暴露到前端。下游 loki/elasticsearch 是否把这些字段当 label/keyword 用？计划未表态。

**修法**：Phase 1 把「低基数」契约扩展到 metric + log label dimension（保留 doc field 全字段），加测试断言 `observability.LogEntry` 输出到 metric pipeline 时 session_id 不进 label。

### MEDIUM-4：Phase 4 持久化 schema 迁移没列

`qualityworkbench/pg_store.go` 当前持久化 Cluster/CandidateRecord 到表（具体表名待确认，~~未读完~~ Phase 4 实施时必须读）。加 domain/source 字段意味着：

- 加列 SQL migration
- 旧行 domain=NULL 兼容
- 索引（按 domain filter 查询性能）

计划 6.1「优先复用现有表」是好原则，6.2「只有在明确无法表达时才允许加列」也是好原则。但 Phase 4 明显需要加列，计划没显式列出 migration 项。

**修法**：Phase 4 加 step：
- 列出每张表的 alter 语句
- 加 `internal/store/postgres_migrate.go` 注册新迁移
- 加双写期：先写新列允许 NULL，回填 backfill job，N 周后改 NOT NULL

### LOW-1：Phase 7 客服试点 9 个准入条件没排序，没估时

Phase 7 列了 9 个 checkbox 作为客服试点重开条件。它们之间有强依赖：
- Phase 1 完成 → Phase 4 才能聚合 domain
- Phase 2 完成 → Phase 7 capability 才能挂上 skill_authoring 类的 router
- Phase 5 完成 → 客服 memory 才能按 domain 隔离

**没排序 + 没估时 + 没中间 milestone** → 客服试点变成"等所有 Phase 完成"的单一里程碑。CEO 视角看这就是黑盒，老板永远不知道什么时候能动。

**修法**：Phase 7 加：
- 9 个准入条件按依赖关系排序
- 每条标注它依赖哪些 Phase
- 给整体里程碑估时（推荐 3-4 周人工 / 1 周 CC+gstack）
- 加 2-3 个中间 milestone（如「Phase 1+2 完成」可启动客服**capability 接入演练**，不开放真实流量）

### LOW-2：`docs/架构设计/Run质量治理地基.md` ADR 尚未存在

Phase 0 acceptance 检查的是计划文档自身的语义。但 ADR 文件本身（`docs/架构设计/Run质量治理地基.md`）当前是 **Create/Modify**，意味着可能还不存在。

```bash
$ ls docs/架构设计/
# 需进一步验证 — 我没有 grep 这一项
```

**修法**：Phase 0 Step 0.1 显式说「若文件不存在则 create with ADR template」，加 template 链接。

### LOW-3：前端 redact 显示策略未定义

`Replay/Trace/Journal` 三个 surface 现在前端会渲染 quality event。Phase 3 改后 quality event 携带 redacted 字段（如 `***redacted***`）。前端是否：
- 直接显示 redact marker？
- 给 admin 一个「unhide」按钮？
- 完全隐藏？

不同选择关系到 admin 排障效率 vs 安全。计划 Phase 3 没说。

**修法**：Phase 3 加 frontend behavior 决策点 + AskUserQuestion 给 user。

---

## 三、必改清单（按优先级分 Phase）

### 立即必改（合并到 Phase 1，P0）

| # | 必改项 | 文件:行号 |
|---|---|---|
| P0-1 | 实现 `internal/agentquality/redact.go`，所有 emit 出口（metric/log/journal）走 redacted version | new file + `internal/master/quality_events.go:61` |
| P0-2 | 加 `TestQualityEventRedactsSecretsBeforeJournal/Log/Metric` 三个测试 | new test |
| P0-3 | 跑 staging 历史 journal 数据扫描，识别已泄漏的 secret/credential | runbook |
| P0-4 | 加 CI 检查 `scripts/check_no_parallel_run_system.sh` 拒绝 `internal/run/` `internal/capability/` 复活 | new script + .github/workflows |
| P0-5 | Phase 1 加字段决策矩阵（顶层 vs 子结构），写 ADR | `docs/架构设计/Run质量治理地基.md` |

### Phase 1（execution identity + owner）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P1-1 | 删除「`SortTraceTimelineItems` 稳定排序」requirement（已实现），改为「契约保护」 | 修订计划 Phase 3 |
| P1-2 | 删除「Web API owner check」implicit requirement（已实现），改为「补回归测试」 | 修订计划 Phase 1 |
| P1-3 | 加「Admin API 跨 owner 聚合必须 redaction」契约 + 测试 | 修订计划 Phase 1 + new test |
| P1-4 | `LogEntry.SessionID` 不进 metric label 的契约 + 测试 | new test |

### Phase 2（capability snapshot）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P2-1 | 决策 `DecisionSpan` vs `RouteDecisionEvent` 合并 / 保留 / 废弃，写 ADR | 修订计划 Phase 2 |
| P2-2 | `IntentCreateSkill` 边界用现有 `skillDomainRule.AllowedIntentKinds` 反查，不新建结构 | `internal/router/decision.go` |
| P2-3 | `BuildAgentTraceTree`（trace_view.go:69-122）的反向耦合显式记入 ADR，Phase 1 改 Delegation 时此处同步 | 修订计划 Phase 1 |

### Phase 4（qualityworkbench）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P4-1 | 加 `cluster_key` 版本化（v1/v2），历史 cluster 保 v1 | `internal/qualityworkbench/cluster.go` + `pg_store.go` |
| P4-2 | 列出每张表的 alter SQL，注册到 `postgres_migrate.go` | `internal/store/postgres_migrate.go` |
| P4-3 | 工作量重估：3-5 天人工 / 1 天 CC+gstack，更新计划估时 | 修订计划 |
| P4-4 | Admin API 跨 owner 加 redaction（与 P1-3 同代码路径） | `internal/api/admin_workbench_handlers.go` |

### Phase 5（memory + KB 边界）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P5-1 | `InjectContext` 改签名加 domain 参数，列调用方 migration list | `internal/memory/injector.go:144` |
| P5-2 | `MemoryTarget` 加 `DomainID` 字段（仅一字段） | `internal/memory/target.go:30-43` |
| P5-3 | 工作量重估：MemoryTarget 改动小，injector 接口改动大 | 修订计划 |

### Phase 6（业务域准入）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P6-1 | **不要扩 `runtimepolicy`**，新建 `internal/router/domain_policy.go` | new file |
| P6-2 | `internal/quota/` 包内容声明（circuit breaker），与配额功能解耦 | `internal/quota/README.md` |
| P6-3 | 修订 Phase 6 acceptance test 名称 | 修订计划 |

### Phase 7（客服试点）补强

| # | 必改项 | 文件:行号 |
|---|---|---|
| P7-1 | 9 个准入条件按依赖关系排序 + 估时 | 修订计划 |
| P7-2 | 加 2-3 个中间 milestone | 修订计划 |

---

## 四、验收和回滚补强

### 4.1 计划现有验收（Section 8）的盲点

| 验收项 | 状态 | 盲点 |
|---|---|---|
| 后端最小回归 | OK | 没覆盖 redaction（CRITICAL-1） |
| CLI 回归 | OK | 没覆盖 `cmd/agentquality replay-shadow` 的 cluster key 兼容 |
| 前端回归 | OK | 没覆盖前端 redact 显示策略 |
| 完整回归 | OK | 完整 `go test ./...` 太粗 |

### 4.2 建议增加的验收

```bash
# A. redaction 必过（P0）
go test ./internal/agentquality -run 'Redact' -count=1
go test ./internal/master -run 'QualityEventRedacts' -count=1

# B. 高基数禁令
go test ./internal/agentquality -run 'MetricLabel(Card|HighCard)' -count=10  # 跑 10 次防 flaky

# C. cluster key 版本化
go test ./internal/qualityworkbench -run 'ClusterKey(Version|Migration|Backcompat)' -count=1

# D. 跨 owner gate
go test ./internal/api -run 'RejectsCrossOwner|AdminRedact' -count=1

# E. 反向耦合保护
go test ./internal/observability -run 'BuildAgentTraceTreeFromQualityDelegation' -count=1

# F. CI 防御
bash scripts/check_no_parallel_run_system.sh
```

### 4.3 回滚预案

计划 Section 7 列了 10 项风险但**没有回滚预案**。每项风险匹配回滚动作：

| 风险 | 回滚动作 |
|---|---|
| Redaction 误删 audit 关键字段 | redaction 走 feature flag，可一键 disable redact path，但 emit 仍 redacted（即"漏过 redact，但 marker 替换为 raw"）|
| Cluster key 版本化错聚 | rollback 到 v1 KeyFields，重 rebuild |
| `InjectContext` 签名变更冲击调用方 | 保留旧签名 forward 到新签名 + log deprecation warning |
| Domain policy 误判 | feature flag `router.domain_policy_enforce=false` 降级到无 domain 检查 |

每项加 runbook。

---

## 五、CEO 最终判断

### 5.1 立场

**正确且珍贵**。「反对重建 RunRuntime / Capability Catalog / DomainPack Registry / 业务域工作台」这条立场是这个计划最值钱的部分，价值远高于具体字段变更。**必须用 ADR + CI script + 路由器审查三层锁死**，否则未来 worker 还会按旧计划起炉灶。

### 5.2 现状摸底

**计划写作时多处未核对现状**，导致：
- 已实现的能力被列为 required behavior（trace 稳定排序、Web API owner check）→ 工作量虚高
- 现状已成熟的结构被夸大改造范围（MemoryTarget）→ 任务边界模糊
- 现状是 P0 风险的项被当 「补强」（Journal redaction）→ 严重低估优先级
- 现状包语义错配的项被强行复用（runtimepolicy + quota）→ 设计风险

### 5.3 工程纪律

**Phase 0 ADR 是好的，但只是文档防御**。必须配合 CI script 才有持续效力。

### 5.4 P0 必须前置

`master/quality_events.go:242` 的 raw json 直入 journal 是 P0 安全风险。**必须从 Phase 3 抽出来前置到 Phase 1 同期完成**，否则每一天 production 都在持续累积泄漏面。

### 5.5 排期

按修订后的工作量重估：

| Phase | 人工估时 | CC+gstack 估时 | 关键产出 |
|---|---|---|---|
| Phase 0 + P0 redaction | 1 天 | 2-3 小时 | ADR + CI script + redactor |
| Phase 1 | 2 天 | 0.5 天 | execution identity 字段决策矩阵 |
| Phase 2 | 1.5 天 | 0.5 天 | capability snapshot + DecisionSpan 决策 |
| Phase 3 | 1 天 | 2-3 小时 | 时间线统一（多数已实现）|
| Phase 4 | 3-5 天 | 1 天 | qualityworkbench 业务域化（含 schema 迁移）|
| Phase 5 | 1.5 天 | 0.5 天 | memory injector 接口 + DomainID |
| Phase 6 | 2 天 | 0.5 天 | router/domain_policy.go 新建 |
| Phase 7 准入 | 0.5 天 | 1-2 小时 | 9 条准入排序 + milestone |
| **合计** | **12-15 天** | **3-4 天** | 客服试点可启动 |

中间 milestone 建议：
- 第 3 天：P0 redaction + Phase 0 ADR + CI script 上线
- 第 6 天：Phase 1+2+3 完成，可对内演示 execution identity 统一
- 第 10 天：Phase 4+5 完成，可启动客服 capability 接入演练（不开放真实流量）
- 第 12-15 天：Phase 6+7 完成，客服试点可开放真实流量

### 5.6 最大隐藏风险

| 排名 | 风险 | 说明 |
|---|---|---|
| 1 | Journal 完全无 redaction | P0 安全，必须前置 |
| 2 | runtimepolicy 包语义错配 | 落地时会发现"塞不进去"，需要中途返工 |
| 3 | InjectContext 接口签名变更 | 隐藏 breaking change，调用方多处 |
| 4 | Cluster key hash 不兼容 | 历史数据断点 |
| 5 | ADR 防御 vs worker 重建 | 唯一长期对抗的是 CI script |

---

## 六、推荐的下一步

1. **立即**：基于本审阅，把计划文档原地修订（合并 P0 redaction 前置 + 删 runtimepolicy 扩展 + 改 cluster 版本化 + 加 CI script + 加 milestone）。如同意上述意见，我可以直接产出修订版计划
2. **第 1 天**：上线 P0 — `internal/agentquality/redact.go` + CI script + ADR 文件
3. **第 2-3 天**：Phase 1+2，落字段决策矩阵
4. **第 4-6 天**：Phase 3+4，时间线 + workbench
5. **第 7-10 天**：Phase 5+6，memory + domain policy
6. **第 11-15 天**：Phase 7 准入验证 + 客服 capability 接入演练

---

## 七、本审阅的局限

诚实标注未读到的部分（占总相关代码 ~20%）：

- `internal/journal/pg_journal.go`（持久化层）
- `internal/observability/pg_writer.go`（log 持久化）
- `internal/qualityworkbench/pg_store.go`（cluster 持久化 schema）
- `internal/qualityworkbench/{replay_runner, batch_eval, report, grouping_*}.go`
- `internal/router/decision_span.go`、`replay.go`、`intent.go`（intent 当前实现细节）
- `internal/memory/runtime_context.go`（可能已有 domain 占位）
- `internal/api/admin_*_handlers.go`（admin gate 现状）
- 前端 `frontend/src/store/replay.ts`、`SessionReplay.tsx`、`qualityworkbench/QualityWorkbench.tsx`
- `cmd/agentquality`、`cmd/quality-weekly-report`、`cmd/quality-batch-eval`

涉及这些代码的结论（主要是 Phase 4 schema 迁移、Phase 5 runtime_context 复用、前端 redact 显示）以「需进一步验证」明示，请实施时实地核对。

---

**审阅完毕。**

本次审阅基于 12 个核心文件 + ~15 处 grep 的实地阅读。第一稿的草率（仅基于 6 文件就妄下「现状清楚」结论）已通过补读纠正。如发现仍有事实错误，请指出，我重新核查。
