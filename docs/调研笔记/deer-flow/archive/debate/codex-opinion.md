# Codex 独立意见：deer-flow 合并报告对 agents-hive 的可借鉴性审查

日期：2026-04-21

角色：agents-hive staff engineer / 反方辩手

审查对象：`docs/research/deer-flow/merged-report.md`

对照源码：

- agents-hive：`/Users/guoss/workspace/company/vast/agents-hive/`
- deer-flow 镜像：`/tmp/deer-flow-src/`

## 0. 总结立场

1. 合并报告对 deer-flow 的源码事实验证总体有价值，但“agents-hive 借鉴清单”和“最终 takeaway”把 Python/LangGraph 的抽象边界带进 Go runtime，存在明显迁移误差。
2. 我同意 P0-C 是当前最高价值缺口，但不同意“抄 `client.py:615-680`”这个表述；Go 侧真正要改的是 stream event contract、EventBus 语义和 tool execution 调度点。
3. 我不同意把 P0-D grounding validator 直接类比为 `wrap_tool_call`；agents-hive 当前没有 LangGraph `AgentMiddleware` 等价物，只有 `ToolBridge` plugin hook 和 `Master.executeTool`，这两个都不是 grounding validator 的天然落点。
4. P0-B “空结果 error envelope”在 agents-hive 已经基本落地，继续照 deer-flow 做 JSON envelope 可能与现有 `IsError=true + 文本错误` 契约冲突。
5. “别抄 RunManager + 多 worker”对 agents-hive 当前形态不是已踩坑，而是未来 HTTP run API / 多副本部署才会踩的坑。
6. “别抄硬编码线程池 3+3+3”在 agents-hive 已经有相邻风险，但形态不是固定 worker pool，而是硬编码 spawn/subagent 限额、无限 goroutine 以及 30 分钟同步等待。
7. “别抄 skill 语义匹配”对 agents-hive 已经部分踩坑：模型通过 `skill` 工具按名称调用，选择哪个 skill 仍主要靠 prompt/描述，不是宿主强制路由；但 agents-hive 又比 deer-flow 多了 registry、scope、disable_model_invocation、自愈安装等约束。
8. 合并报告漏了 agents-hive 当前真正敏感的几个技术盲点：provider stream 增量语义不一致、并发工具执行丢失 tool_call ID、ToolBridge plugin hook 可能改写 error semantics、EventBus 非关键消息可丢、Responses API 与 Chat Completions 的 tool_call 完成时机不一致。
9. 我的 P0 排序是：P0-C event contract 先行，其次 P0-D grounding validator 的 hook 抽象，再补 P0-B 结构化错误兼容层；不要把 P0-B 增强排到独立 P0。
10. 结论一句话：deer-flow 可作为“流式消息种类不能只看 text”的证据，不可作为 agents-hive Go runtime 的施工图。

## 1. 对 3 个“立刻动手”建议的判断

### 1.1 P0-C 流式 tool_call：NEEDS-PRECONDITION

合并报告建议：

> “P0-C 流式 tool_call：抄 `client.py:615-680` 的 messages-mode 分支。”

文件锚点：

- 报告建议：`docs/research/deer-flow/merged-report.md:518`
- 报告蓝军依据：`docs/research/deer-flow/merged-report.md:243-261`
- deer-flow 实现：`/tmp/deer-flow-src/backend/packages/harness/deerflow/client.py:615-680`
- agents-hive LLM stream Responses：`internal/llm/stream_responses.go:88-139`
- agents-hive LLM stream Chat Completions：`internal/llm/stream_completions.go:284-325`
- agents-hive Master stream callback：`internal/master/react_processor.go:357-416`
- agents-hive tool final broadcast：`internal/master/react_processor.go:664-707`
- agents-hive tool execution branch：`internal/master/react_processor.go:722-923`

判断：NEEDS-PRECONDITION。

理由 1：Python 的 `AIMessage/ToolMessage` 分支不是 Go 侧的缺失抽象。

deer-flow 的 `client.py` 在 `mode == "messages"` 下直接区分 `AIMessage` 和 `ToolMessage`，并把 text、tool_calls、tool_message 都 yield 成统一 `StreamEvent`。这成立是因为 LangGraph 的 `astream(..., stream_mode=["messages","values"])` 已经给了它“消息级事件”。

agents-hive 没有 LangGraph message bus。Go 侧的 LLM 层已经能在流式 chunk 中累积 `ToolCalls`：

- Responses API 在 `ResponseFunctionCallArgumentsDoneEvent` 后把 tool call append，并通过 `onChunk(StreamChunk{ToolCalls: toolCalls})` 通知回调，见 `internal/llm/stream_responses.go:119-138`。
- Chat Completions 在 delta 中累积 `pendingCalls`，并在非最终 chunk 调用 `onChunk(StreamChunk{ToolCalls: curToolCalls})`，见 `internal/llm/stream_completions.go:284-325`。

真正的问题在 Master 回调只处理 `ContentSoFar` 和 `ReasoningContent`，遇到纯 tool_call chunk 会被 `if chunk.ContentSoFar == "" && chunk.ReasoningContent == "" { return nil }` 过滤掉，见 `internal/master/react_processor.go:357-364`。

所以 P0-C 不是“把 AIMessage/ToolMessage 分支翻译成 Go”，而是给 `llm.StreamChunk` 明确定义 tool_call delta 的事件语义，并让 Master/EventBus 消费它。

理由 2：Go 侧要先决定 event bus 的 tool_call lifecycle，不只是“yield tool_calls”。

