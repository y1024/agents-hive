# Agent 记忆系统顶级最佳实践改造计划

> **状态**：COMPLETED / ARCHIVED（2026-05-10）
>
> **目的**：把现有 memory 机制升级成“可定位、可治理、可评测、可解释、可回滚”的长期记忆系统，而不是继续堆自由标签和弱召回。
>
> **适用范围**：Hive 的 `internal/memory`、`internal/master`、`internal/api/admin_memory_handlers.go`、`internal/tools/memory.go`、`frontend/src/pages/admin/MemoryGovernance.tsx`
>
> **已锁定的 4 项关键决策（CEO 审核 2026-05-09）：**
> 1. Schema 策略：**渐进双层（C）**——Phase 0 先 metadata 内嵌跑 2 周，Phase 2 升级一等列。
> 2. **新增 Phase -1**：RuntimeContext 改造（SkillName/TaskType/AgentName 注入 ctx），先于 Phase 0。
> 3. Eval gate 强度：**白名单**——`internal/memory/{injector,extractor,governance_ops}` diff 命中才跑 memory-eval。
> 4. LLM-in-loop 指标（任务成功率/token ROI）：**后置到 Phase 4**，Phase 2 先盯 5 个静态指标。
>
> **已锁定的 4 项工程决策（Eng 审核 2026-05-09）：**
> 5. **ScopePolicy 双方法**：`Allow(record, rctx, now) (bool, reason)` + `SQLFilter(rctx) (where, args)`，Search 走 SQL 下沉，Injector/Extractor/Prune 走应用层兜底。
> 6. **Phase 1 加 BatchGet API**：修复 hybrid 检索 N+1 写事务（每次 hybrid 命中 10 次 UPDATE access_count）。
> 7. **fixtureMemoryStore 加 score 排序**：现有 fixture 全部补 score 字段，Phase 1/2 排序变更必须能被 eval 捕捉。
> 8. **Phase 2 backfill 强制 chunked + SKIP LOCKED**：每批 ≤500 行，迁移期 Save 不被阻塞。
>
> **2026-05-09 实施校准（实事求是）：**
> - Phase -1/0/1/2/3 的代码路径已经落地到当前工作树：RuntimeContext、ScopePolicy、BatchGet、target/kind、schema 一等列双写/backfill、memory-eval gate、Hive Admin 监控面板入口。
> - memory-eval fixture 已扩到 **45 条 required**，本地 `go run ./cmd/memory-eval` 输出 `total=45, passed=45, required_passed=45`。
> - Phase 4 已实现 **promotion candidate → approval → apply procedural memory** 的可审计闭环；apply 需要 `subject_type=memory_promotion` 的 lead/admin approve。
> - Nightly eval 已有可执行确定性骨架（任务成功率差异 + token ROI + JUnit/JSON 输出），但还不是接入真实 LLM/replay 后“稳定运行 ≥2 周”的生产事实。
> - 生产监控默认走 Hive 自有 `hive_metrics` + React Admin，不引入 Prometheus/Grafana 强耦合；外部监控仍只能作为可插拔 sink/exporter。

## 归档记录（2026-05-10）

本计划已按“功能实现 + 自动化验证 + 页面实测 + 后台质量事件确认”的标准完成并归档。

完成事实：

- RuntimeContext、ScopePolicy、target/kind/subject_type、BatchGet、schema 一等列双写/backfill、memory-eval、Hive 自有监控面板、promotion approval/apply、nightly eval 骨架均已落地。
- 记忆治理页和 React Admin 监控入口已接入 Hive 自有指标体系；Prometheus/Grafana 未作为默认依赖引入，后续仅作为可插拔 exporter/sink 扩展。
- 自然语言长句召回已修复：严格 FTS/ILIKE 0 命中时，使用高信号词 relaxed recall，避免“请按/历史/建议”等上下文词拖死已有记忆。
- `memory` 工具保存新记忆时已写入默认 governance metadata，避免新增记录进入 Missing Governance 状态。
- 页面实测问题 `go test 失败了，我应该怎么定位失败测试？请按我的历史工作方式给我建议。` 已按历史工作方式回复。
- 后台 `quality.context_build` 已确认运行态注入成功：`memory_injected=true`、`memory_ids=[1]`、`estimated_tokens=67`。

归档验证：

- `env GOCACHE=/tmp/go-build go test ./internal/memory ./internal/tools -count=1` 通过。
- `env GOCACHE=/tmp/go-build go test ./... -count=1` 通过。
- `env GOCACHE=/tmp/go-build go build -o server ./cmd/server` 通过。

遗留说明：

- 旧历史 memory 记录不会自动补齐 governance；新保存记录已具备 governance。若需要批量治理旧数据，应另立“历史 memory governance backfill”小计划。
- Nightly eval 的 LLM-in-loop 长期稳定性属于上线后持续运营观察，不再阻塞本计划归档。

