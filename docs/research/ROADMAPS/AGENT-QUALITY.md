# Agent Quality Roadmap

> **定位**：Hive 当前主线
>
> **优先级**：P0
>
> **目标**：把 Hive 从“能力很多但表现不稳定”推进到“复杂任务稳定做对”
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 代码前提

这条路线不是从零建设主 Agent。

代码里已经有：

- `internal/master/react_processor.go`：ReAct 主循环、streaming、required tool guard、loop detector、cost budget、tool execution。
- `internal/master/session_loop.go`：session worker pool、per-session 串行、用户上下文传递、trace/metric 写入。
- `internal/i18n/prompt_loader.go`：PromptLoader 三层优先级 `DB > 文件 > go:embed`、TTL、缓存失效。
- `internal/master/prompt_builder.go`：system prompt 分段、工具提示、外部 MCP 工具 prompt 注入。
- `internal/tools`：核心工具、LSP、web、batch、task、spawn_agent、question、skill、memory、taskboard、IM 工具。
- `internal/skills`：skill registry、overlay、DB 覆盖、tenant isolation、permission、on-demand search/install。
- `internal/specdriven/eval`：已有 spec-driven eval harness，但只覆盖 spec-driven 场景。
- `internal/journal`、`frontend/src/pages/SessionReplay.tsx`、`frontend/src/pages/ReplayGallery.tsx`：已有会话回放和事件时间线，可作为质量调试工作台的最小承载面。
- `internal/subagent`、`internal/tools/spawn_agent.go`、`internal/tools/task.go`、`internal/acpserver`、`internal/acpclient`：已有本地/远程协作骨架，必须进入最小质量评测，而不是等完整生态后再评估。

因此本路线图的工作不是“补主循环/补 PromptLoader/补工具系统”，而是把现有执行链路做成质量闭环。

## 2. 最高优先级问题

当前最危险的问题是：Hive 有大量能力，但缺少统一的质量度量和回归门禁。

具体表现：

- Prompt 改动能热更新，但缺少 prompt 版本、golden tasks、离线分数和回滚门禁。
- Tool 描述很丰富，但缺少“模型是否选对工具”的自动评测。
- Skill 可以安装、搜索、覆盖和调用，但缺少路由命中率、误触发率、内容质量评估。
- Context 压缩、memory 注入、附件转换都存在，但缺少污染检测和失败归因。
- ReAct 已经有很多护栏，但失败还没有沉淀为可复用 regression case。
- Journal/Replay 已经存在，但还没有展示 `quality.*` 事件、失败类型、retry reason、prompt/tool/skill/context 版本。
- SubAgent/ACP 已经能跑，但还没有证明“什么时候委派能提升成功率，什么时候只是在扩大失败面”。
- 自动优化方向存在价值，但当前只能做建议和样例生成，不能直接改生产 prompt/skill/tool 策略。

## 3. 施工顺序

### 3.1 建主 Agent golden task corpus

先建立一套覆盖真实使用路径的任务集：

- 代码库理解：多文件定位、引用追踪、架构解释。
- 代码修改：小修、跨文件修改、测试驱动修改、避免误改。
- 工具选择：read/grep/glob/bash/edit/apply_patch/multiedit/webfetch/websearch 的选择和顺序。
- Skill 路由：显式调用、隐式匹配、未安装 skill 的自愈提示。
- HITL/Tool Safety：危险命令、不可逆操作、用户补充输入。
- 长会话：压缩前后事实召回、用户偏好保持、上下文污染。
- IM 场景：飞书/微信入口下的危险操作边界、回复状态、renderer fallback。
- Multi-agent 最小场景：只验证 spawn/task 不破坏主 Agent 质量，不扩大协作复杂度。
- ACP 最小场景：只验证远程 session 创建、断线/取消、危险操作 bridge、事件回放，不做生态扩张。
- 质量工作台场景：失败 session 能在 replay 中看到失败分类，并能写入 DB regression candidate pool。