agents-hive 当前前端/ACP 看到 tool call 有两个入口：

- assistant final message payload 携带 `tool_calls`，见 `internal/master/react_processor.go:679-690`。
- 工具开始/成功/错误通过 `EventTypeToolCall` 广播，见 `internal/master/react_processor.go:986-992`、`internal/master/react_processor.go:1187-1194`、`internal/master/react_processor.go:1724-1731`。

这两个入口都发生在 LLM 完整响应之后或工具执行期间。P0-C 如果要“边收 tool_call 边可见”，必须定义新状态，例如：

- `assistant.tool_call_delta`：参数还在增长，不可执行。
- `assistant.tool_call_ready`：参数 JSON 完成，可以进入调度。
- `tool.start`：工具真的开始执行。
- `tool.result`：结果写回 session。

否则直接把 `StreamChunk.ToolCalls` 广播成当前 `tool_calls` payload，会造成 UI/ACP 把半成品 arguments 当成可执行调用，或重复渲染同一个 tool_call。

理由 3：Responses API 与 Chat Completions 的“ready”语义不同。

Responses API 有 `ResponseFunctionCallArgumentsDoneEvent`，可以比较清楚地把 tool call 标记为 ready，见 `internal/llm/stream_responses.go:119-139`。

Chat Completions 只有 delta arguments，当前实现每个 chunk 都把累积中的 `curToolCalls` 回调出去，见 `internal/llm/stream_completions.go:302-325`。这不是 ready 事件，只是 snapshot。

如果 Master 收到任何 `ToolCalls` 就开始执行，会在 Chat Completions 路径上执行半截 JSON。必须先给 `StreamChunk` 加字段区分 `ToolCallDelta`、`ToolCallSnapshot`、`ToolCallDone`，或让底层只在 finish_reason/tool_calls 完整时 emit ready。

理由 4：StreamingExecutor 不是 P0-C 的现成答案。

agents-hive 有 `StreamingExecutor`，但它负责工具执行并发，不负责 LLM stream 中 tool_call 的解析和早执行，见 `internal/master/streaming_executor.go:21-33`。

当前 Master 只有在 `resp.ToolCalls` 最终可得后才进入 `executeToolsConcurrent` 或串行执行分支，见 `internal/master/react_processor.go:792-893`。

因此，P0-C 的前置条件是先确定“是否允许 LLM 尚未结束就开始执行 ready tool_call”。如果允许，需要处理 assistant 消息持久化、tool result 顺序、HITL、取消、terminal cache、loop detector 的一致性。

理由 5：报告说“主回调仍只看文本”是对的，但“抄 client.py”低估了 Go 侧状态机。

Master 回调确实只看文本和 reasoning，见 `internal/master/react_processor.go:361-414`。

但 agent loop 后续还有 P0-A guard、plugin ChatMessageAfter、session append、tool execution、terminal cache、loop detector 等顺序不变量，见 `internal/master/react_processor.go:560-604`、`internal/master/react_processor.go:606-662`、`internal/master/react_processor.go:735-756`。

P0-C 不能绕开这些不变量直接从 stream callback 开工具。

建议施工前置条件：

1. 为 `llm.StreamChunk` 增加工具调用事件类型，不要复用最终 `ToolCalls []ToolCall` 表达 delta。
2. 为 EventBus 增加 tool_call delta/ready 事件，标记为关键事件还是非关键事件要明确。
3. 为 Responses 和 Chat Completions 各自写 ready 语义测试。
4. 先只广播 tool_call ready，不做边流边执行；验证 UI/ACP 无重复后，再评估 early execution。
5. 对 `tool_choice=required` 的 partial suppression 重新设计：当前 suppression 会吞掉文本 partial，但不应吞掉 tool_call ready，见 `internal/master/react_processor.go:385-394`。

### 1.2 P0-D grounding validator：FAIL

合并报告建议：

> “P0-D grounding validator：抄 `tool_error_handling_middleware.py:19-65` 的套路；grounding validator 本质就是另一个 wrap_tool_call。”

文件锚点：

- 报告建议：`docs/research/deer-flow/merged-report.md:519`
- 报告借鉴清单：`docs/research/deer-flow/merged-report.md:455`
- deer-flow middleware：`/tmp/deer-flow-src/backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:19-65`
- agents-hive tool host：`internal/mcphost/host.go:131-164`
- agents-hive ToolBridge：`internal/skills/toolbridge.go:58-192`
- agents-hive ExecuteDirect：`internal/skills/toolbridge.go:195-299`
- agents-hive Master executeTool：`internal/master/react_processor.go:981-1185`
- agents-hive tool message broadcast：`internal/master/react_processor.go:1734-1753`

判断：FAIL。

理由 1：agents-hive 没有 LangGraph `wrap_tool_call` 对应概念。

deer-flow 的 `ToolErrorHandlingMiddleware` 继承 `AgentMiddleware`，通过 `wrap_tool_call` / `awrap_tool_call` 包住 LangGraph tool node。它天然位于“工具调用请求 -> handler -> ToolMessage/Command”的边界，见 `/tmp/deer-flow-src/backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:35-65`。

agents-hive 当前没有 middleware chain。实际路径是：

- `Master.executeTool` 做策略、权限、超时、执行、trace、metric、terminal classification，见 `internal/master/react_processor.go:981-1185`。
- `ToolBridge.ExecuteDirect` 做插件 before/after、read_file cache、tool-not-found 友好提示、wenyan 伪成功错误修正，见 `internal/skills/toolbridge.go:195-299`。
- `mcphost.Host.ExecuteTool` 调具体 executor，见 `internal/mcphost/host.go:131-164`。

