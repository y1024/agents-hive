# Feishu Phase 2A Design

日期：2026-04-24

## 目标

把飞书 Phase 2 拆成一个可独立 ship 的子阶段：`webhook-only lifecycle + minimal governance`。

Phase 2A 只解决两类问题：

1. bot 生命周期事件进入系统后，群状态与会话状态能稳定收敛
2. 群聊下最小治理能力可用，且不引入 `longconn` 级别风险

## 不在本阶段

以下内容明确排除在 Phase 2A 之外：

- `longconn`
- watchdog
- gap fetch
- 单入口切换实现
- `/debug`
- `/audit`
- `/agent`
- `/model`
- percentage rollout
- ops mute API

这些内容统一进入后续 Phase 2B 或更后阶段。

## 阶段边界

Phase 2A 包含：

- `bot_added`
- `bot_removed`
- DB-backed `chat_state`
- `/help`
- `/status`
- `/reset`
- 命令 normalize + whitelist
- tenant-scoped ACL
- `mute`
- deterministic allow/deny rollout
- `TerminateSession(sessionID, reason)`
- `bot_removed` 后 outbound suppression
- lifecycle dedup + monotonic state transition

## 设计原则

### 1. 继续坚持 webhook-only

本阶段只走 webhook 路径。原因：

- `longconn` 开放属于 ingress 风险，不应和治理面一起上线
- watchdog / gap fetch / single-ingress 切换都有独立的失败模式
- Phase 2A 的交付目标是让治理面稳定，而不是重新打开入口面

### 2. 用 `chat_state` 取代“先做内存 binding”

Phase 2A 不做临时内存 binding 方案。必须直接做 DB-backed `chat_state`。

原因：

- `bot_removed` 清理必须跨重启有效
- `mute` / `rollout` / lifecycle 状态都要求统一真相源
- 多副本下内存态没有可证明的一致性

### 3. `/reset` 和 `bot_removed` 共用同一个终止基座

`/reset` 不是“清空历史”按钮，`bot_removed` 也不是“删一条映射”事件。

两者都依赖同一个基础能力：

- `TerminateSession(sessionID, reason)`

如果没有真正的 session termination，本阶段的治理语义都不成立。

## 数据模型

建议新增持久化表：`feishu_chat_state`

字段：

- `platform`
- `tenant_key`
- `chat_id`
- `session_id`
- `state`
- `mute_until`
- `rollout_mode`
- `last_lifecycle_event_id`
- `last_lifecycle_event_time`
- `updated_at`
- `updated_by`

字段语义：

- `state ∈ {active, evicted}`
- `rollout_mode ∈ {allow, deny}`

约束：

- 唯一键：`(platform, tenant_key, chat_id)`
- 所有治理查询都必须带 `tenant_key`
- 缺失 `tenant_key` 时 fail-closed

## 生命周期状态机

### `bot_removed`

只允许：

- `active -> evicted`

处理流程：

1. 先做 lifecycle event dedup
2. 读取 `chat_state`
3. 若已是 `evicted`，直接成功返回
4. 基于 `last_lifecycle_event_time` / `event_id` 做单调校验
5. 更新 `chat_state.state=evicted`
6. 打开 outbound suppression
7. 调用 `TerminateSession(session_id, "bot_removed")`
8. Router 不再把普通消息路由到该 chat
9. 打指标与日志

### `bot_added`

只允许：

- `evicted -> active`

处理流程：

1. 做 event dedup 与顺序校验
2. 更新 `chat_state.state=active`
3. 清除 outbound suppression
4. 可选发送 welcome

不做：

- 不提前创建空 session

## Session Termination

新增导出能力：

- `TerminateSession(sessionID, reason)`

最小语义：

1. 标记 session 为 terminating
2. 拒绝新的 inbound 再路由到该 session
3. 取消 in-flight task
4. 等待 worker 释放 sem
5. 结束 session
6. 幂等，多次调用结果一致

如果只做 `Unbind` 或“删会话记录”，无法保证：

- in-flight task 停止
- sem 最终释放
- 老结果不写回

## 命令与普通消息执行顺序

统一执行顺序：

1. ingress 安全校验 + dedup
2. 读取 `chat_state`
3. 解析并 normalize 输入
4. 判断是否命令
5. 若是命令：
   - whitelist
   - ACL
   - 执行命令
6. 若不是命令：
   - rollout
   - mute
   - 路由到 session
   - agent 处理