## 1. 现状判断

当前系统已经具备可用底座，但还不是顶级最佳实践。

已有能力：

- Postgres 持久化记忆表，带 `type / tags / session_id / metadata / embedding / search_vector`。
- `feedback` 和普通记忆分流注入。
- FTS + 可选 vector 的 hybrid 检索。
- 置信度、过期时间、来源、证据等治理元数据。
- Admin 侧治理页、剪枝、导出导入、向量空间迁移、backlog 统计。

主要缺口（v0.2 校核后修订）：

- 没有一等公民 `target` / `namespace`（确认：`internal/memory/types.go:31-44` MemoryRecord 仅有 user_id；schema `postgres_migrate.go:214-227` 也只有 user_id）。
- `tags` 是辅助过滤，不是主路由（确认）。
- 默认 embedding 关闭，语义召回不是主路径（确认：`internal/config/defaults.go:251-265` 未设 EmbeddingEnabled，`config.go:145` 默认 false）。
- ~~没有 memory eval 作为硬门禁~~ → **修订**：harness 已存在（`internal/memory/eval/runner.go` + 7 条 fixture），缺的是 **CI 接入**（`grep RunCases internal/api internal/master cmd` 零结果），不是从零搭。
- 没有把 semantic / episodic / procedural / feedback / reference 分层成稳定体系（确认：`types.go:11-28` 仅 4 类，缺 procedural/episodic）。
- ~~注入解释和回滚还不够完整~~ → **修订**：`InjectionResult` 已统计 5 类 skip 原因 + skipped_memory_ids（`injection_result.go:13-27`），且已经写到 quality_event（`react_processor.go:91-93`），缺的是 **Admin UI 展示**。
- **新增缺口**：`auth.UserIDFrom(ctx)` 是唯一 scope，TenantID/WorkspaceID/RepoID 在 auth 包零定义；MemoryQueryContext 设计的 11 字段中有 8 个**当前 ctx 根本拿不到**。
- **新增缺口**：跨用户隔离逻辑分布在 5 个位置（`pg_store.go:107`、`pg_store.go:319`、`injector.go:271`、`governance_ops.go:158`、`extractor.go:601`），加 target 维度后会发散，必须收成单一 `ScopePolicy.Allow`。
- **新增缺口**：embedding 异步 goroutine 在 `embeddingSem` 满载时 5 分钟后**静默丢弃**（`pg_store.go:84-100`），且无生产指标。

## 2. 改造目标

把记忆系统做成三层：

1. **Memory Formation**：决定什么值得记、记到哪里、有效期多久。
2. **Memory Retrieval**：决定当前任务允许看哪些记忆、怎么排序、怎么裁剪。
3. **Memory Governance**：决定什么能注入、什么要过期、什么要冲突解决、什么要审计。

最终要求：

- 记忆召回提高任务成功率。
- 不相关记忆不能污染上下文。
- 跨用户、跨租户、跨工作区泄漏为 0。
- 每次注入都能解释“为什么选中/为什么跳过”。
- 所有 memory 相关改动可评测、可回归、可回滚。

## 3. 顶级实践形态

### 3.1 一等公民 Target

新增记忆定位维度，而不是继续让 `tags` 承担过多职责。

建议新增：

- `target_scope`: `user | org | team | workspace | project | repo | session | agent | skill`
- `target_id`: 具体对象 ID
- `visibility`: `private | team | org | global`
- `memory_kind`: `semantic | episodic | procedural | feedback | reference`
- `subject_type`: `user_preference | project_decision | repo_fact | tool_pattern | failure_lesson`

原则：

- `target` 决定“这条记忆该在哪个上下文里生效”。
- `kind` 决定“这是什么性质的记忆”。
- `tags` 只做辅助筛选和运营分析，不做主分类。

### 3.2 分层记忆

把现有记忆明确分层：

- Working memory：当前会话状态、未完成任务、上下文碎片。
- Semantic memory：事实、偏好、决策、稳定知识。
- Episodic memory：历史任务轨迹、成功/失败案例。
- Procedural memory：做事规则、工具使用经验、prompt/skill 建议。
- Governance memory：来源、证据、置信度、过期、冲突、审计。

### 3.3 路由式检索

检索顺序改成：

1. 先按 `user / tenant / visibility / target_scope` 硬过滤。
2. 再按 `task_type / repo / skill / project` 做候选池路由。
3. 再做 `FTS + vector + recency + confidence + usage` 排序。
4. 最后做 governance 过滤和 token budget 裁剪。

不是“搜到什么就注入什么”，而是“当前任务允许哪些记忆进入候选池”。