这些是硬编码流程，不是可组合 middleware。

理由 2：grounding validator 的检查点不是 tool exception 处理点。

ToolErrorHandlingMiddleware 的目标是“工具抛异常时转成 ToolMessage(status=error)”，避免异常穿透让 graph 中断。

grounding validator 的目标通常是“模型最终回答是否被工具结果支撑”。这发生在工具结果写入之后、下一轮或最终 assistant 输出之前，不等价于 tool execution wrapper。

agents-hive 的最终 assistant 输出在 P0-A guard 之后、plugin ChatMessageAfter 之后写入 session 和广播，见 `internal/master/react_processor.go:560-707`。

如果 validator 要验证最终回答，它应插在以下任一位置：

- `TriggerChatAfter` 之前，避免插件先看到未验证内容。
- `appendSessionMessage` 之前，避免污染 DB。
- `BroadcastSessionMessage` 之前，避免前端泄露。

这些位置是 LLM response post-processing，不是 `executeTool`。

理由 3：ToolBridge hooks 不是 validator middleware。

ToolBridge 只有 `ToolExecuteBefore` 和 `ToolExecuteAfter` plugin hook，见 `internal/skills/toolbridge.go:74-85`、`internal/skills/toolbridge.go:160-174`、`internal/skills/toolbridge.go:199-209`、`internal/skills/toolbridge.go:267-281`。

这些 hook 包住单个工具，不知道整轮 LLM response，也不知道用户问题、所有证据、最终回答。用它做 grounding validator 会把问题放错层。

理由 4：agents-hive 已经有异常转 tool result 的基础能力，照抄收益有限。

`executeTool` 对执行 error 会返回 `toolResult{IsError:true}`，并写入 tool message，见 `internal/master/react_processor.go:1093-1127`。

工具业务错误也会从 `mcphost.ToolResult.IsError` 转成 `toolResult.IsError`，见 `internal/master/react_processor.go:1129-1155`。

nil result 也被转成错误，见 `internal/master/react_processor.go:1157-1185`。

因此，deer-flow 这段 middleware 对 agents-hive 的价值是“保留控制流异常、错误变成模型可见消息”的设计原则，而不是 P0-D 的实现参考。

理由 5：如果先引入 middleware 抽象，成本可能超过 P0-D 本身。

要等价引入 LangGraph 式 middleware，需要重新抽象：

- before LLM call
- after LLM response
- before tool call
- after tool result
- on stream chunk
- on final answer
- ordering invariant
- control-flow exceptions / context cancellation

这会触及 `runReActLoop` 的主干，不是 P0-D 的最小修复。

更现实的 P0-D 前置：

1. 定义 `GroundingValidator` 接口，输入 `user query + final answer + tool result messages + source metadata`。
2. 在 `TriggerChatAfter` 之前调用，失败时不要 persist/broadcast。
3. 对失败结果追加 system/tool-style corrective message，让模型重答。
4. 对 websearch/webfetch 结果增加可引用 source metadata，否则 validator 无证据结构可用。
5. 后续再把它纳入统一 processor/middleware，不要为了一个 validator 先大重构。

### 1.3 P0-B 空结果 error envelope：NEEDS-PRECONDITION

合并报告建议：

> “P0-B 增强：把空结果提升到 error envelope；返回 `{"error":"No results found","query":...}` 而不是空数组。”

文件锚点：

- 报告建议：`docs/research/deer-flow/merged-report.md:520`
- 报告借鉴清单：`docs/research/deer-flow/merged-report.md:454`
- agents-hive websearch strict 注册：`internal/tools/tools.go:179-186`
- agents-hive websearch strict 语义：`internal/tools/websearch.go:73-80`
- agents-hive empty raw handling：`internal/tools/websearch.go:184-205`
- agents-hive errorResult：`internal/tools/tools.go:1092-1098`
- agents-hive tests：`internal/tools/websearch_test.go:633-646`

判断：NEEDS-PRECONDITION。

理由 1：P0-B 已落地，且当前契约不是 JSON envelope。

agents-hive 在注册 websearch 时已经从 `cfg.Agent.QualityGuards.WebsearchStrict` 读取 strict flag，见 `internal/tools/tools.go:179-186`。

strict 模式下 `rawCount == 0` 会返回 `errorResult(...)`，即 `ToolResult.IsError=true`，见 `internal/tools/websearch.go:188-196` 和 `internal/tools/tools.go:1096-1098`。

测试也锁定了 “strict + raw==0 必须 IsError=true”，见 `internal/tools/websearch_test.go:633-646`。

这已经解决了“空结果静默进入 LLM 导致幻觉”的核心问题。

理由 2：JSON envelope 可能破坏当前 DecodeContent / UI / LLM 语义。

agents-hive 工具结果统一通过 `jsonText(text)` 包成 JSON 字符串，见 `internal/tools/tools.go:1100-1103`。

`mcphost.DecodeToolContent` 会把 JSON 字符串解码成纯文本，见 `internal/mcphost/host.go:31-73`。

如果 websearch 改成 JSON object envelope，`DecodeToolContent` 目前对 object 没有专门解析，最终可能把原始 JSON 字符串展示给模型和前端。模型能看懂，但与当前所有工具的“文本错误 + IsError”风格不一致。

理由 3：agents-hive 已区分 raw_empty 与 filter_empty，直接套 deer-flow envelope 会退化语义。

agents-hive 当前严格区分：

