# Agent Plan Runtime 与 Todos 实时化重构计划

> 日期：2026-04-30  
> 状态：IMPLEMENTED，2026-05-03 按代码实现同步状态  
> 触发背景：当前 ReAct loop 将“LLM 本轮无 tool call 且 stop/end_turn”视为任务完成；这对短任务成立，但无法可靠承载超长计划、当前 session 临时 todos、计划态/执行态切换和用户可见实时进度。

> **Status semantics:** 本文已从实施计划同步为状态文档。`- [x]` 表示代码已落地并有对应测试/验证；`- [ ]` 表示仍需实现、补测试或重新做产品决策；被后续决策替代的旧要求会在条目中显式说明。

**Goal:** 把“LLM turn 完成”和“session plan 完成”解耦，新增 session-scoped todos、实时 todo snapshot 推送、计划态工具控制和受控完成语义，让长任务可以 checkpoint / resume / 用户可见。

**Architecture:** 保留现有 `internal/master` ReAct loop、EventBus、WebSocket、HITL、tool execution 主链路。在 ReAct loop 外层新增 Plan Runtime Guard；在工具层新增 `todo_write` / `finish_plan` / `enter_plan_mode` / `exit_plan_mode`；在存储层新增 `internal/sessiontodo`，用 snapshot 事件而不是早期 delta 事件降低一致性风险。

**Tech Stack:** Go backend、PostgreSQL/内存双 Store、现有 `master.BroadcastMessage` + `EventBus` + `streaming.WSHandler`、React + Zustand 前端。

---

## 0. 核心判断

### 0.1 当前退出条件不是错，但层级不够

当前 `runReActLoop` 的退出语义可以保留：

```text
len(resp.ToolCalls) == 0 && finish_reason in {stop, end_turn}
=> 当前 LLM turn 结束
```

问题在于它同时被当成：

```text
当前 session task 完成
```

这对短问答、短编辑任务可接受；对“先计划、再执行、可能跨很多轮”的长任务不够。长任务完成应由外部结构化状态判定，而不是由模型本轮是否继续发 tool call 判定。

### 0.2 新语义

```text
LLM turn completion != plan completion
plan completion derives from session plan/todo state
```

本计划新增一层 Plan Runtime：

```text
none -> planning -> awaiting_approval -> executing -> paused/checkpointed -> completed/failed
```

ReAct loop 本轮结束后，Plan Runtime Guard 再决定 session task 的终态：

| 条件 | task 状态 |
|---|---|
| 无 active plan | 沿用现有 completed |
| 有 active plan，todos 全部 completed/cancelled | completed |
| 有 active plan，仍有 pending/in_progress | paused/checkpointed，不标 completed |
| budget/turn limit 触顶但 todos 未完成 | paused/checkpointed，并广播可继续状态 |

---

## 1. 不做什么

- 不重写 ReAct loop。
- 不引入 LangGraph 或静态 DAG。
- 不把 `taskboard` 直接当作 `TodoWriteTool` 暴露给模型。
- 不在第一版做复杂 delta event、事件补偿日志或多副本 EventBus 外置化。
- 不让 agent 自己调用“取消任务”类工具；取消仍是用户/系统控制面能力。
- 不做全自动长任务调度器；第一版只做状态锚、实时可见和完成判定修正。

---

## 2. 为什么不直接用 taskboard

`internal/taskboard` 是长期工作项系统，字段和权限语义适合跨 session 任务管理：`assignee`、`priority`、`tags`、`parent_id`、CRUD。

本计划要做的是当前 session 的临时执行计划：

- 高频更新。
- 低风险，不应每次触发人工审批。
- 需要完整 snapshot 替换。
- 需要 `plan_version` 和实时 UI 推送。
- 生命周期绑定 session，可随 session 清理。

因此新增 `internal/sessiontodo`。后续如需把当前计划保存为长期任务，可单独做显式动作：

```text
promote_todos_to_taskboard
```

---

## 3. 目标架构

### 3.1 后端模块

| 模块 | 职责 |
|---|---|
| `internal/sessiontodo` | session 级 todos 和 plan state 的内存/PG 存储接口 |
| `internal/tools/todo_write.go` | agent 写入当前 session todo snapshot |
| `internal/tools/plan_mode.go` | `enter_plan_mode` / `exit_plan_mode` / `finish_plan` 工具 |
| `internal/master/plan_runtime.go` | Plan Runtime Guard，负责完成判定和状态广播 |
| `internal/master/master.go` | 新增 todo/plan event type |
| `internal/api/session_todos_handlers.go` | todo snapshot 查询 |
| `internal/api/routes.go` | 注册 todo snapshot API 路由 |
| `internal/store/postgres_migrate.go` | 新增 `hive_session_todos` 独立表迁移 |
| `internal/bootstrap/server.go` | plan runtime feature flag 下的 Store / 工具注册 wiring |
| `internal/streaming/websocket.go` | 复用现有 session scoped WebSocket，不新增协议层 |

### 3.2 前端模块

| 模块 | 职责 |
|---|---|
| `frontend/src/store/todos.ts` | Zustand store，按 `plan_version` 接收 snapshot |
| `frontend/src/components/todos/TodosList.tsx` | 当前 session todos 列表 |
| `frontend/src/components/todos/TodoItem.tsx` | 单条 todo 状态只读展示 |
| `frontend/src/api/node-client.ts` | 新增 todos snapshot API |
| 当前 session 页面 | 嵌入 TodosList，显示 plan mode / paused 状态 |

---

## 4. 数据模型

### 4.1 Todo status

```go
type TodoStatus string

const (
    TodoStatusPending    TodoStatus = "pending"
    TodoStatusInProgress TodoStatus = "in_progress"
    TodoStatusCompleted  TodoStatus = "completed"
    TodoStatusCancelled  TodoStatus = "cancelled"
)
```

第一版不引入 `blocked`，避免与“执行失败”和“等待用户输入”混淆。后续如 UI 需要可扩展。

### 4.2 Plan status

```go
type PlanStatus string

const (
    PlanStatusNone             PlanStatus = "none"
    PlanStatusPlanning         PlanStatus = "planning"
    PlanStatusAwaitingApproval PlanStatus = "awaiting_approval"
    PlanStatusExecuting        PlanStatus = "executing"
    PlanStatusPaused           PlanStatus = "paused"
    PlanStatusCompleted        PlanStatus = "completed"
    PlanStatusFailed           PlanStatus = "failed"
)
```

### 4.3 Snapshot

```go
type Todo struct {
    ID        string     `json:"id"`
    SessionID string     `json:"session_id"`
    Content   string     `json:"content"`
    Status    TodoStatus `json:"status"`
    Order     int        `json:"order"`
    Version   int64      `json:"version"`
    TurnID       string `json:"turn_id,omitempty"`
    RuntimeEpoch string `json:"runtime_epoch,omitempty"`
    CreatedAt time.Time  `json:"created_at"`
    UpdatedAt time.Time  `json:"updated_at"`
}

type Snapshot struct {
    SessionID   string     `json:"session_id"`
    PlanStatus  PlanStatus `json:"plan_status"`
    PlanVersion int64      `json:"plan_version"`
    Todos       []Todo     `json:"todos"`
    Source      string     `json:"source,omitempty"`
    TraceID     string     `json:"trace_id,omitempty"`
    SpanID      string     `json:"span_id,omitempty"`
    TurnID      string     `json:"turn_id,omitempty"`
    RuntimeEpoch string    `json:"runtime_epoch,omitempty"`
    SourceToolCallID string `json:"source_tool_call_id,omitempty"`
    SourceChangeID   string `json:"source_change_id,omitempty"`
    SourceRevision   int64  `json:"source_revision,omitempty"`
    UpdatedAt   time.Time  `json:"updated_at"`
}
```