### 3.4 形成流水线

记忆写入必须有明确流水线：

`observe -> propose -> classify -> target -> dedupe -> conflict check -> confidence -> ttl -> approve/apply`

每条记忆都要带：

- 来源 message / span / trace
- evidence 摘要
- confidence
- extractor_version
- expires_at
- supersedes / conflicts_with
- write_reason
- audit status

## 4. 计划方案

### Phase -1: RuntimeContext 打通（CEO 审核新增，必做前置）

目标：让 ctx 能拿到 SkillName/TaskType/AgentName，否则 target 字段写入端永远只有空字符串。

工作项：

- `internal/master/master.go:179-185` 已有 AgentName/SkillName 字段，扩到 ctx propagator。
- `react_processor.go` 在 InjectContextDetailed 调用前把 SkillName/TaskType 注入 ctx。
- `internal/memory/extractor.go` FeedbackInput 增加 SkillName/TaskType/AgentName 三字段，`reflection_evaluator.go:100-109` 调用处补填。
- 新增 `RuntimeContext` 类型（在 `internal/memory/runtime_context.go` 独立文件），后续作为 MemoryQueryContext 基础。
- **ctx.Value 注入模式**：提供 `WithRuntimeContext(ctx, rc) context.Context` 和 `RuntimeContextFrom(ctx) RuntimeContext`，模仿 `auth.UserIDFrom`。所有调用方就近取，避免改 50+ 处函数签名。
- **必须穿透 subagent**：`internal/subagent/compaction/agent.go:163` 的 `ExtractFromSummary` 调用前从父 ctx 复制 RuntimeContext 到 subagent ctx，否则 compaction 写出的记忆 target 字段全空。
- **async embedding goroutine 必须复制 RuntimeContext**：`pg_store.go:84-100` 当前用 `context.Background()`，必须改成在 Save 入口提取 RuntimeContext 并塞回独立 ctx，否则 vector 索引失去 scope 信息。

```
RuntimeContext 数据流：
  HTTP/IM 入口 → auth.WithUser(ctx) → master.processTaskDirectExec
       ↓ (Phase -1 新增)
  WithRuntimeContext(ctx, RuntimeContext{
      UserID: auth.UserIDFrom(ctx),   // 已有
      SessionID, AgentName,            // 已有于 master.go:179-185
      SkillName, TaskType,             // Phase -1 新增
  })
       ↓
  react_processor → memoryInjector.InjectContextDetailed(ctx, ...)
       └─→ ScopePolicy.Allow(record, RuntimeContextFrom(ctx), now)

  reflection_evaluator → ExtractFeedback(ctx, FeedbackInput{...})
       └─→ extractor 从 ctx 取 SkillName 自动填入 governance.skill_name

  compaction.agent → ExtractFromSummary(ctx, ...)
       └─→ ctx 已被父级注入 RuntimeContext，extractor 直接读
```

验收：

- feedback 记忆 metadata 可看到 skill_name/task_type 来源。
- 单测覆盖："skill A 写入的 feedback，在 skill B 上下文检索时被降权或过滤"（即使逻辑还没启用，先把数据写进去）。
- compaction subagent 写出的记忆带完整 SkillName。
- async embedding goroutine 不丢 RuntimeContext。
- 工期：1 周。

### Phase 0: 先把 schema 和语义钉住（采用 Approach C：metadata 内嵌）

目标：不动大表结构，先把语义层补齐到 metadata.target / metadata.kind；2 周观察数据后再决定升一等列。

工作项：

- `internal/memory/governance.go` 旁新增 `Target{Scope, ID, Visibility}` 和 `Kind` 类型，`EncodeTarget` / `DecodeTarget` / `EncodeKind` / `DecodeKind`。
- 复用 metadata jsonb GIN expression index 模式（参考 `postgres_migrate.go:234-237`），新增 `idx_memories_target_scope`、`idx_memories_kind` 两条 expression 索引。
- **ScopePolicy 接口（Eng 审核锁定双方法签名）**：

```go
// internal/memory/scope.go (新文件)
type ScopePolicy interface {
    // Allow: 应用层判断，给 Injector/Extractor/Prune 用
    Allow(record MemoryRecord, rctx RuntimeContext, now time.Time) (allowed bool, reason string)
    // SQLFilter: SQL where 子句生成器，给 pg_store.Search/List 用
    // 返回的 where 片段会被 AND 拼到现有 query 上，args 顺序追加
    SQLFilter(rctx RuntimeContext) (where string, args []any)
}

// 默认实现 DefaultScopePolicy 处理 visibility=private/team/org/global 四种语义
```