- `rawCount == 0`：真零结果 / 反爬 / HTML 变更，`IsError=true`。
- `rawCount > 0 && filtered == 0`：域名过滤导致空，非 `IsError`，提示调整域名过滤。

代码见 `internal/tools/websearch.go:184-205`。

测试也覆盖了 filter 清零不应 IsError，见 `internal/tools/websearch_test.go:642-646`。

如果统一写 `{"error":"No results found"}`，会抹掉这个重要差异。

理由 4：若要 envelope，应先标准化 ToolError schema，而不是只改 websearch。

当前 tools 中大量 `errorResult("...")` 走文本错误。只让 websearch 返回 JSON object，会导致工具错误格式多样化。

如果要引入 envelope，应定义全局 schema，例如：

```json
{
  "error": {
    "code": "websearch.raw_empty",
    "message": "...",
    "retryable": true,
    "query": "...",
    "raw_count": 0,
    "filtered_count": 0
  }
}
```

然后更新 `DecodeToolContent`、前端 renderer、LLM prompt、tests。

结论：P0-B 不应作为“立刻动手”P0。最多做 P1 的统一错误结构化，但不能破坏已落地 strict 契约。

## 2. 对 3 个“别抄”建议的 agents-hive 陷阱判断

### 2.1 别抄进程内 RunManager + 多 worker：未来陷阱，当前未直接踩

合并报告建议：

> “`RunManager` + `MemoryStreamBridge` 进程内状态 × 多 worker 组合不要抄。”

文件锚点：

- 报告 takeaway：`docs/research/deer-flow/merged-report.md:524`
- 报告并表风险：`docs/research/deer-flow/merged-report.md:28`
- agents-hive EventBus：`internal/master/event_bus.go:39-92`
- agents-hive session Postgres store：`internal/store/postgres.go:145-252`
- agents-hive bootstrap streaming executor：`internal/bootstrap/server.go:235`
- agents-hive server entry：`cmd/server/main.go:66`

判断：这是真陷阱，但 agents-hive 当前不是 deer-flow 同款问题。

理由 1：agents-hive 没有 HTTP RunManager 概念。

agents-hive 当前核心是 `Master.ProcessMessage` / session loop / EventBus，而不是 gateway run object。EventBus 是进程内订阅广播，见 `internal/master/event_bus.go:39-92`。

这确实不跨进程，但它服务的是 WebSocket/渠道实时渲染，不是持久 run join/replay API。

理由 2：agents-hive 已经把 session 存储放到 Postgres，而不是 run 全量内存。

`PostgresStore` 提供 session create/save/load/delete 和 messages 读写，见 `internal/store/postgres.go:145-252`、`internal/store/postgres.go:408-460`。

这与 deer-flow `RunManager` in-memory dict 不是一个级别的问题。

理由 3：EventBus 仍是未来多副本实时流的风险。

如果 agents-hive 部署多个 server 实例，同一 session 的 WebSocket 订阅、HITL input、agent status、tool_call event 都在本进程 EventBus 内。跨实例不会自动可见。

`Broadcast` 只遍历本进程 `subs map`，见 `internal/master/event_bus.go:159-214`。

所以问题不是“RunManager + workers”，而是“实时事件通道未外置”。未来需要 Redis pub/sub、Postgres NOTIFY、NATS 或 sticky session。

理由 4：EventBus 还允许非关键事件丢弃。

EventBus 对非关键事件在 subscriber channel 满时直接丢弃，见 `internal/master/event_bus.go:46-56`、`internal/master/event_bus.go:176-214`。

这对 token partial 可以接受，对 P0-C tool_call delta 是否可接受要重新判断。tool_call ready 不能是普通非关键事件。

结论：报告方向正确，但对 agents-hive 应改写为“不要把 P0-C/P0-D 依赖的关键运行时事件只放进进程内 EventBus；当前不是 RunManager 问题，是 realtime bus 问题”。

### 2.2 别抄硬编码线程池 3+3+3：部分已踩，但形态不同

合并报告建议：

> “硬编码线程池 3+3+3 不要抄。”

文件锚点：

- 报告 takeaway：`docs/research/deer-flow/merged-report.md:526`
- 报告蓝军 6：`docs/research/deer-flow/merged-report.md:283-298`
- agents-hive spawn filter：`internal/master/react_processor.go:779-787`
- agents-hive AgentFactory 默认限额：`internal/subagent/factory.go:52-79`
- agents-hive spawn_agent 30 分钟等待：`internal/tools/spawn_agent.go:120-123`
- agents-hive AgentLoop maxTurns：`internal/subagent/agentloop.go:72-100`
- agents-hive StreamingExecutor goroutine：`internal/master/streaming_executor.go:81-106`

判断：这是真陷阱，agents-hive 已有相邻风险。

理由 1：agents-hive 没有 deer-flow 的 3 个 ThreadPoolExecutor。

Go runtime 使用 goroutine，不是固定 3 worker pool。`StreamingExecutor.AddTool` 对 safe 工具直接 `go se.runTool(tool)`，unsafe 工具排队，见 `internal/master/streaming_executor.go:81-106`。

所以“3+3+3 线程池”不是 agents-hive 当前实现。

理由 2：agents-hive 有硬编码 spawn 限额。

Master 单轮最多 3 个 `spawn_agent`，写死在 `const maxConcurrentSpawn = 3`，见 `internal/master/react_processor.go:779-787`。

AgentFactory 默认 `maxPerSession=3`、`maxGlobal=30`、`maxSpawnDepth=1`，见 `internal/subagent/factory.go:52-79`。

这些不是线程池，但同样是隐藏容量策略。

