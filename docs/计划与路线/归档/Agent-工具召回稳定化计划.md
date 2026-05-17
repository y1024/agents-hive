# Agent 工具召回稳定化计划

> 状态：COMPLETED  
> 类型：配置化 / 观测 / 回归集 / 风险分层  
> 结论：旧计划不按原文实施。当前代码已经有 per-turn tool recall、`tool_search`、session discovered tools 和 plan mode gate，本计划只补稳定化收尾。  
> 优先级：P1，排在长任务续航基准之后。

## 0. 完成记录

完成日期：2026-05-07。

本计划已完成工具召回稳定化收尾。完成范围包括：

- `internal/config/config.go`、`internal/config/defaults.go` 已新增 `agent.tool_recall` 配置，支持 `off` / `observe` / `inject`、limit、普通阈值、副作用阈值和候选日志开关。
- `internal/bootstrap/helpers.go`、`internal/bootstrap/server.go`、`internal/cli/app.go`、`internal/master/master.go` 已接通运行时配置读取和 Master 注入。
- `internal/master/tool_visibility.go` 已实现配置驱动召回、per-turn 注入、不持久化 discovered state、plan mode gate 和副作用工具阈值过滤。
- `internal/agentquality/types.go`、`internal/master/quality_events.go`、`internal/master/react_processor.go` 已接入 `quality.tool_recall`，并在模型响应后补齐 `selected_tool` / `model_used_recalled_tool`。
- `internal/agentquality/testdata/aq08_tool_recall_feishu_send.json`、`aq09_tool_recall_no_send.json`、`aq10_tool_recall_plan_gate.json` 已进入 agentquality 基础回归集，其中危险飞书发送用例要求召回命中但最终进入 `needs_user` 权限确认。
- `README.md` 已记录 `mode=off/observe/inject`、回滚方式和 `log_candidates=false` 的隐私效果。

已验证：

```bash
go test ./internal/config -run 'ToolRecall|Config' -count=1
go test ./internal/master -run 'ModelVisibleTools|ToolRecall|PlanMode' -count=1
go test ./internal/tools ./internal/master -run 'ToolSearch|ToolRecall|SideEffect' -count=1
go test ./internal/master ./internal/agentquality -run 'ToolRecall|QualityEvent' -count=1
go test ./internal/agentquality ./internal/master ./internal/tools -run 'AgentQuality|ToolRecall|ToolSearch|ModelVisibleTools' -count=1
go test ./... -count=1
```

后续增强项：

- `agentquality` 召回回归目前以静态 fixture/gate 为主；如需度量真实 Top-K 命中率和误召回率，应单独增加实际调用 `RecallToolCatalog()` / model-visible 路径的 runner。
- §7 中微信联系人、飞书文档查询、README 结构读取等样例尚未全部固化为 case，可作为下一轮覆盖率增强处理。
- `config.example.json` 未加入示例，因为当前生产推荐通过数据库运行时配置控制；如后续改为文件配置优先，应补示例。

## 1. 当前代码事实

已经具备：

- `internal/tools/tool_search.go`：已有 `RecallToolCatalog()`，按 name、description、schema、中文 alias 召回工具。
- `internal/master/tool_visibility.go`：已有每轮自动召回，并通过 `perTurnToolRecallLimit = 5` 临时加入 model-visible tools。
- `internal/master/tool_visibility.go`：per-turn recall 不写入 `session.RecordDiscoveredTools()`。
- `internal/master/tool_visibility.go`：plan mode gate 在候选进入模型前生效。
- `internal/master/tool_visibility_test.go`：已有飞书发送召回、plan mode gate、显式 discovery 的测试。

真正缺口：

- 召回模式、limit、min score 仍是硬编码或缺失。
- 召回结果没有结构化质量事件，难以在 replay/trace 里解释“为什么这个工具进了候选”。
- 没有专门的工具召回质量回归集。
- 副作用工具没有更高召回门槛，容易增加审批噪音。
- 默认行为没有迁移说明，贸然改默认可能造成现有能力回退。

## 2. 继续推进范围

本计划继续推进，但只做五件事：

1. 把当前 per-turn recall 配置化。
2. 保持默认行为不回退。
3. 增加结构化 `quality.tool_recall` 事件。
4. 增加工具召回正负例回归集。
5. 对副作用工具加风险分层阈值。

## 3. 不再推进的旧范围

以下内容不在本计划推进：

