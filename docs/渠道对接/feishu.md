# 飞书 Channel

飞书 IM 是截至 2026-04 唯一接入 harness `EventRenderer` 抽象的 channel——收到用户消息后由
master 广播 `input_received` 事件触发 ack 表情，随后 Router 把本会话的 message/tool_call/input_request
事件流订阅到 `feishuRenderer`，把累积状态 PATCH 进同一张飞书卡片，直到 agent final 落章。

## 架构一图

```
用户消息 ──> feishu webhook / longconn ──> channel.Router.processMessage
                                                      │
                              ┌───────────────────────┴───────────────────────┐
                              ▼                                               ▼
                      master.EventBus.Broadcast(input_received)         master.ProcessMessageFromIM
                              │                                               │
                              ▼                                               ▼
             feishuRenderer.handleInputReceived                    agent 执行 → 流式 emit events
             → client.AddReaction(msg, ack_emoji)                           │
                                                                            ▼
                                                  feishuRenderer.{HandleMessage|HandleToolCall|HandleInputRequest}
                                                                            │
                                                                            ▼
                                              client.PatchCard(accumulated state)
```

架构决策原文见 `openspec/changes/im-streaming-reply/design.md`（D3/D4）。

## 配置

配置存储在数据库 `channel_configs` 表，**不在 `config.example.json`** 中。首次启动时由
`bootstrap.MigrateConfigToDB` 从老版 `config.json` 迁移；之后通过 Web UI / API 修改。

结构体定义见 `internal/config/config.go:FeishuConfig` / `FeishuRendererConfig`。

**Normalize 后的默认终态**：

| 字段 | 默认值 | 语义 |
|---|---|---|
| `enabled` | `false` | 总开关，需显式开启 |
| `ack_emoji` | `"Get"` | 收到消息回贴的表情，飞书 reactions API 的 `emoji_type`（CamelCase）。合法值 `"Get"` / `"Typing"` / `"none"`（禁用）。早期误写的 `"GET"`/`"KEYBOARD"` 由 Normalize 静默迁移到 `"Get"`/`"Typing"`；其他值 warn + 回退到 `"Get"`。 |
| `renderer.disabled` | `false` | **反向语义**：零值 = EventRenderer 启用。显式 `true` 回滚到 legacy `Plugin.Send`（一次性文本）。 |
| `renderer.throttle_ms` | `300` | 卡片 PATCH 最小间隔（毫秒）。`<= 0` 时 Normalize 回退到 300。 |
| `renderer.show_agent_progress` | `false` | 是否在卡片中展示"Agent 思考中"等中间状态文案。 |

**回滚路径**：线上出现卡片 PATCH 失败 / 飞书 API 异常时，设 `renderer.disabled = true` 并触发热重载，
Router 会走 `processViaLegacySend` 一次性文本分支——行为与 EventRenderer 引入前 bit-identical。

> Web UI 侧对应操作：**设置 → IM 通道 → 飞书** 面板取消勾选 *启用流式卡片*（底层写入 `renderer.disabled = true`）。
> JSON/API 直改 `renderer.disabled` 与 UI 等价，二者走同一条 DB 写入 + 热重载路径。

## 事件 → 卡片片段映射

`feishuRenderer` 订阅 Router 通过 `channel.EventBusSubscriber` 转发的本 session 事件流，按事件类型
累积状态到单张卡片。映射关系（与 `internal/channel/feishu/renderer.go` 保持一致）：

事件类型字符串常量定义见 `internal/master/master.go`（`EventTypeInputReceived` / `EventTypeMessage` /
`EventTypeToolCall` / `EventTypeInputRequest` / `EventTypeError` 等）。下表列出的是事件的 `Type` 字段值：

| 事件 `Type` | 触发动作 | 卡片片段 |
|---|---|---|
| `input_received` | `client.AddReaction(msg_id, ack_emoji)` | —（不改卡片，仅贴表情） |
| `message`（`partial: true`） | `PatchCard`（throttle 节流） | 标题 `🤖 生成中…` + 累积正文 |
| `message`（`partial: false`） | `PatchCard`（强制 flush） | 标题 `✅ 完成` + 完整正文 |
| `tool_call`（`phase: start`） | `PatchCard` | `🔧 调用工具：{name}` 行 + spinner |
| `tool_call`（`phase: success`） | `PatchCard` | 工具行变绿 + 勾号 + duration |
| `tool_call`（`phase: error`） | `PatchCard` | 工具行变红 + ❌ + 错误摘要 |
| `input_request`（HITL） | `PatchCard` | `✅ 批准` / `❌ 拒绝` 按钮，`action.value` 带 `request_id` |
| `error` | `PatchCard`（flush） | 标题 `❌ 失败` + 错误正文 |

## 失败与降级

1. **PatchCard 首次失败** → renderer 内部 retry 一次（固定间隔，非指数退避）。
2. **retry 仍失败** → 返回 `*channel.RendererError{LastContent: "<最后一次 intent>"}`。
3. **Router 收到 RendererError** → 调 `plugin.Send(ctx, sessionID, lastContent)` 一次性推送兜底——用户最坏
   拿到一条完整文本，不会看到空卡片或错误栈。

热重载 / renderer 禁用 / 平台不支持 EventRenderer 时，Router 走 `processViaLegacySend`，与旧版本行为一致。

## 排障 cheat sheet

| 现象 | 大概率原因 | 定位 |
|---|---|---|
| 收不到 ack 表情 | `ack_emoji = "none"` 或 master 未注入 Router | `grep "Channel Router EventRenderer 已装配" hive.log` |
| 卡片不更新 | `renderer.disabled = true` 或 EventRenderer 降级 | 看启动日志 `feishu_renderer_enabled` 字段 |
| 卡片更新太频繁 | `throttle_ms` 太小 | 默认 300ms，可调大到 500~1000 |
| 非法 ack_emoji 警告 | 配置里 ack_emoji 写错 | warn 含 `phase` 字段区分 migrate_legacy / plugin_new / (config load) |
| PATCH 日志只有 1-2 行 | renderer 走了 retry 兜底 | 看是否有 `RendererError` 日志 + 检查飞书 API 可用性 |
