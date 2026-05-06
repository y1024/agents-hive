# M6 · 主动推送(Push)

> 让 bot 能**主动**开口——定时播报、长任务完成通知、外部事件 → 飞书。
>
> 2026-04-26 状态更新:
> - 已完成:HTTP push API、PushService、内置模板渲染、`scheduled_push:` 前缀解析、幂等、per-chat rate limit、失败入 retry_queue、热重载。
> - 已完成:`push:write` 已接入 JWT claims scope,`push:write` / `admin` 均可写;保留 `role=admin` 兼容兜底。
> - 已完成:进程内 `master.CronCreate` + 持久化 `scheduled_pushes` + HTTP schedule 管理 API + 启动恢复。
> - 未完成:多副本 leader 选主 / 分布式只跑一次语义 / 更完整的 schedule 观测面。

## 1. 现状

- 历史缺口已补:仓内现在已有 HTTP push API、模板渲染、schedule 管理接口与启动恢复
- 当前剩余缺口主要在多副本只跑一次语义、schedule 观测与运维面

## 2. 方案

### 2.1 PushService(库入口)

```go
// internal/channel/push/service.go
type Service struct {
    router *channel.Router  // 复用现有 plugin 发送能力
    logger *zap.Logger
}

type Request struct {
    Platform channel.Platform  // "feishu" 等
    ChatID   string            // 群 chat_id 或 open_chat_id
    OpenID   string            // 或用 open_id 发私聊(二选一)
    MsgType  channel.MsgType   // text / markdown / interactive / image / file
    Content  string
    Template string            // 可选:模板名,服务端渲染
    Vars     map[string]any    // 模板变量
    IdempotencyKey string      // 可选:相同 key 5 分钟内只发一次
}

func (s *Service) Push(ctx context.Context, req Request) error {
    plugin, ok := s.router.GetPlugin(req.Platform)
    if !ok { return fmt.Errorf("platform not registered: %s", req.Platform) }

    // 幂等性(仅推荐,非强制)
    if req.IdempotencyKey != "" && s.idempotency.seen(req.IdempotencyKey) {
        return nil
    }

    // 模板渲染
    content := req.Content
    if req.Template != "" {
        rendered, err := s.renderTemplate(req.Template, req.Vars)
        if err != nil { return err }
        content = rendered
    }

    // 如果给了 OpenID 没给 ChatID,先把 open_id 换成 p2p chat_id
    chatID := req.ChatID
    if chatID == "" && req.OpenID != "" {
        // feishu:open_id 可直接用作 receive_id 发消息,但 Bind session 需要 chat_id
        chatID = "p2p:" + req.OpenID  // 约定:push-only 场景允许这种伪 chatID
    }

    return plugin.Send(ctx, channel.OutboundMessage{
        Platform: req.Platform,
        ChatID:   chatID,
        MsgType:  req.MsgType,
        Content:  content,
    })
}
```

### 2.2 HTTP API

新增 `internal/api/push_handler.go`:

```
POST /api/v1/channels/push
Authorization: Bearer <token>
Content-Type: application/json

{
  "platform": "feishu",
  "chat_id": "oc_xxxxxx",
  "msg_type": "markdown",
  "content": "## 数据日报\n- 新增订单: 123",
  "idempotency_key": "daily-report-2026-04-22"
}

→ 200 OK { "sent": true, "chat_id": "oc_xxxxxx", "duration_ms": 142 }
→ 400 参数错误
→ 409 IdempotencyKey 重复
→ 502 平台侧发送失败
```

鉴权复用现有 `auth middleware`,额外加 scope 检查(`push:write`)。

> 2026-04-26 实际落地说明:当前仓内已补齐 JWT claims scope 注入。auth 开启时要求用户已登录，且 claims 中具备 `push:write` 或 `admin` scope; 同时保留 `role=admin` 兼容兜底。

### 2.3 模板管理

`internal/channel/push/templates.go`:

```go
// 模板从 DB 表 push_templates 读取,支持热加载
// 内置几个常用:
// - daily_report: 日报卡片
// - task_done: 长任务完成通知
// - alert: 告警卡片(红色边框)
```

模板格式用 Go `text/template`,输出是 markdown 或 interactive JSON。

### 2.4 Scheduled Push

复用 master `CronCreate`,handler 走 PushService:

```go
// scheduled push 注册时:
master.CronCreate(master.CronJob{
    Cron: "0 9 * * *",   // 每天 9am
    Prompt: "scheduled_push:daily_report:chat_id=oc_xxx",
})

// Cron tick 到达时,master 触发一个特殊 prompt 走 PushDispatcher:
// PushDispatcher 识别 "scheduled_push:<template>:<key>=<val>" 前缀,
// 调 PushService.Push 发送 — 不走 LLM,纯静态模板
```

> 2026-04-26 实际落地说明:当前仓内已经落地:
> - `master.CronCreate` 进程内调度执行
> - `scheduled_pushes` 持久化表
> - `POST/GET/DELETE /api/v1/channels/push/schedules`
> - server 启动时自动恢复 enabled schedule
>
> 但这仍不是“分布式 fully done”:
> - 目前是单进程内存调度,多副本会重复执行
> - 没有 leader election / distributed lease
> - 观测面仍只有基础状态字段,尚未形成完整 metrics/dashboard

**设计取舍**:scheduled push 走 master.CronCreate 而不是独立 scheduler,理由:
- 避免额外的持久化/发布入口
- 复用现有 cron 的 7 天 TTL / 幂等保证
- 若需要 LLM 生成内容,把 prompt 写成真实问题即可,不用走 PushDispatcher

### 2.5 外部事件 → 飞书

Push API 已能覆盖——外部系统直接 POST `/api/v1/channels/push`。

一期不做专门的 webhook-in adapter,理由:每家接入方的 webhook 格式都不一样,统一抽象不如让他们写一个 3 行 script 调 push API。

## 3. Config

```go
type FeishuConfig struct {
    ...
    Push FeishuPushConfig `json:"push,omitempty"`
}

type FeishuPushConfig struct {
    Enabled bool `json:"enabled"`  // 默认 false,开了才注册 HTTP API
    // 单 chat_id 每分钟最大推送数(默认 10,防止骚扰)
    PerChatPerMinute int `json:"per_chat_per_minute,omitempty"`
    // 幂等性缓存时长(秒,默认 300)
    IdempotencyTTLSec int `json:"idempotency_ttl_sec,omitempty"`
}
```

**默认 `Enabled=false`**:push 是"让 bot 主动开口",风险点(骚扰/滥用/权限越界)比 inbound 大,必须显式开启。

## 4. 测试

单测:

- PushService 对 OpenID-only 路径转 p2p chatID 正确
- 模板渲染:daily_report 模板 + vars → 预期 markdown
- Idempotency:相同 key 5 分钟内第二次调用返 nil + metric
- HTTP handler:auth 缺失 → 401;scope 不足 → 403;payload 缺字段 → 400
- Per-chat rate limit:10/min 超过后第 11 条返 429

集成:

- 真实租户 push markdown 到测试群:收到 + 格式正确
- scheduled cron 触发 push 模板:按时收到,重启后自动恢复
- 恶意重复 push 10000 条:被 rate limit 拦住 99% 以上,ops 能从 metric 发现

蓝军:

- push content 含 `@所有人` 的 markdown 语法 → 飞书卡片 / markdown 是否真的 @all(确认飞书行为),若是则加 content sanitizer 默认禁用 @all(可配置 `allow_at_all`)
- 外部调用方盗用 token push 到非其管辖的 chat_id → ACL 层(一期 skip,留接口 `PushAuthorizer.CanPushTo(userID, chatID) bool`)
- 模板渲染时 vars 注入 template 语法 → text/template 默认防御;若换 html/template 必须白名单 action

## 5. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/push/service.go` | **新建** PushService 库入口 |
| `internal/channel/push/templates.go` | **新建** 模板渲染 |
| `internal/api/push_handler.go` | **新建** HTTP API |
| `internal/api/push_schedule_handler.go` | **新建** schedule create/list/delete API |
| `internal/api/routes.go` | 注册 `/api/v1/channels/push*` 路由 |
| `internal/master/cron.go` | `CronCreate` / `StopCron` / `ListCrons` |
| `internal/bootstrap/server.go` / `helpers.go` | 启动恢复 enabled schedule |
| `internal/store/postgres_migrate.go` / `postgres.go` | `scheduled_pushes` 持久化 |
| `internal/channel/router.go:130` `GetPlugin` | PushService 从此取 plugin 复用 Send |
| `internal/config/config.go:881` | `FeishuPushConfig` |