`source` / trace / source 字段用于标记本次 snapshot 来源和排障关联。本期复用现有 `observability` trace，不新建 trace 系统：

- `trace_id` / `span_id` 指向当前 `todo_write` 或 Master 汇总写入所属 trace/span。
- `source_tool_call_id` 指向 LLM tool call id，和 span id 不是同一个概念。
- `turn_id` 当前采用 `sessionTraceID` 语义，不另建全局 turn 概念。
- `runtime_epoch` 用于 resume / auto_continue 的并发边界校验。
- `source_change_id` / `source_revision` 已可承载 spec 投影来源；基础 `SyncFromSpec` 已存在，自动 intake hook 与产品闭环仍是 follow-up。

Snapshot 顶层运行字段状态：

| 字段 | 当前状态 |
|---|---|
| `turn_id` | 已落地到 `sessiontodo.Snapshot` / `sessiontodo.Todo` / `sessiontodo.TodoInput` / PG / API / `toolctx.ToolContext`；语义为当前用户输入或自动续跑驱动的 `sessionTraceID` |
| `runtime_epoch` | 已落地到 Snapshot/Todo/PG/API，并由 `Store.ClaimResume` 校验 `plan_version + runtime_epoch + paused`，用于 resume / auto_continue 并发边界 |
| `source_change_id` | 已落地到 Snapshot/Todo/PG/API/UI，用于标记 `spec_projected` 来源 change |
| `source_revision` | 已落地到 Snapshot/Todo/PG/API/UI，用于标记 `spec_projected` 来源 revision |

当前一致性策略：普通写入依赖 PG 中 per-session 单调递增的 `plan_version` CAS；resume / auto_continue 额外依赖 `runtime_epoch` + paused 状态 claim。多实例接管策略仍作为后续增强。

### 4.4 Store interface

```go
type Store interface {
    Replace(ctx context.Context, sessionID string, expectedPlanVersion int64, todos []TodoInput) (Snapshot, error)
    Snapshot(ctx context.Context, sessionID string) (Snapshot, error)
    SetPlanStatus(ctx context.Context, sessionID string, status PlanStatus) (Snapshot, error)
    SetPlanStatusWithMeta(ctx context.Context, sessionID string, status PlanStatus, meta SnapshotMeta) (Snapshot, error)
    ClaimResume(ctx context.Context, sessionID string, expectedPlanVersion int64, expectedRuntimeEpoch, runtimeEpoch, turnID string) (Snapshot, error)
    Clear(ctx context.Context, sessionID string) error
}
```

`sessionID` 必须来自后端上下文，不能信任模型输入。

`Replace` 必须使用 `expectedPlanVersion` 做 CAS。初始写入传 `0`；已有 snapshot 时传当前 `plan_version`。冲突返回导出错误 `ErrPlanVersionConflict`，错误体包含 `Expected int64` 与 `Got int64`，供工具结果和日志统一识别。

### 4.5 PostgreSQL schema

本期使用独立表，不复用 `taskboard` 表，也不塞进 session metadata JSON。

```sql
CREATE TABLE IF NOT EXISTS hive_session_todos (
    session_id       TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    todo_id          TEXT NOT NULL,
    content          TEXT NOT NULL,
    status           TEXT NOT NULL,
    order_index      INTEGER NOT NULL,
    version          BIGINT NOT NULL DEFAULT 1,
    plan_version     BIGINT NOT NULL,
    plan_status      TEXT NOT NULL DEFAULT 'none',
    source           TEXT NOT NULL DEFAULT 'agent',
    trace_id         TEXT,
    span_id          TEXT,
    turn_id          TEXT,
    runtime_epoch    TEXT,
    source_change_id TEXT,
    source_revision  BIGINT,
    source_tool_call_id TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, todo_id)
);

CREATE INDEX IF NOT EXISTS idx_hive_session_todos_session_plan
    ON hive_session_todos(session_id, plan_version);

CREATE INDEX IF NOT EXISTS idx_hive_session_todos_source_change
    ON hive_session_todos(source_change_id);

CREATE INDEX IF NOT EXISTS idx_hive_session_todos_trace
    ON hive_session_todos(trace_id);
```

`plan_version` 是 per-session 单调递增，不是全局序列。

---

## 5. Event 设计

第一版只推完整 snapshot：

```go
const (
    EventTypeTodoSnapshot    = "todo_snapshot"
    EventTypePlanModeChanged = "plan_mode_changed"
)
```

`todo_snapshot` payload 即 `sessiontodo.Snapshot`。

选择 snapshot 而不是 delta 的原因：

- 断线重连简单。
- 乱序处理简单。
- UI 整表替换不容易出现 ghost todo。
- 每个 session todos 数量通常很小，带宽可接受。

EventBus 处理：

- `todo_snapshot` 不进入 `isCriticalEvent`。它是可恢复状态，慢消费者丢失后通过 `GET /api/v1/sessions/{id}/todos` 拉快照兜底。
- `plan_mode_changed` 进入 `isCriticalEvent`。它改变用户对当前任务阶段的理解，不能像 token partial 一样静默丢弃。

---

## 6. 工具设计

### 6.1 `todo_write`

用途：让 agent 写入当前 session 的完整临时计划。

输入：

```json
{
  "expected_plan_version": 3,
  "todos": [
    {"id": "read-context", "content": "阅读现有 EventBus 和 WebSocket 实现", "status": "completed"},
    {"id": "design-schema", "content": "设计 session todo schema", "status": "in_progress"}
  ]
}
```

约束：

- 不允许输入 `session_id`。
- 必须带 `expected_plan_version`；初次写入为 `0`，后续写入为当前 snapshot 的 `plan_version`。
- `id` 为空时后端生成稳定 ID。
- `content` 不能为空。
- `status` 只允许 `pending/in_progress/completed/cancelled`。
- 单次最多 100 条 todo。
- 写入成功后广播 `todo_snapshot`。
- CAS 冲突时返回 tool error，要求模型重新读取/等待最新 snapshot 后再写。

### 6.2 `enter_plan_mode`

用途：进入规划态。

行为：

- `plan_status = planning`。
- 广播 `plan_mode_changed` 和最新 `todo_snapshot`。
- 工具可见性收窄：允许只读上下文工具、`question`、`todo_write`、`exit_plan_mode`。
- 禁止 `write_file/edit/apply_patch/bash/taskboard/send_im_message` 等有副作用工具。
- `PlanMode` 状态保存在 `SessionState`。允许工具列表不存 session 字段，统一由 `AllowedToolsForPlanMode()` 这类确定性函数返回，避免每个 session 出现不同白名单。
- 工具收窄必须同时在两层生效：下轮 prompt 的 `tool_visibility.go` 过滤，以及本 turn / nested tool execution 的执行层 gate。

### 6.3 `exit_plan_mode`

用途：退出规划态，进入待确认或执行态。

行为：

- 若配置要求用户确认：`plan_status = awaiting_approval`。
- 若不要求确认：`plan_status = executing`。
- 退出 planning 不等于完成任务。

### 6.4 `finish_plan`

用途：受控完成 active plan。

硬校验：

- 若存在 `pending` 或 `in_progress` todo，返回 tool error。
- 只有全部 todo 为 `completed/cancelled` 时，才能 `plan_status = completed`。

这避免模型只靠自然语言“完成了”结束长计划。

### 6.5 SubAgent 工具可见性

SubAgent 不可见也不可直接调用：

- `todo_write`
- `finish_plan`
- `enter_plan_mode`
- `exit_plan_mode`

SubAgent 只能通过任务结果向 Master 报告进度；父 session 的 todo snapshot 和 plan status 由 Master 统一更新。

---

## 7. 完成判定重构

### 7.1 当前行为

当前 `runReActLoop` 内部在无 tool call 且 finish reason 为终态时直接 `SendResponse(... Completed: true)`。

### 7.2 新行为

