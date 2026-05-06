# deer-flow 调研最终裁决与落地方案

**审稿日期**: 2026-04-21
**仲裁输入**:
- `docs/research/deer-flow/merged-report.md`（4 报告双盲合并 + 16 条蓝军）
- `docs/research/deer-flow/debate/ceo-opinion.md`（CEO / 产品 ROI / 用户体感视角）
- `docs/research/deer-flow/debate/codex-opinion.md`（staff engineer / Go runtime 视角）

**仲裁人**: 主协调（综合代码事实、施工成本、用户体感）
**适用范围**: agents-hive Go runtime，不含 deer-flow 上游改动

---

## 0. 一页纸结论

| 维度 | 决议 |
|---|---|
| **要做的** | P0-C（分两步）、P0-D（post-answer validator，不建 middleware）、capacity governance 配置化、subagent 同步修 |
| **先别做** | 完整 Middleware Pipeline 重构、JSON envelope 全局改造、LangChain callback 接入 |
| **先识别再动工** | schema-runtime drift audit（agents-hive 自己版本）、react_processor.go 2013 行拆分 |
| **反向输出** | IM 渠道分级 streaming（EventRenderer/Feishu PatchCard）agents-hive 领先 deer-flow |

**一句话**: deer-flow 最值得保留的证据是"stream runtime 不能只处理文本 token，必须把 tool_call/tool_result 当一等事件"，这一条成立；但 `AIMessage/ToolMessage/AgentMiddleware` 这套 LangGraph 抽象不是 agents-hive 的施工图，**看证据、别抄类**。

---

## 1. 三个"立刻动手"的最终裁决

### 1.1 P0-C：流式 tool_call 可见 — **PASS，但拆两步做**

**CEO 立场**: PASS 9/10，抄 `client.py:615-680`，半天 ship，openai 路径后补 3 provider。
**Codex 立场**: NEEDS-PRECONDITION，先定义 `StreamChunk` 事件类型（Delta/Ready/Done），不然 Chat Completions 会执行半成品 JSON。
**分歧点**: CEO 看用户 ROI，认为"有广播总比黑屏强"；Codex 看 provider 差异，认为"半截 JSON 被执行比黑屏更糟"。

**仲裁**: 两人都对，合成两步走。

**裁决方案**:

- **Step 1（本周，0.5 天）**: 只做 **broadcast-only tool_call_ready**，不开 early execution。
  - 修改点：`internal/master/react_processor.go:362-364` 早 return 条件，改成 `if chunk.ContentSoFar == "" && chunk.ReasoningContent == "" && len(chunk.ToolCalls) == 0 { return nil }`
  - 新增事件：`EventTypeToolCallPreview`（非 critical），只给前端/IM 渠道看"模型正在酝酿工具调用"
  - 适配层：Responses API 路径天然 ready（`stream_responses.go:119-139` 的 `ResponseFunctionCallArgumentsDoneEvent` 后 emit）；Chat Completions 路径只在 `finish_reason=tool_calls` 或最终 chunk 时 emit，中间 snapshot **不广播**
  - 用户体感：IM 用户从"黑屏 30s"变成"3s 内看到 `正在调用工具: websearch`"

- **Step 2（下周，2 天）**: 补其余 provider + 评估 early execution。
  - Anthropic / DeepSeek / Gemini 的 tool_call delta 解析差异各写一个 test
  - Early execution（LLM 未结束就开始跑 tool）**暂不做**，要先解决 Codex 盲点 9（HITL 审批顺序）+ 盲点 2（tool_call_id 丢失）
  - 这条要单独立项，不属于 P0-C

**放弃的部分**:
- merged-report 第 518 行"抄 `client.py:615-680`"的措辞在 PR 描述里改成 "**借鉴事件分类原则**"，不要逐行翻译 Python `AIMessage/ToolMessage` 分支（Codex 理由 1-5 全部成立）

**前置必读**:
- Codex 盲点 5：P0-A partial suppression（`react_processor.go:385-394`）不能吞 tool_call ready，修改时加 ordering test
- Codex 盲点 8：`internal/subagent/agentloop.go:203-217` 同样 drop tool_call chunk，Master 修完必须同步修 subagent，否则 spawn 出去的 agent 依旧黑屏

---

