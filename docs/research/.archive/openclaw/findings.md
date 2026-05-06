# OpenClaw Harness 调研

> **状态**：Agent 调研草稿（一次性 WebSearch + WebFetch 通道）
> **核实状态**：每条断言下方标 [HC] 高确定性 / [TBV] 待核实 / [DOUBT] 强烈存疑
> **日期**：2026-04-25
> **范围约束**：Hive 锁定国内 IM 阶段性边界 + harness 工程全球顶级雄心
> **目的**：作为 deer-flow 6-axis 框架的对照源之一（与 Claude Code 并列）

---

## ⚠️ 调研可信度警告

主线程**不应**把本文件的具体数字事实作为决策依据，原因：

1. Agent 通过 WebSearch + WebFetch 一次性收集，**未做交叉源验证**
2. OpenClaw 是 2025-2026 期间发展的项目（Agent 自己说 2025-11 创建），公开资料可能仍处于早期/混乱阶段
3. WebSearch 结果在 2026 年面临 AI 内容污染问题（Medium 文章/Velvetshark/Zenvanriel 等可能为 AI 生成）
4. 关键人物事实（"Peter Steinberger 加入 OpenAI 2026-02"）和数字（"145K stars / 5700 skills"）需独立第二信源核实
5. Hive 项目内部对 OpenClaw 的引用（DESIGN.md 列为 competitor，gstack `pair-agent` 提到"OpenClaw, Hermes, Codex"）证明 OpenClaw 在用户语境真实存在，但具体定位需核实

**主线程使用本文件的姿势**：
- 把整体 axis 框架当作"对照清单可能的方向"
- 每条具体决策落地之前，必须重新独立验证（看 OpenClaw 仓库实际代码 / 关键文档原文）
- 不要把 Agent 报告的数字写进 PR 描述或外部 pitch

---

## 0. 一页纸结论（Agent 报告，未独立核实）

OpenClaw 据 Agent 调研为：
- **开源 AI Agent 框架**，2025 年 11 月创建 [TBV]
- 跨 20+ 消息平台（WhatsApp / Telegram / Slack / Discord 等）[TBV]
- **核心定位**：模型无关的通用个人助手（vs Claude Code 的编码专用） [TBV]
- 145K+ GitHub stars [DOUBT — 数字过于耀眼，需核实]
- 5700+ 社区技能 [DOUBT — 与 Claude Code skills 数量级相去甚远，需核实]
- **Mission Control**：中央运维控制平面（multi-agent 工作流 / 成本监控 / 审计 / 权限） [TBV]

如属实，OpenClaw 对 Hive 的关键映射：
- 多 IM 渠道集成 = Hive 反向输出点（飞书/微信/企微/钉钉，且专做 PatchCard 增量渲染） — Hive 在国内 IM 深度上理论上仍领先
- Skills 系统、Hooks、MCP 双向 = 与 Hive 现有架构对照
- Mission Control 多 agent 编排 = Hive `controlplane` + `acpserver` 已布局

---

## 1-6. 6 Axis 对照（Agent 报告原文，标记可信度）

### Axis 1: Tools 体系
[TBV] 据 Agent：OpenClaw 跨 20+ 消息平台原生集成。具体内置工具集未深挖。

**Hive 可借鉴假设**：跨 IM 渠道的统一抽象层 — Hive 现有 `internal/channel/router` 已实现，需对照 OpenClaw 的抽象粒度。

### Axis 2: Memory
[TBV] 据 Agent：自动上下文压缩 + 检查点恢复，强调"若未写入文件则不存在"的持久化原则。

**Hive 可借鉴假设**：持久化语义边界 — 与 Hive `internal/memory` + `internal/store` 的边界划分对照。

### Axis 3: Skills
[DOUBT] 据 Agent：5700+ 社区技能、ClawHub 注册表、分层加载、动态过滤、沙箱隔离。