理由 3：spawn_agent 是同步长等待，会占住当前 ReAct 工具执行路径。

`spawn_agent` 创建 agent 后立即 `ExecuteTask`，使用 30 分钟兜底 timeout，见 `internal/tools/spawn_agent.go:120-123`。

如果 safe tool 并发路径启用，多个长任务会变成 goroutine 并发；如果串行路径，Master turn 被长时间占住。

理由 4：safe 工具无限 goroutine 有另一类容量风险。

`StreamingExecutor` 没有 worker pool 上限，safe 工具每个开 goroutine。并发上限依赖 LLM 一轮 tool_calls 数、工具 schema 和外层 guard。

这比硬编码 3 更灵活，但也需要 backpressure。

理由 5：配置入口不统一。

`AgentFactory` 有 `SetMaxPerSession` / `SetMaxGlobal`，见 `internal/subagent/factory.go:125-137`。

但 Master 单轮 spawn limit 是局部 const，不一定跟配置一致。未来应把 per-turn、per-session、global、depth、tool concurrency 放到同一个 config section。

结论：不要照抄 3+3+3 是对的；agents-hive 应补的是统一容量治理，不只是“不要硬编码线程池”。

### 2.3 别抄 skill 语义匹配：已经部分踩，但有补救结构

合并报告建议：

> “Skill 触发靠 LLM 语义匹配不要抄。”

文件锚点：

- 报告别抄：`docs/research/deer-flow/merged-report.md:475`
- agents-hive skill tool：`internal/tools/skill.go:51-175`
- agents-hive ListForModel：`internal/skills/registry.go:336-345`
- agents-hive Finder metadata only：`internal/skills/finder.go:81-90`
- agents-hive skill registry lazy invoke：`internal/skills/registry.go:253-281`
- agents-hive self-heal：`internal/tools/skill.go:177-207`

判断：是真陷阱，agents-hive 已经部分采用，但不完全等同 deer-flow。

理由 1：agents-hive 模型可见的是一个通用 `skill` 工具。

`skill` 工具 schema 允许 `name` 为空列出技能，或传入 `name` 调指定技能，见 `internal/tools/skill.go:51-99`。

这意味着“何时调用 skill、调用哪个 skill”仍主要靠 LLM 从描述中判断。

理由 2：agents-hive registry 提供了比 deer-flow 更强的宿主约束。

`ListForModel` 会过滤 `DisableModelInvocation`，见 `internal/skills/registry.go:336-345`。

Finder 只先加载 metadata，见 `internal/skills/finder.go:81-90`。

Registry 支持 personal/public scope、tenant isolation、name validation，这些减少了“纯 prompt 技能匹配”的风险。

理由 3：按 name 调用仍不是强制路由。

如果用户说“用某某技能”，LLM 可能不调用 `skill`，也可能 name 拼错。当前自愈只在 `Get` 失败后提供 suggested_action，见 `internal/tools/skill.go:177-207`。

这对“没调用 skill”无能为力。

理由 4：agents-hive 已有 spec requirements 的非语义匹配雏形。

`FindBySpecRequirements` 可按 `ProvidesRequirements` 做宿主匹配，见 `internal/skills/registry.go:360-400`。

报告没有把这个作为 agents-hive 的差异化路径。未来应优先把 skill selection 从 LLM 语义移动到 spec/task requirement resolver，而不是继续增大 prompt。

结论：报告警告成立，但 agents-hive 不是“本来没考虑”。它已经有 registry/filter/self-heal/spec requirement 的骨架，应继续把 skill activation 变成宿主决策，而不是复制 deer-flow prompt-based progressive loading。

## 3. 对 16 条蓝军里 3 条最可疑结论的独立复核

### 3.1 蓝军 4：`client.py:615-680` 是 P0-C 最直接参考实现

原报告结论：

> “这是 agents-hive P0-C（流式 tool_call 合并）最直接的参考实现。”

文件锚点：

- 报告蓝军 4：`docs/research/deer-flow/merged-report.md:243-261`
- deer-flow client：`/tmp/deer-flow-src/backend/packages/harness/deerflow/client.py:615-680`
- agents-hive LLM stream：`internal/llm/stream_responses.go:119-139`
- agents-hive Master callback：`internal/master/react_processor.go:357-416`

复核结论：源码事实 PASS，迁移结论 PARTIAL。

事实层面，deer-flow 确实在 messages mode 同时处理 AI text、AI tool_calls、ToolMessage。

但对 agents-hive 来说，“最直接参考”只能作为事件分类思路，不是施工参考。

原因 1：agents-hive 已经在 LLM adapter 层有 tool call stream chunk，不缺 parser。

Responses 和 Chat Completions 两条 stream adapter 都会把 ToolCalls 放入 `StreamChunk`，见 `internal/llm/stream_responses.go:119-139` 和 `internal/llm/stream_completions.go:302-325`。

原因 2：缺口在 Master 回调吞掉 pure tool_call chunk。

`react_processor.go` 的回调遇到没有文本/推理的 chunk 直接 return，见 `internal/master/react_processor.go:361-364`。

原因 3：Chat Completions 当前 emit 的是累积 snapshot，不是 complete event。

直接按 deer-flow `AIMessage.tool_calls` 的“完整 tool call”语义理解，会误执行半成品 arguments。

原因 4：deer-flow 的 ToolMessage 是 graph 后续节点产生的消息；agents-hive 的 tool result 是 `executeTool` 写入 session 后广播，见 `internal/master/react_processor.go:848-861`。

所以“AIMessage/ToolMessage”在 Go 里没有一对一类型对应。应建立 agents-hive 自己的 `StreamEvent` 类型。