### 1.2 P0-D：grounding validator — **FAIL「抄 wrap_tool_call」方案，改为 post-answer validator**

**CEO 立场**: NEEDS-PRECONDITION 4/10，前置条件 = 先造 Middleware Pipeline（15x P0-C 成本），merged-report 严重低估。
**Codex 立场**: FAIL，validator 根本不是 tool exception wrapper，应插在 `TriggerChatAfter` 前做 post-LLM 验证，**不要为 1 个 validator 大重构**。
**分歧点**: CEO 认为"迟早要建 Pipeline"；Codex 认为"validator 天然不是 middleware 的落点"。

**仲裁**: **Codex 对**。理由：

1. `wrap_tool_call` 的检查点是工具异常 → ToolMessage，grounding validator 的检查点是"最终回答是否被工具结果支撑"，**这两个发生在不同阶段**（Codex 理由 2）
2. agents-hive 的 `executeTool`（`react_processor.go:1093-1185`）已经把工具异常转成 `IsError=true` tool message，deer-flow 这段 middleware 价值**有限**（Codex 理由 4）
3. 为 1 个 validator 建完整 Middleware Pipeline 是典型"为了模仿 LangGraph 重构 Go 主循环"，CEO 自己的"make something people want"原则反对这种做法

**裁决方案**: P0-D 做**最小 post-answer validator**，不建 Middleware Pipeline。

- **落点**: 在 `react_processor.go:560-604` 的 P0-A guard 之后、`appendSessionMessage` + `BroadcastSessionMessage` + `TriggerChatAfter` **之前**
- **接口**:
  ```go
  type GroundingValidator interface {
      Name() string
      Validate(ctx context.Context, input ValidatorInput) (ValidatorResult, error)
  }

  type ValidatorInput struct {
      UserQuery     string
      FinalAnswer   string
      ToolMessages  []ToolMessage    // 本轮工具结果
      ToolMetadata  []ToolEvidence   // 来源 URL / query / raw_count / filtered_count
  }
  ```
- **失败处理**: validator fail 时**不 persist、不 broadcast**，改为追加 system corrective message + retry 一次；retry 后仍 fail 则 persist 但打 `validator_failed` metric + 前端显示告警
- **覆盖范围**: 第一版只对 websearch/webfetch 触发，避免全任务误伤
- **成本**: 约 P0-C 的 3 倍（不是 15 倍），1 人 1 周

**同步需要补**:
- Codex 盲点 6：为 tool result 增加 `ToolEvidence`（source URL、query、timestamp、raw/filtered count），否则 validator 没有稳定证据结构可用
- Codex 盲点 3：`ToolBridge.ToolExecuteAfter`（`toolbridge.go:160-174`）可改写工具输出，validator 必须明确"验证的是原始证据还是插件后的证据"，不然插件可以绕过或污染 validator

**未来再做（Q3 之后）**:
- 把 validator + P0-A guard + tool_call suppression + terminal detection 抽成 processor chain / plugin pipeline，那时再讨论"是否要 Middleware Pipeline"。**现在不做，避免无用重构**。

---

### 1.3 P0-B：空结果 error envelope — **DEFER，保持现状 + 设计全局 ToolError schema（P1）**

**CEO 立场**: DEFER 3/10，strict 已落地（`internal/tools/websearch.go:73-80`），不要动。
**Codex 立场**: NEEDS-PRECONDITION，不要单独把 websearch 改 JSON envelope，要先设计全局 ToolError schema。
**分歧点**: 程度不同，方向一致。

**仲裁**: **两人同向**。

**裁决方案**:
- **现在不做任何 websearch 改动**
- **P1 立项**: 设计全局 ToolError schema（由 Codex 建议）：
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
- **关键约束**:
  - 保持 `IsError=true` 语义不变
  - 保留 `raw_empty` vs `filter_empty` 区分（Codex 理由 3，测试在 `websearch_test.go:642-646`）
  - `DecodeToolContent`（`internal/mcphost/host.go:31-73`）需要同步升级支持 object envelope
  - 升级后 terminal 判定（`react_processor.go:935-963` 的字符串 contains）可以改成 `err.Code` + `err.Retryable` 判断，去掉脆弱的字符串匹配（Codex 盲点 7）
- **P1 排期**: Q3 前启动，和"统一 tool/agent 容量治理"同批做

