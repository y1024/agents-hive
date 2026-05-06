# M7 · 可观测性与治理

> 让 SRE / 运维能从 metric 看到每个环节的健康度;让 ops 能通过 config flag 快速开关/降级单个模块。

## 1. 现状

- ✅ info/warn/error 日志(`zap`,分散在各文件)
- ✅ master 侧 trace_id(但 debounce 路径会丢)
- ⚠️ 唯一的 channel feature flag:`FeishuRendererConfig.Disabled`
- 🟡 已有部分 feishu-specific metrics:`feishu.resolver.duration_ms`、`feishu.inbound.refs_count`、`feishu.user_cache.hit/miss`、`feishu.bot.degraded`、`feishu.outbound.rejected`、`feishu.outbound.dead_letter`
- ❌ 无 rate limit / circuit breaker 指标
- ❌ 无 HITL callback / lifecycle event 指标

## 2. Metrics 清单

统一命名:`feishu.<module>.<metric>`。Label 统一用 snake_case。

### 2.1 消息摄取(M1)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.inbound.count` | Counter | `chat_type`, `message_type`, `ingress`(longconn/webhook) | 消息到达数 |
| `feishu.inbound.parse_duration_ms` | Histogram | `message_type` | ExtractInboundMessage 耗时 |
| `feishu.inbound.refs_count` | Histogram | — | 单条消息识别到的 DocRef 数 |
| `feishu.resolver.duration_ms` | Histogram | `outcome`(ok/timeout/error) | Resolver.Resolve 耗时 |
| `feishu.resolver.parent_resolved` | Counter | `status`(ok/dropped_self/failed/empty) | 父消息解析结果 |
| `feishu.resolver.wiki_resolved` | Counter | `status` | wiki → obj_token 解析结果 |
| `feishu.im_context.prefix_bytes` | Histogram | — | prompt prefix 字节数(监控膨胀) |

### 2.2 交互回调(M2)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.card_action.received` | Counter | `action`(approve/reject/form_submit/unknown), `ingress` | 卡片回调到达数 |
| `feishu.card_action.handle_duration_ms` | Histogram | `action` | 路由 + broker.SubmitInput 耗时 |
| `feishu.hitl.callback_status` | Counter | `outcome`(ok/expired/broker_error) | HITL 响应最终结果 |
| `feishu.reaction.created` / `deleted` | Counter | `emoji_type` | 反应事件(只监控不进 Agent) |
| `feishu.message.read` | Counter | — | 已读事件 |
| `feishu.message.recalled` | Counter | `chat_type` | 撤回事件 |

### 2.3 生命周期(M3)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.lifecycle.event_count` | Counter | `type`(bot_added/removed/p2p_created/...) | 生命周期事件 |
| `feishu.lifecycle.welcome_sent` | Counter | `type`(group/p2p), `outcome`(ok/error) | welcome 卡片发送结果 |
| `feishu.lifecycle.session_cleaned` | Counter | — | bot 被踢后清理 session 数 |

### 2.4 消息投递(M4)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.outbound.count` | Counter | `msg_type`, `outcome`(ok/retry/failed) | 发送总数 |
| `feishu.outbound.duration_ms` | Histogram | `msg_type` | 端到端发送耗时 |
| `feishu.outbound.retry_count` | Histogram | — | 单次发送的重试次数(0/1/2/3) |
| `feishu.outbound.rate_limited_count` | Counter | `chat_id` hashed | 被限流器拦住的次数 |
| `feishu.api.error_code` | Counter | `api`(send_message/reply/patch_card/...), `code` | 飞书 API 错误码分布 |
| `feishu.ack_reaction_latency_ms` | Histogram | — | 收到消息到贴 ack 表情的延迟 |
| `feishu.card_patch_latency_ms` | Histogram | — | renderer PATCH 到飞书返回的延迟 |

### 2.5 身份(M5)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.user_cache.hit` / `miss` | Counter | — | 用户缓存命中率 |
| `feishu.user_cache.size` | Gauge | — | 当前缓存条目数 |
| `feishu.bot_openid_fetch_duration_ms` | Histogram | — | sync.Once 首次拉取耗时 |

### 2.6 推送(M6)

| metric | 类型 | label | 语义 |
|---|---|---|---|
| `feishu.push.request_count` | Counter | `source`(api/cron/library), `outcome` | 推送请求数 |
| `feishu.push.duration_ms` | Histogram | `msg_type` | 端到端耗时 |
| `feishu.push.idempotency_hit` | Counter | — | 幂等缓存命中(未实际发送) |
| `feishu.push.rate_limited` | Counter | `chat_id` hashed | per-chat 限流命中 |

## 3. Feature Flags

统一挂在 `FeishuConfig` 各子结构下(已在 M1-M6 各文档声明),这里汇总:

