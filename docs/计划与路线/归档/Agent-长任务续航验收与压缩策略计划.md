# Agent 长任务续航基准与恢复治理计划

> 状态：COMPLETED  
> 类型：长任务基准 / 压缩验收 / 预算退出 / 恢复治理  
> 结论：旧计划不按原文实施。当前代码已经有 Plan Runtime、todos、compaction pipeline 和基础预算检查，本计划只补“可重复证明长任务不会停错、忘错、完成错”的缺口。  
> 优先级：P0，先于工具召回稳定化。

## 0. 完成记录

完成日期：2026-05-07。

本计划已完成 deterministic/scripted 长任务治理基线，而不是 live LLM 或真实 ReAct 端到端长跑证明。完成范围包括：

- `internal/agentquality/longrun_types.go`、`internal/agentquality/longrun_runner.go` 和 `internal/agentquality/testdata/longrun/` 已提供 B1/B2/B3/B4 基准、baseline report、指标结构和失败分类校验。
- `internal/master/react_processor.go` 已记录 `quality.agent_turn`，包含 turn、LLM 调用、工具调用、prepared message、visible tool 和 compaction 标记字段。
- budget 触顶时已支持 graceful yield：存在未完成 todos 时写入交接摘要并暂停计划，失败时保留 hard stop 语义。
- resume/restart 基准和 PlanRuntime paused snapshot 测试已覆盖暂停、恢复失败和继续执行场景。
- 默认 compaction pipeline 未被凭感觉调整；当前 baseline 仅固化可治理证据。

已验证：

```bash
go test ./internal/agentquality -run 'LongRun' -count=1
go test ./internal/master -run 'QualityEvent|AgentTurn|ToolRecall|ModelVisibleTools|PlanMode|CostBudget|Graceful|PlanRuntime' -count=1
go test ./internal/config ./internal/tools ./internal/master ./internal/agentquality -count=1
go test ./... -count=1
```

后续增强项：

- 如需证明真实模型长任务能力，应单独增加 live LLM smoke/nightly runner，不放入默认 CI。
- 如需更细粒度 compaction 分析，应把真实 `prepareMessagesWithCompression()` telemetry 直接写入 longrun report，而不是仅依赖 deterministic fixture。

## 1. 当前代码事实

已经具备：

- `internal/master/plan_runtime.go`：已有 `PlanRuntimeGuard`、todo 完成判断、paused/completed/failed 终态。
- `internal/master/prompt_builder.go`：`prepareMessagesWithCompression()` 已接入可插拔 compaction pipeline。
- `internal/master/session_compact.go`：已有 spec state pin，压缩后能保留 spec-driven 状态锚。
- `internal/compaction/`：已有 `tool_budget`、`session_memory`、`history_snip`、`llm_summary`、`truncate`。
- `internal/master/react_processor.go`：ReAct loop 内已记录 `quality.context_build`，并在每轮调用前构造 model-visible tools。
- `internal/master/phase6_test.go`：已有 `checkCostBudget()` 的成本预算单测。

真正缺口：

- 没有可重复运行的 30/80/150 turn 长任务基准。
- 没有统一定义 `turn`，导致续航指标不可比较。
- `agentquality` 当前以静态 case 为主，不能证明长任务真实执行状态。
- 成本触顶现在主要是硬错误，没有稳定的交接摘要和 resume 验收。
- 没有覆盖 process restart / session reload 后继续执行的基准。

## 2. 继续推进范围

本计划继续推进，但只做四件事：

1. 建立 deterministic 长任务基准 harness。
2. 建立 live LLM smoke 入口，但不放进默认 CI。
3. 补 budget graceful yield 和 resume/restart 验收。
4. 用基准数据决定 compaction pipeline 和阈值是否需要调整。

## 3. 不再推进的旧范围

以下内容不在本计划推进：

- 不做 EventBus 外置化。
- 不做 SubAgent 自动规划器。
- 不做多副本任务调度。
- 不做多业务事件总线。
- 不凭感觉直接修改默认 compaction pipeline。
- 不用一次 live LLM 跑通来声称长任务能力已完成。

## 4. 术语定义

`turn` 在本计划中定义为一次 ReAct iteration：

```text
一次 turn = 构造 preparedMessages + 一次 LLM ChatWithTools 调用 + 处理该次返回的 tool calls 或最终文本
```

不要把用户消息数、工具调用数、前端消息数当成 turn。

指标字段：

- `turn_count`：ReAct iteration 数。
- `llm_call_count`：LLM 调用数，正常等于 `turn_count`。
- `tool_call_count`：工具调用总数。
- `compaction_count`：实际压缩触发次数，不包含 lazy skip。
- `lazy_compaction_skip_count`：懒惰压缩跳过次数。
- `tokens_before_compaction`：压缩前估算 token。
- `tokens_after_compaction`：压缩后估算 token。
- `todo_lost_count`：压缩/恢复后丢失的 todo 数。
- `constraint_lost_count`：用户约束丢失数。
- `duplicate_tool_failure_count`：重复无效工具调用次数。
- `budget_exit_mode`：`none` / `hard_stop` / `graceful_yield`。
- `final_status`：`completed` / `paused` / `failed`。

