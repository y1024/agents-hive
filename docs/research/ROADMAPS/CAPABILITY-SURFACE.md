# Capability Surface Roadmap

> **定位**：能力面最小推进
>
> **优先级**：P3
>
> **目标**：保留产品能力版图，但近期只推进能直接提升 P0 质量闭环的最小 surface
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 代码前提

能力面已经很大，不能再按“缺少产品面”来排。

代码里已经有：

- API：session、message、fork、revert、regenerate、stop、star、tag、journal、HITL、tools invoke、push schedule。
- Admin：users/quota、usage、auth providers、Prompt 管理、Skill 管理、LLM providers/models。
- Frontend：Chat、Sessions、Skills、Agents、Settings、ReplayGallery、SessionReplay、AdminSettings、PromptManager、UsageStats。
- HITL：WebSocket、ApprovalCard、pending input、command。
- Artifact/canvas：Markdown/HTML/Code/JSON renderer。
- Taskboard：持久化 taskboard 和 tool。
- Channel：飞书、微信、企微、钉钉、push、renderer、dedup、debounce、retry、gap fetch。
- Gateway：统一 RPC/WS 方法，覆盖 sessions、agents、skills、HITL、MCP、channel、config、remote agents。

因此这条路线不是“补 surface”，而是约束 surface 扩张，让已有 surface 服务质量和治理。

## 2. 总原则

能力面冻结扩张，除非满足以下条件：

- 能直接提高 P0 指标。
- 能复用现有 API/Gateway/Admin/channel/taskboard/artifact。
- 有明确 eval 或 QA 验收。
- 不引入新的危险操作语义分叉。
- 不增加不可观测的执行路径。

同时要明确：完整产品化工作台后置，不等于质量工作台后置。已有 Replay/Admin/Gateway surface 必须尽早承接 P0 质量闭环，否则 eval、失败归因和人工修复无法落地。

## 3. 近期只做的最小子集

### 3.1 质量调试 surface

优先补让研发能定位 agent 失败的 surface：

- 在 SessionReplay/Journal 中展示质量事件。
- 在工具调用 UI 中展示 failure_type、retry_reason、dangerous operation decision。
- 在 PromptManager 中展示 prompt version 和 eval 风险。
- 在 UsageStats 中展示失败成本和模型路由。
- 在 ReplayGallery 中按失败类型、危险操作、tool error、context pollution、subagent/ACP 失败筛选。
- 在 EventDetailPanel 中展示 prompt/tool/skill/context 版本、args hash、parent/child trace。
- 支持从失败 replay 写入 DB regression candidate，默认 `required=false`，不直接进入生产 eval 集。

最小代码入口：

- `internal/journal/journal.go`：扩展或复用 `DecisionEntry` 承载 quality event 摘要。
- `internal/master/quality_events.go`：emit quality event 时同步写入 journal decision。
- `frontend/src/components/replay/EventDetailPanel.tsx`：展示 quality fields。
- `frontend/src/pages/ReplayGallery.tsx`：增加失败类型筛选。
- `frontend/src/pages/admin/UsageStats.tsx`：展示失败成本。
- `frontend/src/pages/admin/PromptManager.tsx`：展示 prompt smoke eval 风险。

### 3.2 HITL / intervention surface

只做危险操作相关增强，不扩大审批范围：

- 审批卡片展示 actor、tool、args fingerprint、workspace、risk reason。
- 支持最小 scope remember，不做粗粒度永久放行。
- 普通工具和安全命令不展示审批卡片。

### 3.3 Taskboard / todos

已有 `internal/taskboard` 和 `taskboard` 工具，不做完整产品化待办系统。

近期只做：

- 让 eval/golden tasks 能记录 expected task state。
- 让长任务失败时沉淀 blocked task。
- 让人工介入能看到未完成事项。

### 3.4 Channel / artifact surface

复用现有 channel 抽象：

- 不再新建 ChannelAdapter。
- 飞书已是最成熟样板，其它 channel 只补关键一致性缺口。
- Artifact/canvas 只做影响任务理解和调试的增强。

### 3.5 Multi-agent / ACP 调试 surface

这里只做质量调试，不做完整协作工作台：

- Replay 中显示 parent task、child agent、remote ACP session 的链路。
- 子 Agent 工具白名单、spawn depth、max turns、结果状态可见。
- ACP cancel、disconnect、permission bridge、partial result 可见。
- 失败时能定位是主 Agent 决策错误、子 Agent 执行错误、远程 transport 错误，还是危险操作被拦截。

### 3.6 工具广度

新增工具必须满足：

- 有真实高频任务。
- 有 tool-choice eval。
- 有危险操作边界策略。
- 有 timeout/cost/observability。
- 有失败恢复路径。

## 4. 延后推进

- 完整工作台产品化。
- 大规模工具补齐。
- todos 全功能产品面。
- 多端协同大改版。
- 每个 MCP 工具专属 UI。
- 为 ACP 生态扩张做大量 surface。

不延后的最小部分：

- 质量事件展示。
- replay 失败筛选。
- prompt smoke eval 风险提示。
- 失败成本展示。
- DB regression candidate 生成和审核入口。
- subagent/ACP trace 展示。

## 5. 不做什么

- 不为了“看起来完整”补页面。
- 不重复发明 channel/event/task/artifact 抽象。
- 不绕过 Gateway/API 已有能力直接接新路径。
- 不做没有质量指标支撑的工具铺货。
- 不把质量工作台做成独立新产品，先复用现有 Replay/Admin。
- 不让 DB regression candidate 自动进入生产门禁，必须人工确认并晋升。

## 6. 启动条件

这条线升级投入前必须满足：

- `AGENT-QUALITY.md` 的主指标已有明确改善。
- `FOUNDATIONS.md` 的危险操作边界、观测、runtime policy 已稳定。
- 有真实用户场景驱动。
- 新 surface 不扩大不可控执行路径。

例外：质量调试 surface 是 P0/P1 的一部分，不需要等完整主指标改善后才启动；它本身就是改善主指标的前置工具。

## 7. 与其他路线图的关系

- 受 `AGENT-QUALITY.md` 约束：没有质量收益不做。
- 受 `FOUNDATIONS.md` 约束：没有治理接入不做。
- 为 `MEMORY-AND-CONTEXT.md` 提供调试和审计入口。
- 为 `MULTI-AGENT-AND-ACP.md` 提供最小 trace/replay 控制面；完整协作工作台不提前铺开。
