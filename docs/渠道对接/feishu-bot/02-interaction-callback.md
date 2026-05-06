# M2 · 交互回调(Card Action / Reactions / Read / Recall)

> 修复**最高优先级的死链**:Agent 发的 HITL approve/reject 按钮用户点了但收不到。

## 1. 现状 — HITL 死链

`internal/channel/feishu/card_builder.go:181-197` 把按钮渲染成这样:

```json
{
  "tag": "button",
  "text": { "tag": "plain_text", "content": "✅ 批准" },
  "type": "primary",
  "value": { "action": "approve", "request_id": "req-xxx" }
}
```

按飞书协议,用户点击后会推送 `card.action.trigger` 事件(webhook 或 longconn),`event.action.value` 就是这个 `value` 字段。

**现状**:
- `longconn.go` 6 个注册的 handler 里**没有** `OnP2CardActionTriggerV1`
- `webhook.go:88-91` `if event.Event.Message == nil { return 200 }` —— **直接丢弃非消息事件**
- 全项目 grep `SubmitInput`(`hitl_broker.go` 的 HITL 响应入口),飞书侧零调用

⇒ **Agent 发审批请求 → 卡片按钮发出 → 用户点按钮 → 飞书推送事件 → 代码丢弃 → Agent 永远在 hitl_broker 里等,直到超时**。

这是一条静默 P0 缺陷。

## 2. 方案总览

```
飞书 card.action.trigger 事件
  │ (longconn 或 webhook)
  ▼
解析 action.value → imctx.CardAction
  │
  ▼
Router.HandleCardAction(ctx, action)       ← 新接口
  │
  ▼
InteractionHandler 按 action.Action 路由:
  - approve / reject → HITLBridge → master.HITLBroker.SubmitInput
  - form submit(预留)→ 绑定 session 追加 user 消息
  - 其他(未知 action)→ warn + 忽略
  │
  ▼
HITLBridge 成功后:
  - PatchCard(去掉按钮,展示"已批准/已拒绝 · <user_name> · HH:MM")
  - 回给飞书 200 OK 带 toast 提示(飞书卡片回调协议)
```

## 3. `imctx.CardAction` 叶子类型

```go
// internal/imctx/context.go(续)
type CardAction struct {
    Platform   string            // "feishu" / "wecom"
    ChatID     string
    MessageID  string            // 卡片消息 ID,用于 PatchCard
    OpenID     string            // 点击者 open_id
    UserName   string            // 点击者真名(best-effort)
    Action     string            // value.action:"approve"/"reject"/"form_submit"/...
    RequestID  string            // value.request_id(HITL 响应用)
    FormValues map[string]any    // 表单提交场景
    RawValue   json.RawMessage   // 原始 value,给 debugging
    Timestamp  time.Time
}
```

## 4. 飞书事件注册

### 4.1 longconn 补齐

`internal/channel/feishu/longconn.go:72` 现场注册 `OnP2MessageReceiveV1` 之后加:

```go
// 新注册:卡片按钮点击
eventDispatcher.OnP2CardActionTriggerV1(func(handlerCtx context.Context, event *larkcallback.CardActionTriggerEvent) error {
    return c.handleCardAction(handlerCtx, event)
})
```

`handleCardAction`(新增):

```go
func (c *LongConnClient) handleCardAction(_ context.Context, event *larkcallback.CardActionTriggerEvent) error {
    if event == nil || event.Event == nil {
        return nil
    }

    action, err := parseCardActionEvent(event, c.client.BotOpenID())
    if err != nil {
        c.logger.Warn("card action parse failed", zap.Error(err))
        return nil // 吞错,不让 SDK 重试
    }

    // dedup(MessageID + Action + RequestID 三元组作 key,防飞书重投)
    return c.router.HandleCardAction(c.getContext(), action)
}
```

### 4.2 webhook 补齐

`webhook.go:88` 现场 `if event.Event.Message == nil { return 200 }` **必须删**,改为按 `header.event_type` 分发:

```go
if event.Header == nil {
    w.WriteHeader(http.StatusOK); return
}

switch event.Header.EventType {
case "im.message.receive_v1":
    if event.Event != nil && event.Event.Message != nil {
        h.dispatchMessage(w, r, event)
    } else {
        w.WriteHeader(http.StatusOK)
    }
case "card.action.trigger":
    h.dispatchCardAction(w, r, bodyBytes)   // 新增
case "im.chat.member.bot.added_v1", "im.chat.member.bot.deleted_v1", "im.p2p_chat.created_v1":
    h.dispatchLifecycle(w, r, event)        // M3 实现
default:
    w.WriteHeader(http.StatusOK) // 未知事件,静默 200
}
```