失败分类：

- `context_overflow`
- `todo_loss`
- `constraint_loss`
- `tool_loop`
- `premature_completion`
- `budget_hard_stop`
- `resume_failed`
- `trace_missing`

## 5. 基准分层

### 5.1 Deterministic 基准

用于 CI 和本地稳定回归，不依赖真实 LLM。

实现方式：

- 使用 fake/stub LLM 按脚本返回固定 tool calls 和文本。
- 使用内存 session todo store。
- 使用 fake tools 产生固定成功/失败/大输出。
- 直接断言状态、事件、todo、compaction 统计。

### 5.2 Live LLM Smoke

用于人工或 nightly 验证真实模型行为，不进入默认 `go test ./...`。

实现方式：

- 文件名使用 `_integration_test.go` 或 build tag。
- 依赖真实 LLM key 时默认 skip。
- 输出 JSON 报告，不以一次模型波动直接判定代码失败。

## 6. 基准用例

### B1：30 turn 文件工作流

目标：验证普通长任务不会丢步骤。

任务形态：

- 多文件读取。
- 小范围修改。
- 测试失败一次后修复。
- 最后给出验证命令。

通过条件：

- `turn_count >= 30`。
- todos 不丢。
- 未完成 todos 时不能 completed。
- 最终 completed 时所有 todo 为 completed/cancelled。
- 最终回复包含真实验证命令字段。

### B2：80 turn 压缩工作流

目标：验证 compaction 后仍保留目标、约束、未完成事项。

任务形态：

- 3 个以上模块探索。
- 多轮工具输出。
- 至少一次大工具输出触发 `tool_budget`。
- 至少一次 full pipeline compaction。

通过条件：

- `compaction_count >= 1`。
- 压缩后仍保留用户目标。
- 压缩后仍保留“不改 generated 文件”“不扩大 scope”等约束。
- 不把已完成事项重新标为未完成。

### B3：150 turn 压力工作流

目标：验证极长任务不会在压缩、预算、反思、工具循环之间错误完成。

任务形态：

- 多阶段代码任务。
- 多次测试失败和修复。
- 多轮上下文压缩。
- 接近预算上限。

通过条件：

- 不爆 context。
- 不提前 completed。
- `duplicate_tool_failure_count` 不超过阈值。
- trace 能定位每次压缩和失败修复点。

### B4：resume/restart 工作流

目标：验证暂停、预算交接、进程重启后的可恢复性。

任务形态：

- 执行中仍有 pending/in_progress todos。
- 触发 budget graceful yield。
- 模拟 session reload 或 store reload。
- 继续执行剩余 todos。

通过条件：

- budget 触顶后状态为 paused，不是 completed。
- session 中存在交接摘要。
- reload 后 todos、plan status、约束仍存在。
- 继续执行后能进入 completed 或明确 failed。

## 7. 实施任务

### Task 1：新增长任务基准数据结构

涉及文件：

- 新增：`internal/agentquality/longrun_types.go`
- 新增：`internal/agentquality/longrun_types_test.go`

实现要求：

- 定义 `LongRunCase`、`LongRunStep`、`LongRunReport`、`LongRunFailure`。
- `LongRunReport` 必须包含 §4 的全部指标字段。
- `LongRunFailure.Type` 只能取 §4 的失败分类。

验收：

- 单测覆盖 JSON marshal/unmarshal。
- 非法 failure type 返回错误。

命令：

```bash
go test ./internal/agentquality -run 'LongRun' -count=1
```

### Task 2：新增 deterministic longrun harness

涉及文件：

- 新增：`internal/agentquality/longrun_runner.go`
- 新增：`internal/agentquality/longrun_runner_test.go`
- 修改：`internal/agentquality/harness.go`

实现要求：

- runner 接收 `LongRunCase`，输出 `LongRunReport`。
- 使用 fake LLM script 驱动固定 turn 数。
- 支持 fake tool success、fake tool failure、fake large output。
- 每个失败都必须带 `case_id`、`turn`、`failure_type`、`reason`。

验收：

- B1 deterministic case 可稳定运行。
- 失败时报告具体 turn 和 failure type。
- 不依赖真实 LLM key。

命令：

```bash
go test ./internal/agentquality -run 'LongRunRunner|LongRunHarness' -count=1
```

### Task 3：接入 ReAct turn 观测

涉及文件：

- 修改：`internal/master/react_processor.go`
- 修改：`internal/master/quality_events.go`
- 修改：`internal/agentquality/types.go`
- 修改：`internal/master/quality_events_test.go`

