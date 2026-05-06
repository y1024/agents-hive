# Runbook: Sessiontodo Observability

## Scope

适用于 Plan Runtime + sessiontodo 的 dashboard、alert 和事故排查。

入口：

- Admin API: `GET /api/v1/admin/sessiontodo/ops/snapshot?window_minutes=60`
- 数据源: `hive_metrics`, `hive_traces`, `hive_logs`
- 代码聚合: `internal/sessiontodo/ops_dashboard.go`

## Dashboard

核心面板：

- `todo_writes_total`: `hive_sessiontodo_writes_total`
- `todo_write_error_rate`: `status != ok / total`
- `plan_version_conflict_rate`: `hive_sessiontodo_version_conflicts_total / todo_writes_total`
- `snapshot_broadcast_errors`: `hive_todo_snapshot_broadcast_total{status!="ok"}`
- `plan_runtime_decisions`: `hive_plan_runtime_decisions_total{decision}`
- `plan_mode_gate_denied`: `hive_plan_mode_gate_denied_total`
- `todo_write_avg_latency_ms` / `todo_write_p95_latency_ms`: `todo_write.execute` span

## Alerts

默认阈值：

- `todo_write_error_rate_high`: `todo_write_error_rate > 5%`
- `plan_version_conflict_rate_high`: `plan_version_conflict_rate > 5%`
- `todo_snapshot_broadcast_failed`: 任意广播失败
- `plan_runtime_failed_decisions`: 任意 `decision=failed`
- `plan_mode_gate_denied_spike`: 24h 内 gate denied 超过 10 次，级别为 info，可按团队噪声关闭

## Troubleshooting Steps

按这个顺序排查，先看状态，再看事件，再看日志：

1. 确认功能开关和读路径可用。
   - `GET /api/v1/admin/sessiontodo/ops/snapshot?window_minutes=60`
   - 预期返回 200；如果是 404/503，先检查 `agent.plan_runtime.enabled` 和 `sessiontodo` 读器是否初始化。
2. 确认当前 session 的快照是否存在。
   - `GET /api/v1/sessions/{id}/todos`
   - 如果这里有数据但 UI 没显示，问题在 WebSocket/前端分发；如果这里也空，问题在写入/恢复链路。
3. 看最近的写入与决策指标。
   - 优先查 `hive_sessiontodo_writes_total`、`hive_sessiontodo_version_conflicts_total`、`hive_plan_runtime_decisions_total`
   - 冲突飙升通常意味着旧 snapshot 重试或并发写入
4. 看 `plan_mode audit` 和 `plan_runtime.decide_turn_completion`。
   - 如果 plan mode 期间工具被拦，先确认是不是写文件 / 取消类危险操作
   - 如果决策一直停在 `paused`，再看是否还有 pending todos
5. 看广播和重连。
   - `todo_snapshot` 丢失时，客户端应靠 GET snapshot 恢复
   - 如果慢消费者被剔除，检查 EventBus/WebSocket 是否仍健康
6. 看 resume / auto_continue。
   - 检查 `runtime_epoch` 是否一致
   - 检查 `ClaimResume` 是否成功、`turn_id` 是否已刷新
   - 自动续跑失败时先看预算 guard，再看 claim 和广播

## SQL

最近 1 小时写入错误率：

```sql
WITH m AS (
  SELECT labels->>'status' AS status, sum(value) AS v
  FROM hive_metrics
  WHERE name = 'hive_sessiontodo_writes_total'
    AND ts > now() - interval '1 hour'
  GROUP BY 1
)
SELECT
  coalesce((SELECT v FROM m WHERE status <> 'ok'), 0)
  / NULLIF((SELECT sum(v) FROM m), 0) AS error_rate;
```

最近 CAS 冲突：

```sql
SELECT ts, labels, value
FROM hive_metrics
WHERE name = 'hive_sessiontodo_version_conflicts_total'
ORDER BY ts DESC
LIMIT 20;
```

Plan Runtime 决策 trace：

```sql
SELECT ts, session_id, status, attributes
FROM hive_traces
WHERE operation = 'plan_runtime.decide_turn_completion'
ORDER BY ts DESC
LIMIT 20;
```

Plan mode 审计：

```sql
SELECT ts, session_id, attributes
FROM hive_logs
WHERE message = 'plan_mode audit'
ORDER BY ts DESC
LIMIT 20;
```

## Response

- `todo_write_error_rate_high`: 查最近错误日志，确认是 store 写失败、广播失败还是非 Master 调用；若是 DB 故障，先恢复 PG，再让 agent 基于最新 snapshot 重试。
- `plan_version_conflict_rate_high`: 通常是并发写或旧 snapshot 重试；检查是否存在多个 agent 同时调用 `todo_write`，必要时暂停并发任务。
- `todo_snapshot_broadcast_failed`: 检查 EventBus/WebSocket，客户端可通过 `GET /api/v1/sessions/{id}/todos` 恢复，不需要回滚 snapshot。
- `plan_runtime_failed_decisions`: 查对应 trace span 的 `error` attribute；若 store/broadcast 失败，先处理底层依赖，再由用户消息触发 resume。
- `plan_mode_gate_denied_spike`: 先看 `blocked_tool_name`，删除/写文件类拦截属于预期；如果频繁误拦 read-only 工具，再调整 `planModeAllowedTools`。

## Rollback

Plan Runtime 默认开启。若需要紧急回滚，可通过配置关闭：

```json
{
  "agent": {
    "plan_runtime": {
      "enabled": false
    }
  }
}
```

关闭后 session todo API 返回 disabled，已有 `hive_metrics/hive_traces/hive_logs` 只保留历史数据，不需要删除。
