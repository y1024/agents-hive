# M3 · 生命周期事件

> 让 bot 像"活人"一样响应进群/退群/首次打招呼。目前 5 个生命周期 handler 都是空实现。

## 1. 现状

`internal/channel/feishu/longconn.go`:

| 行 | 事件 | 现状 | 实际语义 |
|---|---|---|---|
| 78-80 | `OnP2ChatAccessEventBotP2pChatEnteredV1` | ⚠️ 空 | 用户打开与 bot 的私聊窗口 |
| 84-86 | `OnP1P2PChatCreatedV1` | ⚠️ 空 | 首次建立 P2P 聊天(比 Entered 早一步) |
| 90-92 | `OnP2MessageReadV1` | ⚠️ 空 | 用户读了 bot 的消息 |
| 97-102 | `OnP2MessageReactionCreated/Deleted` | ⚠️ 空 | 表情反应(M2 §7/§8 已说明) |
| — | `im.chat.member.bot.added_v1` | ❌ 未注册 | **bot 被拉进群** |
| — | `im.chat.member.bot.deleted_v1` | ❌ 未注册 | **bot 被踢出群** |
| — | `im.chat.member.user.added_v1` | ❌ 未注册 | 群有新成员(可选) |

`webhook.go:88-91` 全部非消息事件被丢,上面所有 event 走 webhook 路径都会被静默 200 OK。

## 2. 方案

### 2.1 `imctx.LifecyclePayload`

```go
// internal/imctx/context.go(续)
type LifecycleEventType string

const (
    LifecycleBotAdded      LifecycleEventType = "bot_added"
    LifecycleBotRemoved    LifecycleEventType = "bot_removed"
    LifecycleP2PCreated    LifecycleEventType = "p2p_created"
    LifecycleP2PEntered    LifecycleEventType = "p2p_entered"
    LifecycleMemberAdded   LifecycleEventType = "member_added"
    LifecycleMemberRemoved LifecycleEventType = "member_removed"
)

type LifecyclePayload struct {
    Type      LifecycleEventType
    Platform  string
    ChatID    string
    ChatType  string // "p2p" / "group"
    OperatorID string // 执行者 open_id(谁拉/踢的)
    TargetIDs []string // 被操作对象 open_id(bot 自己或某用户)
    Timestamp time.Time
    Raw       json.RawMessage
}
```

### 2.2 `LifecycleHandler` 接口

```go
// internal/channel/types.go
type LifecycleHandler interface {
    HandleLifecycle(ctx context.Context, p *imctx.LifecyclePayload) error
}

func (r *Router) SetLifecycleHandler(h LifecycleHandler) { ... }
```

Router 路由:

```go
func (r *Router) HandleLifecycle(ctx context.Context, p *imctx.LifecyclePayload) error {
    // dedup:event_id 10 分钟窗口
    if r.lifecycleDedup.isDuplicate(p.Raw) { return nil }
    if r.lifecycleHandler == nil {
        r.logger.Debug("lifecycle event dropped (no handler)", zap.String("type", string(p.Type)))
        return nil
    }
    return r.lifecycleHandler.HandleLifecycle(ctx, p)
}
```

### 2.3 longconn handler 补齐

```go
// internal/channel/feishu/longconn.go
eventDispatcher.OnP2ChatMemberBotAddedV1(func(_ context.Context, e *larkim.P2ChatMemberBotAddedV1) error {
    return c.handleLifecycle(parseLifecycleBotAdded(e))
})
eventDispatcher.OnP2ChatMemberBotDeletedV1(func(_ context.Context, e *larkim.P2ChatMemberBotDeletedV1) error {
    return c.handleLifecycle(parseLifecycleBotRemoved(e))
})
eventDispatcher.OnP1P2PChatCreatedV1(func(_ context.Context, e *larkim.P1P2PChatCreatedV1) error {
    return c.handleLifecycle(parseLifecycleP2PCreated(e))  // 替换原空实现
})
```

### 2.4 webhook 分发(见 M2 §4.2 的总分发器)

```go
case "im.chat.member.bot.added_v1":
    payload := parseLifecycleBotAddedFromWebhook(bodyBytes)
    go h.router.HandleLifecycle(bgCtx, payload)
case "im.chat.member.bot.deleted_v1":
    payload := parseLifecycleBotRemovedFromWebhook(bodyBytes)
    go h.router.HandleLifecycle(bgCtx, payload)
case "im.p2p_chat.created_v1":
    ...
```

### 2.5 默认 Handler 实现

