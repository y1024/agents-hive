# M9 · 可靠性与一致性

> 多副本部署、断线重连、消息补偿、失败重放。当前所有去重/一致性机制都是**单进程内存级**,HA 部署下必然漏丢重。

## 1. 现状

| 项 | 位置 | 状态 |
|---|---|---|
| `event_id` 去重 | 内存 LRU | ⚠️ 单进程,多副本各自去重窗口独立 → **同一 event 被两个副本各处理一次** |
| at-least-once 投递 | 依赖飞书 + SDK | 🟡 飞书保证 at-least-once,我方必须幂等,**当前非幂等** |
| longconn 断线补偿 | 无 | ❌ 断线期间消息直接丢 |
| 飞书 API 失败降级 | 无 | ❌ ratelimit / 5xx 用户无感知 |
| 消息顺序保证(同 chat) | debounce | ⚠️ 单副本内序,多副本下两副本同时收同 chat 消息 → 无序 |
| 发送失败持久化重放 | `retry_queue.go` | 🟡 已有 Postgres retry_queue worker / 重排 / dead-letter metric,手工 replay / 运维面仍待补齐 |

## 2. 分布式去重

### 2.1 存储选型

| 选项 | 优点 | 缺点 | 决策 |
|---|---|---|---|
| Redis SETEX | 低延迟 | 多一个依赖 | 备选 |
| Postgres UPSERT + TTL | 无新依赖(已有 pgx) | 写 P99 ~5ms | ✅ 首选 |
| 内存 + gossip | 无依赖 | 复杂度高 | 拒绝 |

### 2.2 表结构

```sql
CREATE TABLE IF NOT EXISTS feishu_event_dedup (
    event_id     TEXT PRIMARY KEY,
    tenant_key   TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at   TIMESTAMPTZ,                          -- 阶段 1 标记(红队修正)
    processed    BOOLEAN NOT NULL DEFAULT FALSE,       -- 阶段 2 标记
    processed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_feishu_event_dedup_first_seen ON feishu_event_dedup(first_seen);
CREATE INDEX IF NOT EXISTS idx_feishu_event_dedup_reclaim
    ON feishu_event_dedup(claimed_at) WHERE processed = FALSE;

-- 定期清(只清已 processed 的,未 processed 的留给 reclaim worker):
-- DELETE FROM feishu_event_dedup WHERE processed = TRUE AND processed_at < NOW() - INTERVAL '24 hours';
```

### 2.3 使用

> ⚠️ **红队修正(2026-04-22)·链 A 核心不变量**:原稿"单阶段 claim + fail-open"是**消息永失 + 双回复**的根源。必须做两阶段 claim + fail-closed 短超时 + 业务错误不 return err。详见 §2.4。

```go
// internal/channel/feishu/dedup.go
type DistributedDedup struct {
    pool *pgxpool.Pool
    ttl  time.Duration
}

// ClaimEvent 尝试原子"认领" event(阶段 1:claimed)。
// 返回 (claimed bool, err error):
//   claimed=true  → 本副本负责处理(必须在处理完成后调 MarkProcessed)
//   claimed=false → 已被认领,进一步检查 processed 决定是 drop 还是 reclaim
func (d *DistributedDedup) ClaimEvent(ctx context.Context, eventID, tenantKey, eventType string) (bool, error) {
    row := d.pool.QueryRow(ctx, `
        INSERT INTO feishu_event_dedup (event_id, tenant_key, event_type, claimed_at)
        VALUES ($1, $2, $3, NOW())
        ON CONFLICT (event_id) DO NOTHING
        RETURNING event_id
    `, eventID, tenantKey, eventType)
    var id string
    if err := row.Scan(&id); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return false, nil  // 已被认领
        }
        return false, err
    }
    return true, nil
}

// MarkProcessed 在业务处理成功后调用,标记 processed=true(阶段 2)。
// 未调 MarkProcessed 的 claim 会被 reclaim worker 重新认领。
func (d *DistributedDedup) MarkProcessed(ctx context.Context, eventID string) error {
    _, err := d.pool.Exec(ctx, `
        UPDATE feishu_event_dedup SET processed = TRUE, processed_at = NOW()
        WHERE event_id = $1
    `, eventID)
    return err
}
```

