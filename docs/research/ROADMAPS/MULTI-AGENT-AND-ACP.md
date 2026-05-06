# Multi-Agent And ACP Roadmap

> **定位**：协作与生态受控孵化
>
> **优先级**：P4
>
> **目标**：把 multi-agent / ACP 的最小质量杠杆纳入当前闭环，同时延后完整生态化扩张
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 代码前提

Multi-agent 和 ACP 不是空白。

代码里已经有：

- `internal/subagent`：BaseAgent、AgentLoop、Registry、AgentFactory、固定 Agent、动态 Agent。
- `internal/tools/task.go`：派发任务到 SubAgent，带 caller 限制、深度限制、系统 Agent denylist。
- `internal/tools/spawn_agent.go`：动态创建专用 Agent 并执行任务，Master-only。
- `internal/tools/parallel_dispatch.go`：并行派发任务。
- `internal/subagent/factory.go`：per-session 隔离、maxPerSession、maxGlobal、maxSpawnDepth、工具白名单。
- `internal/acpclient`：连接远程 ACP Agent，并包装成本地 subagent。
- `internal/acpserver`：Hive 作为 ACP server，支持认证、session、危险操作 bridge、MCP passthrough、stream。
- `internal/a2abridge`：Agent-to-Agent 协议桥接骨架。

所以近期不是“实现 multi-agent/ACP”，而是把已有能力接入门禁、评测、恢复和边界。

## 2. 当前判断

这条线不能再被理解为“以后再说”。正确拆法是：

- **近期必须做质量杠杆最小版**：golden cases、trace linkage、危险操作边界、取消/超时/恢复、replay。
- **近期不能做完整扩张版**：团队模型、marketplace、远程 runtime 大规模接入、自动选择本地/远程 Agent。

原因：

- 多 Agent 有明显质量收益潜力，可以提高复杂任务分解、并行调查和专业工具使用能力。
- 多 Agent 也会放大 prompt、tool、context、危险操作边界的不稳定性。
- 远程 ACP 会增加网络、认证、会话隔离、恢复、事件一致性复杂度。
- 如果没有量化评测，多 Agent 很容易变成“把失败分散到更多地方”。

## 3. 近期质量杠杆最小版

### 3.1 本地 SubAgent 门禁

只补质量闭环必需能力，不扩复杂协作模式：

- 为 task/spawn_agent/parallel_dispatch 建 golden cases。
- 记录父子 Agent trace linkage。
- 记录 delegated task 的输入、工具白名单、结果、失败类型。
- 验证 caller 限制、深度限制、系统 Agent denylist。
- 验证 DestroyAgent 必定释放 per-session 配额。
- 验证子 Agent 成本、quota、userID 继承。
- 验证子 Agent 工具白名单不能被 prompt 绕过。
- 验证子 Agent 输出能被主 Agent 合并，并能识别 partial/fail。

### 3.2 协作质量评测

评测必须回答：

- 委派是否真的提高成功率。
- 委派是否减少主 Agent token 成本。
- 委派是否增加失败恢复难度。
- 子 Agent 是否错误使用工具。
- 子 Agent 输出是否可被主 Agent 正确整合。

没有这些结果，不扩大多 Agent 使用范围。

最低 golden cases：

- 主 Agent 自己做更优：验证不会过度委派。
- 委派 code search 更优：验证 task/spawn_agent 能提高定位速度或成功率。
- 并行调查更优：验证 parallel_dispatch 的 partial failure 不拖垮主任务。
- 危险操作委派：验证子 Agent 不能绕过父 session 的危险操作边界。
- 子 Agent 错误输出：验证主 Agent 能识别和恢复，而不是盲信。

### 3.3 ACP 稳定性治理

只做生态扩张前必须的基础：

- ACP server auth token 必须生产强制配置。
- ACP session 与 Master session 隔离验证。
- 危险操作 bridge 路由到正确 session。
- Remote ACP Agent 断线、超时、取消、重连策略。
- ACP/MCP passthrough 的危险操作边界和审计。
- ACP 事件进入统一 quality trace。
- ACP cancel/refusal/transport error 进入失败分类。
- ACP prompt response 的 stop reason 进入 replay。

### 3.4 恢复与回放

Multi-agent/ACP 必须可回放：

- 父任务。
- 子任务。
- 远程 prompt。
- dangerous-operation 决策。
- tool calls。
- stop/cancel。
- failed/partial result。

### 3.5 质量工作台接入

Multi-agent/ACP 的价值只有在可观察时才能放大。

近期必须接入：

- Replay 中显示 parent task -> child agent -> tool call 的链路。
- Replay 中显示 ACP session id 与 Master session id 的绑定。
- EventDetailPanel 展示子 Agent 工具白名单、spawn depth、max turns、cost、failure type。
- ReplayGallery 支持筛选 subagent failed、ACP disconnected、permission bridge、partial result。
- Admin quality 页面统计委派正收益、负收益、超时、取消、成本。

## 4. 延后推进

- Coordinator mode 全面产品化。
- TeamCreate/TeamDelete 等复杂团队模型。
- 远程 Agent marketplace。
- ACP runtime 大规模接入。
- 跨进程持久 multi-agent 编排。
- 自动选择本地/远程 Agent。
- 多 agent 编排 DSL。
- 面向外部开发者的 ACP 插件生态。

## 5. 启动条件

最小质量杠杆版立即进入 P0/P1/P3 的施工范围。进入完整扩张前必须满足：

- `AGENT-QUALITY.md` 主指标稳定提升。
- 本地 subagent eval 显示明确正收益。
- `FOUNDATIONS.md` 的危险操作边界/runtime/observability 已覆盖子 Agent 和 ACP。
- ACP 断线/取消/危险操作/会话隔离有回归测试。
- 有真实用户场景必须依赖远程或跨 runtime 协作。

## 6. 风险控制

- 不把 multi-agent 当作单 Agent 质量不足的替代品。
- 不把 ACP 当作先进性工程目标。
- 不让远程 Agent 绕过本地危险操作边界、quota、observability。
- 不在无法回放的情况下引入复杂协作。
- 不允许子 Agent 使用超出白名单的高风险工具。
- 不允许把失败的远程 Agent 输出当作可信事实直接合并。
- 不允许没有 eval diff 的协作模式进入默认工具提示。

## 7. 和未来版图的关系

这条线保留长期方向：

- 本地多 Agent。
- 远程 ACP Agent。
- MCP/ACP runtime 协同。
- A2A bridge。
- 跨 IDE/IM/Web 的协作入口。

近期施工最小质量版，完整生态不扩张。