---

## 2. 三个"别抄"的最终裁决

### 2.1 别抄 in-memory RunManager + multi-worker — **TRUE，但当前不是已踩坑**

**CEO**: 对 agents-hive 暂时是伪风险（当前无 run 对象、session 已在 Postgres）。
**Codex**: 真陷阱，但形态是"实时事件通道未外置"，不是"RunManager"。

**合并表述（更新 merged-report）**:
> agents-hive 当前没有 HTTP RunManager，session 已在 Postgres。真实风险是 `internal/master/event_bus.go:39-92` 是**进程内订阅广播**，未来多副本部署时 WebSocket / HITL / P0-C tool_call_ready 跨实例不可见。需要在走多副本之前切 Redis pub/sub / Postgres NOTIFY / NATS / sticky session，**不是改 RunManager**。

### 2.2 别抄硬编码线程池 3+3+3 — **TRUE，但形态不同**

**CEO**: Go 语境下无线程池，伪借鉴。
**Codex**: 真陷阱，形态是分散的硬编码 spawn limit + 无上限 goroutine + 30 分钟同步等待。

**合并表述（更新 merged-report）**:
> agents-hive 没有 ThreadPoolExecutor，但有：单轮 `maxConcurrentSpawn=3`（`react_processor.go:779-787`）、`AgentFactory.maxPerSession=3/maxGlobal=30`（`factory.go:52-79`）、safe tool 无上限 goroutine（`streaming_executor.go:81-106`）、`spawn_agent` 30 分钟兜底 timeout（`spawn_agent.go:120-123`）。需要做的是**统一容量治理**：配置化 per-turn spawn limit + safe tool max concurrency + long-running tool 独立 admission control + 排队/拒绝/超时 metric。这块**会影响 P0-C 是否敢开 early execution**，排到 P0 旁边。

### 2.3 别抄 skill 语义匹配 — **TRUE，但 agents-hive 已有补救骨架**

**CEO + Codex 一致**：agents-hive `ListForModel`（过滤 `DisableModelInvocation`）、scope、self-heal、`FindBySpecRequirements`（`registry.go:360-400`）已经比 deer-flow 多出一层宿主约束。

**合并表述（更新 merged-report）**:
> 继续把 skill activation 从"LLM 从描述里挑"迁移到"宿主 requirement resolver 决定"，利用已有的 `ProvidesRequirements`/spec context 做强制路由，**不继续增大 prompt**。

---

## 3. 最终 P0 优先级

按"用户体感 × 落地成本 × 风险"重排：

| 排序 | 工作项 | 成本 | 依赖 | 负责窗口 |
|---|---|---|---|---|
| **P0-1** | P0-C Step 1（broadcast-only tool_call_ready，Responses + Chat Completions done-only） | 0.5d | - | 本周 |
| **P0-2** | Subagent 同步修（`agentloop.go:203-217`） | 0.5d | P0-1 | 本周 |
| **P0-3** | agents-hive schema-runtime drift audit（借鉴 deer-flow 的 `enqueue` 501 / command-checkpoint 漂移方法） | 3d | - | 本周并行 |
| **P0-4** | P0-C Step 2（补 Anthropic/DeepSeek/Gemini tool_call delta + ordering test） | 2d | P0-1 | 下周 |
| **P0-5** | P0-D Minimal post-answer validator（先 websearch/webfetch） | 5d | ToolEvidence schema | 下周起 |
| **P0-6** | Capacity governance 配置化（spawn/goroutine/timeout） | 3d | - | P0-4 之后 |
| **P1-1** | 全局 ToolError schema + DecodeToolContent 升级 | 1w | - | Q3 |
| **P1-2** | EventBus 外置化评估（Redis/NATS/sticky session） | 2w | 多副本需求触发 | Q3 |

**CEO 原方案里被裁掉的**:
- ❌ "Middleware Pipeline 1.5w" 不做，P0-D 用最小 validator 替代
- ❌ "4 middleware impls 1.5w" 不做，等 Pipeline 需要时再说

**Codex 提到但延后的**:
- "完整 processor chain 抽象" 留给 P0-D 稳定后再看

---

## 4. 施工前必读（Codex 10 盲点 × P0 分类）

