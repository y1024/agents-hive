# JS Widget 嵌入 + SDK 实施计划（修正版总览）

> **Status:** Accepted
> **Date:** 2026-05-16
> **Scope:** 已完成实施前拍板；后续按本总览和决策清单执行。

## 目标

为 Hive 增加一个 **Embed Ingress**：外部业务系统可以通过 Headless SDK 或 JS Widget 把 Hive 平台发布的 Agent 嵌入到业务流程里，并把租户、用户、页面、订单、审批流、业务对象等上下文注入 Agent harness。

这个能力不是单纯的网页聊天插件。正确抽象是：

```
外部业务系统
  -> SDK / Widget
  -> Embed Gateway
  -> Master Harness
  -> ReAct Loop
  -> Tool Runtime / MCP / Host Tools
  -> EventBus 审计与流式事件
```

外部传入的“环境变量”必须按 **不可信业务上下文** 处理。它能帮助 Agent 感知业务状态，但不能当作密钥、权限来源或后端真实环境变量。

产品完整目标是：**业务侧可以使用 Hive 已授权的全部 AI 能力**，包括普通对话、工具调用、业务域 KB、证据引用、文件/图片/PDF 资产、Agent artifact 和后续业务工具。Embed 只是统一接入层，不应把业务侧降级成“只能聊天”的子集。

因此实施上分两种口径：

- **文字/状态感知首通链路**：Embed P0-P4 可以先验证 token、session、stream、context、SDK、Widget。
- **完整 Hive AI 能力外露**：必须同时落地 KB 与统一对象存储依赖；否则业务侧无法完整使用 KB 图片、PDF/DOCX、附件、artifact 和证据资产。

## 当前代码真实情况

| 能力 | 当前代码位置 | 文档修正 |
| --- | --- | --- |
| 消息入口 | `internal/master/public_api.go` 的 `ProcessMessageWithOptions` | Embed 应新增 `WithEmbedContext` 走现有 harness，不重建 runtime。 |
| 会话创建 | `Master.CreateSession(ctx, name, mode)` | 当前没有 `CreateSessionOpts`，不能直接把 metadata 传进去。 |
| 会话元数据 | `internal/master/session.go` 的 `SessionState.Metadata` | 这是运行时字段，当前 `sessions` 表和 `store.SessionRecord` 都没有持久化 metadata。 |
| SSE | `internal/streaming/handler.go` | `SSEWriter` 只是写入器，不是完整订阅端点；需要新增 Embed SSE handler。 |
| WebSocket | `internal/streaming/websocket.go` | 当前面向普通 JWT/static token，Embed 需要独立 scoped token 鉴权。 |
| Agent 列表 | `internal/api/handlers.go` 的 `/api/v1/agents` | 当前偏 subagent，不是可发布嵌入的业务 Agent 配置源。 |
| 工具准入 | `internal/master/tool_visibility.go`、`react_processor.go` | Embed 沙箱必须接入现有 RouteDecision、allowed tools、ActionGuard、runtime gate。 |
| 前端聊天 | `frontend/src` 的后台 UI | 后台 Chat 依赖全局状态和认证，Widget 应做独立包，谨慎复用纯展示组件。 |

## 修正后的执行优先级

实施前决策已经拍板：

- `2026-05-16-Embed-实施前决策清单.md`

该决策清单是 P0-P5 执行基线。后续如需改变 Agent 发布物、token 签发、context schema、工具权限、stream 协议或审计字段，必须先更新决策清单和本总览。

文件名仍保留 Claude Code 生成的 Phase 编号，但实际执行按下面 P0-P5 排序。

| 优先级 | 对应文件 | 目标 | 验收标准 |
| --- | --- | --- | --- |
| P0 | `2026-05-16-Embed-Phase2-安全与Token.md` | 定义可嵌入 Agent 配置源、Scoped Token、Origin、CORS、限流、审计边界 | 没有后台 JWT 也能验证 embed token；未授权 origin/agent/tool 被拒绝。 |
| P1 | `2026-05-16-Embed-Phase1-后端API与上下文注入.md` | 新增 Embed API、session 元信息持久化、`EmbedContext` 注入 harness | SDK 能创建 embed session、发送消息、通过 SSE 收到现有 EventBus 事件。 |
| P2 | `2026-05-16-Embed-Phase5-工具沙箱与动态召回.md` | 把工具白名单、KB 三工具和上下文召回接入现有工具准入链 | 模型不可见、runtime 不可执行超出 token/agent/session allowlist 的工具；KB citation 能通过 stream/SDK/Widget 到达业务侧。 |
| P3 | `2026-05-16-Embed-Phase4-Headless-SDK.md` | 先做 TypeScript Headless SDK | SDK 覆盖 createSession、sendMessage、stream、updateContext、close。 |
| P4 | `2026-05-16-Embed-Phase3-JS-Widget.md` | Widget 建在 SDK 之上，独立构建和样式隔离 | 一行脚本能挂载 Widget，不能污染后台 UI build，也不能依赖后台 Zustand/auth。 |
| P5 | `2026-05-16-Embed-补充计划-遗漏点与风险缓解.md` | 配额、成本、生命周期、监控、文档和 QA 硬化 | 有用量归因、会话 TTL、错误指标、端到端测试和集成文档。 |

