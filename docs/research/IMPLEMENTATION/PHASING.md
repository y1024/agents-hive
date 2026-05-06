# Implementation Phasing Closeout

> **当前口径**: 2026-04-30 `qa_up`。
>
> 本文件只解释历史切片为什么存在,不再表达新的 roadmap。执行入口以 `IMPLEMENTATION/README.md` 和各 P 文档的“当前实现/边界/待办”为准。

## 历史切片结果

### P3 Quality Workbench

原 3A/3B/3C/3D 切片已经收口为一个候选驱动的 Admin 工作台:

- 聚类: `internal/qualityworkbench/cluster.go`
- replay: `internal/qualityworkbench/replay.go`, `replay_runner.go`
- batch eval: `internal/qualityworkbench/batch_eval.go`
- dashboard/report: `dashboard.go`, `report.go`
- PG store: `qualityworkbench_replay_jobs`, `qualityworkbench_batch_eval_runs`, `qualityworkbench_weekly_reports`
- grouping rules: `agentquality_grouping_rules` PG CRUD/API/UI 最小入口
- 前端: `frontend/src/pages/admin/qualityworkbench/QualityWorkbench.tsx`

边界以当前实现为准:

- grouping rule 持久化 CRUD 已落地；复杂编辑器、审计和租户权限仍是运营增强。
- 没有 t-digest version-matrix 存储。
- replay 是 eval runner 验证,不是线上会话动作重放。
- batch eval 接受 manual/replay/full/incremental/shadow kind,但 full/incremental/shadow 还没有差异化后台状态机。

### P2 Memory Production

原 2P-A/2P-B 切片已经收口为治理、冲突、导入导出、vector-space plan 和 backlog worker:

- governance stats/prune: `internal/memory/governance_ops.go`
- conflict governance: `internal/memory/conflict_governance.go`
- import/export: `internal/memory/import_export.go`
- vector-space plan: `internal/memory/vector_space.go`
- embedding backlog worker: `internal/memory/embedding_backlog.go`
- PG backlog store: `internal/memory/pg_embedding_backlog.go`
- bootstrap worker: `internal/bootstrap/server.go`
- Admin: `/api/v1/admin/memory/*`

边界以当前实现为准:

- persisted default policy 已落在 `memory_governance_policies`。
- embedding backlog 已落 `embedding_backlog` PG 表,有进程内 fallback。
- Server 启动时在 embedding enabled 且 API key 可用时注册 backlog worker；worker 成功后同步 metadata/vector index。
- vector-space 迁移是 API plan/apply,不是旧文档中的独立 CLI。

### P4 Multi-agent / ACP

原 4-A/4-B 切片已经收口为“可评测、可观测、有边界”的最小生态:

- delegation eval: `cmd/delegation-eval`, `internal/evaluation/delegation.go`
- orchestration: `internal/orchestration/orchestrator.go`
- quota breaker: `internal/quota/circuit_breaker.go`
- ACP safe client surface: `internal/acpclient/client_impl.go`
- ACP session bridge: `internal/acpserver/session_bridge.go`
- ACP transport/drift: `internal/acpclient/transport.go`, `internal/acpserver/spec_drift.go`
- 前端: `frontend/src/pages/admin/MultiAgentEcosystem.tsx`

边界以当前实现为准:

- ACP terminal 是只读白名单命令,没有交互 stdin。
- safe write-file 只能创建 workspace 内新文件,拒绝覆盖和路径逃逸。
- SessionBridge 有 user/token/TTL 绑定；validation method fixture drift checker 已落地,但不是完整上游 spec 认证。

### P5 Auto Optimization

原“线上灰度 + 自动回滚”已经降级为当前最终安全方案:

- suggestion 生成和 store: `suggestion.go`, `suggestion_store.go`, `pg_suggestion_store.go`
- eval diff: `eval_diff.go`
- approval: `approval_record.go`, `pg_approval_store.go`
- writable stores: `optimization_writable_store.go`
- eval diff store: `pg_evaldiff_store.go`
- apply/rollback audit: `rollout_store.go`, `rollback_alert.go`, `pg_rollback_store.go`
- Admin: `/api/v1/admin/optimization/*`
- 前端: `frontend/src/pages/admin/AutoOptimization.tsx`

安全边界:

- suggestion 不自动应用。
- approved 不等于已上线。
- apply 和 rollback 都必须由管理员显式触发。
- rollback alert 只生成告警和记录,不自动执行恢复。

## 已关闭的历史前置

- Cross-plan event 字段统一: 当前权威字段是 `Event.Prompt`, `ToolDecision.Actual`, `Delegation.SpawnDepth`, `Attributes["skill"]` 和 `Attributes["error_message"]`。旧文档中的 `PromptRef` 字段名、`ToolDecision.Tool`、`Delegation.Depth` 不再作为实现名使用。
- Admin 路由注册门: `internal/api/routes.go` 中 admin endpoints 位于 `if s.authEngine != nil` 内,所有 Workbench/Memory/Optimization admin endpoint 都继承这个条件。

## 当前待办池

- 如果要把 grouping rules 做成完整产品化能力,补复杂 UI 编辑、审计和租户权限。
- 如果要做真正 version matrix,新增可持久化 artifact metrics,不要复用当前 `POST /version-diff` 的离线差异接口冒充实时矩阵。
- 如果要进一步运营化 embedding backlog,补失败率/耗时指标、重放恢复命令和前端 job 明细页。
- 如果要完整 ACP spec,补全量上游 spec fixture、server-side fs/terminal/HITL 端到端验证。
- 如果要线上灰度,接现成 feature flag 服务,不要在 P5 当前人工闭环里偷偷加自动写配置。
