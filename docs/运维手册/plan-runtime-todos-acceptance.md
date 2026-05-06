# Plan Runtime 与 Session Todos 验收用例

## 目标

验证 `Agent-计划状态与Todos实时化重构计划.md` 的用户可见能力已经生效，而不是只验证 `1+1` 这种短问答：

- 短问答不受 Plan Runtime 干扰。
- 用户明确要求计划时，agent 能进入 plan mode，生成 session-scoped todos。
- todos 面板能实时显示 plan status、todo 状态和版本更新。
- 未完成计划不会被错误标记为 completed。
- 暂停后可以通过前端继续执行。
- plan mode 下危险或写入类工具被 gate 拦截，读上下文工具仍可用。
- WebSocket 缺帧后，刷新/REST snapshot 能恢复 todos。
- observability 能看到 sessiontodo 写入、plan runtime decision 和 gate 事件。

## 前置条件

- 使用本地或测试环境，建议使用真实 PostgreSQL；内存模式只能验功能，不能完整验 SQL 观测。
- 使用包含本计划实现的分支启动后端和前端。
- 配置中不要显式关闭 Plan Runtime。默认应开启；如需确认，可确保配置没有以下关闭项：

```json
{
  "agent": {
    "plan_runtime": {
      "enabled": false
    }
  }
}
```

- 浏览器打开 DevTools，观察 Network / WS Frames / Console。
- 后端日志可 grep；如要验 observability，准备 PG 查询权限。

启动建议：

```bash
go run ./cmd/server/main.go --config config.json --log-level=debug
cd frontend
npm run dev
```

## 用例 1：短问答保持轻量，不强制进入计划态

目的：确认 Plan Runtime 默认开启后，不把普通短问答复杂化。

步骤：

1. 新建一个聊天会话。
2. 发送：`1+1等于几`
3. 等待回复完成。
4. 不要求发送消息本身触发 `GET /api/v1/sessions/<session_id>/todos`。如需验证 todos 状态，任选一种方式：

```bash
curl -s http://localhost:8080/api/v1/sessions/<session_id>/todos | jq .
```

或刷新当前会话页，并在 Network 中观察页面进入时的 `GET /api/v1/sessions/<session_id>/todos`。

预期：

- AI 正常回复 `2`。
- 当前消息轮次不出现空白。
- Todos 面板不显示。
- 如果手动查询 REST snapshot，允许返回 404（没有 snapshot）或返回 `plan_status=none` 且 `todos=[]`；两者都表示短问答没有创建 active plan。
- 后端日志中本轮 Plan Runtime decision 应为 `completed`，`plan_status=none`。

失败判定：

- 短问答触发明显 plan/todo 面板。
- 回复空白或只显示用户消息。
- 输入框长期 loading。
- 没有 active plan 时把 REST 404 误判为失败。

## 用例 2：明确要求制定计划时，生成可见 session todos

目的：确认用户明确要求计划时，agent 使用 `enter_plan_mode` + `todo_write`，并把计划暴露到 UI。

步骤：

1. 新建会话。
2. 发送：

```text
请先制定一个执行计划，不要马上改代码。目标：分析当前项目的 README 和 docs 目录，列出 3 个需要验证的风险点。
```

3. 等待 agent 回复计划。
4. 观察右侧或移动端 todos 面板。
5. 调用 REST snapshot：

```bash
curl -s http://localhost:8080/api/v1/sessions/<session_id>/todos | jq .
```

预期：

- UI 出现 Todos 面板。
- snapshot `session_id` 等于当前会话。
- `plan_status` 为 `planning`、`awaiting_approval`、`executing` 或 `paused` 中的合理状态；不应是 `none`。
- `todos` 至少 2 条，内容与计划步骤相关。
- `plan_version >= 1`。
- WS Frames 中能看到 `todo_snapshot`；进入/退出计划态时能看到 `plan_mode_changed`。

失败判定：

- agent 只输出自然语言计划，但没有 todos snapshot。
- REST 返回 404/503，且配置未关闭 Plan Runtime。
- UI 不显示 todos，但 REST 有有效 snapshot。

## 用例 3：执行计划时 todo 状态实时推进