```go
// internal/channel/feishu/lifecycle_handler.go
type LifecycleHandlerImpl struct {
    client   *Client
    router   *channel.Router
    master   LifecycleMaster  // master 侧最小接口
    logger   *zap.Logger
    cfg      LifecycleConfig  // 从 FeishuConfig.Lifecycle 继承
}

type LifecycleMaster interface {
    EndSession(sessionID string) error            // bot 被踢时清理
}

type LifecycleConfig struct {
    WelcomeOnBotAdded bool   // 默认 true
    WelcomeOnP2PCreate bool  // 默认 true
    WelcomeTemplate string   // 卡片 JSON 模板,为空则用内置默认
}

func (h *LifecycleHandlerImpl) HandleLifecycle(ctx context.Context, p *imctx.LifecyclePayload) error {
    switch p.Type {
    case imctx.LifecycleBotAdded:
        // 1) 发 welcome 卡片
        if h.cfg.WelcomeOnBotAdded {
            _ = h.client.SendCard(ctx, p.ChatID, buildWelcomeCard(p, h.cfg.WelcomeTemplate))
        }
        // 2) 预绑定 session(防止首条消息走默认 binding 路径时竞态)
        sessionID := "im-feishu-" + p.ChatID
        h.router.Bind(channel.Binding{
            Platform: channel.PlatformFeishu, ChatID: p.ChatID, SessionID: sessionID,
        })
    case imctx.LifecycleBotRemoved:
        // 1) 解绑
        h.router.Unbind(channel.PlatformFeishu, p.ChatID)
        // 2) 清 master 侧 session(防止残留占用 sem)
        sessionID := "im-feishu-" + p.ChatID
        if err := h.master.EndSession(sessionID); err != nil {
            h.logger.Warn("end session after bot removed",
                zap.String("session_id", sessionID), zap.Error(err))
        }
    case imctx.LifecycleP2PCreated:
        if h.cfg.WelcomeOnP2PCreate {
            _ = h.client.SendCard(ctx, p.ChatID, buildP2PWelcomeCard(p, h.cfg.WelcomeTemplate))
        }
    case imctx.LifecycleP2PEntered:
        // 一期不主动发消息(避免用户每次打开窗口都收到 hi),只记 metric
        metrics.Counter("feishu.lifecycle.p2p_entered", 1)
    case imctx.LifecycleMemberAdded, imctx.LifecycleMemberRemoved:
        // 一期只记 metric,不介入
        metrics.Counter("feishu.lifecycle.member_change", 1, "type", string(p.Type))
    default:
        h.logger.Debug("unknown lifecycle event", zap.String("type", string(p.Type)))
    }
    return nil
}
```

### 2.6 Welcome 卡片模板

内置默认(BuildWelcomeCard / BuildP2PWelcomeCard 在 `card_builder.go` 新增):

```
┌──────────────────────────────────────┐
│ 👋 Hi,我是 <BotName>                │
│                                      │
│ 我能帮你:                           │
│  • 读写飞书文档、表格、多维表       │
│  • 查日程 / 通讯录 / 审批            │
│  • 执行自定义 Agent 任务             │
│                                      │
│ 发消息前请 @我,或在私聊直接发。    │
│                                      │
│ [ 查看帮助 ] [ 常用命令 ]             │
└──────────────────────────────────────┘
```

模板字段从 `FeishuConfig.Lifecycle.WelcomeTemplate` 读取,空则用内置。

## 3. Config

```go
// internal/config/config.go
type FeishuConfig struct {
    ... // 现有字段
    Lifecycle FeishuLifecycleConfig `json:"lifecycle,omitempty"`
}

type FeishuLifecycleConfig struct {
    WelcomeOnBotAdded  bool   `json:"welcome_on_bot_added"`   // 默认 true
    WelcomeOnP2PCreate bool   `json:"welcome_on_p2p_create"`  // 默认 true
    WelcomeTemplate    string `json:"welcome_template,omitempty"` // 卡片 JSON 模板
    Enabled            bool   `json:"enabled"`                // 总开关,默认 true
}

// Normalize:Enabled 零值默认 true,其他 welcome 开关零值也默认 true。
```

## 4. 测试清单

单测:

- `parseLifecycle*FromWebhook` / `*FromEvent` 两套解析对同一语义 event 产出同样 LifecyclePayload
- dedup:同一 event_id 10 分钟内只 handle 一次
- bot_added → SendCard + router.Bind 都被调
- bot_removed → router.Unbind + master.EndSession 被调;EndSession 失败 warn 但不 return error
- p2p_entered 不发消息(metric only)

集成:

- bot 被拉进测试群:收到欢迎卡片,后续 @bot 消息能走到 session
- bot 被踢:binding 消失;master 内存里该 sessionID 被清
- 用户新建 P2P → 自动收到 welcome

回滚:

- `FeishuLifecycleConfig.Enabled = false` → LifecycleHandler 全部 skip(事件照收,action 不触发)
- 或 `Router.SetLifecycleHandler(nil)` → 零副作用

## 5. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/longconn.go:78-102` | 现有空 handler,改为调 `handleLifecycle` |
| `internal/channel/feishu/longconn.go:72` | 附近追加 bot added/removed handler |
| `internal/channel/feishu/webhook.go:88-91` | 改为按 event_type 分发,新增 4 个 lifecycle 分支 |
| `internal/channel/feishu/lifecycle_handler.go` | **新建**,默认 handler 实现 |
| `internal/channel/feishu/card_builder.go` | 新增 `BuildWelcomeCard` / `BuildP2PWelcomeCard` |
| `internal/channel/router.go` | `SetLifecycleHandler` / `HandleLifecycle` 新增 |
| `internal/master/public_api.go` | 确认 `EndSession(sessionID string)` 是否已导出;如未导出需新增 |
| `internal/bootstrap/server.go:377` | 装配 LifecycleHandlerImpl → Router.SetLifecycleHandler |
| `internal/config/config.go:881` | `FeishuConfig` 加 `Lifecycle` 字段 + Normalize |