并行地基顺序必须调整为：

| 地基 | 计划 | 约束 |
| --- | --- | --- |
| A0 | `internal/asset` 统一对象存储层 | 已落 `internal/asset`、元数据表、resolve API 和最小安全 resolver；Widget/KB 后续只复用该层，遗留项跟踪在 `TODOS.md`。 |
| A1 | `docs/计划与路线/归档/业务域KB与证据层计划总览.md` + KB P0/P1 | 先落 KB tree-mode 三工具、binding resolver、evidence ledger、citation 透出；否则 Embed 不能宣称支持 Hive KB。 |
| A2 | KB P2 + 对象存储接入 | PDF/DOCX、Markdown 图片、KB asset、客服试点、业务侧文件资产进入完整能力范围。 |

## 架构边界

### Embed Gateway

负责公开接入能力：

- 验证 scoped embed token，不复用普通用户 JWT。
- 校验 Origin、tenant、agent、widget、session scope。
- 执行 rate limit、并发 session 限制、token revocation。
- 把 token claims 和请求上下文转换为后端可信的 `EmbedContext`。

### Master Harness

复用现有运行时：

- 通过 `ProcessMessageWithOptions` 进入 `SessionManager` 和 ReAct loop。
- `EmbedContext` 作为 `SessionRequest` 的可选字段传递。
- prompt 注入时明确标记外部上下文为不可信业务数据。
- toolctx 只传递非密钥上下文，供工具读取业务状态。

### Tool Runtime

不新增独立策略引擎：

- 工具候选集、模型可见性、runtime 执行都要取 token、agent config、session context、RouteDecision 的交集。
- 继续复用 `SessionState.SetAllowedTools`、`SetAllowedToolInputs`、`RouteDecision`、`enforceToolExecutionGate`、`ActionGuard`。
- 动态召回只扩大“候选分析上下文”，不能突破授权边界。

### SDK / Widget

- Headless SDK 是基础层，Widget 只负责 UI 和交互。
- Browser 端 token 视为暴露信息，只允许短期、窄 scope、origin-bound。
- 不把长期密钥写进 `<script data-token>`。
- Widget 独立 Vite lib 构建，不写入 `internal/webui/dist`。

## 跨计划协同

实施前必须和以下计划保持一致：

| 计划 | 协同点 |
| --- | --- |
| `docs/计划与路线/归档/业务域KB与证据层计划总览.md` | Embed 要支持 Hive 全部 AI 能力时，KB P0/P1 是硬依赖。首版走 `kb.doc.meta` / `kb.doc.structure` / `kb.section.text`，namespace 授权由 `KBBindingResolver` 做；citation/evidence 必须透过 stream、SDK 和 Widget。 |
| `internal/asset` 统一对象存储层 | 完整业务侧能力必须复用 `internal/asset`。Widget 附件、KB 图片、PDF/DOCX 转换资产、Agent artifact 都复用该层；`asset://` 不是授权凭证。即使 Widget 上传 UI 后置，v1 协议也要兼容 asset refs。原对象存储实施计划已归档到 `docs/计划与路线/归档/2026-05-16-统一对象存储层计划.md`。 |
| `docs/架构设计/Tool-Routing.md` | 工具可见性和执行权限继续走 `RouteDecision` / `AllowedToolInputs` / runtime gate。 |
| `docs/架构设计/安全权限模型.md` | HITL 不是策略引擎；Embed 模式下 `ToolPolicyDeny` 不能放宽，`ToolPolicyAsk` 默认不自动放行。 |
| `docs/架构设计/Run质量治理地基.md` | 不新增平行 `embed_runs` 事实源；用 session/turn/trace/span + tenant/agent/widget/token 扩展现有观测链路。 |
| `docs/架构设计/业务域准入模型.md` | `agent_id` 和 context 不授予业务域权限；业务域能力仍走 host-side domain admission。 |
| `DESIGN.md` | Widget 不复用后台 Chat 全局状态；可复用 leaf primitive 思想，但必须独立构建和样式隔离。 |

## 不做的事

- 不新建第二套 Agent runtime。
- 不把浏览器传来的 env/context 当作真实后端环境变量或权限。
- 不在 P0-P4 做 iframe 嵌入。
- 不在 MVP 做 Go SDK；先用 TypeScript SDK 验证协议。
- 不手工编辑 `internal/webui/dist`。

## 主要风险

| 风险 | 修正后的处理方式 |
| --- | --- |
| Token 暴露 | token 短期、origin-bound、agent/tool scoped、可撤销；推荐 host backend 动态签发。 |
| Prompt injection | 上下文结构化注入并标记不可信；权限由后端工具 gate 控制，不靠字符串清洗。 |
| 工具越权 | token scope、agent allowlist、session allowlist、RouteDecision、ActionGuard 多层取交集。 |
| 流式实现偏离现有架构 | 基于 EventBus 新增 scoped SSE/WS subscriber，不另造事件体系。 |
| Agent 概念不清 | 先落 `embed_agent_configs` 或等价配置源，再暴露 `/embed/agents/{id}`。 |