目的：确认执行过程中 todo 状态能从 `pending` / `in_progress` 变为 `completed`，且版本递增。

步骤：

1. 接用例 2 的会话。
2. 继续发送：

```text
开始按刚才的计划执行。每完成一个步骤都更新当前 todo 状态。
```

3. 观察 Todos 面板和 WS Frames。
4. 多次查询 REST snapshot：

```bash
curl -s http://localhost:8080/api/v1/sessions/<session_id>/todos \
  | jq '{status:.plan_status, version:.plan_version, todos:[.todos[] | {id, content, status, order}]}'
```

预期：

- 至少一个 todo 进入 `in_progress`。
- 已完成步骤变为 `completed`。
- `plan_version` 随 snapshot 更新递增。
- UI 中 todo 顺序稳定，按 `order` 展示。
- agent 回复和 todos 状态一致，不出现“口头说完成但 todo 仍 pending”的明显不一致。

失败判定：

- 所有 todo 一直停在 `pending`，但 agent 已声称执行完成。
- `plan_version` 不递增。
- UI 顺序跳动或重复显示 todo。

## 用例 4：未完成 todos 时，不允许错误完成计划

目的：确认 `finish_plan` 和 Plan Runtime Guard 不会把仍有 open todos 的计划标成 completed。

步骤：

1. 新建会话。
2. 发送：

```text
请制定一个两步计划：第一步检查 README，第二步检查 docs。现在只完成第一步，然后尝试结束计划。
```

3. 等待 agent 执行。
4. 查询 snapshot。

预期：

- 如果仍存在 `pending` 或 `in_progress` todo，`plan_status` 不应为 `completed`。
- 后端若 agent 调用了 `finish_plan`，应返回类似“仍有未完成 todo”的工具错误。
- UI 应保留未完成 todo，而不是隐藏面板。

失败判定：

- 有未完成 todo，但 `plan_status=completed`。
- 面板消失，导致用户看不到剩余任务。

## 用例 5：长任务中断后进入 paused，可手动继续

目的：验证长任务 checkpoint / resume 用户路径。

步骤：

1. 新建会话。
2. 发送一个会产生多步执行的任务，例如：

```text
请完整检查 README、docs/计划与路线、docs/运维手册，并形成 6 个检查项。执行时逐项更新 todo。
```

3. 在任务执行中途点击停止，或等待系统因预算/轮次限制进入 paused。
4. 观察 Todos 面板是否出现 paused 提示和“继续”按钮。
5. 点击“继续”。
6. 查询 REST snapshot。

预期：

- 停止或 checkpoint 后 `plan_status=paused`。
- snapshot 中包含 `runtime_epoch`。
- UI 显示继续按钮。
- 点击继续后，请求：

```text
POST /api/v1/sessions/<session_id>/todos/resume
```

返回 `200`，snapshot `plan_status=executing`。
- 后续 agent 从未完成 todo 继续，不从头重做全部事项。

失败判定：

- paused 状态没有继续入口。
- 点击继续返回 409 时 UI 没有可理解错误。
- 继续后重复执行已 completed 的 todo。

## 用例 6：Plan mode 下工具权限 gate 生效

目的：确认计划态只允许读上下文、提问、todo 写入和退出计划态，不允许直接写文件/执行危险动作。

步骤：

1. 新建会话。
2. 发送：

```text
进入计划模式，然后尝试直接修改 README.md，加一行“plan mode write test”。
```

3. 观察 agent 行为和后端日志。
4. 检查 README.md 是否被修改。

预期：

- agent 应先进入计划态，而不是直接写文件。
- `write_file` / `edit` / `bash` 等写入或危险工具应被 gate 拦截，除非用户明确审批且运行时允许。
- README.md 不应被修改。
- 后端日志或 observability 中应出现 `plan_mode audit` 或 `hive_plan_mode_gate_denied_total` 相关记录。

失败判定：

- plan mode 中直接修改了文件。
- 没有任何审批或拦截痕迹。
- 读上下文工具也被误拦，导致无法制定计划。

## 用例 7：WebSocket 断开后，刷新可通过 REST 恢复 todos

目的：确认 `todo_snapshot` 不作为 critical event 时，REST snapshot 兜底有效。

步骤：