- **替换 5 处分散检查**：注入器 (`injector.go:271`) / extractor 去重 (`extractor.go:601`) / governance prune (`governance_ops.go:158`) → 用 Allow；pg_store Search/List/Get → 用 SQLFilter。Get 维持 SQL where 直接比较 user_id（hot path 性能优先）。
- `internal/memory/types.go` 扩 MemoryType 增加 `procedural` 和 `episodic`（仅占位，Phase 4 真用）。
- `tags` 主路由角色禁止：在代码 review 检查表加一条"新 PR 不能用 tags 做检索分类逻辑"。
- Admin 导出导入带上 metadata.target / metadata.kind（`admin_memory_handlers.go:104-115` ImportMemoriesJSON 透传）。

验收：

- 新写入记忆 100% 含 target/kind 字段。
- ScopePolicy 单元测试覆盖 6 种组合：(global, user_ctx) / (user, user_ctx) / (user, anon_ctx) / (team, user_ctx_in_team) / (team, user_ctx_outside_team) / (org, cross_user_ctx)。
- 现有 7 条 eval fixture 全绿；新增 5 条 fixture 覆盖 target 路由（5 ↑ 12）。
- 旧数据兼容：Search 回退路径不依赖 target 字段，老记忆 metadata 缺字段视为 `target_scope=user, visibility=private`。
- 工期：1.5 周。

### Phase 1: 改检索，不改结果格式（Eng 审核增强）

目标：让注入先路由，再排序，再治理；同时修复 hybrid N+1 写事务。

工作项：

- 引入 `MemoryQueryContext`（构造时从 `RuntimeContextFrom(ctx)` 取数）。
- 改造 injector，让检索先按 target 路由（调用 `ScopePolicy.SQLFilter` 下沉到 SQL）。
- feedback、semantic、episodic 分开候选池。
- tags 只作为过滤或 boost，不作为主分类。
- 继续保留 FTS 兜底。
- **新增 BatchGet API（Eng 审核锁定）**：修复 `injector.go:202-218` 当前 hybrid 路径循环 Get 问题。Get 实现是 `UPDATE ... RETURNING *`，每次 hybrid 命中 = 10 次写事务 + 行锁竞争。

```go
// pg_store.go 新增方法
func (s *PostgresMemoryStore) BatchGet(ctx context.Context, ids []int64) ([]MemoryRecord, error) {
    // 1. SELECT ... WHERE id = ANY($1) AND user_id = $2 — 纯读，不更新
    // 2. 异步 goroutine 发一条合批 UPDATE：
    //    UPDATE memories SET accessed_at=NOW(), access_count=access_count+1 WHERE id = ANY($1)
    //    用独立 ctx，失败不影响主流程，加生产指标计数器
}
```

性能预期：单次 hybrid 写事务从 10 → 1，p99 改善 5–10×。

- **Admin 注入解释面板**（独立 lane，并行）：消费已有 `quality_event` 数据（`react_processor.go:91-93` 已写入），前端 `MemoryGovernance.tsx` 加"最近 N 次注入"列表 + skip 原因下钻。**不需要 Phase 0 完成即可启动**。

验收：

- 相同问题在不同 workspace / project / skill 下召回不同。
- 无关记忆注入率下降。
- 低置信、过期、跨用户记忆不会进入上下文。
- hybrid pgbench 压测：50 并发 session 下 p99 < 100ms（修复前估算 500ms+）。
- Admin 用户能看到"为什么这条 memory 没注入"。
- 工期：1.5 周（路由 1 周 + BatchGet 0.5 周；Admin 面板独立 lane 1 周）。

### Phase 2: eval gate 接入 + schema 升级一等列（合并 CEO 审核后修订）

目标：memory 相关改动必须过评测；同时基于 Phase 0 metadata 沉淀的真实数据，升级 target 字段到 SQL 一等列。

工作项（eval gate 部分）：

- 新建 `cmd/memory-eval`，加载 `internal/memory/eval/testdata/` 全量 fixture，输出 JUnit + JSON。
- GitHub Actions：扩展现有 `phase0-gates` workflow，**白名单触发**（diff 命中 `internal/memory/{injector,extractor,governance_ops,hybrid}.go` 或 `eval/testdata/*.json` 才跑），其余 PR 跳过。
- baseline.json 阈值：required pass rate=100%（已有 fixture 全 required），optional pass rate ≥ 90%。
- **fixtureMemoryStore 加 score 排序（Eng 审核锁定）**：`internal/memory/eval/store_test_helpers.go:40` `filterFixtureRecords` 改成按 `record.Score` DESC 排序后 limit；现有 7 条 fixture 全部补 `score` 字段（mc01 → 0.95、mc04 → ID5 score=0.95 / ID4 score=0.6 等）。否则 Phase 1/2 SQL 排序变更过不了 eval。
- fixture 扩到 30 条，5 类静态指标各 4-6 条：
  - 正确召回率（含 hybrid 命中场景）
  - 无关注入率（同义词诱导、长尾 query）
  - 跨用户泄漏率（含 visibility=team / org 边界）
  - 过期抑制率（边界时间 ±1s）
  - 冲突优先级（同主题 confidence 高/低对照、supersedes 链）