**核实重点**：5700 这个数字数量级。如果属实，Hive `Skill 市场协议`（`docs/架构设计/Skill-市场协议.md`）应直接对照其 ClawHub 注册表协议。

### Axis 4: MCP / 外部协议
[TBV] 据 Agent：OpenClaw 既作为 MCP server 也作为 MCP client，可与 Claude Code / Codex 互联。

**核实重点**：双向 MCP 集成的具体协议适配。Hive 当前主要是 MCP server 角色（`internal/mcphost`），是否需要补 MCP client 路径取决于此条核实结果。

### Axis 5: Channels / Renderer
[TBV] 据 Agent：跨 20+ 消息平台。

**核实重点**：渠道适配的颗粒度（是单一 SSE 流复制到各渠道，还是像 Hive 飞书 `PatchCard` 这样的渐进式 patch？）。如果是前者，Hive `EventRenderer` 仍领先。

### Axis 6: Prompts / System
[TBV] 据 Agent：未深挖。System prompt 结构、tool description 风格未呈报。

**核实重点**：与 Claude Code system prompt 110+ 条件指令架构对照，OpenClaw 的 prompt 是否类似 modular？

---

## 7. Hooks 架构（Agent 单独提到的差异点）

[TBV] 据 Agent：OpenClaw 有事件驱动 Hooks 架构（事件生产者 / Gateway Event Bus / Hook Executor），消除轮询延迟（秒级降至毫秒级）。

**核实重点**：与 Claude Code Hooks（26+ 生命周期事件）对照，OpenClaw 的 Hook 模型设计差异。

**Hive 现状对照**：Hive `internal/master/event_bus.go` 是进程内订阅广播（deer-flow final-verdict §2.1 已识别），多副本部署需要外置化（Redis pub/sub / Postgres NOTIFY / NATS）— 这条与 OpenClaw 的 Gateway Event Bus 设计可能直接对照。

---

## 8. 与 Claude Code 的差异（Agent 报告）

[TBV] 据 Agent：
- OpenClaw = 通用生活助手（模型无关，支持 Claude/GPT/Kimi）
- Claude Code = 编码专用（Anthropic 生态）
- OpenClaw 有持久记忆 + 多平台集成
- Claude Code 重视安全性 + 代码理解

**Hive 视角**：Hive 同样是模型无关（9+ LLM Provider），同样多 IM 集成（虽然只国内），同样持久记忆。**Hive 与 OpenClaw 的真实差异点**待核实，但初步看：spec-driven ReAct + 国内 IM 深度 + SafeExecutor-first 权限模型可能是 Hive 独有。

---

## 9. 仍需核实清单（强制核实，否则不得基于本文件做决策）

1. **GitHub repo 真实存在性**：`github.com/openclaw/openclaw` 是否真存在？stars 数实际值？
2. **创始人事实**：Peter Steinberger 是否真为创始人？是否真加入 OpenAI 2026-02？
3. **Mission Control**：`github.com/abhi1693/openclaw-mission-control` 是否真存在？官方 vs 社区？
4. **5700 skills 数字**：ClawHub 注册表实际 skill 数量
5. **20+ IM 平台**：实际支持清单
6. **MCP 双向客户端**：协议适配代码是否真存在
7. **OpenClaw 与 gstack `pair-agent`/Hermes/Codex 的实际竞争关系**

**核实路径**：
- 直接 Read OpenClaw repo（如属实可 git clone 一份本地 rsync，参考 deer-flow 调研做法）
- 找 1-2 篇官方博客/release notes 而非二手 Medium 文章
- 让人工 reviewer 交叉验证关键数字

---

## 10. 文件索引

- 本文件：`docs/research/openclaw/findings.md`
- 对照：`docs/调研笔记/deer-flow/PLAN.md`、`docs/调研笔记/deer-flow/archive/final-verdict.md`
- 同期：`docs/research/claude-code/findings.md`

*— End of OpenClaw 调研草稿（待核实）—*