例外规则：

- `/help` / `/status` / `/reset` bypass `mute/rollout`
- `bot_removed` bypass 全部治理判断
- `bot_added` 不受 `mute` 阻断

## 命令语义

### `/help`

- 返回当前上下文下可用命令
- 群聊只显示当前调用者有权限使用的命令
- 私聊可显示更完整版本

### `/status`

- 只返回粗粒度状态
- 例如：
  - `active`
  - `muted until ...`
  - `rollout denied`
  - `bot evicted`

明确不返回：

- 内部队列深度
- 审计细节
- 敏感配置

### `/reset`

私聊：

- 用户本人可执行

群聊：

- 必须通过 ACL

执行语义：

1. 调 `TerminateSession`
2. 等 in-flight 停止
3. 切到干净 session
4. 更新 `chat_state.session_id`

## ACL

ACL 必须 tenant-scoped，且 fail-closed。

不允许：

- 缺 `tenant_key` 时退回默认 tenant
- 先放行后补租户隔离

如果真实群管 SDK 查询未验证稳定，Phase 2A 可以先采用：

- 显式 allowlist
- fail-closed

但不能伪装成“完整 ACL 已交付”。

## Rollout

Phase 2A 只做 deterministic allow/deny list，不做 percentage sample。

原因：

- percentage rollout 引入采样语义和用户感知问题
- 它不解决本阶段核心风险
- allow/deny list 已足够满足最小治理目标

## Mute

Mute 状态必须落在 `chat_state` 或等价持久化存储中。

原因：

- 重启后仍需生效
- 需要与 lifecycle 状态统一推理

## Outbound Suppression

`bot_removed` 之后，不仅要阻断新的 inbound，还要阻断该 chat 的 outbound / retry。

否则会出现：

- 被踢后仍继续发消息
- retry 风暴
- 无意义错误日志

## 回滚开关

至少提供以下配置开关：

- `Feishu.Lifecycle.Enabled`
- `Feishu.Commands.Enabled`
- `Feishu.Rollout.Mode`
- `Feishu.Mute.Enabled`
- `Feishu.Governance.EnableTerminateSession`

语义：

- `Lifecycle.Enabled=false`：跳过 lifecycle handler
- `Commands.Enabled=false`：跳过命令解析
- `Rollout.Mode=all`：关闭 allowlist 灰度
- `Mute.Enabled=false`：短路 mute
- `EnableTerminateSession=false`：紧急回退，但不建议长期使用

## 测试矩阵

### 生命周期幂等

- 重复 `bot_removed` 只清一次
- 旧 `bot_removed` 不覆盖新的 `bot_added`
- `bot_added -> bot_removed -> bot_added` 状态收敛正确

### Session termination

- `TerminateSession` 幂等
- 有 in-flight task 时最终释放 sem
- 终止后旧任务结果不得写回

### 命令治理

- `/reset` 私聊允许，群聊走 ACL
- `/help / status / reset` bypass `mute/rollout`
- 普通消息仍走 `rollout -> mute`
- normalize 后大小写 / 零宽字符 / unicode 规则一致

### Chat state

- tenant 隔离
- 缺 `tenant_key` fail-closed
- 重启后治理状态仍在

### Outbound suppression

- `bot_removed` 后 retry/outbound 不再发往该 chat

### 蓝军

- 伪造群身份尝试 `/reset`
- lifecycle 乱序重投
- reset 与普通消息并发
- mute 群里管理员恢复命令可用，普通消息不可用

## 实施顺序

建议固定为四步：

1. `chat_state` + migration + repo
2. `TerminateSession`
3. lifecycle handler 接入 `bot_added / bot_removed`
4. commands + ACL + rollout + mute 接入 router

不要并行打乱顺序。第 1、2 步是本阶段基座。

## 验收

- `bot_removed` 后该 chat 新消息不再进入旧 session
- `bot_removed` 后对应 session 最终不占 sem
- `/reset` 群聊拒绝非 ACL 用户
- `/reset` 后旧 in-flight 不再写回
- rollout deny 的普通消息静默 drop
- `/help / status / reset` 在 mute/deny 群里仍可按权限执行

## Verdict

Phase 2A 在以上边界下可开工。

若去掉以下任一项，本阶段都不建议开工：

- DB-backed `chat_state`
- `TerminateSession`
- lifecycle 幂等与单调保护
- outbound suppression
