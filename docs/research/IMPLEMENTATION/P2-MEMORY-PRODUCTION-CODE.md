# P2 Memory Production Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> 本文件记录 memory 生产治理当前实现,不再保留旧的补丁计划和伪代码。

## 最终能力

Memory 生产治理已经进入代码实施阶段,能力面包括:

- governance stats 和 prune, 支持 dry-run 和显式执行删除。
- persisted default governance policy, 表为 `memory_governance_policies`。
- conflict governance, 支持 newest/versioned/weighted/manual。
- import/export, 支持 strict user isolation。
- vector-space plan, 支持 dry-run/apply/resume token。
- embedding backlog PG queue 和 worker, 支持 atomic claim、stale claim 回收、指数退避、成功后同步 embedding state/vector space。
- Admin 前端治理页, 展示风险计数、当前 policy 和剪枝计划。

## 当前实现入口

- Governance ops: `internal/memory/governance_ops.go`
- Conflict governance: `internal/memory/conflict_governance.go`
- Embedding backlog: `internal/memory/embedding_backlog.go`
- PG backlog store: `internal/memory/pg_embedding_backlog.go`
- Vector-space plan: `internal/memory/vector_space.go`
- Import/export: `internal/memory/import_export.go`
- Admin handlers: `internal/api/admin_memory_handlers.go`
- Bootstrap worker: `internal/bootstrap/server.go`
- 前端: `frontend/src/pages/admin/MemoryGovernance.tsx`
- API client: `frontend/src/api/node-client.ts`

## Admin API

| Method | Path | 当前语义 |
|---|---|---|
| GET | `/api/v1/admin/memory/governance` | 返回 total/missing/expired/low_confidence/cross_user_risk 和 policy |
| POST | `/api/v1/admin/memory/prune` | 默认 dry-run, `dry_run=false` 才真实删除 |
| GET | `/api/v1/admin/memory/export` | 导出 memory JSON, 可按 user_id 隔离 |
| POST | `/api/v1/admin/memory/import` | 导入 memory JSON, 可 reset ids |
| POST | `/api/v1/admin/memory/vector-space/plan` | 生成 vector-space migration plan, `apply` 或 `dry_run=false` 才写入 |
| GET | `/api/v1/admin/memory/backlog/stats` | Postgres 环境返回 PG backlog stats,无 Postgres 时返回进程内 fallback stats |

## 持久化现状

已持久化:

- `memory_governance_policies`
- `embedding_backlog`,含 `memory_id/user_id/content/vector_space/status/attempts/claim/next_run/error`。

运行语义:

- `POST /memory/vector-space/plan` 默认 dry-run,只有 `apply=true` 或 `dry_run=false` 才更新 memory metadata 并入队。
- PG queue 用 `FOR UPDATE SKIP LOCKED` 原子 claim；claimed job 超过 `EmbeddingBacklogClaimLease` 会被后续 worker 回收。
- Server 在 memory embedding 启用且 API key 可用时注册后台 worker；成功后写回 embedding BYTEA、vector index 和 `metadata.vector_space.embedding_state=ready`。
- 非 Postgres store 或测试 server 使用 `memory.NewInMemoryEmbeddingBacklog()` fallback。

## 与旧文档的命名修正

- 当前冲突文件是 `internal/memory/conflict_governance.go`,不是 `conflict.go`。
- 当前隔离能力在 import/export、injector 和 governance stats 中体现,没有 `isolation_assert.go` 文件。
- 当前前端文件是 `frontend/src/pages/admin/MemoryGovernance.tsx`,不是 `MemoryManagement.tsx`。
- 当前 PG backlog 迁移在 `internal/store/postgres_migrate.go`,不是独立 `postgres_migrate_backlog.go`。
- 当前没有 `cmd/memory-vector-space-migrate`。vector-space 入口是 Admin API `/memory/vector-space/plan`。

## 运行验证

```bash
go test ./internal/memory -run 'Governance|Conflict|EmbeddingBacklog|VectorSpace|Import|Export' -count=1
go test ./internal/api -run Memory -count=1
cd frontend && npm test -- --run src/api/__tests__/node-client.test.ts
```

## 待办

- 如需运营级 re-embedding queue,新增失败率/耗时指标、job 明细查询、手动重放恢复命令。
- 如需批量迁移 CLI,以现有 `PlanVectorSpaceMigration` 为核心新增 `cmd/memory-vector-space-migrate`,不要绕开 Admin API 的 dry-run/apply 语义。
- 如需前端管理 import/export/vector-space/backlog job 明细,扩展 `MemoryGovernance.tsx`,不要重新引入 `MemoryManagement.tsx` 命名。