**注意飞书 card action 回调的 body schema 跟 event 不一样**(顶层不是 `{ header, event }` 而是 `{ action: {...}, operator: {...}, ... }`)。`parseCardActionWebhookBody(bodyBytes)` 独立解析。

### 4.3 `parseCardActionEvent`

把 longconn/webhook 两侧的原始载荷**都**规范化到同一个 `imctx.CardAction`,避免两份解析逻辑漂移。

```go
// internal/channel/feishu/card_action.go
func parseCardActionEvent(event *larkcallback.CardActionTriggerEvent, botOpenID string) (*imctx.CardAction, error) {
    var v struct {
        Action    string         `json:"action"`
        RequestID string         `json:"request_id"`
        FormValues map[string]any `json:"form_values,omitempty"`
    }
    raw := event.Event.Action.Value
    if err := json.Unmarshal(raw, &v); err != nil {
        return nil, fmt.Errorf("card value unmarshal: %w", err)
    }
    return &imctx.CardAction{
        Platform:  "feishu",
        ChatID:    event.Event.Context.OpenChatID,
        MessageID: event.Event.Context.OpenMessageID,
        OpenID:    event.Event.Operator.OpenID,
        Action:    v.Action,
        RequestID: v.RequestID,
        FormValues: v.FormValues,
        RawValue:  raw,
        Timestamp: time.Now(),
    }, nil
}

func parseCardActionWebhookBody(body []byte) (*imctx.CardAction, error) { /* 同构,body schema 略异 */ }
```

## 5. Router 侧接口

### 5.1 `InteractionHandler` 接口(新增)

```go
// internal/channel/types.go
type InteractionHandler interface {
    HandleCardAction(ctx context.Context, action *imctx.CardAction) error
}

// Router 增加
func (r *Router) SetInteractionHandler(h InteractionHandler) { ... }
func (r *Router) HandleCardAction(ctx context.Context, action *imctx.CardAction) error {
    if r.interactionHandler == nil {
        r.logger.Warn("card action received but no handler", zap.String("request_id", action.RequestID))
        return nil
    }
    // dedup:同一 MessageID+RequestID 10 分钟窗口只处理一次
    key := action.MessageID + ":" + action.RequestID
    if r.cardDedup.isDuplicate(key) {
        return nil
    }
    return r.interactionHandler.HandleCardAction(ctx, action)
}
```

### 5.2 HITLBridge 实现

```go
// internal/bootstrap/hitl_bridge.go(新增,或归入 hitl_adapter.go)
type FeishuHITLBridge struct {
    broker    master.HITLBroker   // 已有 master.SubmitInput 接口
    feishuCli *feishu.Client
    logger    *zap.Logger
}

func (b *FeishuHITLBridge) HandleCardAction(ctx context.Context, a *imctx.CardAction) error {
    switch a.Action {
    case "approve", "reject":
        if a.RequestID == "" {
            b.logger.Warn("HITL action without request_id", zap.String("message_id", a.MessageID))
            return nil
        }
        decision := master.InputDecisionApprove
        if a.Action == "reject" {
            decision = master.InputDecisionReject
        }
        if err := b.broker.SubmitInput(master.InputResponse{
            ID:       a.RequestID,
            Decision: decision,
            UserID:   a.OpenID,
        }); err != nil {
            // 常见原因:request 已超时 / 已被其他用户响应过
            b.logger.Warn("HITL submit failed",
                zap.String("request_id", a.RequestID), zap.Error(err))
            // 给用户回一个 toast
            _ = b.feishuCli.UpdateCardAfterAction(ctx, a.MessageID,
                "⚠️ 该请求已失效(超时或已被处理)")
            return nil
        }
        // 成功后更新卡片
        txt := "✅ 已批准"
        if a.Action == "reject" { txt = "❌ 已拒绝" }
        _ = b.feishuCli.UpdateCardAfterAction(ctx, a.MessageID,
            fmt.Sprintf("%s · <@%s> · %s", txt, a.OpenID, time.Now().Format("15:04")))
        return nil
    case "form_submit":
        // 预留:form 提交可以追加用户消息到 session
        return b.handleFormSubmit(ctx, a)
    default:
        b.logger.Warn("unknown card action", zap.String("action", a.Action))
        return nil
    }
}
```

### 5.3 `Client.UpdateCardAfterAction`

复用 `PatchCard` 即可——把卡片替换成"已批准/拒绝 + actor + 时间"的静态版本。核心是**把按钮移除**,避免用户重复点击。

```go
// internal/channel/feishu/client.go(新增)
func (c *Client) UpdateCardAfterAction(ctx context.Context, messageID, resolutionLine string) error {
    card := BuildResolutionCard(resolutionLine) // 生成无按钮的静态卡片 JSON
    return c.PatchCard(ctx, messageID, card)
}
```