### 3.2 蓝军 15：backend 测试 110 个，所以 middleware behavior contract 更可信

原报告结论：

> “backend 测试覆盖相当密集……这也反过来证明了 middleware 顺序 / rollback / stream 等不变量是被 test 锁住的。”

文件锚点：

- 报告蓝军 15：`docs/research/deer-flow/merged-report.md:391-400`
- deer-flow tests count：`/tmp/deer-flow-src/backend/tests/test_*.py`

复核结论：数字 PASS，推论 FAIL。

原因 1：测试文件数量不是行为契约覆盖。

110 个 `test_*.py` 只能说明文件多。它不能证明 P0-C 相关的 stream tool_call behavior 被锁住。

原因 2：报告自己已经发现 stream_mode events 前后端漂移。

报告蓝军 9 显示前端支持 `"events"`，后端 worker 跳过 `"events"`，见 `docs/research/deer-flow/merged-report.md:314-328`。

如果 stream 不变量真的被测试锁住，这种协议裂缝不应存在。

原因 3：报告自己也发现 schema/runtime 漂移。

`enqueue` 501、`command/checkpoint` 半接入都被蓝军确认，见 `docs/research/deer-flow/merged-report.md:330-420`。

这说明测试数量没有阻止 API contract 漂移。

原因 4：agents-hive 不能因为 deer-flow 测试多就信任 middleware 顺序。

即使 deer-flow 的 ClarificationMiddleware 顺序被测，agents-hive 没有同构 middleware chain。迁移时仍要给 Go runtime 写自己的 ordering test。

### 3.3 蓝军 12：build_tracing_callbacks 生产调用点缺失只是 PARTIAL

原报告结论：

> “PARTIAL（值得追查）。”

文件锚点：

- 报告蓝军 12：`docs/research/deer-flow/merged-report.md:424-439`

复核结论：作为 deer-flow 调研可 PARTIAL；作为 agents-hive 借鉴应视为 FAIL 信号。

原因 1：观测链路如果只有 factory 定义和 tests 命中，不能作为可抄实现。

报告说生产调用点未找到，这就足够说明它不应进入“结构可借鉴”清单。

原因 2：agents-hive 已经有自己的 span/metric enqueue。

Master 在 LLM call 和 tool execute 中写 span/metric，见 `internal/master/react_processor.go:498-535`、`internal/master/react_processor.go:1106-1123`、`internal/master/react_processor.go:1203-1210`。

对 agents-hive 来说，LangChain callback 接入模式不是主路径。

原因 3：P0-C/P0-D 都需要 runtime-level event/validation observability。

如果照抄一个“可能靠 env global tracer 自动接入”的方案，无法保证 tool_call delta drop、validator fail、retry/fail 等关键事件可观测。

原因 4：合并报告没有把“观测先行”列为 P0 前置，这是盲点。

P0-C 改 event contract 时，必须同时加 metrics：tool_call_delta_received、tool_call_ready_broadcast、tool_call_ready_dropped、tool_call_execute_started、validator_failed 等。

## 4. agents-hive staff engineer 视角的 P0 排序建议

### 4.1 第一优先：P0-C，但定义为 stream event contract，不是 client.py 移植

排序：P0-C-1。

原因：

- P0-A tool_choice 已落地后，required 模式下模型是否真的产生 tool_call 是核心闭环。
- LLM adapter 已经提供 ToolCalls chunk，但 Master 层吞掉了非文本 chunk，见 `internal/master/react_processor.go:357-364`。
- 当前只在最终 response 后广播 tool_calls，见 `internal/master/react_processor.go:664-707`。
- 若 provider 在 tool_call streaming 上有异常，用户/前端/ACP 缺少实时证据。

建议拆分：

1. `StreamChunk` 增加事件类型。
2. Responses API 只在 arguments done emit ready。
3. Chat Completions 先只 emit snapshot 到 diagnostics，不执行。
4. Master callback 广播 `tool_call_ready`，不立即 early execute。
5. EventBus 将 tool_call_ready 标记为 critical。
6. 增加 ordering tests，确保 P0-A bad content suppression 不吞 tool_call ready。

### 4.2 第二优先：P0-D，但先做最小 post-answer validator，不引入全 middleware

排序：P0-D-1。

原因：

- 当前 websearch strict 只能保证工具失败时模型不应凭记忆回答，不能验证最终回答是否引用了工具结果。
- grounding validator 应在 final assistant persist/broadcast 前执行。
- 该插入点与 P0-A guard 的防泄露顺序一致，见 `internal/master/react_processor.go:560-604`。

建议：

1. 定义 `GroundingValidator` 接口。
2. 在 `TriggerChatAfter` 之前插入。
3. 失败时追加 system corrective message，并 retry 一次。
4. 先只对 websearch/webfetch 触发，避免所有任务误伤。
5. 输出结构化 validator failure event 和 metric。

### 4.3 第三优先：P0-B 结构化错误兼容，不改核心 strict 语义

排序：P0-B-compat。

原因：

- P0-B strict 已落地，见 `internal/tools/websearch.go:184-205`。
- 当前 error text 对 LLM 已足够明确。
- 贸然改 JSON envelope 会破坏 `DecodeToolContent` 和 UI 的文本假设。

建议：

1. 保持 `IsError=true`。
2. 在 metadata 或 JSON-string 内引入 machine-readable code，而不是改成 JSON object。
3. 统一 ToolError schema 后再迁移 websearch。
4. 保留 raw_empty/filter_empty 区分。