| 开关 | 默认 | 作用 |
|---|---|---|
| `Feishu.Enabled` | false | 飞书通道总开关(不变) |
| `Feishu.Renderer.Disabled` | false(启用) | 流式卡片 → legacy 一次性发送回滚 |
| `Feishu.Inbound.EnableContextResolver` | true | 消息摄取 Resolver(M1)开关 |
| `Feishu.Interaction.EnableCardCallback` | true | 卡片按钮回调(M2)开关 |
| `Feishu.Interaction.EnableReactionMetric` | true | 反应事件记 metric(关了就不注册 handler) |
| `Feishu.Lifecycle.Enabled` | true | 生命周期事件总开关(M3) |
| `Feishu.Lifecycle.WelcomeOnBotAdded` | true | bot 入群是否发 welcome |
| `Feishu.Lifecycle.WelcomeOnP2PCreate` | true | P2P 首建是否发 welcome |
| `Feishu.Outbound.EnableBinaryTransfer` | false | 图/文件 upload/download 工具(M4) |
| `Feishu.Identity.EnableGroupEnrich` | true | 群聊是否调 enrichCtx(M5) |
| `Feishu.Push.Enabled` | false | 主动推送 API(M6) |

**设计原则**:
- 新能力默认**启用**(true),除非有显式理由默认关
- 高风险能力(push、binary transfer)默认关,需要显式 opt-in
- 每个开关都支持**热重载**,不需要重启进程

## 4. 降级路径

按"影响面最小 → 最大"排序:

1. `Inbound.EnableContextResolver = false` → Agent 只看原始 content,引用识别失效。链路:**完全不调 feishu API,零延迟影响**
2. `Interaction.EnableCardCallback = false` → HITL 按钮重新变死,回到 M2 之前的状态(但其他消息路径不受影响)
3. `Lifecycle.Enabled = false` → event 仍收到,只是 handler 不 action(空 body)
4. `Outbound.EnableBinaryTransfer = false` → 图/文件工具从 Agent tool 列表消失(Agent 看不到就不调)
5. `Renderer.Disabled = true` → 全 channel 回退到一次性 Send,用户看不到流式更新
6. `Feishu.Enabled = false` → 整个通道下线

**每一级降级都应该能通过 DB 改 channel_configs 触发热重载,不需要改代码/重启**。

## 5. 日志字段统一

所有 feishu 相关日志必须带以下 structured fields(当场景适用时):

- `platform` = "feishu"(让 SRE 一键过滤)
- `chat_id`
- `message_id`
- `sender_id`(open_id,**不要**打真名,避免 PII 泄漏)
- `event_type`(longconn/webhook event 分类)
- `phase`(ingress/resolver/dispatch/send/hitl 等)

示例:

```go
logger.Info("card action dispatched",
    zap.String("platform", "feishu"),
    zap.String("chat_id", a.ChatID),
    zap.String("message_id", a.MessageID),
    zap.String("action", a.Action),
    zap.String("phase", "hitl_dispatch"),
)
```

## 6. Trace 连续性

目前 debounce 路径会把 ctx 换成 `context.Background()`(`router.go:241` 注释写明"已知限制"),trace_id 丢。

**一期不修**(scope 大),但在以下地方把 trace_id 作为 logger field 显式传递,保留关联关系:

- `InboundMessage.MessageID` 作为 per-message 的稳定标识(不是 trace_id 但可用于 log 聚合)
- 所有跨组件日志都带 `message_id`

未来若需要完整 trace,debounce 侧可以保留原 ctx 的 `trace_id` value 重新 inject 到新 ctx,不做全量 ctx 复用。

## 7. 热重载

现有机制:`channel_configs` 表修改触发 `hive` 的 config watcher,下发到 `Router` 等组件的 setter。

**新增要求**:M1-M6 每个模块在 bootstrap 注册时必须实现 `ReloadFromConfig(cfg FeishuConfig)` 接口:

```go
// internal/channel/feishu/reloadable.go
type Reloadable interface {
    ReloadFromConfig(cfg config.FeishuConfig) error
}

// Resolver / LifecycleHandler / HITLBridge / UserCache / PushService 都实现此接口
```

`bootstrap` 在 config 变化时遍历调用 ReloadFromConfig。

**不允许**的做法:整个 Plugin 重建(会丢掉运行中的 session binding、dedup 窗口、缓存状态)。

## 8. 测试

单测:

- 所有 metric 名称集中在 `metrics_names.go`,单测断言没有拼写漂移(grep 所有 `metrics.Counter("feishu.` 必须命中该文件里的常量)
- ReloadFromConfig 对 resolver/cache 的热切换:从 Enabled=true → false 不 panic,in-flight 请求正常完成

集成:

- 跑一遍消息-resolver-HITL-lifecycle-push 全链路,查 metric endpoint 有对应指标上报
- 配置热改 Renderer.Disabled=true:后续消息走 legacy,metric `feishu.outbound.count{msg_type=text}` 上涨

## 9. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/observability/metrics.go` | 现有 metric 入口,新增 metric 在此注册 |
| `internal/channel/feishu/metrics_names.go` | **新建**,所有 metric 名常量 |
| `internal/channel/feishu/reloadable.go` | **新建**,Reloadable 接口 |
| `internal/config/config.go:881` | `FeishuConfig` 所有子 config 聚合点 |
| `internal/bootstrap/server.go:981` | 配置热重载触发点(调各组件 ReloadFromConfig) |