## 6. `bootstrap` 装配

`internal/bootstrap/server.go:377` 附近(feishu plugin 创建之后):

```go
feishuPlugin := feishu.New(cfg.Feishu, router, logger)
router.Register(feishuPlugin)

// ★ 新增:HITL bridge
if broker, ok := masterImpl.HITLBroker().(master.HITLBroker); ok {
    bridge := bootstrap.NewFeishuHITLBridge(broker, feishuPlugin.Client(), logger)
    router.SetInteractionHandler(bridge)
}
```

(注:`feishu.Plugin` 需新增 `Client() *Client` getter。)

## 7. 反应事件(Reactions)— 一期只记 metric

`longconn.go:97-102` 现场的两个空 handler 保留,但 body 换成 metric 上报:

```go
eventDispatcher.OnP2MessageReactionCreatedV1(func(_ context.Context, e *larkim.P2MessageReactionCreatedV1) error {
    metrics.Counter("feishu.reaction.created", 1,
        "emoji_type", safeEmojiType(e.Event))
    return nil
})
```

**故意不做**:把用户加表情作为 Agent 信号输入。原因:
- 信噪比低(用户随便加的表情会被错误翻译成"赞同")
- 需要按 session 绑定反应 → message 的逻辑,复杂度高
- 未见真实用户需求

保留钩子,后续有明确场景再扩。

## 8. 消息已读事件 — 一期只记 metric

同 §7。用于看 bot 消息的送达率,不进 Agent。

## 9. 消息撤回事件 — 可选,一期 skip

注册 `OnP2ImMessageRecalledV1`(如果 SDK 版本支持),一期只记 metric:

```go
metrics.Counter("feishu.message.recalled", 1, "chat_type", chatType)
```

**故意不做**:从 session.Messages 移除对应消息。原因:
- session 消息已流入 LLM 上下文,物理上无法"取消"
- 实现复杂(索引对齐、message_id 映射、持久化)
- 合规视角:用户撤回是对外部可见消息的撤回,bot 的记忆是否跟着忘,是独立问题

## 10. 测试清单

单测:

- `parseCardActionEvent` 正常 approve/reject/form_submit
- `parseCardActionEvent` value 缺 request_id
- `parseCardActionWebhookBody` 的飞书 body schema
- webhook 分发器:4 种 event_type 正确路由,未知类型静默 200
- `HandleCardAction` dedup(同 key 10 分钟内只处理一次)
- HITLBridge approve → broker.SubmitInput 被调且 Decision=Approve
- HITLBridge request 已失效时 → 发 toast 卡片,返 nil(不 error)
- UpdateCardAfterAction 渲染无按钮的静态卡片

集成:

- 端到端:Agent 触发 HITL → 卡片发出 → 模拟用户点按钮(调 webhook 打桩)→ broker 收到 response → Agent 继续执行
- 并发点击防抖:同一按钮被点两次,第二次被 dedup
- request 超时(broker 已回收)→ 用户点按钮 → 卡片更新为"已失效",broker 不抛 panic

蓝军:

- 飞书重投同一 card action 事件(header.event_id 相同,但 Sometimes 内容一致)→ dedup 拦住
- 恶意 value:`{"action":"__proto__","request_id":"evil"}` → `action=__proto__` 走 default 分支 warn + 忽略
- form_values 里含 `]]>` 序列化到后续 prompt → CDATA 转义处理(见 M1 §9)
- race:approve 在 broker 已 resolved 时 → broker.SubmitInput 返错 → bridge 不 crash,改卡片为"已失效"

## 11. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/longconn.go:72` | 现有 OnP2MessageReceiveV1 注册点,附近加 OnP2CardActionTriggerV1 |
| `internal/channel/feishu/webhook.go:88-91` | **必删的 `if Message == nil return` 丢弃逻辑**,改事件类型分发 |
| `internal/channel/feishu/card_builder.go:32-35,181-197` | HITL 按钮 value schema(接收侧契约) |
| `internal/channel/feishu/client.go:814` | `PatchCard`(UpdateCardAfterAction 复用) |
| `internal/channel/types.go` | `InteractionHandler` 接口新增点 |
| `internal/channel/router.go` | `HandleCardAction`/`SetInteractionHandler` 新增点 |
| `internal/master/hitl_broker.go` | `SubmitInput` / `InputResponse` / `InputDecision` 契约(待桥接) |
| `internal/bootstrap/server.go:377` | bridge 装配点 |
| `internal/bootstrap/hitl_adapter.go` | 现有 HITL 适配器(可扩展,或并列新增 feishu bridge) |