### 4.4 第四优先：统一 tool/agent 容量治理

排序：P0-adjacent。

原因：

- 单轮 spawn limit、per-session/global dynamic agent limit、safe tool goroutine 并发、长工具 timeout 分散在多个文件。
- 这会影响 P0-C early execution 是否安全。

文件锚点：

- `internal/master/react_processor.go:779-787`
- `internal/subagent/factory.go:52-79`
- `internal/master/streaming_executor.go:81-106`
- `internal/tools/spawn_agent.go:120-123`

建议：

1. 配置化 per-turn spawn limit。
2. 配置化 safe tool max concurrency。
3. 对 long-running tools 加独立 admission control。
4. metrics 记录排队、拒绝、超时。

### 4.5 暂不建议做：完整 middleware 抽象重构

原因：

- 对 P0-D 来说成本过高。
- 当前 `runReActLoop` 已有多个顺序不变量，贸然拆分容易引入泄露路径。
- 可以先用小接口封装 validator 和 stream event router，等 P0 稳定后再抽 processor chain。

## 5. 合并报告没识别出的技术盲点

### 5.1 盲点 1：P0-C 的 provider 差异不是同一语义

文件锚点：

- Responses done event：`internal/llm/stream_responses.go:119-139`
- Chat Completions snapshot：`internal/llm/stream_completions.go:302-325`

说明：

Responses API 有明确 arguments done；Chat Completions 当前只看到 delta accumulation。报告把“stream tool_call”抽象成单一问题，没有识别 provider-specific readiness。

影响：

如果实现 early execution，Responses 路径可以较安全，Chat Completions 路径可能执行半截 JSON。

### 5.2 盲点 2：StreamingExecutor 并发路径丢失 tool call ID

文件锚点：

- executor 签名无 ID：`internal/master/streaming_executor.go:18-20`
- 并发路径用 fingerprint 回查：`internal/master/react_processor.go:1929-1955`
- append result 用原始 toolCall ID：`internal/master/react_processor.go:1977-2000`

说明：

`executeToolsConcurrent` 里 executor 闭包无法拿到 ID，只能用 canonical fingerprint 找 pendingCall。注释承认“同一轮 name+args 的 toolCtx 一致”，见 `internal/master/react_processor.go:1929-1955`。

风险：

同一轮两个相同 name+args 但不同 call ID 的工具调用，在执行层 trace/log/tool event 可能缺 ID 或错配。P0-C 如果引入早执行，会放大这个问题。

### 5.3 盲点 3：ToolBridge plugin hook 可改写工具输出，validator 证据可能被污染

文件锚点：

- ToolExecuteAfter 改写 output：`internal/skills/toolbridge.go:160-174`
- ExecuteDirect after hook：`internal/skills/toolbridge.go:267-281`

说明：

ToolBridge after hook 会把 `hookOutput.Output` 重新 marshal 回 `result.Content`。grounding validator 如果基于工具结果验证，必须知道它验证的是原始工具证据还是插件改写后的证据。

风险：

插件可能清洗掉 URL/source，导致 validator 误判；也可能注入未验证文本，导致 validator 被绕过。

### 5.4 盲点 4：EventBus 对非关键事件可丢，与 P0-C tool_call delta 冲突

文件锚点：

- critical event 列表：`internal/master/event_bus.go:24-37`
- 非关键满通道丢弃：`internal/master/event_bus.go:176-214`

说明：

当前 critical event 包含 input_request/input_response/error/agent_status，但不包含 message/tool_call delta 的更细分类型。

风险：

如果 tool_call ready 走普通 message 或非关键 event，慢订阅者可能丢失关键调用可见性，导致 UI/ACP 状态不一致。

### 5.5 盲点 5：P0-A partial suppression 与 P0-C 有直接冲突

文件锚点：

- suppression：`internal/master/react_processor.go:385-394`
- required guard：`internal/master/react_processor.go:560-604`

说明：

P0-A 为避免 required 模式坏文本泄露，抑制 stream partial。这个策略合理，但目前 suppression 放在 chunk callback 内，且 callback 先过滤无文本 chunk。

风险：

如果 P0-C 只在 callback 里加 tool_call 广播，但仍在 suppression 后，required 模式下可能继续吞 tool_call event。

### 5.6 盲点 6：P0-D 缺 source metadata，validator 很难可靠

文件锚点：

- websearch result fields：`internal/tools/websearch.go:28-33`
- formatSearchResults 输出路径：`internal/tools/websearch.go:204-205`

说明：

websearch 内部有 title/url/description，但最终给 LLM 的是格式化文本。grounding validator 如果只看文本，很难稳定抽取 source。

建议：

为 tool result 增加 metadata 或 artifact-like evidence store，至少保留 source URL、query、timestamp、raw_count、filtered_count。

### 5.7 盲点 7：tool error 的 terminal/non-terminal 分类是字符串匹配

文件锚点：

- terminal patterns：`internal/master/react_processor.go:935-963`
- terminal cache：`internal/master/react_processor.go:820-839`

说明：

错误是否 terminal 由字符串 contains 判断。JSON envelope 如果引入 code/retryable，可以替换这套脆弱匹配。

但这应作为统一 ToolError schema 做，不应只改 websearch。

### 5.8 盲点 8：subagent stream 也吞 tool_call chunk

文件锚点：

- subagent callback：`internal/subagent/agentloop.go:203-217`

说明：

SubAgent 的 stream callback 同样只把 content/reasoning 推给 `streamFn`，不处理 `chunk.ToolCalls`。

影响：