- **不做** "任务成功率" 和 "token ROI"——这两个是 LLM-in-loop 指标，列入 Phase 4，理由：现 harness 是纯静态 InjectContextDetailed 调用，跑 react 循环是另一个量级工程。

工作项（schema 升级部分）：

- 添加一等列：`target_scope TEXT NOT NULL DEFAULT 'user'`、`target_id TEXT NOT NULL DEFAULT ''`、`visibility TEXT NOT NULL DEFAULT 'private'`、`memory_kind TEXT NOT NULL DEFAULT ''`、`subject_type TEXT NOT NULL DEFAULT ''`。
- 复合 B-tree 索引：`(user_id, target_scope, target_id) WHERE user_id != ''`、`(memory_kind, accessed_at DESC)`。
- 双写过渡：Phase 0 的 metadata.target/kind 继续写，但 SQL 优先读一等列。
- **Backfill 必须 chunked + SKIP LOCKED（Eng 审核锁定）**：

```sql
-- 每批 ≤500 行，迷你事务，失败可重试
DO $$
DECLARE
  affected INT := 1;
BEGIN
  WHILE affected > 0 LOOP
    WITH batch AS (
      SELECT id FROM memories
      WHERE target_scope = ''
      LIMIT 500
      FOR UPDATE SKIP LOCKED
    )
    UPDATE memories SET
      target_scope = COALESCE(metadata->'target'->>'scope', 'user'),
      visibility   = COALESCE(metadata->'target'->>'visibility', 'private'),
      memory_kind  = COALESCE(metadata->'kind', type)
    WHERE id IN (SELECT id FROM batch);
    GET DIAGNOSTICS affected = ROW_COUNT;
    PERFORM pg_sleep(0.1);  -- 给 Save 让出窗口
  END LOOP;
END $$;
```

不允许单条 UPDATE。10000 行表上估算 backfill 耗时 ~30s，期间 Save p99 < 100ms（无大事务锁）。
- 升级条件：Phase 0 上线 ≥ 2 周 + eval pass rate 稳定 ≥ 100% required。

验收：

- memory 白名单 PR CI 强制跑 memory-eval 且通过。
- 升级后 fixture 全绿，无回归。
- 老数据 backfill 完整性：`SELECT COUNT(*) FROM memories WHERE target_scope='' OR target_scope IS NULL` = 0。
- 工期：1.5 周（eval 接入 1 周 + schema 升级 0.5 周，可并行）。

### Phase 3: 打开 hybrid 和 embedding 治理

目标：让语义召回成为受控增强，而不是静默切换。

工作项：

- 记录 embedding provider / model / vector space / dimension（已有 `vector_space.go` 框架，需在 admin 暴露）。
- **必须新增的 4 个生产指标**（走现有可插拔 recorder，默认落 `hive_metrics`，不新增 Prometheus `/metrics` 耦合）：
  - `memory_embedding_dropped_total{reason}` —— 当前 `pg_store.go:84-100` 满载丢弃静默
  - `memory_embedding_latency_seconds`（histogram）
  - `memory_embedding_backlog_depth`（gauge）
  - `memory_vector_space_mismatch_total`
- 禁止静默混用向量空间：`hybrid.go:71` 当前 fallback 仅 `Warn` 日志，需配指标计数 + alert 规则。
- 指标展示走 **Hive 自有 Admin 监控面板**，不交付 Grafana 面板：
  - 后端提供 `GET /api/v1/admin/memory/metrics`，从 `hive_metrics` 聚合 snapshot + series + 内置告警提示。
  - 前端 `MemoryGovernance.tsx` 展示 dropped / fallback / mismatch / backlog / latency p95、原因分布和最近趋势。
  - 外部监控系统只作为可选 sink/exporter，不进入 memory 核心包依赖。