保留 ReAct turn 结束条件，但 task 完成由 Plan Runtime Guard 判定。Guard 位于 `session_loop.go` / `processTask` 外层，与 `applySpecDrivenIntake` 同层；`react_processor.go` 只表达“本轮 LLM turn 已结束”，不再独自决定整个 session task 的 completed 语义。

```go
func (g *PlanRuntimeGuard) DecideTurnCompletion(ctx context.Context, sessionID string, llmContent string) (CompletionDecision, error)
```

决策：

| Plan state | Todos state | Decision |
|---|---|---|
| none | N/A | completed |
| planning | any | paused |
| awaiting_approval | any | paused |
| executing | all completed/cancelled | completed |
| executing | any pending/in_progress | paused |
| paused | any pending/in_progress | paused |
| completed | all completed/cancelled | completed |
| failed | any | failed |

当前实现已支持受控 resume / auto_continue 扩展：

```text
auto_continue=true && budget_ok && pending_todos_exist
=> enqueue continuation task
```

实现约束：resume 必须走 `ClaimResume`，校验 `plan_version + runtime_epoch + paused`；auto_continue 计数按 `session_id:plan_version:runtime_epoch` 维度，并且只在 claim 成功后扣次数。恢复执行失败时必须回退到 `paused` 并广播恢复后的 snapshot。

`TaskResponse` 兼容策略：

```go
type TaskStatus string

const (
    TaskStatusCompleted TaskStatus = "completed"
    TaskStatusPaused    TaskStatus = "paused"
    TaskStatusFailed    TaskStatus = "failed"
)

type TaskResponse struct {
    Content   string `json:"content"`
    Status    string `json:"status,omitempty"`
    Completed bool   `json:"completed"`
    Error     string `json:"error,omitempty"`
    Exit      bool   `json:"exit"`
    Message   string `json:"message,omitempty"`
}
```

兼容规则：所有新构造点必须同时填 `Status` 和 `Completed`，其中 `Completed = (Status == "completed")`。旧 Web/IM 渲染继续读 `completed`，新 plan runtime 逻辑读 `status`。

paused 状态恢复规则：早期计划要求 message-driven resume 且不做按钮；后续产品决策已升级为显式 Resume 按钮 + 后端 guarded resume endpoint，同时保留用户消息继续执行和 auto_continue 扩展。UI paused banner 仍必须明示 `Send a message to continue`，但“不加按钮”已废弃。

---

## 8. 并发工具边界

### 8.1 `batch.parallel`

现有 `batch` 支持 `parallel=true`。本计划要求收紧：

- 只读工具可并发：`read_file`、`glob`、`grep`、`ls`、`websearch`、`webfetch`。
- 状态/写操作强制串行或拒绝并发：`todo_write`、`finish_plan`、`taskboard`、`write_file`、`edit`、`apply_patch`、`bash`、`send_im_message`。
- `batch` 执行子工具前必须调用统一执行层 gate，不能只依赖模型可见性过滤。plan mode 下即使模型通过 `batch` 间接请求写工具，也必须被拒绝。
- `parallel_dispatch` 当前不是直接子工具执行器，而是 Master 委派任务入口；plan mode 下 Master 直接调用会被执行层 gate 拒绝。已补回归测试锁死该边界；固定 Agent 直接经工具宿主调用时也复用 `NestedToolGate`，避免绕过 plan mode。

### 8.2 `parallel_dispatch`

保留现有 `parallel_dispatch`，但要与 todos 打通：

- 已完成：不允许 subagent 直接修改 parent session 的 plan status；`todo_write` / plan mode / handoff / promote 工具都要求 Master caller。
- 已决策：task group started / completed 不自动映射为 todo snapshot。`task_progress` 是运行进度流，session todos 是计划权威状态；自动映射会污染计划并增加 CAS 冲突。本期保持两条 UI/状态链路独立，后续如要沉淀必须由 Master 显式汇总后写入。

### 8.3 错误恢复与可观测性

第一版必须补齐最低限度的错误恢复和可观测性；内置 ops snapshot API、alert 聚合和 runbook 已完成，外部 Grafana/PagerDuty 模板不作为阻塞项。

| 场景 | 处理 |
|---|---|
| `todo_write` CAS 冲突 | 返回 `ErrPlanVersionConflict`，错误体包含 `Expected` / `Got`，要求模型基于最新 snapshot 重试 |
| WebSocket 丢失 `todo_snapshot` | `todo_snapshot` 非 critical，前端通过 `GET /api/v1/sessions/{id}/todos` 恢复 |
| 模型通过 `batch` 间接调用写工具 | 执行层 gate 拒绝，不只依赖 prompt 可见性 |
| subagent 完成事件与 Master 写入竞争 | Master 统一汇总并通过 `expected_plan_version` CAS 写入 |
| active plan 未完成但模型本轮直接回答 | Plan Runtime Guard 返回 paused/checkpointed，不标 completed |
| `finish_plan` 时仍有 pending/in_progress | tool error，不改变 plan status |
| feature flag 关闭 | 不实例化 Store，不注册相关工具；API 如存在则返回 feature disabled |
| 老客户端只读 `Completed` | 新构造点同时填 `Status` 和 `Completed`，保持兼容 |

最小指标：

- `hive_sessiontodo_writes_total{source,status}`
- `hive_sessiontodo_version_conflicts_total{source}`
- `hive_sessiontodo_plan_status_transitions_total{from,to}`
- `hive_plan_runtime_decisions_total{plan_status,decision}`
- `hive_plan_mode_gate_denied_total{tool_name,caller_type}`
- `hive_todo_snapshot_broadcast_total{status}`

metrics 仍遵守低基数原则，不把 `session_id`、`trace_id`、`span_id`、`user_id` 放入 metric labels。

Trace spans：

- `plan_runtime.decide_turn_completion`
- `sessiontodo.replace`
- `todo_write.execute`
- `plan_mode.enter`
- `plan_mode.exit`
- `finish_plan.execute`

Trace 接入方式：

- `executeTool` 在工具执行前生成 `toolSpanID`，而不是执行结束后才生成。
- `toolctx.ToolContext` 增加 `TraceID`、`SpanID`、`ParentSpanID`、`TurnID`、`ToolCallID`。
- `todo_write` 从 `toolctx` 读取 trace/span/turn/tool_call_id，写入 snapshot 并广播。
- Plan Runtime Guard 和 sessiontodo replace 写标准 span，挂到当前 session trace 下。

Structured logs：

- 状态变化、CAS 冲突、Guard 决策、gate 拒绝、broadcast 结果必须打结构化日志。
- 字段包含：`session_id`、`trace_id`、`span_id`、`turn_id`、`runtime_epoch`、`plan_version`、`expected_plan_version`、`plan_status`、`decision`、`source`、`source_tool_call_id`、`caller_type`、`tool_name`、`error_code`。

不做项：

- 不新增独立于 trace 的全局 turn 系统；当前 `turn_id` 采用 `sessionTraceID` 并已贯穿 toolctx、snapshot、audit 和 trace。
- 不接入 OpenTelemetry 全量链路。
- 不在本期交付外部 Grafana/PagerDuty 模板；内置 ops snapshot API、alert 聚合和 runbook 已完成。

---

## 9. 总结工具边界

新增 `create_handoff_summary`，但不放入第一阶段。

推荐输出结构：

```text
目标
已完成
关键决策
修改过的文件/模块
当前 todos 状态
剩余工作
风险/阻塞
```

它用于用户显式要求“总结/交接/保存当前状态”。内部 compaction summary 仍走 `internal/compaction`，不暴露为普通模型工具。

---

## 10. 实施阶段

## Phase 1：sessiontodo 存储和实时 snapshot

**Files:**

