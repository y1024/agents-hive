# Agent 架构演进方案

> 日期：2026-04-23
> 决策输入：代码现状盘点、deer-flow 调研（final-verdict.md）、Hermes Agent 2026.4.16 对标、Claude × Codex 红蓝对抗辩论

---

## 原则

- **不建 Middleware Pipeline**。所有检查作为明确的函数调用插入主循环，保持单向数据流。
- **不抄 LangGraph 抽象**。看证据、别抄类。
- **不引入静态多智能体 DAG**。维持动态 ReAct + Sub-agent dispatch。
- **先启用已有原语，再造新的**。仓库已有 `history_snip`、`StreamingExecutor`、`loopDetector`、`checkCostBudget`、`resilience/retry.go`，优先调优而非重建。

---

## Step 1: 感官与幻觉止血（本周）

### P0-1 流式 tool_call preview (1天)

- 修复 `react_processor.go:362-364` 早 return 条件：`chunk.ToolCalls` 非空时不能吞掉。
- 同步修 SubAgent：`agentloop.go:203-213` 同款 bug。
- 新增 `EventTypeToolCallPreview`，归为 EventBus critical 事件（`event_bus.go:55`）。
- P0-A suppression 分支内补 emit：`shouldSuppressStreamPartial` 屏蔽文本 partial 但不能吞 tool_call ready。
- 只做 preview，不做 early execution。

### P0-2 ToolEvidence schema + Grounding Validator (4天)

**前置（1.5天）**：扩展 `ToolResult`（`mcphost/host.go:25`）增加 `Evidence` 字段：

```go
type ToolEvidence struct {
    SourceURLs    []string
    Query         string
    RawCount      int
    FilteredCount int
    FetchMode     string    // webfetch: "jina" / "direct" / "cached"
    Timestamp     time.Time
}
```

`websearch.go` 和 `webfetch.go` 填充 evidence，不改 Content 格式，只加 metadata 通道。

**Validator 本体（2天）**：在 `react_processor.go` 的 P0-A guard 之后、`TriggerChatAfter` 之前插入。

- Trust boundary：validator 验证插件改写前的原始 evidence（在 `ToolExecuteAfter` 之前 snapshot），防止插件污染证据链。
- 失败处理：不 persist、不 broadcast，注入 system corrective message + retry 一次。
- 接入 `PostValidation` flag（`config.go:307`）作为灰度开关，消除僵尸配置。
- 第一版仅覆盖 `websearch/webfetch`。

### P0-3 并发执行器修复 + Capacity Governance (2天)

- 修复 `executeToolsConcurrent` 的 tool_call_id 丢失（`react_processor.go:1995-1996`）。
- 并发路径恢复 `recordCallResult`（`react_processor.go:1889`），补全调用级循环检测。
- 配置化 `spawn_agent` per-turn limit（当前硬编码 `maxConcurrentSpawn=3`）。
- 配置化 safe tool max concurrency（当前无上限）。
- 标记 `websearch`/`webfetch` 为 `IsConcurrencySafe=true`。

---

## Step 2: 记忆外化与上下文续命（下周）

### 2.1 TodoList 结构化记忆 (3-4天)

- 增加 `TodoWrite`（Merge/Replace 模式）和 `TodoRead` 工具。
- 状态附加在 `SessionState` 上，每次 compact 后作为 pin 注入 System Prompt 首部（复用已有 `SPEC-STATE` pin 机制，`session_compact.go:17-71`）。
- 持久化：session metadata JSON 字段或独立 DB 列，不依赖 `appendSessionMessage`。
- Hermes 参考：`tools/todo_tool.py` 的 Merge 模式和 `format_for_injection()` 注入机制。

### 2.2 启用 history_snip + 结构化摘要 (2-3天)

- 仓库已有 `HistorySnipCompactor`（`compaction/history_snip.go`）：保留首条 user + 最近 N 条 + 中间摘要。
- 把默认 compaction pipeline 从 `tool_budget, session_memory, truncate` 调整为 `tool_budget, session_memory, history_snip, llm_summary/truncate`（`defaults.go:89-90`）。
- 补充结构化摘要模板（Resolved / Pending 追踪）。
- 下调默认 compaction 阈值（当前 `MaxTokens=500000` 对 150 轮验收触发不了压缩）。
- Hermes 参考：`agent/context_compressor.py`（在线压缩）。注意 `trajectory_compressor.py` 是离线后处理，不可直接移植。

### 2.3 预算优雅退出 (1天)

- 改造 `checkCostBudget`（`react_processor.go:1542-1558`）：触顶后不硬停，触发 summarize + yield。
- 增加 `cfg.Agent.LongRun.MaxTurns`（默认 200），作为 turn 硬顶兜底。
- Hermes 参考：`run_agent.py` 的 `_budget_grace_call` 机制。

---

## Step 3: 多副本一致性（Q3，3-4 周）

### EventBus 外置化

当前 `event_bus.go:90-236` 是进程内 map of channels。多 Pod 部署时 WebSocket、HITL 审批、tool_call_ready 广播会跨副本丢失。引入 Redis Pub/Sub 或 PostgreSQL NOTIFY。涉及消息可靠性、订阅生命周期、critical event 重试、WS sticky routing。

### Sub-Agent Auto-dispatch

把 `agentloop.go` 的手动触发升级为基于任务复杂度的探测自动路由。触发规则（保守起步）：预期 tool result > 5K token、单轮 TODO ≥ 3 步子任务、用户显式请求探索类任务。Sub-agent 返回结构化 summary 进主 context。

---

## 时间线

| 阶段 | 内容 | 工期 | 前置 |
|------|------|------|------|
| P0-1 | 流式 tool_call preview（主循环 + SubAgent） | 1天 | 无 |
| P0-2 | ToolEvidence + Grounding Validator | 4天 | P0-1 |
| P0-3 | 并发修复 + Capacity Governance | 2天 | 可与 P0-2 并行 |
| 2.1 | TodoList 结构化记忆 | 3-4天 | Step 1 完成 |
| 2.2 | 启用 history_snip + 结构化摘要 | 2-3天 | 可与 2.1 并行 |
| 2.3 | 预算优雅退出 + MaxTurns | 1天 | 可与 2.1 并行 |
| **Step 1+2** | | **~2.5 周** | |
| Step 3 | EventBus 外置化 + Auto-dispatch | Q3, 3-4周 | Step 1+2 稳定 |

---

## 验收标准

1. **证据链闭环**：所有包含外部引用的回答，其来源必须被 Grounding Validator 验证存在于 `ToolEvidence` 中（阻断"调用了工具但依然夹带私货编造"的高级幻觉）。
2. **长尾续航**：单任务连续执行 > 150 轮不爆栈不偏离（history_snip + TodoList 锚定）。验收需定义任务集基准、compaction 触发阈值、模型上下文窗口。
3. **体感透明**：所有长耗时动作前端 3s 内获得感知反馈（tool_call preview event）。