- **中文 fallback 性能修复**：`pg_store.go:336-347` ILIKE 中文回退当前不走 GIN，10000 行起会顺扫。加：

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_memories_content_trgm ON memories USING GIN(content gin_trgm_ops);
```

预期 p99 从 100ms+ 降到 < 20ms。
- 只有通过 eval（`hybrid_recall_rate ≥ baseline`）后才扩大 hybrid 默认使用范围。
- Admin UI 注入解释面板（已有 quality_event 数据，只需前端展示）：列出最近 N 次注入的 SkippedExpired/SkippedLowTrust/SkippedCrossUser/SkippedTokenBudget 计数，点击可下钻到 SkippedMemoryIDs。

验收：

- hybrid 失败时有明确 fallback + 指标计数。
- Admin 自有监控页面能看到最近 24h memory 生产指标和告警提示。
- 向量空间切换可审计、可回放、可恢复。
- Admin 用户能在 UI 上回答："为什么这条 memory 没注入？"
- 工期：1 周。

### Phase 4: feedback/procedural 闭环 + LLM-in-loop eval（v0.2 修订）

目标：让系统学到"怎么做事"，而不只是"记住事实"，并补上 Phase 2 后置的 2 个高阶指标。

工作项（闭环部分）：

- ~~"将 evaluator/reflection 中的高价值反馈稳定写成 feedback 记忆"~~ → 已完成 50%（`reflection_evaluator.go:92-113`），剩下：
  - MemoryType 增加 `procedural` 真正使用（Phase 0 占位升正式）
  - 把"工具调用模式 / skill 推荐 / prompt 片段"分类写入 procedural
- 对高价值 memory 生成可审计的改进建议：复用 `internal/api/admin_optimization_handlers.go` 的 ApprovalSubjectType 框架，新增 `subject_type=memory_promotion` 类型。
- 允许人工审批后应用到治理策略、prompt 或 skill。

工作项（LLM-in-loop eval 部分）：

- 新增 nightly job（不入 PR CI）：跑一组 task fixture，每个任务前后对比"注入 vs 不注入 memory"的：
  - 任务成功率（reflection_evaluator 给分 ≥ 7）
  - token ROI（注入消耗的 token / 任务整体 token，目标 < 5%）
- 输出趋势曲线，每周对比 baseline。

验收：

- 反馈不只是被记录，还能反向改善后续任务。
- 改进建议可追踪、可回滚。
- LLM-in-loop nightly job 稳定运行 ≥ 2 周，任务成功率不退化。
- 工期：2 周（闭环 1 周 + LLM eval 1 周）。

### 总工期估计（v0.2 锁定）

| Phase | 工期 | 累计 |
|---|---|---|
| Phase -1 RuntimeContext | 1 周 | 1 周 |
| Phase 0 metadata 内嵌 + ScopePolicy | 1.5 周 | 2.5 周 |
| Phase 1 Injector 路由化 + Admin 解释面板 | 1 周 | 3.5 周 |
| Phase 2 eval 接入 + schema 升级 | 1.5 周 | 5 周 |
| Phase 3 embedding 治理 + 指标 | 1 周 | 6 周 |
| Phase 4 procedural 闭环 + LLM eval | 2 周 | 8 周 |

## 5. 需要新增的能力

### 5.1 MemoryQueryContext

至少包含：

- `user_id`
- `tenant_id`
- `workspace_id`
- `project_id`
- `repo_id`
- `session_id`
- `agent_name`
- `skill_name`
- `task_type`
- `current_files`
- `tool_intent`

### 5.2 Injection Explainability

每次注入要记录：

- 选中了哪些 memory
- 候选池有多少条
- 哪些被跳过
- 跳过原因：`cross_user / expired / low_confidence / conflict / low_relevance / budget`
- 最终注入片段

### 5.3 Memory Governance Dashboard

Admin 页面要能看到：

- 当前 policy
- 剪枝计划
- 过期/低置信/跨用户风险
- backlog 状态
- 记忆注入解释
- 生产监控指标：
  - embedding dropped / latency / backlog
  - hybrid fallback
  - vector-space mismatch
  - 最近趋势和原因分布

默认展示层是 Hive 自己的 React Admin，不是 Grafana；`hive_metrics` 仍保留为统一指标数据源，后续如需 Prometheus/OpenTelemetry/Grafana 只通过可插拔 writer/exporter 接入。

## 6. 不做什么

- 不重建 memory store。
- 不把 tags 升级成主分类体系。
- 不默认把所有记忆都做 embedding。
- 不静默混用不同向量空间。
- 不把 compaction summary 直接当高置信长期事实。
- 不为了“更聪明”扩大默认注入量。
- 不把 memory 核心包耦合到 Prometheus、Grafana 或 Admin UI。

## 7. 成功标准

- 记忆相关 golden tasks 通过率稳定提升。
- 无关记忆注入率持续下降。
- 跨用户泄漏率为 0。
- 记忆命中后任务成功率上升。
- memory 改动有完整 eval 和回滚记录。
- Admin 能解释每次注入和剪枝。

## 8. 风险

- 语义字段设计不稳定，后续迁移成本高。
- target 设计太粗会导致召回退化。
- embedding 默认打开会引入不可控行为。
- 没有 eval gate 时，记忆系统很容易变成“看起来更聪明，实际上更污染”。

## 9. 结论

记忆系统的顶级最佳实践，不是“有个 memory 表”，而是：

**target-aware routing + typed memory + governance + eval gate + explainability + rollback**

Hive 现在已经有底座，下一步重点不是重写，而是把“谁的记忆、什么记忆、何时可见、为何可见、错了怎么回滚”这五件事做成系统能力。

---

## 10. 实施状态记录（2026-05-09）

本轮多 agent 并行实施后的状态如下，按可验证事实记录，不把测试环境结论等同于生产稳定性。

### 已落地

- RuntimeContext 已独立成 `internal/memory/runtime_context.go`，并被 master/react/reflection/compaction/embedding 路径使用，避免 target 维度只剩 user_id。
- ScopePolicy 已独立成 `internal/memory/scope.go`，提供应用层 `Allow` 和 SQL 层 `SQLFilter`，跨用户/target/scope 过滤集中在 memory 包内。
- MemoryRecord 类型已扩展 `procedural` / `episodic`，metadata 中规范化 `target`、`kind`、`subject_type`。
- Hybrid 路径已通过 `BatchGet` 避免逐条 `Get` 写事务，并保留 FTS fallback。
- Postgres schema 已新增 `target_scope / target_id / visibility / memory_kind / subject_type` 一等列，Save/Update/embedding sync 双写，并加入 chunked `FOR UPDATE SKIP LOCKED` backfill。
- memory eval 已接入 `cmd/memory-eval` 与 `.github/workflows/memory-eval.yml`，白名单触发，不扩大到全仓 CI。
- eval fixture 当前为 45 条 required，覆盖 scope、hybrid、BatchGet、vector-space drift、schema metadata、cross-user feedback、tag-not-primary-route、min_score 等路径。
- Phase 3 指标抽象保持可插拔：`internal/memory/metrics.go` 定义 recorder，`internal/memoryobs` 适配 Hive `hive_metrics`，Admin API `GET /api/v1/admin/memory/metrics` 供 React Admin 面板展示。
- Admin Memory Governance 页面已展示生产监控、注入解释、scope/kind 过滤、vector-space dry-run、promotion candidates。
- Phase 4 已实现 promotion candidate → `memory_promotion` approval → apply procedural memory，并写入 promotion audit metadata。
- Nightly eval 已新增 `cmd/memory-nightly-eval`、`.github/workflows/memory-nightly-eval.yml` 和运维手册，输出 JSON/JUnit、success-rate delta、memory token ROI。

### 当前验证

- `env GOCACHE=/tmp/go-build go test ./... -count=1` 通过。
- `env GOCACHE=/tmp/go-build go run ./cmd/memory-eval` 通过，45/45 required。
- `env GOCACHE=/tmp/go-build go run ./cmd/memory-nightly-eval` 通过，`memory_token_roi=0.0119`。
- `cd frontend && npm test -- --run src/api/__tests__/node-client.test.ts` 通过。
- `cd frontend && npm run lint` 通过。
- `cd frontend && npm run build` 通过。

### 仍需生产证明

- 尚未在真实 Postgres `TEST_DATABASE_URL` 上跑 live migration/backfill 集成测试；当前覆盖是 SQL 生成与 Go 单测。
- Nightly eval 当前是确定性骨架，不是调用真实 LLM/replay 的 LLM-in-loop；“稳定运行 ≥2 周”必须等上线后观察。
- Promotion apply 已写入 procedural memory，但回滚/去重策略仍是基础版：已应用记录会因 promotion audit 不再生成候选，仍建议后续补 UI 级 applied history 和 rollback。
- Prometheus/Grafana 没有被引入默认路径；如后续需要外部观测，只能通过可插拔 exporter 接入，不应让 `internal/memory` 直接依赖外部监控系统。

---

## 11. CEO 审核记录（2026-05-09）

**审核结论：HOLD SCOPE — 计划方向正确，但需要补"怎么落地"。**

### 关键修订

1. 计划第 1 节自评有 2 处把已有能力当未做：eval harness 已存在（缺 CI 接入）；InjectionResult 解释能力已存在（缺 UI 展示）。修订后工作量更准确。
2. 新增 **Phase -1（RuntimeContext 打通）** 为前置必做项——否则 Phase 0/1 写入端拿不到数据。
3. Phase 0 锁定 **Approach C（metadata 内嵌 → 升一等列）**，避免在 ctx 数据空缺时硬定 schema。
4. Phase 2 合并 schema 升级，eval gate 采用 **白名单触发**（diff 命中 injector/extractor/governance_ops 才跑）。
5. Phase 3 列出 **4 个必须的生产指标**，封堵 `pg_store.go:84-100` 静默丢弃。
6. Phase 4 收下 LLM-in-loop eval（任务成功率 + token ROI），走 nightly 不入 PR CI。

### 蓝军盲点清单（计划须正面回应）

| # | 严重度 | 盲点 | 处置 |
|---|---|---|---|
| 1 | CRITICAL | Phase 2 误判工作量（harness 已存在） | 修订主要缺口表，eval 接入工作量重估 |
| 2 | HIGH | FeedbackInput 缺 SkillName/TaskType | Phase -1 解决 |
| 3 | HIGH | auth ctx 仅 user_id，9 维 scope 设计悬空 | Phase 0 限定到当下能拿的字段，不冒进 |
| 4 | MED | extractor 关键词硬编码，subject_type 提取无策略 | Phase 4 配 LLM 提取 + admin 标注 |
| 5 | MED | 跨用户检查 5 处分散 | Phase 0 收成单一 ScopePolicy |
| 6 | LOW | Phase 4 描述过时（已做 50%） | 修订为"扩 procedural 类型 + 接审批" |
| 7 | LOW | embedding 异步 goroutine 静默丢弃 | Phase 3 配 4 个指标 |

### 后续门禁

- Phase -1 完成前不允许动 schema。
- Phase 0 metadata 数据沉淀 < 2 周不允许 Phase 2 schema 升级。
- 任何阶段 memory-eval required pass rate < 100% 阻断合并。

---

## 12. Eng 审核记录（2026-05-09）

**审核结论：FULL_REVIEW，4 项 P0 + 3 项 P1，全部修复方案已并入计划。**

### 关键修订（已落地到 Phase -1 / 0 / 1 / 2 / 3 章节）

| # | 严重度 | 问题 | 修复落点 |
|---|---|---|---|
| Arch-1 | P0 | ScopePolicy 接口签名缺失 | Phase 0 锁定双方法 Allow + SQLFilter |
| Arch-2 | P0 | RuntimeContext 必须穿透 subagent + async embed | Phase -1 加数据流图 + ctx.Value 模式 |
| Arch-3 | P1 | Phase 2 backfill 单 UPDATE 锁住 Save | Phase 2 强制 chunked + SKIP LOCKED |
| Arch-4 | P2 | async embed 丢 RuntimeContext | Phase 3 工作项补充 |
| CQ-1 / Perf-1 | P0 | Hybrid N+1 写事务 | Phase 1 加 BatchGet API |
| CQ-2 | P1 | extractor 关键词硬编码 | Phase 0 改 table-driven |
| CQ-3 | P2 | Admin 缺 scope 过滤 | Phase 0 admin handler 补参数 |
| Perf-2 | P1 | 中文 ILIKE 全表扫 | Phase 3 加 pg_trgm 索引 |
| Test-1 | P0 | Phase 0 必备 8 条 ScopePolicy fixture | Phase 0 验收硬门 |
| Test-2 | P0 | fixtureMemoryStore 不模拟排序 | Phase 0 加 sort + score 字段 |
| Test-3 | P1 | hybrid vec_space 漂移 fixture 缺 | Phase 3 加 mc20 |

### Critical Failure Modes（必须有 test + handler + 显式日志）

- **F1 Hybrid N+1 写**：Phase 1 BatchGet 修复
- **F2 embedding 静默丢**：Phase 3 `memory_embedding_dropped_total` 指标 + Warn 升 Error
- **F5 跨用户去重误判**：Phase 0 ScopePolicy.Allow 在 isDuplicate 调用前过滤
- **F8 backfill 长事务**：Phase 2 chunked 强制

### 测试覆盖目标

- Phase 0 完成时：fixture 7 → 15+（含 8 条 ScopePolicy + score-aware 改造）
- Phase 1 完成时：fixture 15 → 22+（含 hybrid + BatchGet 集成）
- Phase 2 完成时：fixture 22 → 30+（含 schema 升级回归）
- Phase 3 完成时：32+（含向量空间漂移）
- required pass rate：100% 不可商量

### 工期最终估计（v0.2 锁定）

| Phase | 工期 | 累计 | 并行 lane |
|---|---|---|---|
| Phase -1 RuntimeContext | 1 周 | 1 周 | A |
| Phase 0 ScopePolicy + metadata + fixture 扩 | 1.5 周 | 2.5 周 | A（Lane B 前端可启动） |
| Phase 1 Injector 路由 + BatchGet + Admin 面板 | 1.5 周 | 4 周（Admin 并行 3 周完成） | A + B |
| Phase 2 eval CI + schema 升级 | 1.5 周 | 5.5 周 | A + C |
| Phase 3 embedding 治理 + 4 指标 + pg_trgm | 1 周 | 6.5 周 | A + D |
| Phase 4 procedural 闭环 + LLM eval | 2 周 | 8.5 周 | A |

并行后实际 8.5 周 → 7 周。
