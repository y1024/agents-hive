# Hive Implementation Status

> **定位**: `FINAL-PLAN.md` 的代码落地索引。
>
> **当前口径**: 2026-04-30, branch `qa_up`。本目录只保留最终方案、当前实现、运行验证和待办边界。不再维护互相竞争的 v1/v2 方案或路线口吻。

## 能力版图

### Quality Workbench

当前已经进入代码实施阶段，入口集中在:

- 后端包: `internal/qualityworkbench`
- Admin API: `/api/v1/admin/quality-workbench/*`
- 前端: `/admin/quality-workbench`, `frontend/src/pages/admin/qualityworkbench/QualityWorkbench.tsx`
- CLI: `go run ./cmd/quality-weekly-report`, `go run ./cmd/quality-batch-eval`
- 测试: `go test ./internal/qualityworkbench ./internal/api -run Workbench`

已落地能力:

- 失败候选聚类: `AggregateClusters`, `DefaultGroupingRules`, grouping preview。
- Grouping rules: `agentquality_grouping_rules` PG 表、内存/PG store、GET/PUT/DELETE API 和前端最小保存/删除入口。
- Replay jobs: `qualityworkbench_replay_jobs` PG 表、内存 store、`POST /api/v1/admin/quality-workbench/replays/{id}/run` 真实执行入口。
- Batch eval: 支持手动触发、`manual/replay/full/incremental/shadow` kind 校验、读取 `cases_dir`、生成 summary/diff/case_results。
- Dashboard: 基于 candidates/clusters 的 snapshot/series。
- Weekly report: API/CLI 生成 markdown, 可落表,支持 `/reports/{id}/download` 下载 markdown。
- 前端工作台: 聚类、replay、batch eval、周报和失败类型分布集中展示。

明确边界:

- grouping rules 已有持久化 CRUD；preview 仍是只读预览,不会自动重写历史 cluster。
- version diff 当前是 `POST /api/v1/admin/quality-workbench/version-diff`, 输入 baseline/treatment case result 后计算差异,不是实时 version-matrix 表。
- dashboard 当前不是 cost/latency t-digest 看板,主要是候选状态、失败类型和验证结果计数。
- replay run 使用 `qualityworkbench.ReplayRunner` 加载 candidate/cluster case 并跑显式配置的 eval runner；未配置 runner 时不会假成功,不是重放真实线上会话动作。
- Admin 路由只在 `authEngine != nil` 时注册。未启用认证的 dev/test server 不会暴露这些 admin endpoint。

### Memory 生产治理

当前已经进入代码实施阶段，入口集中在:

- 后端包: `internal/memory`
- Admin API: `/api/v1/admin/memory/governance`, `/prune`, `/export`, `/import`, `/vector-space/plan`, `/backlog/stats`
- 前端: `/admin/memory-governance`, `frontend/src/pages/admin/MemoryGovernance.tsx`
- 持久化 policy: `memory_governance_policies`
- 测试: `go test ./internal/memory ./internal/api -run Memory`

已落地能力:

- governance metadata 编解码、注入过滤、skip 计数和质量事件接入。
- governance stats 和 prune plan, 删除默认 dry-run, 真实删除必须显式传 `dry_run=false`。
- persisted default policy, 无策略时回退 `min_confidence=0.5`。
- conflict governance: `conflict_governance.go` 支持 newest/versioned/weighted/manual 策略。
- import/export 带 strict user isolation。
- vector-space plan 支持 dry-run/apply, apply 会更新 memory metadata 并加入 embedding backlog。
- embedding backlog 已有 PG 表和 PG store；Server 有 Postgres 时使用 PG backlog,无 Postgres 时回退进程内 backlog。
- embedding backlog worker 已在 bootstrap 注册,支持 atomic claim、5 分钟 stale claim 回收、指数退避,成功时同步 embedding state/vector space。

明确边界:

- `/memory/backlog/stats` 在 Postgres 环境读取 PG backlog stats,非 Postgres/dev test 环境读取进程内 fallback。
- `/memory/vector-space/plan` 是计划/metadata 迁移入口,不是完整的生产向量重嵌入 CLI。
- Admin 前端当前是治理/prune 页面,文件名是 `MemoryGovernance.tsx`,不是旧文档中的 `MemoryManagement.tsx`。

### Multi-agent / ACP

当前已经进入代码实施阶段，入口集中在:

- 后端包: `internal/evaluation`, `internal/orchestration`, `internal/quota`, `internal/acpclient`, `internal/acpserver`
- CLI: `go run ./cmd/delegation-eval`
- 前端: `/admin/multi-agent`, `frontend/src/pages/admin/MultiAgentEcosystem.tsx`
- 测试: `go test ./internal/acpclient ./internal/acpserver ./internal/orchestration ./internal/quota ./internal/evaluation`

已落地能力:

- delegation eval decision summary。
- sequential/parallel/fanout-fanin orchestration 和 agent tree 构建。
- delegation circuit breaker, 覆盖 depth/total/concurrent/timeout。
- ACP client safe surface: workspace 内读文件、只创建新文件、拒绝路径逃逸和覆盖。
- ACP client terminal surface: 只允许只读白名单命令,输出可读取,危险命令拒绝。
- ACP client HTTP transport: `transport:"http"` 会真实 POST line-delimited JSON-RPC,使用 headers,并桥接 JSON/NDJSON/SSE 响应。
- ACP RequestPermission 对危险请求保守拒绝或选择 reject option。
- ACP SessionBridge 已有 user/token/TTL 绑定和 cleanup。
- ACP validation drift checker: 本地 validated-method fixture 覆盖 `initialize/session/new/session/prompt/session/cancel/session/request_permission`。

明确边界:

- ACP server-side handler 不是完整 spec 产品面。当前有 validation/session bridge/permission/quality tests 和 validated-method drift checker,但不是联网拉取上游 spec 的全量兼容认证。
- terminal 是非交互只读能力,没有 stdin/write terminal primitive。
- Multi-agent 页面是观测和边界面,不是自动扩张生态 marketplace。

### Auto Optimization

当前已经进入代码实施阶段，入口集中在:

- 后端包: `internal/agentquality`
- Admin API: `/api/v1/admin/optimization/*`
- 前端: `/admin/auto-optimization`, `frontend/src/pages/admin/AutoOptimization.tsx`
- PG 表: `agentquality_optimization_suggestions`, `optimization_eval_diffs`, `optimization_approvals`, `optimization_rollback_alerts`, `optimization_rollbacks`, `optimization_tool_descriptions`, `memory_governance_policies`, `optimization_rollouts`
- 测试: `go test ./internal/agentquality ./internal/api -run Optimization`

已落地能力:

- suggestion 生成: candidate 和 eval diff 都能生成 reviewable suggestion。
- eval diff: baseline/treatment offline run 对比,两侧 proportion p-value。
- approval: 独立 approval record, admin/lead 可审批, engineer 不能审批优化变更。
- 人工 apply: prompt、skill、tool description、memory governance policy。
- rollback: 已应用 suggestion 可按 suggestion id 恢复 previous value,并更新 rollout 状态。
- rollback alert: 可基于 eval diff 阈值生成告警记录。

明确边界:

- 系统不会自动 apply suggestion,不会自动写生产配置,不会自动 rollback。
- eval diff、approval、rollback alert/record 在无 Postgres 时回退进程内 store；Postgres 可用时已有 PG store 与迁移表。
- A/B report 是离线 eval diff markdown,不是线上灰度。

## 文档索引

- `P0-AGENT-QUALITY-CODE.md`: agent quality 基础能力和当前边界。
- `P1-FOUNDATIONS-CODE.md`: runtime policy、observability、dangerous operation、delegation trace、ACP bridge 的底座状态。
- `P2-MEMORY-CONTEXT-CODE.md`: memory/context 注入治理状态。
- `P2-MEMORY-PRODUCTION-CODE.md`: memory 生产治理状态。
- `P3-QUALITY-WORKBENCH-CODE.md` + PART1-4: Quality Workbench 当前实现和各切片入口。
- `P4-MULTI-AGENT-ACP-CODE.md`: Multi-agent / ACP 当前实现和边界。
- `P5-AUTO-OPTIMIZATION-CODE.md`: 自动优化闭环当前实现和安全红线。
- `PHASING.md`: 历史切片只作为上下文,不再作为待执行 roadmap。

## 统一验证

推荐最小验证集:

```bash
go test ./internal/agentquality ./internal/qualityworkbench ./internal/memory ./internal/acpclient ./internal/acpserver ./internal/orchestration ./internal/quota ./internal/evaluation ./internal/api -count=1
go test ./cmd/quality-weekly-report ./cmd/quality-batch-eval ./cmd/delegation-eval -count=1
cd frontend && npm test -- --run src/App.lazyRoutes.test.tsx src/pages/admin/QualityCandidates.test.ts
cd frontend && npm run build
```

完整回归仍以仓库根目录的 `go test ./... -v` 和 `frontend/npm run build` 为准。