- 不重写工具系统。
- 不扩主 system prompt。
- 不新增平台专用快捷工具。
- 不把召回结果写入 discovered state。
- 不绕过 HITL 或权限策略。
- 不做 UI 管理页。

## 4. 配置设计

新增配置段：

```json
{
  "agent": {
    "tool_recall": {
      "mode": "inject",
      "limit": 5,
      "min_score": 0.35,
      "side_effect_min_score": 0.65,
      "log_candidates": true
    }
  }
}
```

字段语义：

- `mode = off`：不执行 per-turn recall。
- `mode = observe`：执行 recall 并记录事件，但不注入 model-visible tools。
- `mode = inject`：执行 recall、记录事件，并把候选临时注入本轮 model-visible tools。
- `limit`：每轮最多召回多少个隐藏候选。
- `min_score`：普通工具最低召回分数。
- `side_effect_min_score`：副作用工具最低召回分数。
- `log_candidates`：是否记录候选名称和分数。

默认策略：

- 代码默认保持当前行为：`mode=inject`、`limit=5`。
- 生产环境可通过配置切 `observe`。
- 回滚方式：设置 `mode=off`。

这个默认策略是为了避免把已有 per-turn recall 能力误降级。

## 5. 副作用工具分层

副作用工具定义：

- 发送外部消息。
- 写文件。
- 执行 shell。
- 调用外部 API 写操作。
- 删除、创建、更新远端资源。

首批可用启发式：

- `send_im_message`
- `feishu_api`
- `wechat_send_rich_message`
- `bash`
- `write_file`
- `edit`
- `multiedit`
- `apply_patch`

实现时允许先用工具名表，后续再接权限策略或 MCP metadata。

## 6. 质量事件设计

新增事件名：

```text
quality.tool_recall
```

事件字段：

- `mode`
- `turn_id`
- `trace_id`
- `query_preview`
- `candidate_count`
- `candidate_names`
- `candidate_scores`
- `visible_before_count`
- `visible_after_count`
- `selected_tool`
- `model_used_recalled_tool`
- `blocked_by_plan_gate`
- `side_effect_candidate_count`

隐私要求：

- `query_preview` 最长 80 字符。
- `log_candidates=false` 时不记录 candidate names 和 scores。
- 不记录完整用户消息。

## 7. 回归集

正例：

- `发送给飞书用户:郭松`
- `把刚才结论发给飞书用户郭松`
- `通知飞书群 oc_xxx：任务完成`
- `给微信联系人张三发消息`
- `查一下飞书文档里项目排期`

负例：

- `帮我润色一段飞书通知文案，但不要发送`
- `解释一下什么是飞书开放平台`
- `总结刚才的讨论，不要发给任何人`
- `进入 plan mode 后请求发送外部消息`
- `读取 README 并告诉我项目结构`

指标：

- 正例 Top-K 召回命中率 >= 90%。
- 普通非工具请求误召回副作用工具率 <= 5%。
- plan mode 发送类负例必须 100% 被 gate 拦住。
- `mode=observe` 不改变 model-visible tools。
- `mode=off` 不执行 recall。

## 8. 实施任务

### Task 1：新增配置结构和默认值

涉及文件：

- 修改：`internal/config/config.go`
- 修改：`internal/config/defaults.go`
- 修改：`internal/config/config_test.go`

实现要求：

- 新增 `ToolRecallConfig`。
- `AgentConfig` 增加 `ToolRecall ToolRecallConfig`。
- 默认 `mode=inject`、`limit=5`、`min_score=0.35`、`side_effect_min_score=0.65`。
- 非法 mode 回退到 `off` 或返回配置错误，必须有测试。
- 非法 limit / score 归一化，不能导致无限召回或全量召回。

验收命令：

```bash
go test ./internal/config -run 'ToolRecall|Config' -count=1
```

### Task 2：改造召回路径为配置驱动

涉及文件：

- 修改：`internal/master/tool_visibility.go`
- 修改：`internal/master/tool_visibility_test.go`
- 修改：`internal/master/react_processor.go`

实现要求：

- 替换硬编码 `perTurnToolRecallLimit` 的使用点。
- `off`：不调用 `tools.RecallToolCatalog()`。
- `observe`：调用 recall，但不合并进 visible tools。
- `inject`：调用 recall 并合并进 visible tools。
- per-turn recall 仍不写入 `RecordDiscoveredTools()`。
- plan mode gate 仍在候选进入模型前过滤。