实现要求：

- 新增或复用质量事件记录每轮 ReAct iteration。
- 事件 attributes 至少包含：
  - `turn_id`
  - `turn_index`
  - `llm_call_count`
  - `tool_call_count`
  - `prepared_message_count`
  - `visible_tool_count`
  - `compaction_triggered`
- 事件必须能通过 trace/replay 定位。

验收：

- 单测断言一次 ReAct turn 事件字段完整。
- `quality.context_build` 仍保留现有 memory 注入字段。

命令：

```bash
go test ./internal/master ./internal/agentquality -run 'Quality|LongRun|ContextBuild|AgentTurn' -count=1
```

### Task 4：补 compaction 报告字段

涉及文件：

- 修改：`internal/master/prompt_builder.go`
- 修改：`internal/master/compaction_tracker.go`
- 修改：`internal/master/session_compact_test.go`
- 修改：`internal/agentquality/longrun_runner.go`

实现要求：

- `prepareMessagesWithCompression()` 输出或记录压缩前后 token、stage names、lazy skip、elapsed。
- deterministic runner 能读取这些字段并写入 `LongRunReport`。
- 不改变默认 `DefaultCompactionPipelineStages`。

验收：

- B2 能看到 `compaction_count >= 1`。
- lazy skip 和真正压缩分开统计。
- stage names 出现在报告中。

命令：

```bash
go test ./internal/master ./internal/compaction ./internal/agentquality -run 'Compaction|LongRun' -count=1
```

### Task 5：实现 budget graceful yield

涉及文件：

- 修改：`internal/master/react_processor.go`
- 修改：`internal/master/plan_runtime.go`
- 修改：`internal/master/quality_events.go`
- 修改：`internal/agentquality/types.go`
- 修改：`internal/master/phase6_test.go`

实现要求：

- `checkCostBudget()` 触顶后，长任务优先产出交接摘要。
- 交接摘要至少包含：
  - 目标
  - 已完成事项
  - 未完成事项
  - 最近失败
  - 下一步建议
- 交接成功时 session 状态为 paused，`budget_exit_mode=graceful_yield`。
- 交接失败时才返回 hard stop，`budget_exit_mode=hard_stop`。

验收：

- 预算触顶 + 未完成 todos 时不是 completed。
- session 中能看到交接摘要。
- 质量事件区分 graceful yield 和 hard stop。

命令：

```bash
go test ./internal/master ./internal/agentquality -run 'CostBudget|Graceful|PlanRuntime|LongRun' -count=1
```

### Task 6：新增 resume/restart 基准

涉及文件：

- 新增：`internal/agentquality/testdata/longrun/b4_resume_restart.json`
- 修改：`internal/agentquality/longrun_runner_test.go`
- 修改：`internal/master/plan_runtime_test.go`

实现要求：

- 模拟 paused snapshot 写入 store。
- 模拟重新读取 snapshot。
- 验证 pending todos、plan status、约束摘要仍存在。
- 验证继续执行不会误 completed。

验收：

- B4 deterministic case 稳定通过。
- resume 失败能分类为 `resume_failed`。

命令：

```bash
go test ./internal/agentquality ./internal/master -run 'Resume|Restart|LongRun' -count=1
```

### Task 7：生成 baseline 报告

涉及文件：

- 新增：`internal/agentquality/testdata/longrun/b1_30_turn.json`
- 新增：`internal/agentquality/testdata/longrun/b2_80_turn.json`
- 新增：`internal/agentquality/testdata/longrun/b3_150_turn.json`
- 新增：`internal/agentquality/testdata/longrun/baseline-report.json`

实现要求：

- 报告必须可提交到 git。
- 不写入 `.gstack/`，因为当前 `.gitignore` 忽略 `.gstack/` 和 `docs/`。
- 报告中包含 B1/B2/B3/B4 的 case id、指标、失败分类。

验收：

- baseline report 能被单测读取。
- case id 缺失或指标缺失时测试失败。

命令：

```bash
go test ./internal/agentquality -run 'LongRunReport|LongRunTestdata' -count=1
```

## 8. 完成定义

该计划完成后才能说“长任务续航进入可治理状态”：

- B1/B2/B3/B4 deterministic 基准存在并能运行。
- 至少一份 baseline report 位于 `internal/agentquality/testdata/longrun/`。
- `turn`、压缩、预算、resume 指标可从报告看到。
- budget 触顶能 paused + 交接摘要，不误标 completed。
- compaction pipeline 是否调整有报告结论。

## 9. 推荐实施顺序

1. Task 1 + Task 2：先有 harness。
2. Task 3 + Task 4：补观测字段。
3. Task 5：补预算交接。
4. Task 6：补恢复基准。
5. Task 7：固化 baseline。

不要先改默认 compaction pipeline。没有 B1/B2/B3/B4 数据前，默认值保持现状。