即使 Master 修了 P0-C，SubAgent 路径仍然不可见 tool_call streaming。报告只看 Master/P0，没有覆盖 subagent loop。

### 5.9 盲点 9：P0-C early execution 与 HITL 权限审批冲突

文件锚点：

- permission check：`internal/master/react_processor.go:1022-1039`
- approvedCalls cache：`internal/master/react_processor.go:842-867`

说明：

如果流中 tool_call ready 后立即执行，HITL 审批可能在 assistant final message persist 前发生。前端看到的审批卡片与 assistant tool_call 消息顺序可能倒置。

必须先定义：assistant tool_call message 是否在 tool execution 前持久化。

### 5.10 盲点 10：报告没有识别 agents-hive 的 spec-driven 路径会影响 skill/validator 设计

文件锚点：

- spec context at ReAct entry：`internal/master/session_loop_specdriven_react.go:7-49`
- skill requirement resolver：`internal/skills/registry.go:360-400`

说明：

agents-hive 已有 spec-driven context 和 requirement matching。P0-D grounding validator 可以利用 spec context 限定“需要证据的 task”，skill selection 也可以用 requirements，而不是纯 prompt。

报告完全从 deer-flow LangGraph 视角出发，漏了 agents-hive 自己的 spec-driven architecture。

## 6. 对合并报告具体措辞的修改建议

### 6.1 原句：P0-C 抄 `client.py:615-680`

建议改为：

> P0-C 应借鉴 deer-flow 的“stream 事件必须同时表达 text、tool_call、tool_result”原则，但 agents-hive 不应直接抄 `AIMessage/ToolMessage` 分支。Go 侧先补 `llm.StreamChunk` 工具调用事件类型、Master callback 消费、EventBus critical event 和 provider-specific ready semantics。

### 6.2 原句：P0-D grounding validator 本质就是另一个 wrap_tool_call

建议改为：

> P0-D 不能直接类比 `wrap_tool_call`。agents-hive 当前没有 AgentMiddleware；最小实现应插在 final assistant persist/broadcast 前，作为 post-LLM validator。只有未来抽象 processor chain 时，才考虑把它做成 middleware。

### 6.3 原句：P0-B 增强返回 error envelope

建议改为：

> P0-B strict 已落地。下一步不是单独把 websearch 改 JSON envelope，而是设计全局 ToolError schema，在保持 `IsError=true` 和 raw_empty/filter_empty 语义的前提下增加 machine-readable code。

### 6.4 原句：别抄 RunManager + MemoryStreamBridge

建议改为：

> 对 agents-hive 当前风险应表述为：不要让关键 runtime event 只存在进程内 EventBus。若未来支持多副本 WebSocket/HITL/P0-C tool_call ready，需要外置 pub/sub 或 sticky session。

### 6.5 原句：别抄硬编码线程池 3+3+3

建议改为：

> agents-hive 的对应风险是容量治理分散：单轮 spawn=3、per-session dynamic agents=3、global=30、safe tool goroutine 无上限、spawn_agent 30 分钟同步等待。应统一配置和 admission control。

### 6.6 原句：别抄 skill 语义匹配

建议改为：

> agents-hive 已有 skill registry/scope/disable_model_invocation/self-heal/spec requirements，应继续把 skill activation 从 LLM 语义判断迁移到宿主 requirement resolver。

## 7. 最终判定表

| 报告建议 | 我的判断 | 关键理由 |
|---|---|---|
| P0-C 抄 `client.py:615-680` | NEEDS-PRECONDITION | Go 侧已有 tool_call chunk，缺的是 Master/EventBus 事件契约；AIMessage/ToolMessage 无一对一对应 |
| P0-D 抄 `tool_error_handling_middleware.py:19-65` | FAIL | agents-hive 无 `wrap_tool_call`；grounding validator 是 post-answer 验证，不是 tool exception wrapper |
| P0-B 空结果 error envelope | NEEDS-PRECONDITION | strict 已落地；直接 JSON envelope 会冲突 `IsError + text` 契约并抹掉 raw_empty/filter_empty |
| 别抄 RunManager + 多 worker | TRUE BUT FUTURE | 当前无 RunManager，但进程内 EventBus 是未来多副本实时事件风险 |
| 别抄硬编码线程池 3+3+3 | TRUE, DIFFERENT SHAPE | Go 没 3 pool，但有硬编码 spawn limits、无限 goroutine 和长等待 |
| 别抄 skill 语义匹配 | TRUE, PARTIAL | 已部分靠 LLM 选择 skill；但 registry/scope/spec requirements 提供了更好的宿主路由基础 |

## 8. 结论

合并报告最值得保留的洞察是：deer-flow 证明“流式 agent runtime 不能只处理文本 token，必须把 tool_call 和 tool_result 当一等事件”。

但它最危险的表达也是这里：它把 LangGraph `AIMessage/ToolMessage/AgentMiddleware` 当成可直接迁移的施工图。

agents-hive 的 Go runtime 真实边界是：

- `llm.StreamChunk`
- `Master.runReActLoop`
- `EventBus`
- `ToolBridge`
- `mcphost.Host`
- `SessionState` / Postgres store
- `subagent.AgentLoop`

所以 staff engineer 视角的正确落点是：

1. 先把 P0-C 做成 Go-native stream event contract。
2. 再把 P0-D 做成 final answer 前的最小 validator。
3. 保持 P0-B strict，不急着改 JSON envelope。
4. 同步补 runtime event observability 和容量治理。
5. 暂缓完整 middleware 抽象，避免为了模仿 LangGraph 重构 Go 主循环。