每个 case 必须记录：

- 输入。
- 期望工具调用序列或允许集合。
- 期望最终结果。
- 失败分类。
- 使用的 prompt 版本、skill 版本、tool schema 版本。

### 3.2 建 ReAct 质量事件 schema

在现有 event/span/metric 基础上增加结构化质量事件，不直接散写自由 JSON。

最小字段：

- `case_id`：离线评测或线上采样任务 ID。
- `session_id_hash`：脱敏后的会话标识，不把原始高基数字段作为通用 label。
- `prompt_version`：system/tool/skill prompt 的版本。
- `tool_decision`：候选工具、实际工具、是否命中预期。
- `failure_type`：prompt/tool/skill/context/model/permission/runtime/user_input。
- `retry_reason`：required-zero-tool、tool_error、loop_warning、schema_error、permission_denied。
- `final_status`：pass/fail/blocked/needs_user。
- `parent_trace_id` / `child_trace_id`：本地 subagent、parallel dispatch、ACP remote agent 的父子链路。
- `replay_ref`：可定位到 journal/replay 中的 session、step、event。

### 3.3 Prompt 质量治理

基于现有 PromptLoader 做治理：

- 为每个 prompt key 建版本和 changelog。
- 对 `system/base`、`system/execution`、`system/code_editing`、`system/safety`、`system/reply` 建离线 eval。
- 对 `tools/<mcpName>` 和核心工具描述建 tool-choice eval。
- Admin Prompt 保存前可运行 smoke eval，失败则提示风险。
- Prompt 改动支持灰度和回滚，不直接全量影响所有会话。

明确不做：

- 不重写 PromptLoader。
- 不把所有 prompt 再搬到另一套系统。
- 不在没有 eval 的情况下做大规模 prompt 重写。

### 3.4 Tool 选择质量治理

围绕“模型是否选对工具”做评测，而不是继续追工具数量。

必须覆盖：

- 应该 `grep` 时是否错误 `read_file` 大文件。
- 应该 `edit/multiedit` 时是否错误 `write_file` 覆盖。
- 应该问用户时是否擅自执行。
- 应该走 `skill` 时是否直接硬做。
- 应该走 `webfetch/websearch` 时是否凭空回答。
- 工具失败后是否进入有效恢复，而不是循环同一调用。

验收指标：

- 工具选对率。
- 工具参数有效率。
- 工具失败恢复率。
- required tool guard 触发后的恢复率。
- 重复工具循环发生率。

### 3.5 Skill 路由质量治理

基于现有 `internal/skills` 和 `internal/tools/skill.go` 做闭环：

- 统计 skill 被列出、被选择、被调用、调用失败、自愈建议的全链路。
- 给高频 skill 建专属 eval case。
- 对 skill frontmatter、description、trigger、permission scope 做静态 lint。
- 对 on-demand install/search 做“误安装/误推荐”回归。
- 对 personal/public overlay 做租户隔离回归。

验收指标：

- skill 命中率。
- skill 误触发率。
- skill 调用成功率。
- 未安装 skill 的正确自愈率。
- personal/public 覆盖正确率。

### 3.6 Context 质量治理

本路线图只处理与主 Agent 成功率直接相关的 context 问题，深治理见 `MEMORY-AND-CONTEXT.md`。

近期必须做：

- 在 ReAct 每轮记录 context 构成：原始消息、压缩摘要、memory 注入、附件转换、system/tool prompt。
- 对 compaction 前后做事实召回测试。
- 对 memory 注入做污染检测：不相关记忆、过期记忆、跨用户记忆、低置信记忆不能进入关键决策。
- 对附件转换失败建立明确失败分类。

### 3.7 最小质量工作台

这不是“完整工作台产品化”，而是 P0 必需的质量闭环入口。

近期必须做：