- Create: `internal/sessiontodo/sessiontodo.go`
- Create: `internal/sessiontodo/memory_store.go`
- Create: `internal/sessiontodo/memory_store_test.go`
- Create: `internal/sessiontodo/pg_store.go`
- Create: `internal/sessiontodo/pg_store_test.go`
- Modify: `internal/master/master.go`
- Modify: `internal/master/event_bus.go`
- Modify: `internal/toolctx/context.go`

**Tasks:**

- [x] 定义 `TodoStatus`、`PlanStatus`、`Todo`、`TodoInput`、`Snapshot`、`Store`。
- [x] 实现内存 Store，覆盖 Replace / Snapshot / SetPlanStatus / Clear。
- [x] 在 `internal/store/postgres_migrate.go` 新增 `hive_session_todos` 独立表，外键联到 `sessions(id) ON DELETE CASCADE`。
- [x] 实现 PG Store，并把内存 Store 仅作为测试/开发 fallback。
- [x] `Replace` 使用 `expected_plan_version` 做 CAS；成功后递增 `plan_version`，并重建 todo order。
- [x] 导出 `ErrPlanVersionConflict`，错误体包含 `Expected int64` 与 `Got int64`。
- [x] Store 内部不持锁广播；锁顺序固定为 `Store mutex < EventBus.mu`。
- [x] `toolctx.ToolContext` 增加 `TraceID`、`SpanID`、`ParentSpanID`、`ToolCallID`。
- [x] 新增 `EventTypeTodoSnapshot`、`EventTypePlanModeChanged`。
- [x] 仅把 `plan_mode_changed` 加入 critical event；`todo_snapshot` 不 critical。

**Tests:**

- [x] `go test ./internal/sessiontodo -count=1`
- [x] `go test ./internal/master -run TestEventBus -count=1`

**Acceptance:**

- 内存 Store 并发安全。
- `plan_version` 单调递增。
- 旧 `expected_plan_version` 的 `Replace` 必须失败。
- `todo_snapshot` 非 critical，但断线重连能通过 GET snapshot 恢复。

---

## Phase 2：`todo_write` 工具和后端 API

**Files:**

- Create: `internal/tools/todo_write.go`
- Create: `internal/tools/todo_write_test.go`
- Modify: `internal/tools/tools.go`
- Create: `internal/api/session_todos_handlers.go`
- Create: `internal/api/session_todos_handlers_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/routes.go`
- Modify: `internal/bootstrap/server.go`

**Tasks:**

- [x] 新增 `todo_write` 工具 schema。
- [x] 从 `toolctx.GetSessionID(ctx)` 获取 sessionID；空 sessionID 返回 tool error。
- [x] 调用 `sessiontodo.Store.Replace(ctx, sessionID, expectedPlanVersion, todos)`。
- [x] 从 `toolctx` 读取 `TraceID`、`SpanID`、`ToolCallID`，写入 snapshot 的 trace/source 字段。
- [x] 写入后广播 `todo_snapshot`。
- [x] 注册 `GET /api/v1/sessions/{id}/todos`。
- [x] 不注册用户 PATCH endpoint。本期 todos 由 agent/runtime 权威维护，前端只读。
- [x] `agent.plan_runtime.enabled=false` 时不实例化 Store、不注册 `todo_write` / plan mode 工具；API handler 如被注册则返回 feature disabled。
- [x] 写入、版本冲突、状态转换打 metrics 和结构化日志。

**Tests:**

- [x] `go test ./internal/tools -run TestTodoWrite -count=1`
- [x] `go test ./internal/api -run TestSessionTodos -count=1`

**Acceptance:**

- 模型不能通过参数写其他 session 的 todos。
- `todo_write` 成功后能从 snapshot API 读到同一版本。
- feature flag 关闭时不会产生 todo 事件，也不会向模型暴露相关工具。

---

## Phase 3：前端实时 Todos UI

**Files:**

- Create: `frontend/src/store/todos.ts`
- Create: `frontend/src/components/todos/TodosList.tsx`
- Create: `frontend/src/components/todos/TodoItem.tsx`
- Modify: `frontend/src/api/node-client.ts`
- Modify: 当前 session 详情/聊天页面文件
- Modify: 当前 WebSocket message 分发入口

**Tasks:**

- [x] 新增 `Todo`、`TodoSnapshot` TypeScript 类型。
- [x] `useTodosStore` 支持 `loadSnapshot(sessionId)`。
- [x] `useTodosStore` 支持 `applySnapshot(snapshot)`，忽略 `plan_version <= localVersion` 的旧事件。
- [x] WebSocket 收到 `todo_snapshot` 时调用 `applySnapshot`。
- [x] `TodosList` 显示当前 session todos。
- [x] `TodoItem` 只读展示状态和 content，不提供编辑、取消、拖拽。
- [x] 不依赖不存在的 `frontend/src/components/ui/Alert.tsx` / `Sheet.tsx`；当前使用本功能局部组件。

**Tests:**

- [x] `cd frontend && npm run lint`
- [x] `cd frontend && npm run build`

**Acceptance:**

- 进入 session 时先显示 API snapshot。
- agent 调用 `todo_write` 后 UI 1 秒内更新。
- WebSocket 重连后重新拉 snapshot 不丢状态。
- 旧版本事件不会覆盖新 UI 状态。

---

## Phase 4：Plan Mode 工具 + Plan Runtime Guard（同一 PR）

**Files:**

- Create: `internal/tools/plan_mode.go`
- Create: `internal/tools/plan_mode_test.go`
- Modify: `internal/tools/tools.go`
- Modify: `internal/master/tool_visibility.go`
- Modify: `internal/master/session.go`
- Modify: `internal/master/session_loop.go`
- Modify: `internal/master/react_processor.go`
- Create: `internal/master/plan_runtime.go`
- Create: `internal/master/plan_runtime_test.go`

**Tasks:**

- [x] 新增 `enter_plan_mode`。
- [x] 新增 `exit_plan_mode`。
- [x] 新增 `finish_plan`。
- [x] session 保存当前 plan status。
- [x] `SessionState` 增加 `PlanMode` / plan status 状态；允许工具列表通过统一函数计算，不存 per-session 白名单。
- [x] planning 模式收窄模型可见工具。
- [x] 新增执行层 gate，覆盖本 turn 直接工具调用、`batch` 嵌套调用和 `parallel_dispatch` 固定 Agent 委派边界；`parallel_dispatch` 直接调用在 plan mode 下被 master gate 拒绝。
- [x] `finish_plan` 校验 pending/in_progress todos。
- [x] 计划态变化广播 `plan_mode_changed` 和 `todo_snapshot`。
- [x] 新增 `TaskResponse.Status`，并按 `Completed = Status == "completed"` 兼容旧调用方。
- [x] `Plan Runtime Guard` 位于 `session_loop.go` 外层；paused 时不广播 completed，不设置 `TaskResponse.Completed=true`。
- [x] 调整 `session_loop.go` 的 completed 兜底 defer，避免 paused/checkpointed 被误广播成 completed。
- [x] `executeTool` 在工具执行前生成 tool span id，并把 trace/span/tool_call_id 注入 `toolctx`。
- [x] Plan Runtime Guard 写 `plan_runtime.decide` span。

**Tests:**

- [x] `go test ./internal/tools -run TestPlanMode -count=1`
- [x] `go test ./internal/master -run TestToolVisibility -count=1`
- [x] `go test ./internal/master -run TestPlanRuntime -count=1` — table-driven 覆盖 8 cells (none/planning/awaiting_approval/executing-done/executing-pending/paused/completed/failed) **+ 3 edge sub-tests**:
  - `TestPlanRuntime_FinishReasonLength` — `finish_reason=length` (token 截断) 当 paused 处理,不当 completed
  - `TestPlanRuntime_LLMError` — LLM 调用错误时返回 failed 不返回 paused
  - `TestPlanRuntime_NoActivePlanBackwardCompat` — 无 active plan 旧路径行为不变(REGRESSION 关键)

