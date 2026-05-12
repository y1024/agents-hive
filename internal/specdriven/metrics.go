package specdriven

// Package metrics 锚点文件：集中定义 Phase 2 所有生产 metric counter 的
// 名称与 label enum（task 12.11 / Sprint 2.3）。
//
// 为什么集中在这：
//   1. Prom cardinality 红线（MEMORY.md §4.1）要求 label 取自有限 enum；
//      分散定义会导致未来加 label 值时无人记得约束——集中常量 + AllowedXxxLabels
//      切片 + 单元测试 TestCASConflict_ScenarioLabelsIndependent / TestContinuationLabelEnum
//      做一一锁定。
//   2. 名称一旦发布就是外部契约（Prometheus scrape target / Grafana dashboard ID）。
//      集中放便于 review、rename 时扫描引用点。
//   3. CI 的 specdriven gate 验证这个文件的 enum 测试绿 + 对应 emit 点存在。

// Counter 名称常量。不要在业务代码里 hard-code 字符串，通过常量引用。
const (
	// MetricContinuationAskTotal 计 Resolve 返回 ASK 的次数，label=reason（Trigger）。
	// emit 位点（规划中）：session_loop_specdriven.go wire continuation.Resolve 后。
	MetricContinuationAskTotal = "specdriven.continuation_ask_total"

	// MetricContinuationResumeTotal 计 Resolve 返回 RESUME 的次数，label=trigger。
	MetricContinuationResumeTotal = "specdriven.continuation_resume_total"

	// MetricCASConflictTotal 计 SpecChangeStore 三路 CAS 冲突次数，label=scenario。
	// Codex R5-3 红线：duplicate_create / ghost_id / stale_rev 每条 case 都必须 emit。
	// emit 位点：internal/store/spec_store.go 的三路 switch。
	MetricCASConflictTotal = "specdriven.cas_conflict_total"

	// MetricPlanFallbackTotal 计 planner 触发 fallback 的次数，label=reason。
	// emit 位点（规划中，Sprint 3.3 Runner 落地时 wire）：
	// internal/specdriven/ingress/runner.go。
	MetricPlanFallbackTotal = "specdriven.plan_fallback_total"

	// MetricPlanOverbudgetTotal 计 planner 超 token_budget 次数，无 label。
	MetricPlanOverbudgetTotal = "specdriven.plan_overbudget_total"

	// MetricPlanTokenCostTotal 累计 planner token 开销（sum of tokens），无 label。
	MetricPlanTokenCostTotal = "specdriven.plan_token_cost_total"

	// MetricDualDiffTotal 计 mode=dual 分支的 spec vs legacy 决策差异次数。
	// label=differs，取值 "true" / "false"（字符串形态便于 Prom 聚合）。
	//   - differs=false：dual 下 spec 路径成功产出 plan（decision.Path==PathDual），
	//     operators 读这条认知到"spec 观点可用、legacy 仍为主响应"。
	//   - differs=true：dual 下 spec 路径失败/降级（decision.Path==PathLegacy），
	//     表示本轮 dual 实际退化成 legacy，operators 据此估算 dual rollout 健康度。
	// emit 位点：applySpecDrivenIntake mode=dual 分支（session_loop_specdriven.go）。
	// task 10.5（harden-spec-driven-phase2）。
	MetricDualDiffTotal = "specdriven.dual_diff_total"

	// MetricSpecFallbackTotal 计 mode=spec 分支的 primary-spec 失败触发 fallback 的次数。
	// label=reason，取值来自 PlanFallbackReason 白名单（schema_invalid / llm_timeout /
	// over_budget / unknown）。仅在 mode=spec AND specErr != nil 时 emit——
	// plan_fallback_total 覆盖所有 non-legacy mode，本 metric 专门隔离 primary-spec 失败，
	// operators 据此算 fallback rate（SLO ≤ 5% 见 docs/运维手册/spec-driven-rollout.md）。
	// emit 位点：applySpecDrivenIntake mode=spec 分支。
	// task 10.6（harden-spec-driven-phase2）。
	MetricSpecFallbackTotal = "specdriven.spec_fallback_total"

	// MetricPlanTotal 是 plan_fallback_total / spec_fallback_total 的 SLO 分母——
	// 计 spec runner 真正被调用的次数（mode=dual/spec 进入 runner 分支即 +1，无论成败）。
	// 没有 label——cardinality 红线（MEMORY.md §4.1）。
	// 无此 counter，runbook 里 "fallback 率 = plan_fallback_total / plan_total" 的 SLO 公式
	// 分母悬空（Round 5 P10/Codex 共识 G1）。
	// emit 位点：applySpecDrivenIntake mode!=legacy 分支进入 runner 调用前。
	MetricPlanTotal = "specdriven.plan_total"

	// MetricSpecChangeUpsertTotal 是 cas_conflict_total 的 SLO 分母——计 SpecChangeStore
	// 成功 commit 的 upsert 次数（不含 conflict 退出，不含其他 read-only 调用）。
	// 没有 label。
	// 无此 counter，"CAS 冲突率 = cas_conflict_total / spec_change_upsert_total" 同样悬空。
	// emit 位点：store.SpecChangeStore.UpsertWithCAS commit 后；通过 UpsertObserver 回调
	// 解耦 store ↔ specdriven（避免反向依赖，与 CASConflictObserver 同样模式）。
	MetricSpecChangeUpsertTotal = "specdriven.spec_change_upsert_total"

	// MetricSpecChangeStoreDisabled 启动时一次性 emit=1，标识本进程的 spec_change_store
	// 因 PG 缺席而禁用。Round 5 N3：避免"CAS counter 永远 0"被 operators 误读为"系统健康"。
	// 无 label。Prom 上面用 max_over_time 可看出哪些 instance 处于降级态。
	MetricSpecChangeStoreDisabled = "specdriven.spec_change_store_disabled"

	// MetricExecutionPathTotal 计每次 session ingress 实际走的执行路径，label=path。
	// Round 5 G2：之前 session_loop.go 直接 `_ = m.applySpecDrivenIntake(...)` 把
	// intake.Path 扔了，operators 无法确认 dual / spec mode 下路由是否真生效——只能
	// 通过 intake_decision_total 反推（间接证据）。本 counter 是 ingress 真路由的
	// 直接证据：Stage 2 promotion 后 `path="spec"` 占比应 >> `path="legacy"`，否则降级。
	// label values：legacy / dual / spec（与 intake.Path enum 1:1）。
	// emit 位点：session_loop.go ingress hook，每 session 入口 +1。
	MetricExecutionPathTotal = "specdriven.execution_path_total"
)