- `quality.*` 事件同步到现有 journal/replay 能展示的事件流，至少以 decision 或专用 quality event 形式可见。
- `SessionReplay` 展示失败类型、retry reason、tool decision、prompt version、skill version、context source。
- `ReplayGallery` 支持按失败、危险操作、tool error、required-zero-tool、context pollution 筛选。
- Admin quality 页面能列出 golden cases、smoke eval 结果、最近失败 session、DB regression candidates。
- PromptManager 保存前能看到 smoke eval 风险，UsageStats 能看到失败成本。

验收指标：

- 一个失败 session 不需要查日志就能定位失败分类。
- 一个失败 session 能生成 DB regression candidate，人工确认后进入 `internal/agentquality/testdata`。
- 质量工作台只读展示和候选生成默认放行，不引入频繁审批。

### 3.8 自动优化的安全入口

自动优化必须先作为“建议生成器”进入 P0，而不是作为“自动修改器”进入生产。

允许：

- 从失败轨迹生成 golden task 候选。
- 从 eval diff 生成 prompt diff 建议。
- 从成功轨迹生成 skill 草稿。
- 给 tool description 生成可评测的改写建议。

禁止：

- 自动修改生产 prompt。
- 自动安装或发布 skill。
- 自动放宽危险操作边界。
- 自动改变 tool profile/filter 可见性。
- 自动替换 ReAct 主执行路径。

## 4. 回归门禁

任何影响以下区域的改动都必须跑对应 eval：

- `internal/master`
- `internal/i18n`
- `internal/tools`
- `internal/skills`
- `internal/memory`
- `internal/compaction`
- `internal/specdriven`
- `internal/channel`
- `frontend/src/pages/admin/PromptManager.tsx`

最低门禁：

- 必选 golden tasks 不能退化。
- required tool guard 相关 case 不能退化。
- dangerous-operation/security 相关 case 不能退化。
- prompt 改动必须附带 eval diff。
- tool schema/description 改动必须附带 tool-choice eval diff。
- subagent/ACP 相关改动必须附带父子 trace、取消/超时、危险操作 bridge 的回归结果。
- 自动优化产物只能作为候选进入 review，不得直接进入生产默认路径。

## 5. 验收指标

主指标：

- 复杂任务一次完成率。
- 工具选对率。
- skill 路由选对率。
- 长会话事实召回率。
- 失败可归因率。
- 失败到 regression candidate 转化率。
- 质量工作台可定位率。

护栏指标：

- 危险操作误放行率。
- HITL 超时和拒绝后的恢复率。
- 重复工具循环率。
- required-zero-tool 发生率。
- context 污染率。
- subagent 委派负收益率。
- ACP session/cancel/reconnect 失败率。

P0 第一批硬阈值：

- Required golden cases 通过率必须是 100%。
- 危险操作误放行数必须是 0。
- 失败归因率必须 >= 90%。
- Tool 选择基础命中率必须 >= 85%。
- Replay 可定位率必须 >= 90%。
- DB regression candidate 生成率必须 >= 80%。
- Required-zero-tool 回归数必须是 0。
- Subagent/ACP 最小 case 的 delegation trace 覆盖率必须是 100%。

## 6. 与其他路线图的关系

- 依赖 `FOUNDATIONS.md` 提供 typed metric、SLO、危险操作边界一致性和运行稳定性。
- 依赖 `MEMORY-AND-CONTEXT.md` 深化 memory/compaction 质量。
- 约束 `CAPABILITY-SURFACE.md`：没有质量收益证明的新能力不得进入近期施工。
- 约束 `MULTI-AGENT-AND-ACP.md`：最小协作评测必须前置，完整协作扩张必须等质量收益被证明。

## 7. 当前执行原则

1. 任何新增能力必须先绑定 eval case。
2. 任何 prompt、tool、skill、context 改动必须能解释质量收益。
3. 没有失败归因的线上问题，优先补 telemetry 和 regression case，再谈重构。
4. 能复用现有模块就不新建抽象。
5. 质量指标没改善的能力扩张，一律后置。