**Acceptance:**

- planning 模式下写文件、bash、apply_patch 不可见或不可执行。
- `finish_plan` 在 todos 未完成时失败。
- `exit_plan_mode` 不会把 plan 标 completed。
- 无 active plan 的旧任务行为不变。
- 有 active plan 且 todos 未完成时，模型直接回答不会导致 task completed。
- 有 active plan 且 todos 全完成时，正常 completed。
- `TaskResponse.Status` 与 `Completed` 兼容规则在旧 Web/IM 路径不回退。
- paused banner 明示 `Send a message to continue`；用户下一条消息进入同 session 后按现有 active todos 继续执行/判定。

---

## Phase 5：并发边界和 summary follow-up

**Files:**

- Modify: `internal/tools/batch.go`
- Modify: `internal/tools/batch_test.go`
- Modify: `internal/tools/parallel_dispatch.go`
- Modify: `internal/tools/parallel_dispatch_test.go`
- Optional Create: `internal/tools/handoff_summary.go`

**Tasks:**

- [x] 为工具注册增加 concurrency safety 分类，或在 `batch` 内用白名单先收紧。
- [x] `batch.parallel` 遇到写工具时返回错误或降级串行，优先选择返回错误。
- [x] `batch` 在执行子工具前调用执行层 gate，plan mode 限制递归生效。
- [x] 补 `parallel_dispatch` plan-mode 边界回归测试：确认 Master 直接调用被 gate 拒绝，固定 Agent 委派路径显式复用同一 gate。
- [x] `parallel_dispatch` 任务进度不自动映射到 todos；SubAgent 不直接写父 session todos 已由 Master-only plan/todo 工具约束覆盖。后续如需映射，必须另立 Master 汇总写入设计。
- [x] 设计并实现 `create_handoff_summary` 工具；不再作为 Phase 1-5 阻塞项。

**Tests:**

- [x] `go test ./internal/tools -run TestBatch -count=1`
- [x] `go test ./internal/tools -run TestParallelDispatch -count=1`

**Acceptance:**

- 并发 batch 不允许同时写 todos 或文件。
- 只读 batch 仍可并发。

---

## 11. 验收矩阵

| 场景 | 期望 |
|---|---|
| 普通问答，无 active plan | 行为与当前一致，正常 completed |
| agent 写入 todos | Web UI 实时显示当前计划 |
| active plan 未完成，模型直接回答“先到这里” | task 状态 paused/checkpointed，不标 completed |
| active plan 全完成，模型无 tool call 终态回答 | task completed |
| `finish_plan` 时仍有 pending todo | tool error，不改变 plan_status |
| planning 模式中模型尝试写文件 | 被工具可见性/执行层拒绝 |
| WebSocket 断线重连 | GET snapshot 恢复最新 todos |
| 旧 `expected_plan_version` 的 `todo_write` | tool error，提示重新读取最新 snapshot |
| 用户试图编辑 todo | 本期无入口，UI 只读 |
| paused 后用户发送下一条消息 | 触发 message-driven resume，不新建 plan，不误标 completed |

---

## 12. Claude Code 审核重点

请重点审核以下问题：

1. `Plan Runtime Guard` 应插在 `runReActLoop` 内部，还是应移动到 `processTask` / `executeSessionTask` 外层？
2. 第一版只推 `todo_snapshot` 是否足够，是否需要立即做 delta event？
3. `todo_snapshot` 是否应该进入 `isCriticalEvent`，还是依赖重连 snapshot 即可？
4. `finish_plan` 作为工具是否必要，还是应只做后端 guard？
5. `taskboard` 是否完全不应复用，还是可在 PG schema 层共享表结构？
6. planning 模式工具收窄应通过 `tool_visibility.go`、tool policy，还是在 tool execution 层二次拦截？
7. ReAct loop 当前 `SendResponse(... Completed: true)` 的修改点是否会破坏现有 Web/IM 终态渲染？
8. `batch.parallel` 写工具禁用策略是否会影响已有用例？

---

## 12.1 2026-05-05 实际剩余项

本节是当前文档的任务源。Phase 1-5 本次计划代码项已清零；剩余只保留明确 future follow-up，不作为当前计划未完成代码。

- [x] 补 `parallel_dispatch` plan-mode 边界回归测试：确认 Master 直接调用被 gate 拒绝，固定 Agent 委派路径显式复用同一 gate。
- [x] 决定是否实现 `parallel_dispatch` progress -> session todos 自动映射：本期不自动映射，保持 task progress 与 todos 两条链路独立。
- [x] UI 增强：Todos/Canvas 独立折叠、tablet rail、fade-in、aria-live 已落地；三断点由响应式实现覆盖，完整 e2e 仍作为非阻塞 QA 扩展。
- [ ] Future follow-up：若项目引入 Storybook，再补 Todos 五态 stories；当前仓库未发现 stories 基础设施。
- [ ] Future follow-up：自动 intake hook 与 spec→todos 产品闭环、多实例接管策略仍是后续能力，不属于本次计划未完成代码。

## 13. 迁移与灰度

- 新功能默认开启；如需回滚，可显式配置 `agent.plan_runtime.enabled=false`。
- `todo_write` 仅在 plan runtime 开启时暴露；默认开启意味着新部署默认具备 session todos / Plan Runtime 能力。
- Web UI 本期只读显示 todos，不做用户编辑入口。
- PG Store 本期交付；内存 Store 仅作测试/开发 fallback。
- 老 session 无 plan state 时按 `PlanStatusNone` 处理。
- **启动硬约束**:`bootstrap/server.go` 启动时若检测到 `agent.plan_runtime.enabled=true` 且 Store 实例为内存 Store(非 PG),必须返回 `ErrInvalidConfig` 终止启动。生产部署 flag-on 必须配 PG Store,否则 master 重启后 `expected_plan_version` CAS 永远冲突,长任务恢复路径静默炸。此约束在加入 generation epoch (ER-D6 follow-up) 后可移除。

---

## 14. 最小可交付切片

如果需要压缩范围，第一版只交付：

1. `internal/sessiontodo` 内存 Store。
2. `hive_session_todos` 独立 PG 表 + PG Store。
3. `todo_write` 工具。
4. `todo_snapshot` EventBus 推送。
5. `GET /api/v1/sessions/{id}/todos`。
6. 前端只读 TodosList。
7. Plan Mode 工具 + Plan Runtime Guard 同一 PR 上线：active todos 未完成时不标 completed。
8. 执行层 gate 覆盖 plan mode、本 turn、`batch` 和 `parallel_dispatch`。

暂缓：

- 用户 PATCH todos。
- spec 到 todos 自动投影已实现基础 `SyncFromSpec`，但尚未接入自动 intake hook；自动投影产品闭环仍暂缓。
- 自动续跑调度器已具备 guarded auto_continue 基础能力；生产调度策略仍暂缓。
- 内置 dashboard / alert / runbook 已完成；外部 Grafana/PagerDuty 模板不在本计划范围。
- generation epoch 已以 `runtime_epoch`/`ClaimResume` 形式覆盖 resume 并发边界；多实例接管仍需后续策略。
- turn_id 已采用 `sessionTraceID` 作为当前语义，不另建全局 turn 概念。
- handoff summary 已实现 `create_handoff_summary`。

这个切片已经能验证最核心假设：**模型一轮结束不等于计划完成，当前计划必须由 session todos 外化并实时可见。**


---

## 15. Review 输入处理清单(2026-04-30)

本节保留 CEO / Eng / Design review 的输入，并记录 2026-05-02 逐条讨论后的最终处理。主体 §0-§14 已改写确定执行范围；后续项只作为明确 follow-up，不作为当前 PR 阻塞项。

### 已并入主体的确定项