// CASConflictScenario 枚举三路 CAS 冲突场景。label 值取自本 enum，防 cardinality 漂移。
// 必须与 internal/store/spec_store.go 的 CAS switch 三路 case 一一对应（Codex R5-3）。
type CASConflictScenario string

const (
	// CASScenarioDuplicateCreate：create 时发现同 ID 已存在（exists=true, ExpectRevision=0）。
	CASScenarioDuplicateCreate CASConflictScenario = "duplicate_create"
	// CASScenarioGhostID：update 时目标 ID 不存在（exists=false, ExpectRevision>0）。
	CASScenarioGhostID CASConflictScenario = "ghost_id"
	// CASScenarioStaleRevision：update 时 ExpectRevision 与当前 revision 不符（exists=true, 但 rev mismatch）。
	CASScenarioStaleRevision CASConflictScenario = "stale_revision"
)

// AllowedCASConflictScenarios 是 CASConflictScenario 的白名单；metric label 校验用。
var AllowedCASConflictScenarios = []CASConflictScenario{
	CASScenarioDuplicateCreate,
	CASScenarioGhostID,
	CASScenarioStaleRevision,
}

// PlanFallbackReason 枚举 planner fallback 的 label。
type PlanFallbackReason string

const (
	// FallbackReasonSchemaInvalid：planner 返回 JSON 但 schema 校验失败。
	FallbackReasonSchemaInvalid PlanFallbackReason = "schema_invalid"
	// FallbackReasonLLMTimeout：planner LLM 超时。
	FallbackReasonLLMTimeout PlanFallbackReason = "llm_timeout"
	// FallbackReasonOverBudget：planner 超 token_budget 被降级。
	FallbackReasonOverBudget PlanFallbackReason = "over_budget"
	// FallbackReasonUnknown：兜底 label——未分类 error 统一入此。新增子类型时应
	// 提升为独立 enum；不要让 "unknown" 吞噬真实分布。
	FallbackReasonUnknown PlanFallbackReason = "unknown"
)

// AllowedPlanFallbackReasons 白名单；label enum 锁。
var AllowedPlanFallbackReasons = []PlanFallbackReason{
	FallbackReasonSchemaInvalid,
	FallbackReasonLLMTimeout,
	FallbackReasonOverBudget,
	FallbackReasonUnknown,
}

// DualDiffLabel 是 MetricDualDiffTotal 的 differs 标签取值 enum。
// 只有二值——"true"/"false"——但显式 enum 避免未来 label 漂移（例如有人加
// "unknown"/"n/a" 会立刻撞白名单测试）。
type DualDiffLabel string

const (
	// DualDiffAgree：dual 下 spec 路径成功产出 plan，operators 认为 spec 观点可用。
	DualDiffAgree DualDiffLabel = "false"
	// DualDiffDiffer：dual 下 spec 路径失败/降级，实际走了 legacy。
	DualDiffDiffer DualDiffLabel = "true"
)

// AllowedDualDiffLabels 白名单；MetricDualDiffTotal 的 label enum 锁。
var AllowedDualDiffLabels = []DualDiffLabel{
	DualDiffAgree,
	DualDiffDiffer,
}