1. 在一个已有 active todos 的会话中打开 DevTools。
2. Network request blocking 添加规则：`*/api/v1/ws*`。
3. 刷新页面。
4. 等待页面加载完成。
5. 观察 Todos 面板和 Network 中的 `GET /api/v1/sessions/<session_id>/todos`。

预期：

- 即使 WS 连接失败，页面仍通过 REST 拉到最新 snapshot。
- Todos 面板显示当前计划。
- 不出现“未知事件”或 Console error 风暴。

失败判定：

- 刷新后 todos 面板消失，但 REST 实际有 snapshot。
- 前端只能依赖 WS，无法从 REST 恢复。

验收后清理：

1. 删除 Network request blocking 规则。
2. 刷新页面，确认 WS 恢复。

## 用例 8：观测面板能反映 Plan Runtime 状态

目的：验证 dashboard / alert 数据源可用。

步骤：

1. 先执行用例 2、3、6，产生 todo 写入、decision 和 gate 拦截。
2. 调用 Admin snapshot：

```bash
curl -s 'http://localhost:8080/api/v1/admin/sessiontodo/ops/snapshot?window_minutes=60' | jq .
```

3. 如使用 PG，查询指标：

```sql
SELECT name, labels, sum(value) AS total
FROM hive_metrics
WHERE name IN (
  'hive_sessiontodo_writes_total',
  'hive_sessiontodo_version_conflicts_total',
  'hive_todo_snapshot_broadcast_total',
  'hive_plan_runtime_decisions_total',
  'hive_plan_mode_gate_denied_total'
)
  AND ts > now() - interval '1 hour'
GROUP BY name, labels
ORDER BY name, total DESC;
```

预期：

- `todo_writes_total` 大于 0。
- `plan_runtime_decisions` 有 completed / paused 等决策记录。
- 如果跑了用例 6，`plan_mode_gate_denied` 大于 0。
- alert 不应出现高错误率，除非你故意制造错误。

失败判定：

- 功能层有 todos，但 observability 全无数据。
- Admin snapshot 500。
- 错误率高但没有可定位 labels。

## 用例 9：显式关闭 Plan Runtime 时安全降级

目的：确认回滚开关有效，且关闭后不会暴露半残功能。

步骤：

1. 在测试环境配置：

```json
{
  "agent": {
    "plan_runtime": {
      "enabled": false
    }
  }
}
```

2. 重启后端。
3. 新建会话并发送：

```text
请先制定计划，并用 todos 跟踪。
```

4. 查询：

```bash
curl -i http://localhost:8080/api/v1/sessions/<session_id>/todos
```

预期：

- `/todos` 返回 404，错误含 `session todos feature disabled`。
- prompt 中不应包含 `enter_plan_mode` / `todo_write` / `finish_plan` 行为指导。
- 前端不显示 Todos 面板。
- 普通聊天仍可用。

失败判定：

- 关闭后仍暴露 todo 工具或 todos API 正常返回 active snapshot。
- 关闭后普通聊天失败。

## 自动化回归命令

Go 后端：

```bash
go test ./internal/sessiontodo ./internal/tools ./internal/master ./internal/api ./internal/config ./internal/bootstrap -count=1
```

前端：

```bash
cd frontend
npm test -- --run \
  src/store/__tests__/todos.test.ts \
  src/api/__tests__/node-client.test.ts \
  src/hooks/__tests__/useWebSocketConnection.urlBuilding.test.ts \
  src/hooks/__tests__/useWebSocket.reconnect.test.tsx \
  src/hooks/__tests__/useWebSocket.handleDisconnected.test.ts
npm run build
```

如有 PG 测试库：

```bash
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/sessiontodo -run TestPG -count=1
```

## 验收结论模板

```text
环境：
分支 / commit：
配置：PlanRuntime.Enabled =
存储：PostgreSQL / Memory
模型：
浏览器：

用例 1 短问答：
用例 2 显式计划生成 todos：
用例 3 todo 状态实时推进：
用例 4 未完成不允许完成：
用例 5 paused + resume：
用例 6 plan mode gate：
用例 7 WS 断开 REST 恢复：
用例 8 observability：
用例 9 关闭降级：

自动化回归命令：
遗留问题：
```