| ID | 改动点 | 影响章节 |
|---|---|---|
| D1 | sessiontodo、taskboard、specdriven 明确边界；sessiontodo 是当前 session 临时计划状态 | §2 §3 §4 |
| D4 | Plan Runtime Guard 上移到 `session_loop.go` / `processTask` 外层，react_processor 仅返回 turn 结束信号 | §7 Phase 4 |
| D5 | `finish_plan` 工具 + Plan Runtime Guard 双重(工具拒绝未完 todos,Guard 以 todos 状态推) | §6.4 |
| D6 | plan_mode 工具收窄走 tool_visibility(下轮)+ tool execution 二次拦截(本 turn)；判定源来自 SessionState 与统一 gate 函数，不写入 per-session 白名单 | §6.2 §8.1 |
| D7 | 复用并轻量增强现有 observability trace：tool execution 提前生成 tool span id，并把 trace/span/turn/tool_call_id 注入 `toolctx`；snapshot 保存 `trace_id` / `span_id` / `turn_id` / `source_tool_call_id`；`turn_id` 采用 `sessionTraceID`，不新增独立全局 turn 系统 | §4.3 §8.3 Phase 1+2+4 |
| D8 | Subagent 工具集**不可见** todo_write/finish_plan/plan_mode/exit_plan_mode | §6 §8.2 |
| D9 | Replace 加 `expected_plan_version` CAS;**保护 agent 自身 todo_write 之间 + master 代写 subagent 完成事件之间的并发**(D18 后不再涉及用户) | §4.4 §6.1 |
| D10 (1/2) | 本期实现最低可用观测：metrics、trace spans、structured logs；内置 ops snapshot API、alert 聚合与 runbook 已完成，外部 Grafana/PagerDuty 模板后置 | §8.3 |
| D13 | master 代写 subagent 完成事件路径也走 expected_plan_version CAS,与 D9 同机制 | §8.2 §6 |
| D15 | `todo_snapshot` **不**作 critical event,依赖 reconnect 拉 GET snapshot 兜底;只有 `plan_mode_changed` 进 critical | §5 |
| D16 (1/2) | `batch` 子工具入口调用统一执行层 gate；`parallel_dispatch` 当前不是子工具执行器，Master 直接调用在 plan mode 下被 gate 拒绝，固定 Agent 委派边界已复用同一 gate 并补回归测试 | §8.1 |
| D16 (2/2) | 旧决策已被替代：当前实现采用显式 Resume 按钮 + guarded resume endpoint，同时保留 `Send a message to continue` 文案与 auto_continue 扩展 | §7.2 §11 |
| ER-D5 | `SessionState` 只保存 `PlanMode` / `PlanStatus`，不保存 `PlanModeAllowedTools`；白名单由统一 gate 函数计算 | §6.2 Phase 4 |

### 必删:D18 决议永久不上线

| ID | 删除点 | 原计划章节 |
|---|---|---|
| D18 | `Store.Patch` 接口 | §4.4 |
| D18 | `Snapshot.expected_version` 概念用于"用户 PATCH"路径(保留用于 agent 并发写 D9 路径)| §4.4 |
| D18 | `PATCH /api/v1/sessions/{id}/todos/{todo_id}` 端点 | §10 Phase 2 |
| D18 | `TodoItem.tsx` 编辑/取消按钮 | §10 Phase 3 |
| D10 (2/2) | PATCH ownership middleware 不需要(没 PATCH 端点了)| §3.1 |

### 流程动作

| ID | 动作 |
|---|---|
| D11 | Phase 3 实现前已完成 `/plan-design-review`；确定只读 TodosList、paused 文案、五态 UI、响应式边界。`spec_projected` 视觉标记与跳转入口已落地，`source_change_id/source_revision` 作为追溯字段继续保留 |
| D17 | D1 双系统决策维持;outside voice 提出"specdriven 投影到 WS 替代 sessiontodo"作为**未来重考点**记入(条件:hidden-spec-layer 改为可见,或 specdriven planner 成本压到所有 session 可承担) |
| D12 | 已跑 Outside Voice(Claude subagent)、TODOS.md 已补齐 sessiontodo follow-up 清单 |

### 逐条讨论后的后续项，不进入本期

| ID | 后续项 | 当前处理 |
|---|---|---|
| D3 / D14 | spec→todos 自动投影，以及 `intake.SyncFromSpec` hook | 基础 `SyncFromSpec` / reverse progress patch 已存在；自动 intake hook 与产品闭环仍后续单独设计 |
| ER-D6 | generation epoch，处理 master 重启后的旧 version | 已通过 `runtime_epoch` + `ClaimResume` 覆盖 resume/auto_continue 竞争；剩余仅是多实例主动接管策略 |
| D10 dashboard/runbook | dashboard / alert / runbook | 已完成内置 ops snapshot API、alert 聚合与 `docs/运维手册/sessiontodo-observability.md`；外部 Grafana/PagerDuty 模板不进入本期 |
| follow-up | 显式 Resume 按钮 / 自动续跑调度器 | 显式 Resume 按钮和 guarded resume endpoint 已实现；自动续跑已有基础能力，生产调度策略仍后续收口 |
| follow-up | `turn_id` | 当前采用 `sessionTraceID` 作为 turn 语义，并已进入 toolctx、snapshot、Plan Runtime trace 和 plan_mode audit；如后续需要全局独立 turn 概念，必须先设计统一语义 |

### 已修正的 review 误表述

- D6 不再写成“扩展 runtimepolicy.Policy”。当前主体采用 `SessionState` 记录 plan mode 状态，并由统一 gate 函数计算可执行工具。
- D7 不再写成“trace_id 从 ctx 零成本获取”。当前主体要求轻量改造 `toolctx`，把现有 trace/span/tool_call_id 贯穿到 `todo_write` 和 snapshot。

### 与本期解耦的 follow-up(已写入 TODOS.md)

P2/P3 follow-up 当前剩余：自动 intake hook 与 spec→todos 产品闭环、多实例接管策略。EventBus 慢消费者 e2e 测、内存 Store GC、dashboard / alert / runbook、spec_projected 反向跳转 spec change 均已落地。`parallel_dispatch progress→todos` 本期已决策不自动映射，除非后续另立 Master 汇总写入设计。

### 已补入主体的缺口

- **错误恢复表**：见 §8.3。
- **最小可观测性**：metrics + structured logs + trace spans，见 §8.3。
- **Subagent 工具边界**：见 §6.5 / §8.2。

---

## 16. Eng Review 输入处理清单(2026-04-30 /plan-eng-review FULL_REVIEW)

本节保留 `/plan-eng-review` 的工程输入。已确认正确的条目已并入主体；后续增强项只作为 follow-up，不作为当前执行约束。

### 已并入主体的 Eng 项

| ID | 改动点 | 影响 |
|---|---|---|
| ER-D1 | `TaskResponse{Completed bool}` 加 `Status string` 枚举(completed/paused/failed),Completed 保留为 derived getter — 老 IM/WebUI 渲染层零改动,Status 是新代码权威 | `internal/master/session.go::TaskResponse` |
| ER-D2 | sessiontodo 用**独立 PG 表** `hive_session_todos`,PK(session_id, todo_id) + INDEX(session_id, plan_version) + INDEX(source_change_id) 可选 + ON DELETE CASCADE 联到 sessions 表 | 计划 §10 Phase 1 PG schema |
| ER-D3 | Plan Runtime Guard 状态表 + token 截断 / LLM error / no active plan 等边界测试，用 Go table-driven 覆盖 | 计划 §10 Phase 4 测试 |
| ER-D4 | Plan Mode 工具与 Plan Runtime Guard 同一 PR 上线，避免中间窗口期 plan 状态错位 | 计划 §10 Phase 4 |
| ER-D5 | `PlanMode bool` / plan status 放 `SessionState`；`PlanModeAllowedTools` 不放 SessionState，统一函数计算 | `internal/master/session.go` + tool gate |
| ER-D7 | `agent.plan_runtime.enabled=false` 时 sessiontodo Store **不实例化**(OE8),broadcast 代码也跳过 — 无死代码无未知事件警告 | `bootstrap/server.go` 加 flag check |

