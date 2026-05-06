# P3 Quality Workbench Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> Quality Workbench 已进入代码实施阶段。本文件记录当前最终能力、入口、验证和边界,不再保留旧的分期路线。

## 最终能力

Quality Workbench 当前是候选驱动的质量运营平面:

- 从 P0 `agentquality_candidates` 读取候选失败。
- 按默认或持久化 grouping rules 聚合失败 cluster。
- 创建/列出/取消/运行 replay job。
- 运行 batch eval,支持 candidates summary、可选 golden `cases_dir`、`manual/replay/full/incremental/shadow` kind 和 `case_results`。
- 生成 dashboard snapshot/series。
- 生成 weekly report markdown。
- 前端集中展示聚类、replay queue、batch eval、weekly report 和失败分布。

## 当前实现入口

- 领域包: `internal/qualityworkbench`
- API: `internal/api/admin_workbench_handlers.go`
- 路由: `/api/v1/admin/quality-workbench/*`
- PG 迁移: `agentquality_grouping_rules`, `qualityworkbench_replay_jobs`, `qualityworkbench_batch_eval_runs`, `qualityworkbench_weekly_reports`
- 前端: `frontend/src/pages/admin/qualityworkbench/QualityWorkbench.tsx`
- CLI: `cmd/quality-weekly-report`, `cmd/quality-batch-eval`

## API 现状

| Method | Path | 当前语义 |
|---|---|---|
| GET | `/api/v1/admin/quality-workbench/clusters` | 从 candidate store 读取并即时聚类 |
| POST | `/api/v1/admin/quality-workbench/clusters/recompute` | 重新计算当前查询窗口聚类并返回计数 |
| POST | `/api/v1/admin/quality-workbench/grouping-rules/preview` | 使用当前有效规则 preview,不写表 |
| GET | `/api/v1/admin/quality-workbench/grouping-rules` | 列出持久化 grouping rules |
| PUT | `/api/v1/admin/quality-workbench/grouping-rules/{id}` | upsert grouping rule |
| DELETE | `/api/v1/admin/quality-workbench/grouping-rules/{id}` | 删除 grouping rule |
| POST | `/api/v1/admin/quality-workbench/replays/fanout` | 计算 cluster fanout 批次计划,不创建 job |
| POST | `/api/v1/admin/quality-workbench/version-diff` | 对请求体 baseline/treatment case results 做离线 diff |
| POST | `/api/v1/admin/quality-workbench/replays` | 创建 replay job |
| GET | `/api/v1/admin/quality-workbench/replays` | 列出 replay jobs |
| GET | `/api/v1/admin/quality-workbench/replays/{id}` | 获取 replay job |
| POST | `/api/v1/admin/quality-workbench/replays/{id}/run` | 标记 running,跑 `ReplayRunner`,写入成功/失败结果 |
| POST | `/api/v1/admin/quality-workbench/replays/{id}/cancel` | 取消 queued/running job |
| POST | `/api/v1/admin/quality-workbench/batch-evals` | 创建 batch eval,支持 `manual/replay/full/incremental/shadow` kind |
| GET | `/api/v1/admin/quality-workbench/batch-evals` | 列出 batch eval runs |
| GET | `/api/v1/admin/quality-workbench/batch-evals/{id}` | 获取 batch eval run |
| GET | `/api/v1/admin/quality-workbench/dashboard/snapshot` | 基于 candidates/clusters 汇总当前窗口 |
| GET | `/api/v1/admin/quality-workbench/dashboard/series` | 按日生成候选状态/失败类型/验证结果 series |
| GET | `/api/v1/admin/quality-workbench/reports` | 列出 weekly reports |
| GET | `/api/v1/admin/quality-workbench/reports/{id}` | 获取 weekly report |
| GET | `/api/v1/admin/quality-workbench/reports/{id}/download` | 下载 markdown report |
| POST | `/api/v1/admin/quality-workbench/reports/generate` | 生成并保存 weekly report |

## 与 P0 的边界

P0 提供:

- `agentquality.CandidateRecord`
- `CandidateStore.ListCandidates/GetCandidate`
- `EvalRunner`, `GateRunner`
- Candidate 状态机,含 `promoted_verified` / `promoted_regressed`
- `BuildOptimizationSuggestions`

P3 当前提供:

- 工作台 API/前端入口。
- 基于 candidate 的 cluster/replay/batch/report。
- replay/batch 的 PG/memory stores。

## 明确边界

- `agentquality_grouping_rules` 持久化 CRUD 已落地；`grouping-rules/preview` 仍是只读预览,不会自动重写历史 cluster。
- 当前没有 `agentquality_version_metrics`、t-digest 存储或实时 version-matrix API。
- 当前 dashboard 不承诺 success/cost/latency 三联 t-digest 趋势,只展示当前实现中的候选/聚类/验证计数。
- 当前 batch eval 已接受 `manual/replay/full/incremental/shadow` kind,但 full/incremental/shadow 还没有差异化后台状态机。
- 当前 replay run 使用显式配置的 eval runner 验证 case；未配置 runner 时不会假成功,也不是执行真实 SessionReplay 录制。
- Admin endpoints 只在 `authEngine != nil` 时注册。

## 运行验证

```bash
go test ./internal/qualityworkbench -count=1
go test ./internal/api -run Workbench -count=1
go test ./cmd/quality-weekly-report ./cmd/quality-batch-eval -count=1
cd frontend && npm run build
```

## 分文件状态

- `P3-QUALITY-WORKBENCH-PART1-CLUSTER.md`: 聚类和 grouping preview。
- `P3-QUALITY-WORKBENCH-PART2-VERSION-DASHBOARD.md`: 当前 version-diff 和 dashboard 边界。
- `P3-QUALITY-WORKBENCH-PART3-REPLAY-BATCH.md`: replay/batch eval 当前实现。
- `P3-QUALITY-WORKBENCH-PART4-REPORT-FRONTEND.md`: report/frontend 当前实现。