### 2.4 两阶段 claim + fail-closed 短超时(红队链 A 修复)

**问题**:原稿是单阶段 `INSERT ON CONFLICT` + fail-open,触发两条致命链:

1. **消息永失**:副本 A claim 成功 → 崩溃 / OOM / 被 K8s 驱逐 → 副本 B 收到同一 event,看到 `DO NOTHING` 直接 drop → 用户永不收回复。
2. **双回复**:DB 抖动时 `ClaimEvent` 返 err → fail-open 继续处理 → 两副本都 fail-open → 回复两次。

**修复**:

1. **表新增两列**:`claimed_at TIMESTAMPTZ`、`processed BOOLEAN`、`processed_at TIMESTAMPTZ`。
2. **入口改为两阶段**:

```go
// webhook dispatcher 入口
claimCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)  // 短超时
defer cancel()

claimed, err := dedup.ClaimEvent(claimCtx, eventID, tenantKey, eventType)
if err != nil {
    // Fail-closed:短超时内 DB 不可达 → 拒绝处理(返 nil,让飞书继续重投,另一副本处理)
    // 不 fail-open,避免链 B 双回复
    metrics.Counter("feishu.dedup.fail_closed", 1)
    return nil  // 返 nil!不是 err,不触发 500
}
if !claimed {
    metrics.Counter("feishu.event.deduped", 1)
    return nil
}

// 业务处理
if err := handleEvent(ctx, event); err != nil {
    // 业务错误也 return nil,避免 SDK 触发 500 → 飞书重投 → 链 A
    // 失败 event 由 reclaim worker + retry_queue 承接
    logger.Error("handle event failed", zap.Error(err))
    metrics.Counter("feishu.event.handler_error", 1)
    enqueueRetry(ctx, event, err)
    return nil
}

// 成功 → 标记 processed
_ = dedup.MarkProcessed(ctx, eventID)
return nil
```

3. **Reclaim worker**:周期性扫 `claimed_at < NOW() - claim_ttl AND processed = FALSE`,把"claim 了但没 processed"的 event 的 claim 释放(或直接置 `claimed_at = NULL`),让下次 webhook 重投时另一副本可重新 claim。

```sql
-- reclaim worker,每 30s:
UPDATE feishu_event_dedup
SET claimed_at = NULL
WHERE processed = FALSE
  AND claimed_at IS NOT NULL
  AND claimed_at < NOW() - INTERVAL '90 seconds'
RETURNING event_id;
```

(飞书 webhook 重试窗口 > claim_ttl 即可,实际 ~3-5 次 exponential backoff。)

4. **handler 永不返 err**:SDK `dispatcher.processError` 在 `event/dispatcher/dispatcher.go:63` 把 handler 的 err 直接转成 `http.StatusInternalServerError`,飞书看到 500 就重投。因此业务错误必须在 handler 内部"吃掉"并落盘到 retry_queue(§5),**绝不返 err**。

**为什么不 fail-open**:fail-open 在单入口 + 两阶段 claim 下也不安全 —— DB 抖动时两副本都 fail-open,两个副本都处理同一条 event,最终会 `messages` 表层幂等去重,但 agent 已经跑了两次,LLM token 成本翻倍,HITL 副作用(审批按钮 / 工具调用)不可撤回。短超时 fail-closed 把失败推回飞书重投,由 Feishu 的 at-least-once 语义承接。

## 3. 幂等

### 3.1 消息处理幂等