### 已并入主体的默认项

- **OE4** 锁顺序文档明示:`Store mutex < EventBus.mu`;Guard 调 Store 后必须释放 Store mutex 再 Broadcast
- **OE9** plan_version 单调递增是 **per-session**(不是全局);CAS 失败错误码标准化为 `ErrPlanVersionConflict`(导出符号)+ 错误体携带 `Got int64` 和 `Expected int64`

### Eng follow-up，不进入本期

| ID | 后续项 | 当前处理 |
|---|---|---|
| ER-D6 | 是否加入 generation epoch | 已通过 `runtime_epoch` + `ClaimResume` 落地 resume/auto_continue 竞争保护；多实例主动接管策略仍后续单独设计 |

### Outside Voice 追加测试(已写入 test plan)

- **OE5** `frontend/e2e/todos-burst.spec.ts::TestBurst50TodoWritesEventualConsistency`
- **OE6** `internal/sessiontodo/eventbus_isolation_test.go::TestParallelTestsUseOwnEventBus`
- **OE7** Snapshot 实际字段 byte budget:33KB / snapshot,subscriberBufferSize=256 上限 < 10MB / 订阅者(超即 dead subscriber 清理)

### Worktree 并行化策略

```
Lane A (sessiontodo backend core)         独立模块,无依赖
  Phase 1: internal/sessiontodo/
  Phase 2: internal/tools/todo_write.go
  → 工作在 internal/sessiontodo/ + internal/tools/(部分)

Lane B (frontend)                         独立 frontend 文件夹,等 Phase 2 API ready
  Phase 3: frontend/src/store/todos.ts + components/todos/*
  ↓ 依赖 Lane A 的 GET /sessions/{id}/todos API 契约

Lane C (plan-mode + Guard)                ER-D4 强制同一 PR
  Phase 4: internal/tools/plan_mode.go + internal/master/plan_runtime.go +
             session_loop.go 改 + tool_visibility.go 改
  ↓ 依赖 Lane A 的 sessiontodo.Store

Lane D (concurrency boundary)             Phase 5 收尾
  internal/tools/batch.go (D16 递归 check)
  internal/tools/parallel_dispatch.go
  ↓ 依赖 Lane C 的统一执行层 gate + SessionState.PlanMode

执行顺序:
  Step 1: Lane A 独立 worktree 启动
  Step 2: Lane A 完成 GET API → Lane B 启动
  Step 3: Lane A 完成 → Lane C 启动(单 PR Phase 4+5)
  Step 4: Lane C 完成 → Lane D 启动
  Step 5: 全部合并 → /plan-design-review → 集成测试

冲突标记:
- Lane A 与 Lane C 都改 internal/tools/tools.go(注册新工具)→ 顺序合并,Lane A 先
- Lane C 与 Lane D 都需要统一执行层 gate；避免把工具白名单拆到多个位置维护
```

### Eng Review 完成总结

```
+====================================================================+
|              ENG REVIEW — Agent Plan Runtime / Todos                |
+====================================================================+
| Mode                 | FULL_REVIEW                                  |
| Step 0 Scope         | accepted (CEO review 已锁,eng 不重审)        |
| Architecture Review  | 6 issues, 2 拍板 (D1/D2), 其余实现层 OK      |
| Code Quality Review  | 7 findings, 全实现层非阻塞                    |
| Test Review          | 52 测试条目 + 3 REGRESSION 关键 + 2 LLM eval |
|                      | + OE5/OE6/OE7 追加,落盘 test plan artifact  |
| Performance Review   | No issues, CEO review D2/D15/T7 已覆盖       |
| Outside Voice        | Claude subagent (codex 不可用),9 项发现     |
|                      | 确定项已并入主体,多实例接管作为 follow-up   |
| NOT in scope         | written in §15                               |
| What already exists  | toolctx.CallerType / event_bus critical /    |
|                      | tool_visibility / batch.go / spawn_agent     |
| Failure modes        | 确定缺口已补入主体,后续增强项未固化           |
| Worktree lanes       | 4 lanes (A/B/C/D), B+C 可并行,Step 1 独立  |
| Lake Score           | 7/7 推荐选 A complete option                 |
+====================================================================+
```

---

## 17. Design Review 决策清单(2026-04-30 /plan-design-review)

本节由 `/plan-design-review` 在 2026-04-30 追加。Design review 共 4 项决策,初始评分 5/10 → 终评 8.5/10。

### 7 段评分

| Pass | 维度 | 初始 | 终评 |
|---|---|---|---|
| 1 | Information Architecture | 3 | 8 |
| 2 | Interaction State Coverage | 2 | 9 |
| 3 | User Journey & 情感弧 | 4 | 8 |
| 4 | AI Slop Risk | 7 | 9 |
| 5 | Design System 对齐 | 6 | 9 |
| 6 | Responsive & A11y | 1 | 8 |
| 7 | Unresolved Decisions | — | 4 全部 resolved |

### 4 项 Design 决策(DR-D1 ~ DR-D4)

**DR-D1**:`TodosList` 嵌入位置 = **右侧与 `CanvasPanel` 同侧 stack**。当前实现为桌面右侧 workspace stack；移动端放在输入区上方并可展开。原“各自独立可折叠 / tablet 32px rail”未完整落地，保留为 UI 增强项。

**DR-D2**:**empty 状态完全不渲染 panel**。当前已按 `shouldShowTodosPanel` 控制无 snapshot/none 时不渲染；fade-in 动画未单独实现，保留为视觉增强项。

**DR-D3**:`paused` banner copy 保留 **"Send a message to continue"**；“不加显式 Resume 按钮”已被后续产品决策推翻。当前实现显示 Resume 按钮并调用 guarded resume endpoint。

**DR-D4**:`spec_projected` 标记 = **左侧 4px 蓝色边条**(`border-l-4 border-blue-400`)+ hover tooltip 显示 source change ID 和 revision。轻信号不压 content,渐进式揭露,对齐 DESIGN.md calm 工业控制台风。

> **状态更新:** `source_change_id` / `source_revision` 字段和 `spec_projected` 基础 UI 标记已存在；自动 intake hook 与完整 spec 投影闭环仍是 follow-up。

### 详细 UI 规格(写入 Phase 3)

#### TodosList 容器

```
桌面 (>=1024px)                        移动 (<768px)
┌─────────────┬───────────────────┐   ┌──────────────────┐
│ MessageList │ TodosList (320px) │   │ MessageList      │
│             │ ┌─plan banner────┐│   │                  │
│             │ │ [planning] 3/5 ││   │                  │
│             │ └────────────────┘│   ├──────────────────┤
│             │ ☐ Read context    │   │▼ 3 todos · 1/3  │← sticky 40px
│             │ ⏵ Design schema   │   ├──────────────────┤
│             │ ☑ Implement       │   │ ChatInput        │
├─────────────┴───────────────────┤   └──────────────────┘
│ ChatInput                       │
└─────────────────────────────────┘
```

- 容器:`<aside role="complementary" aria-label="Session todos">`,`w-80 border-l border-zinc-200 dark:border-zinc-800`
- 折叠按钮:左上角 `ChevronRight` 图标,折叠后变 32px 边条,显示 `Vertical "TODOS · 3"` 文本
- empty 时 `display: none`(由 `useTodosStore` snapshot 长度驱动)

#### plan_status banner(5 状态)