| 盲点 | 影响 P0 | 应对 |
|---|---|---|
| 1. Responses vs Chat Completions readiness 不同 | P0-C | Step 1 裁决方案已覆盖：只在 done 时 emit |
| 2. `executeToolsConcurrent` 并发路径丢 tool_call ID（fingerprint 回查） | P0-C Step 2 + P0-D | early execution 前必须先修；validator 需要 ID 对齐 tool result |
| 3. `ToolBridge.ToolExecuteAfter` 可改写 tool output 污染 validator 证据 | P0-D | validator 明确验证原始还是 plugin 后证据；或在 validator 前 snapshot 原始 content |
| 4. EventBus 非关键事件满通道丢弃（`event_bus.go:176-214`） | P0-C | `tool_call_ready` 必须归为 critical 事件；否则慢订阅者看不到 |
| 5. P0-A partial suppression 可能吞 tool_call | P0-C | suppression 分支内 `if len(chunk.ToolCalls) > 0 { emit }` |
| 6. websearch 没有 source metadata（URL/query/count） | P0-D | 先扩 `ToolEvidence` schema 再做 validator |
| 7. Terminal 判定是字符串 contains（`react_processor.go:935-963`） | P1 ToolError schema | schema 引入 `code/retryable` 后替换 |
| 8. SubAgent stream callback 同样 drop tool_call | P0-C | P0-2 同步修 |
| 9. P0-C early execution 与 HITL 审批顺序冲突 | P0-C Step 2 延后项 | early exec 前先明确 assistant tool_call message 何时 persist |
| 10. spec-driven context 被 merged-report 漏写 | P0-D + skill | validator 利用 `spec_context` 限定"需要证据的 task"；skill 继续走 requirement resolver |

---

## 5. merged-report 的具体措辞修订

针对最可能误导工程师的 4 处，强制改措辞（直接更新 `merged-report.md:455-475,518-520`）：

| 原句（merged-report） | 替换为 |
|---|---|
| "P0-C 流式 tool_call：抄 `client.py:615-680` 的 messages-mode 分支" | "P0-C：修复 `react_processor.go:362-364` 吞 tool_call chunk 的 bug；Responses API 路径在 `ResponseFunctionCallArgumentsDoneEvent` 后 emit；Chat Completions 路径仅在 `finish_reason=tool_calls` / 最终 chunk emit。**不翻译 AIMessage/ToolMessage 分支**，只借鉴"stream 事件同时表达 text/tool_call/tool_result"原则" |
| "P0-D grounding validator：抄 `tool_error_handling_middleware.py:19-65` 的套路；grounding validator 本质就是另一个 wrap_tool_call" | "P0-D：在 `react_processor.go:560-604` 的 P0-A guard 之后、`TriggerChatAfter` 之前插入 `GroundingValidator` 接口。**不建 Middleware Pipeline**，不做 `wrap_tool_call` 类比。失败 retry 一次，只覆盖 websearch/webfetch" |
| "P0-B 增强：把空结果提升到 error envelope" | "P0-B：保持现状（strict 已落地，`IsError=true` + 文本错误 + raw/filter 区分）。全局 ToolError schema 升级放 P1，不单独改 websearch" |
| "借鉴清单：LangGraph AgentMiddleware / create_agent() 可作为未来架构参考" | "借鉴清单：LangGraph `AgentMiddleware` 不是 agents-hive 施工图。Go runtime 真实边界是 `llm.StreamChunk` + `Master.runReActLoop` + `EventBus` + `ToolBridge` + `mcphost.Host` + `SessionState` + `subagent.AgentLoop`。借鉴时看**事件分类原则**，不抄**抽象类型**" |

---

## 6. agents-hive 的差异化（不要只当"借鉴方"）

CEO 辩手指出的反向输出点：

1. **IM 渠道分级 streaming 领先 deer-flow**: agents-hive 的 `EventRenderer` + `BroadcastSessionMessage` + Feishu `PatchCard` 已经做到"按事件类型分通道分级渲染"，deer-flow 只有单一 SSE 流。建议：整理成设计文档，作为 agents-hive 对外差异化点。
2. **skill registry 比 deer-flow 严格**: `DisableModelInvocation` / scope / tenant isolation / `FindBySpecRequirements` 都是 deer-flow 没有的宿主约束。继续投入 requirement-driven activation。
3. **spec-driven ReAct（`session_loop_specdriven_react.go`）是独有路径**: deer-flow 是"pure prompt agent"，agents-hive 有 spec context 可以给 validator 和 skill 做强约束依据。这是差异化机会，**不要丢掉**。