- `session_id` 一律由 `BuildSessionID(tenantKey, chatID)` 构造为 `im-feishu-{tenantKey}-{chatID}`(Phase 0 多租户前向兼容,详见 [`11-multi-tenant.md §8`](11-multi-tenant.md#8-一期落地要求phase-0-硬性不可推迟)),同一 event 重复进来 → 同一 session
- `messages` 表写入前按 `(session_id, client_message_id)` upsert,`client_message_id = feishu_event_id`
- Agent 执行过的 action 若带 feishu message_id,master 侧按 message_id 幂等

### 3.2 HITL 幂等

- `master.HITLBroker.SubmitInput` 对同一 `InputID` 重复提交应返 "already resolved" 而非报错
- Card patch(移除按钮)非幂等但可容忍,PatchCard 失败时 log warn 不 return error

### 3.3 Push 幂等

见 `06-push.md` §2.1 的 `IdempotencyKey`。底层也走 Postgres UPSERT,与 event dedup 同一个表模式。

## 4. longconn 断线补偿 [Phase 2B — 2B.1/2B.2/2B.3 已完成]

> ⚠️ **红队修正(2026-04-22)**:本节原稿依赖 `larkws.WithStatusChangeHandler` 这个**在 SDK v3.5.3 不存在的符号**。经验证 `ws/client.go:46` 只有 5 个 Option:`WithEventHandler / WithLogLevel / WithLogger / WithAutoReconnect / WithDomain`,**没有任何状态回调**。`Start()` 里 `connect` 之后直接 `select {}` 常驻,SDK 不暴露"正在重连 / 已重连"事件。因此原稿"按连接状态触发 gap fetch"的方案**落点前提是错的**,整节必须重写。
>
> **当前状态(2026-04-25)**:Phase 2B 已按 3 个独立子阶段完成落地。`2B.1 single-ingress switching` 已完成,解决 `webhook | longconn` 显式单入口切换与 fail-closed 热切换。`2B.2 reconnect watchdog` 已完成自建状态机与恢复钩子。`2B.3 gap fetch` 已完成标准路由链路回补、自动恢复回放与多副本 single-leader 编排。

### 4.1 场景(Phase 2B)

飞书 longconn SDK 自动重连(larkws 内置),但**重连期间推送给本实例的 event 全丢**。这是 longconn 方案的结构性缺陷,webhook 路径不受影响。

生产入口选型(CEO 决议):

- **Phase 0 生产模式:webhook-only**。客户私有化但一般有 ingress / 反向代理,webhook 可覆盖主要场景。
- **Phase 2B.1 已先完成单入口切换**:任何后续 longconn 能力都必须建立在 `never dual ingress` 之上。
- **Phase 2B.2 才实现 watchdog**:先解决“是否断线/是否恢复”的判定问题。
- **Phase 2B.3 才实现 gap fetch**:再解决“恢复后补哪些消息”的问题。
- **禁止 webhook + longconn 在同一进程并存**(红队链 B:双入口 × dedup 漏洞会双投)。

### 4.2 SDK 限制(事实)

`ws.Client` 暴露的 Option 清单(`github.com/larksuite/oapi-sdk-go/v3@v3.5.3/ws/client.go:46` 起):

```
WithEventHandler(h)   // 事件 dispatcher
WithLogLevel(level)
WithLogger(logger)
WithAutoReconnect(b)  // 只能打开/关闭,不告诉你"现在正在重连"
WithDomain(d)
```

**没有** `WithStatusChangeHandler`,**没有** `larkws.Status` / `StatusReconnecting` / `StatusConnected` 这些类型。SDK 内部 `Start()` 是 `connect(); select {}` 阻塞,重连由内部循环处理,**不对外发信号**。

结论:要在重连后补偿消息,只能**自建 watchdog**,不能依赖 SDK 回调。

### 4.3 Phase 2B.2 自建 watchdog 现状

当前实现要点:

1. **时间水位探测**:`LongConnClient` 维护 `lastEventAt`,超过 `HeartbeatStaleWindow` 未收到事件则进入 `reconnecting`。
2. **状态推进**:当前稳定产出 `healthy -> reconnecting -> recovered` 状态推进;恢复由第一条重新进入的事件触发。
3. **恢复触发**:`markEventObserved()` 在恢复时准备 pending gap window,并触发 `onRecovered` 回调。
4. **可观测性**:当前已暴露结构化日志 + `ReliabilityStatus()` 快照,包含 `Reconnecting / ReliabilityLeader / GapFetchPending / PendingGapFetchWindow / PendingGapFetchWindowCapped / LastGapFetch*`。
5. **多副本互斥**:自动回放通过 Postgres session advisory lock 做 single leader gate,非 leader 副本跳过恢复回放。

### 4.4 Phase 2B 验收清单

#### 4.4.1 Phase 2B.1 single-ingress switching(已完成)

- [x] 显式 `ingress_mode=webhook|longconn`
- [x] Feishu webhook 常驻路由 + runtime gate
- [x] reload 切换期间 **never dual ingress**
- [x] 切换失败保持 fail-closed,不自动放开另一入口

#### 4.4.2 Phase 2B.2 reconnect watchdog(已完成)

- [x] 不依赖 `WithStatusChangeHandler` / `larkws.Status` 等 SDK 不存在能力
- [x] watchdog 能区分 `healthy` / `reconnecting` / `recovered`
- [x] reconnecting 至少有一种稳定可查的 observability:结构化日志 + 可靠性状态快照
- [x] 多副本下只有一个副本执行恢复回放 leader 逻辑
- [x] watchdog 本身不直接拉消息,只负责状态探测、窗口准备与恢复回调

#### 4.4.3 Phase 2B.3 gap fetch(已完成)

- [x] gap fetch 仅在 watchdog 判定恢复后触发,不会在未知状态盲拉
- [x] `Im.Message.List` 显式传 `ContainerIdType` 与 `EndTime`
- [x] gap fetch 受 `GapFetchMaxWindow` 上限约束,超出窗口会截断并暴露日志/状态
- [x] 补回消息逐条进入标准 dedup/claim/dispatcher 流程,不旁路幂等保护
- [x] gap fetch 失败时保持单入口语义,不会退化成 webhook + longconn 双收

### 4.5 config(Phase 2B)

```go
FeishuReliabilityConfig.LongconnEnabled         bool  // 默认 false(Phase 0)
FeishuReliabilityConfig.LongconnGapFetchEnabled bool  // 默认 false(Phase 0)
FeishuReliabilityConfig.HeartbeatStaleWindow    time.Duration  // Phase 2 才读
FeishuReliabilityConfig.GapFetchMaxWindow       time.Duration  // Phase 2,默认 10 分钟
```

Phase 0 代码层需要做的(**仅**这些):

- 配置项存在但默认 `false`,保证 feature flag 未打开即完全无 longconn 代码路径。
- `bootstrap/server.go` 对 `LongconnEnabled=true` 直接 `log.Fatal("longconn is Phase 2 only")`,避免有人偷偷打开。

## 5. Outbound 失败重放队列

### 5.1 场景

`withRetry` 3 次耗尽后还是失败(比如飞书服务商 15 分钟宕机) → 当前直接丢。对"必达"消息(push API + scheduled 播报)不可接受。

### 5.2 设计

> ⚠️ **红队修正(2026-04-22)**:
> 1. 原稿 schema 缺 `tenant_key`,与 [`11-multi-tenant.md §8`](11-multi-tenant.md#8-一期落地要求phase-0-硬性不可推迟) 要求"所有新建表带 `tenant_key`"冲突 → 已补。
> 2. `content TEXT NOT NULL` 直接长存原文,把"日志不落 PII"的治理转移成"队列表常驻 PII"的新泄露面 → Phase 5 worker 完整化时必须改为 **payload 最小化**:只存 `chat_id` / `msg_type` / `template_id` / `template_args_json`(脱敏),原文由 template 重建。Phase 0 的"骨架"表先按下面 schema 落,但 Phase 0 **不写真实业务消息**进表(只供 §2.4 handler wrapper 兜底落 event 重试元数据)。

```sql
CREATE TABLE IF NOT EXISTS feishu_outbound_retry_queue (
    id              BIGSERIAL PRIMARY KEY,
    tenant_key      TEXT NOT NULL DEFAULT 'default',
    platform        TEXT NOT NULL,
    chat_id         TEXT NOT NULL,
    msg_type        TEXT NOT NULL,
    -- Phase 0:content 暂留 NOT NULL,但只承载脱敏后的最小元数据;
    -- Phase 5 worker 完整化时拆为 template_id + template_args_json(payload 最小化)
    content         TEXT NOT NULL,
    retry_count     INT NOT NULL DEFAULT 0,
    next_retry_at   TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    idempotency_key TEXT
);
CREATE INDEX IF NOT EXISTS idx_feishu_retry_next ON feishu_outbound_retry_queue(next_retry_at)
    WHERE retry_count < 5;
CREATE INDEX IF NOT EXISTS idx_feishu_retry_tenant ON feishu_outbound_retry_queue(tenant_key);
```

worker 定时扫:

```go
// 每 30s:
rows := SELECT * FROM feishu_outbound_retry_queue WHERE next_retry_at <= NOW() AND retry_count < 5 LIMIT 100
for each row:
  err := plugin.Send(...)
  if err == nil: DELETE
  else: UPDATE SET retry_count+=1, next_retry_at = NOW() + exp(retry_count) * 1min, last_error = err
```

### 5.3 范围

**仅 push API + scheduled + lifecycle welcome 卡片入队**。普通 inbound 触发的 reply **不入队**——用户看不到响应会自己再问,入队反而造成延迟回复混乱。

### 5.4 config

```go
FeishuReliabilityConfig.RetryQueueEnabled bool  // 默认 true(仅对 push/scheduled/welcome)
FeishuReliabilityConfig.RetryQueueMaxAttempts int  // 默认 5
```

## 6. 消息顺序

### 6.1 现状

同 chat 内多条消息 → master debounce 合并成一轮 agent 执行。顺序由 debounce window 内的到达顺序决定,多副本场景下两副本各收一半 → 无序。

### 6.2 策略

**按 `chat_id` 分区到同一副本**。

- K8s 场景:Service 的 session affinity(基于 chat_id hash)→ 飞书 webhook POST 按 chat_id hash 路由到固定 pod
- 非 K8s:前置 nginx/envoy 配 hash key 为 `chat_id`(从 JSON body 提取)

这样 dedup 表更多是 **兜底**(chat affinity 破坏时的 safety net),正常情况下不触发。

### 6.3 longconn 无此问题

longconn 每个副本独立连飞书,飞书自己决定路由到哪个副本。但 longconn **本来就是开发场景**(§4.1),生产走 webhook 时顺序由 affinity 保证。

## 7. Config

> ⚠️ **红队修正(2026-04-22)**:原稿 `FailOpenOnDedupError` 配置项**已删除**。fail-closed 是 Phase 0 的硬不变量(链 B 的根因之一),不允许通过 config 重新打开 fail-open。`LongconnGapFetchEnabled` 默认值改为 `false`(Phase 0 webhook-only)。

```go
type FeishuReliabilityConfig struct {
    // 分布式去重(Phase 0 必开,无开关)
    DistributedDedupEnabled bool          `json:"distributed_dedup_enabled"`           // 默认 true
    DedupTTL                time.Duration `json:"dedup_ttl,omitempty"`                 // 默认 24h
    DedupClaimTimeout       time.Duration `json:"dedup_claim_timeout,omitempty"`       // 默认 200ms,fail-closed 短超时
    DedupReclaimAfter       time.Duration `json:"dedup_reclaim_after,omitempty"`       // 默认 90s,reclaim worker 阈值

    // Longconn(Phase 2 才启用,Phase 0 LongconnEnabled=true 启动 fatal)
    LongconnEnabled         bool          `json:"longconn_enabled,omitempty"`          // 默认 false
    LongconnGapFetchEnabled bool          `json:"longconn_gap_fetch_enabled,omitempty"` // 默认 false
    HeartbeatStaleWindow    time.Duration `json:"heartbeat_stale_window,omitempty"`    // Phase 2 才读
    GapFetchMaxWindow       time.Duration `json:"gap_fetch_max_window,omitempty"`      // Phase 2,默认 10m

    // 失败重放队列(Phase 0 仅落盘,worker 在 Phase 5)
    RetryQueueEnabled       bool          `json:"retry_queue_enabled"`                 // 默认 true
    RetryQueueMaxAttempts   int           `json:"retry_queue_max_attempts,omitempty"`  // 默认 5,Phase 5 用

    // ❌ 已删除:FailOpenOnDedupError —— Phase 0 起强制 fail-closed,不留 config 后门
}
```

## 8. 降级

| 失效组件 | 影响 | 降级路径 |
|---|---|---|
| Postgres | dedup 失败 | **fail-closed**,入口返 `nil` 让飞书重投,禁止 fail-open |
| Postgres | retry queue 不可写 | push/scheduled 失败直接返错给调用方 |
| longconn | 重连中/疑似断线 | 2B.2 watchdog 进入 reconnecting / fail-closed 态,2B.3 再做 gap fetch 补偿 |
| webhook | HTTP 服务故障 | 不自动切到 longconn,运维显式切 `ingress_mode` 后再 reload |

## 9. 测试

单测:
- [Phase 0/2A 已完成] DistributedDedup:并发 1000 次 claim 同 event_id,恰好 1 次 claimed=true
- [Phase 0/2A 已完成] dedup 短超时失败 → fail-closed,返 `nil` 等飞书重投
- [Phase 5] retry queue:5 次耗尽后不再重试,落 dead letter metric
- [Phase 2B.3] gap fetch 对已处理 event 被 dedup 拦截

集成:
- [Phase 0/2A 已完成] 3 副本部署,同时收 webhook:只有 1 副本处理;其他 2 副本 deduped metric +1
- [Phase 0/2A 已完成] 人为把 Postgres 断电/超时:fail-closed,本副本不处理,等待飞书重投
- [Phase 2B.1 已完成] webhook ⇋ longconn reload:切换窗口内 never dual ingress
- [Phase 2B.2/2B.3] longconn 手动断网 1 分钟:先进入 reconnecting,恢复后再触发 gap fetch 补处理

蓝军:
- event_id 为空 / 重复 → 保守 drop + metric
- longconn 断线且 watchdog 连续探测失败:必须暴露 reconnecting / fail-closed 可观测信号
- retry queue 单表 10 万行待重试 → worker 不应 block;批处理 + LIMIT
- gap fetch 窗口 10 分钟内飞书返 5 万条(爆群):分页 + rate limit + `GapFetchMaxWindow` 截断

## 10. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/dedup.go` | **新建**,`DistributedDedup` |
| `internal/channel/feishu/retry_queue.go` | **新建**,失败重放 worker |
| `internal/channel/feishu/reconnect_watchdog.go` | **待新建**,自建 watchdog(不依赖 SDK status callback) |
| `internal/channel/feishu/gap_fetch.go` | **新建**,longconn 重连后补偿 |
| `migrations/<ts>_feishu_dedup.sql` | **新建**,dedup + retry queue 建表 |
| `internal/channel/feishu/webhook.go` | 入口调 `ClaimEvent` |
| `internal/channel/feishu/longconn.go` | longconn 入口与 `lastEventAt` 刷新,不依赖 `StatusChangeHandler` |
| `internal/channel/feishu/plugin.go` | send 失败时对 push/scheduled/welcome 入 retry queue |
| `internal/config/config.go` | `FeishuReliabilityConfig` |