| 状态 | bg | text | icon | copy |
|---|---|---|---|---|
| `planning` | `bg-amber-50 border-amber-200` | `text-amber-900` | `Lightbulb` | "Planning… {N} todos so far" |
| `awaiting_approval` | `bg-blue-50 border-blue-200` | `text-blue-900` | `Eye` | "Plan ready · review and approve [Approve] [Reject]" |
| `executing` | `bg-emerald-50 border-emerald-200` | `text-emerald-900` | `Play` | "Executing · {done}/{total} steps · current: {currentTodoContent}" |
| `paused` | `bg-zinc-100 border-zinc-300` | `text-zinc-700` | `Pause` | "Paused · {done}/{total} steps · Send a message to continue" |
| `completed` | `bg-emerald-100 border-emerald-300` | `text-emerald-900` | `CheckCircle` | "Completed · {total} steps" — 5s 后 fade-out 整 panel |

- ARIA:`<div role="status" aria-live="polite">`,paused 时屏幕阅读器读出 copy
- 字体:Geist Medium 14px(状态标题)+ DM Sans 13px(进度文本)

#### TodoItem 单条(36-48px 高)

```
[●] Read context (current)              ← in_progress, blue pulse 8px dot
[✓] Design schema                       ← completed, emerald with strikethrough? NO,只 dot 变色
[○] Implement Phase 1                   ← pending, slate-400 dot
[━] Skipped (scope reduction)           ← cancelled, gray-500 dot,content strikethrough
│ [○] Source-driven from spec change    ← spec_projected,左 border-l-4 border-blue-400
```

- 高度 36px(单行)/ 48px(两行 wrap)
- 字体:DM Sans 14px / line-height 20px
- 状态 dot 8×8px,colors:
  - `pending` `#94a3b8` (slate-400)
  - `in_progress` `#3b82f6` (blue-500) + `animate-pulse`
  - `completed` `#10b981` (emerald-500) + `Check` glyph
  - `cancelled` `#6b7280` (gray-500) + content `line-through`
- spec_projected:左 `border-l-4 border-blue-400`;hover tooltip "Synced from spec change `{change_id}` (rev {revision})"
- 整行可点击:`hover:bg-zinc-50 dark:hover:bg-zinc-900` + `cursor-pointer`(展开/收起 content 详情)
- 触摸目标 44px(整行 padding 调整)
- 无 box-shadow,无圆角(对齐 DESIGN.md 锐利工业感)

#### 五态 UI 完整规格

| 状态 | TodosList | TodoItem | banner |
|---|---|---|---|
| **loading** | 骨架屏 3 行 占位 + `animate-pulse` | — | "Loading…" 灰条 |
| **empty** | `display: none`(panel 不渲染) | — | — |
| **success** | 列表渲染 | 状态 icon + content | 当前 plan_status |
| **error** | 顶部红条 "Couldn't load · Retry in 5s [Manual retry]" + 缓存内容灰度 | — | red banner |
| **partial**(WS 断 但有缓存) | 列表保留 + 顶部 toast "Reconnecting…" | — | yellow "Disconnected, will retry" |

#### 响应式断点

| 断点 | TodosList 行为 |
|---|---|
| `>=1024px` desktop | 320px 固定 panel,可手动折叠成 32px 边条 |
| `768-1023px` tablet | 默认折叠成 32px 边条,点击覆盖右半屏(`w-1/2`) |
| `<768px` mobile | 底部 sticky 40px tab(显示 `▼ 3 todos · 1/3`),点击展开 80vh bottom sheet |

#### a11y 完整规格

- Keyboard:`Tab` 进 panel,`↑↓` 在 todos 间导航,`Enter` 展开当前 todo 详情,`Esc` 折叠 panel
- ARIA landmarks:`<aside role="complementary" aria-label="Session todos">`
- 列表:`<ul role="list">` + 每项 `<li role="listitem" aria-current="step">`(in_progress 项)
- plan_status 变化:`<div role="status" aria-live="polite">`(paused 时读出 "Paused, send a message to continue")
- 触摸目标:整行 ≥ 44px,折叠按钮 44×44px
- 对比度:DM Sans 14px 用 `#1a1a1a` on `#fafafa` (>7:1) — DESIGN.md 默认
- 视觉信号 + 文本双轨:状态 dot 颜色 + ARIA label 同步

### 复用现有组件

- 不假设存在 `frontend/src/components/ui/Alert.tsx` / `Sheet.tsx`。实现时优先复用现有 `components/ai-elements/ui`，缺口用 `components/todos/` 下的局部组件补齐。
- plan_status banner 可做为 `TodosList` 内部局部组件；移动端 bottom sheet 可基于现有布局/overlay 组件实现。
- TodoItem 全新建在 `frontend/src/components/todos/TodoItem.tsx`
- 颜色:全走 DESIGN.md `--color-success`/`--color-warning`/`--color-error`/`--color-info` semantic tokens
- 间距:DESIGN.md 8px grid

### NOT in scope(已被 D18 + 设计决策克制)

- `TodoItem` 编辑 / 取消 按钮(D18 永久只读)
- 拖拽重排 todos(agent 是权威 owner)
- todo 详情页 / drawer(本期只展开 content,不另开页)
- Markdown 渲染 todo content(本期纯文本 + escape)
- spec_projected 反向编辑跳转 spec change(T1 follow-up)

### 实现状态 checklist(2026-05-03 同步)

- [x] DR-D1 部分落地:右侧 workspace stack with CanvasPanel；桌面 320px TodosPanel 已实现。
- [x] DR-D1 UI 增强:TodosList 与 CanvasPanel 各自独立折叠，tablet 32px rail。
- [x] DR-D2 部分落地:`empty` / `none` 时不渲染 panel。
- [x] DR-D2 UI 增强:首次出现时 fade-in 动画 200ms。
- [x] DR-D3 已按新决策落地:paused banner copy "Send a message to continue" + 显式 Resume 按钮；旧“不加按钮”要求废弃。
- [x] DR-D4 基础落地:spec_projected 左侧强调 + hover/title 详情 + spec link。
- [x] plan_status / todo status 文案已进入 `frontend/src/i18n/locales/`。
- [x] TodoItem 使用现有 DM Sans 全局字体与 DESIGN.md CSS token。
- [x] aria-live 区域绑定 plan_status 变化。
- [x] 桌面 320px / tablet 折叠 / 移动 bottom sheet 三断点响应式实现已落地；完整 e2e 测试作为后续 QA 扩展，不阻塞本计划代码完成。
- [ ] Future follow-up: 五态 UI 的 storybook stories；当前项目未发现 storybook stories 基础设施。

---

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| CEO Review | `/plan-ceo-review` | Scope & strategy | 1 | clean (PLAN) | SELECTIVE EXPANSION, 12 proposals accepted, 8 deferred, 19 decisions |
| Codex Review | `/codex review` | Independent 2nd opinion | 0 | — | not run (codex CLI unavailable, claude subagent ran instead) |
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | clean (PLAN) | FULL_REVIEW, 13 issues, 7 decisions, OE 9 findings 4 critical pinned |
| Design Review | `/plan-design-review` | UI/UX gaps | 1 | clean (PLAN) | score 5/10 → 8.5/10, 4 design decisions resolved, full UI specs |
| DX Review | `/plan-devex-review` | Developer experience gaps | 0 | — | not applicable (internal refactor, no external API) |

- **OUTSIDE VOICE (CEO):** 5 critical gaps surfaced (subagent race / spec→todo feedback / critical event / batch recursion / paused resume),确定执行项已并入主体；spec→todo 自动投影保留为 follow-up。
- **OUTSIDE VOICE (Eng):** 9 findings (OE1-OE9),确定项已合并；多实例主动接管策略保留为 follow-up。
- **CROSS-MODEL:** CEO + Eng outside voices independently flagged Phase 4/5 sequencing — strong signal validating ER-D4.
- **UNRESOLVED:** 无阻塞当前 PR 的 unresolved；后续项见 §15 / §16。
- **VERDICT:** 主体已合并确定决策，可作为 Claude Code 审核输入；follow-up 不按当前 PR 执行。
