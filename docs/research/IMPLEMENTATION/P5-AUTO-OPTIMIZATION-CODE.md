# P5 Auto Optimization Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> P5 已进入代码实施阶段。本文件记录当前“建议生成 -> eval diff -> 审批 -> 人工 apply -> 人工 rollback”的最终安全方案。

## 安全红线

**禁止任何对生产配置的自动写入,包括正向 apply 和反向 rollback。**

当前实现遵守以下规则:

- suggestion 只生成 diff/草稿/提案。
- `approved` 只表示人工背书,不表示已上线。
- apply 必须由管理员显式调用 API。
- rollback 必须由管理员显式调用 API。
- rollback alert 只生成告警记录,不自动执行恢复。
- A/B report 是离线 eval diff markdown,不是线上灰度。

## 最终能力

当前能力面:

- Candidate suggestion: 从失败候选生成 prompt/tool/skill/context 相关建议。
- Eval diff suggestion: 从 baseline/treatment offline eval diff 生成 prompt/tool/skill suggestion。
- Suggestion store: 内存和 PG 实现。
- Approval record: admin/lead 可审批, engineer 不可审批优化变更。
- Eval diff: 对比 baseline/treatment case run,计算 success/cost/latency delta 和 two-sided p-value。
- Writable stores: prompt、skill、tool description、memory governance policy。
- Rollout audit: 记录人工 apply 后的 previous/applied value。
- Manual rollback: 按 suggestion id 恢复 previous value 并标记 `rolled_back`。
- Rollback alert: 根据 eval diff 阈值生成 open alert。
- Store fallback: 无 Postgres 时使用进程内 store,有 Postgres 时 suggestion/eval diff/approval/rollback/rollout 均接 PG store。
- Admin frontend: `/admin/auto-optimization`。

## 当前实现入口

- Suggestion model/generator: `internal/agentquality/suggestion.go`
- Suggestion store: `internal/agentquality/suggestion_store.go`, `pg_suggestion_store.go`
- Eval diff: `internal/agentquality/eval_diff.go`
- Eval diff store: `internal/agentquality/pg_evaldiff_store.go`
- Approval: `internal/agentquality/approval_record.go`, `pg_approval_store.go`
- Writable stores: `internal/agentquality/optimization_writable_store.go`
- Rollout audit: `internal/agentquality/rollout_store.go`
- Rollback alert/record: `internal/agentquality/rollback_alert.go`, `pg_rollback_store.go`
- API: `internal/api/admin_optimization_handlers.go`
- Frontend: `frontend/src/pages/admin/AutoOptimization.tsx`
- API client tests: `frontend/src/api/__tests__/node-client.test.ts`

## Admin API

| Method | Path | 当前语义 |
|---|---|---|
| GET | `/api/v1/admin/optimization/suggestions` | 列出 suggestions |
| POST | `/api/v1/admin/optimization/suggestions` | 从 candidate 生成 suggestions |
| POST | `/api/v1/admin/optimization/suggestions/{id}/approve` | 人工审批 |
| POST | `/api/v1/admin/optimization/suggestions/{id}/reject` | 人工拒绝 |
| POST | `/api/v1/admin/optimization/suggestions/{id}/apply` | 人工 apply |
| POST | `/api/v1/admin/optimization/suggestions/{id}/rollback` | 人工 rollback |
| POST | `/api/v1/admin/optimization/eval-diffs` | 计算 offline eval diff |
| GET | `/api/v1/admin/optimization/eval-diffs` | 列出 eval diffs,Postgres 可用时持久化 |
| GET | `/api/v1/admin/optimization/eval-diffs/{id}` | 获取 eval diff |
| POST | `/api/v1/admin/optimization/eval-diffs/suggestions` | 从 eval diff 生成 suggestions |
| POST | `/api/v1/admin/optimization/eval-diffs/{id}/report` | 生成 markdown report |
| GET | `/api/v1/admin/optimization/approvals` | 列出 approvals |
| POST | `/api/v1/admin/optimization/approvals` | 记录独立 approval |
| POST | `/api/v1/admin/optimization/rollback-alerts/evaluate` | 基于 eval diff 生成 rollback alert |
| GET | `/api/v1/admin/optimization/rollback-alerts` | 列出 rollback alerts |
| GET | `/api/v1/admin/optimization/rollbacks` | 列出 rollback records |

## 持久化现状

PG 已落地:

- `agentquality_optimization_suggestions`
- `optimization_eval_diffs`
- `optimization_approvals`
- `optimization_rollback_alerts`
- `optimization_rollbacks`
- `optimization_tool_descriptions`
- `memory_governance_policies`
- `optimization_rollouts`

进程内 fallback:

- 非 Postgres store 或测试 server 会使用 in-memory suggestion/eval diff/approval/rollback/rollout/writable stores。

注意: `agentquality_optimization_suggestions.source_eval_diff_id` 已在迁移中存在,用于 eval-diff suggestion 反查。

## Apply / Rollback 边界

当前可写 target:

- `prompt`: 调用 prompt store upsert,并 invalidates prompt loader cache。
- `skill_content`: 调用 skill store upsert。
- `tool_description`: 写 `optimization_tool_descriptions`。
- `memory_governance`: 写 `memory_governance_policies`。

当前不支持:

- ACP quota 自动优化。
- 线上灰度配置。
- 自动 rollout percentage。
- 自动 rollback worker。
- 写入 `internal/i18n/prompts/` 或 skills 文件目录。

## 当前测试

```bash
go test ./internal/agentquality -run 'Suggestion|Approval|EvalDiff|Rollout|Rollback|Optimization' -count=1
go test ./internal/api -run Optimization -count=1
cd frontend && npm test -- --run src/api/__tests__/node-client.test.ts
cd frontend && npm run build
```

代表性测试:

- `TestSuggestionGeneratorGenerateFromEvalDiffCreatesPromptToolAndSkillSuggestions`
- `TestComputeEvalDiffComparesGoldenCaseRuns`
- `TestApprovalPolicyAllowsLeadAndAdminButRejectsEngineer`
- `TestAdminOptimizationApplySuggestion_RejectsNonApprovedSuggestions`
- `TestAdminOptimizationApplySuggestion_ApprovedPromptUpsertsAndInvalidatesCache`
- `TestAdminOptimizationRollbackSuggestion_RestoresWritableTargets`
- `TestEvaluateRollbackAlertOnlyCreatesAlert`
- `TestPGApprovalStore_RecordAndListApprovals`
- `TestPGEvalDiffStore_UpsertGetAndList`
- `TestPGRollbackStore_RecordAlertAndRollback`

## 待办

- 如果要线上灰度,接 OpenFeature/GrowthBook/Unleash 等现成 feature flag 服务,不要把它塞进当前人工 apply/rollback 闭环。