---

## 7. react_processor.go 监控项

CEO 指出的技术债：`react_processor.go` = 2013 行单文件巨 processor。

**结论**：**不做主动拆分**（会触及 P0-A/P0-C/P0-D 所有不变量），但设 guard：
- 任何新 PR 往这个文件加代码 > 50 行需要 review flag "consider extraction"
- P0-D validator 落点、P0-C 事件契约 merge 完后，把它拆到 `master/processors/` 下按职责分文件（LLM call / tool exec / stream callback / guard / terminal detect）是**下一阶段**立项

---

## 8. 三份报告交叉覆盖率

| 议题 | merged-report | ceo-opinion | codex-opinion | 本裁决采纳来源 |
|---|---|---|---|---|
| P0-C 施工方式 | 抄 client.py | PASS 9/10 半天 ship | 先定义 StreamChunk 事件类型 | **CEO 执行速度 + Codex provider 差异（两步走）** |
| P0-D 定位 | 类比 wrap_tool_call | 需先建 Pipeline | post-answer validator | **Codex：最小 validator，不建 Pipeline** |
| P0-B 处理 | 改 JSON envelope | DEFER | NEEDS-PRECONDITION（全局 schema） | **两人合流：保持现状，P1 全局 schema** |
| Middleware Pipeline | 推荐建 | 必须建（15x 成本） | 不建（为 1 validator 重构不值） | **Codex：不建，留到未来** |
| P0 排序 | 未排序 | P0-C → drift-audit → Pipeline → ... | P0-C → P0-D → P0-B compat → capacity | **Codex 排序为主 + drift-audit 并行** |
| 盲点识别 | 16 条蓝军 | 3 条战略 | 10 条代码级 | **Codex 代码级全量采纳，CEO 战略补充** |

---

## 9. 下一步（可直接 CRUD 的 checklist）

- [ ] 本周 D1（0.5d）：修 `react_processor.go:362-364` early return + `EventTypeToolCallPreview` 定义 + EventBus critical list 增补 + P0-A suppression 内补 emit
- [ ] 本周 D2（0.5d）：同步修 `agentloop.go:203-217` subagent stream callback
- [ ] 本周 D3-D5（3d）：agents-hive schema-runtime drift audit（借 deer-flow 方法）— 至少覆盖 ACP /api/run/ enqueue/command/events/checkpoint 路径 + tool schema Pydantic-equivalent vs runtime execution
- [ ] 下周 D1-D2（2d）：P0-C Step 2 补 Anthropic/DeepSeek/Gemini tool_call delta 解析
- [ ] 下周 D3 起（5d）：扩 `ToolEvidence` schema → 落地 `GroundingValidator` → 插 `TriggerChatAfter` 前 → websearch/webfetch 覆盖
- [ ] D+10（3d）：Capacity governance 配置化（spawn limit + safe tool concurrency + timeout admission control）
- [ ] **不做**：Middleware Pipeline 重构、JSON envelope 全局改造、LangChain callback 接入
- [ ] Q3 立项：全局 ToolError schema + EventBus 外置化（多副本触发）

---

## 10. 执行参考链路

CEO 意见原文：`docs/research/deer-flow/debate/ceo-opinion.md`
Codex 意见原文：`docs/research/deer-flow/debate/codex-opinion.md`
合并调研报告（需按第 5 节修订措辞）：`docs/research/deer-flow/merged-report.md`
原始 phase 输出（保留用于可追溯）：
- `docs/research/deer-flow/claude-phase1-output.md`
- `docs/research/deer-flow/claude-phase2-output.md`
- `docs/research/deer-flow/codex-phase1-output.md`
- `docs/research/deer-flow/codex-phase2-output.md`

P0 详细 spec（agents-hive 侧）：`docs/agent-quality-remediation-plan.md`

---

**最终口径**: deer-flow 是一次好用的"事件模型反例"调研，不是施工图。agents-hive 下一步的正确动作是"修 3 行 early return + 加 1 个 validator 接口 + 配置化容量治理"，总工期约 2 周，不是 merged-report 暗示的 1.5 个月。