验收命令：

```bash
go test ./internal/master -run 'ModelVisibleTools|ToolRecall|PlanMode' -count=1
```

### Task 3：增加 min score 和副作用阈值

涉及文件：

- 修改：`internal/master/tool_visibility.go`
- 修改：`internal/tools/tool_search.go`
- 修改：`internal/tools/tool_search_test.go`
- 修改：`internal/master/tool_visibility_test.go`

实现要求：

- `RecallToolCatalog()` 保持纯函数，不执行工具，不改变注册表。
- 可以新增过滤函数，如 `FilterToolRecallHits()`，按 score 和 side effect 阈值过滤。
- 副作用工具按 `side_effect_min_score` 判断。
- 普通工具按 `min_score` 判断。

验收命令：

```bash
go test ./internal/tools ./internal/master -run 'ToolSearch|ToolRecall|SideEffect' -count=1
```

### Task 4：记录 quality.tool_recall 事件

涉及文件：

- 修改：`internal/agentquality/types.go`
- 修改：`internal/master/quality_events.go`
- 修改：`internal/master/tool_visibility.go`
- 修改：`internal/master/quality_events_test.go`

实现要求：

- 新增 `EventToolRecall EventName = "quality.tool_recall"`。
- 事件通过 `emitQualityEvent()` 写入现有质量事件链路。
- 事件 attributes 包含 §6 字段。
- `log_candidates=false` 时不写候选明细。

验收命令：

```bash
go test ./internal/master ./internal/agentquality -run 'ToolRecall|QualityEvent' -count=1
```

### Task 5：关联最终使用工具

涉及文件：

- 修改：`internal/master/react_processor.go`
- 修改：`internal/master/tool_visibility.go`
- 修改：`internal/master/tool_visibility_test.go`

实现要求：

- 本轮 recall 结果需要能和后续 tool call 对齐。
- 如果模型调用了 recall 候选，事件中 `model_used_recalled_tool=true`。
- 如果工具被 plan mode gate 拦住，事件中 `blocked_by_plan_gate=true`。
- 不要求跨 turn 追踪。

验收命令：

```bash
go test ./internal/master -run 'ToolRecall|ToolCall|PlanMode' -count=1
```

### Task 6：新增 agentquality 回归集

涉及文件：

- 新增：`internal/agentquality/testdata/aq08_tool_recall_feishu_send.json`
- 新增：`internal/agentquality/testdata/aq09_tool_recall_no_send.json`
- 新增：`internal/agentquality/testdata/aq10_tool_recall_plan_gate.json`
- 修改：`internal/agentquality/loader.go`
- 修改：`internal/agentquality/eval_runner.go`
- 修改：`internal/agentquality/eval_runner_test.go`

实现要求：

- 覆盖 §7 正负例。
- case 失败时输出具体 case id。
- `StaticEvalRunner` 可先支持这些 case 的静态断言。
- 后续 live runner 单独立项，不塞进本计划。

验收命令：

```bash
go test ./internal/agentquality ./internal/master ./internal/tools -run 'AgentQuality|ToolRecall|ToolSearch|ModelVisibleTools' -count=1
```

### Task 7：补运行文档和回滚说明

涉及文件：

- 修改：`README.md` 或新增可追踪运维文档。
- 修改：`config.example.json`

实现要求：

- 写清楚 `mode=off/observe/inject`。
- 写清楚生产初始推荐 `observe`。
- 写清楚回滚配置为 `mode=off`。
- 写清楚 `log_candidates=false` 的隐私效果。

验收：

```bash
rg -n "tool_recall|mode=off|mode=observe|mode=inject" README.md config.example.json docs -S
```

## 9. 完成定义

该计划完成后可以归档：

- 工具召回配置化完成。
- 默认行为保持当前 inject 能力，不发生静默回退。
- `off` / `observe` / `inject` 三种模式都有测试。
- `quality.tool_recall` 可在 trace/replay 原始事件中定位。
- 副作用工具阈值有测试覆盖。
- 正负例进入 `agentquality`。
- README 或配置文档写明回滚方式。

## 10. 推荐实施顺序

1. Task 1 + Task 2：先把硬编码变成配置。
2. Task 3：再补副作用风险分层。
3. Task 4 + Task 5：补观测和最终使用关联。
4. Task 6：固化回归集。
5. Task 7：补文档和回滚说明。
