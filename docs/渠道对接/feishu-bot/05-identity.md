# M5 · 身份与权限

> 让 bot"认得人"。当前 Phase 3 已完成主链路：bot 自身 open_id 同步缓存、发送者/mentions 真名回填、群聊 enrich 与 `IMContextValue` 注入、群管理员按需查询能力、`/reset` 群管理员 ACL 接线，以及基于 `Region` 的默认 locale 选择。剩余尾项主要是线上指标命中率验证。

## 1. 现状

| 项 | 位置 | 状态 |
|---|---|---|
| Bot open_id 获取 | `client.go` / `webhook.go` / `longconn.go` / `resolver.go` | ✅ `Client.BotOpenID()` sync.Once 缓存,webhook/longconn/resolver 一致 |
| SenderName 解析 | `resolver.go` | ✅ resolver 会把 `SenderName` 从 open_id 回填成真名 |
| 用户详情缓存 | `user_cache.go` | ✅ TTL+singleflight+LRU+tombstone 已接入 |
| 群管理员识别 | `tools/feishu_tools.go` / `formatter.go` | ✅ 已支持 `get_chat_admins(chat_id)` 按需查询 |
| IM → 内部 user_id 绑定 | `router.go` `enrichCtx` / `IMContextValue` | ✅ 群聊 enrich 已支持 |
| 权限/命令鉴权 | `acl.go` / `governance.go` | ✅ `/reset` 已按群管理员/私聊本人判定 |

## 2. Bot OpenID 同步化

### 2.1 Client 侧

```go
// internal/channel/feishu/client.go
type Client struct {
    ...
    botOpenID     string
    botOpenIDOnce sync.Once
    botOpenIDErr  error
}

// BotOpenID 返 bot 自身 open_id,首次调用同步拉取并缓存。
// 为什么同步:父消息自反射防御依赖 open_id,失败会让 bot 把自己的回复当用户 prompt。
// 首次阻塞 < 5s,之后全内存读。
func (c *Client) BotOpenID() string {
    c.botOpenIDOnce.Do(func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        c.botOpenID, c.botOpenIDErr = c.fetchBotOpenID(ctx)
    })
    return c.botOpenID
}
```

### 2.2 启动期预热

`internal/bootstrap/server.go:377` feishu client 构造后立即:

```go
feishuClient := feishu.NewClient(...)
_ = feishuClient.BotOpenID()  // 预热,阻塞最多 5s。失败返空串,parent 自反射防御会自动 degrade(保守放行)
```

`longconn.go:52-62` 原异步拉取**删除**。webhook/longconn 全部从 `client.BotOpenID()` 取,状态一致。

### 2.3 失败语义

bot open_id 拉失败 → `BotOpenID()` 返空串 → 父消息自反射判断 `parent.SenderOpenID == ""` 永远 false → 保守策略:任何父消息都不会被误判为自反射,最多让 agent 看到自己的历史回复(可能混淆但不致命)。

## 3. 用户真名解析

### 3.1 Resolver 路径

M1 §8 的 `ContextResolver.Resolve` 里,在解析父消息之后、构建 prefix 之前,补发送者真名:

```go
if msg.SenderName == "" || msg.SenderName == msg.SenderID {  // 占位符状态
    if user, err := r.userCache.GetOrFetch(ctx, msg.SenderID, r.client); err == nil {
        msg.SenderName = user.Name  // 写回到 InboundMessage
    }
}
```

### 3.2 用户详情缓存

```go
// internal/channel/feishu/user_cache.go
type UserCache struct {
    // LRU + TTL(12 小时)
    lru  *simplelru.LRU
    ttl  time.Duration
    mu   sync.RWMutex
    sf   singleflight.Group  // 并发同一 open_id 只打一次 API
}

type CachedUser struct {
    OpenID   string
    Name     string
    EnName   string
    Email    string
    JobTitle string
    FetchedAt time.Time
}

func (c *UserCache) GetOrFetch(ctx context.Context, openID string, client *Client) (*CachedUser, error)
```

当前实现:TTL 12h + singleflight 去重 + LRU 淘汰 + tombstone(失败短墓碑) 已完成。

### 3.3 mentions 真名回填

M1 §3 的 `imctx.Mention` 已有 `Name` 字段,但飞书 event 里 mentions 的 Name 有时只是昵称。Resolver 里:

```go
for i := range out.Mentions {
    if out.Mentions[i].OpenID == "" { continue }
    if u, err := r.userCache.GetOrFetch(ctx, out.Mentions[i].OpenID, r.client); err == nil {
        out.Mentions[i].Name = u.Name   // 覆盖为真名
    }
}
```

### 3.4 Prompt prefix 受益

替换后 prompt 片段:

```xml
<mentions>
  <m name="张三" is_bot="false"/>
  <m name="李四" is_bot="false"/>
  <m name="DataBot" is_bot="true"/>
</mentions>
```

而不是之前的 `<m name="ou_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"/>`。Agent 在对话里能真实指代用户名。

## 4. 群管理员识别(可选,按需用)

**不主动**拉取群成员(每次消息一次 `ListChatMembers` 太重),通过工具让 Agent 按需查:

`tools/feishu_tools.go` 现已提供 `get_chat_admins(chat_id)`，底层复用 `GetChatInfo` 返回的 `owner_id` / `user_manager_id_list` / `bot_manager_id_list`，可直接让 Agent 判断群主、用户管理员、机器人管理员。

当前已把 `/reset` 接到群管理员 ACL：私聊始终允许，群聊按需调用 `GetChatInfo` 判断 `owner_id` / `user_manager_id_list` / `bot_manager_id_list`，配置里的 `reset_allowlist` 作为 super-admin 兜底。未来若扩到 `/debug` `/mute` `/audit`，可继续复用同一路径并补 `ChatRoleCache`。

## 5. IM → 内部 user_id 绑定

### 5.1 现状

历史现状:

```go
if r.enrichCtx != nil && msg.SenderID != "" && msg.ChatType == ChatDirect {
    ctx = r.enrichCtx(ctx, msg.SenderID, platformToProvider(msg.Platform))
}
```

当前实现已改为群聊也可 enrich,并通过 `FeishuIdentityConfig.EnableGroupEnrich` 控制是否开启。

### 5.2 扩展

当前实现:

```go
type imContextKey struct{}
type IMContextValue struct {
    SenderOpenID string
    Platform     string
    ChatType     ChatType   // p2p / group
    InternalUserID string   // 绑定的内部 user_id,群聊也填
}

// processMessageImpl 里:
ctxValue := &IMContextValue{
    SenderOpenID: msg.SenderID,
    Platform: string(msg.Platform),
    ChatType: msg.ChatType,
}
if r.enrichCtx != nil && msg.SenderID != "" {
    // enrichCtx 返回增强后的 ctx,内部挂 auth.User
    ctx = r.enrichCtx(ctx, msg.SenderID, platformToProvider(msg.Platform))
    if u := auth.UserFrom(ctx); u != nil {
        ctxValue.InternalUserID = u.ID
    }
}
ctx = context.WithValue(ctx, IMContextKey{}, ctxValue)
```

下游在需要"发消息的用户是我们系统的谁"时可以 `auth.UserFrom(ctx)` 或取 `IMContextValue.InternalUserID`。

### 5.3 群聊用户绑定的安全含义

- `checkSessionAccess`(现有 auth 中间件)对 IM 路径 `IsAuthEnabled=false` 放行,不变
- 但 master 里的 usage metric / quota 现在能按群聊消息的发送者归属到具体用户(之前只能归属到 session)

## 6. 权限层

当前已落地最小权限层：群管理员能触发 `/reset`，普通群员不能；私聊仍允许用户重置自己的 session。

当前接口:

```go
type CommandACL interface {
    CanReset(ctx context.Context, tenantKey, chatID, openID string, isDirect bool) (bool, error)
}
```

后续若扩更多命令，再把接口推广到 `CanDebug` / `CanMute` / `CanAudit` 等细粒度判定。

## 7. Config

```go
type FeishuConfig struct {
    ... // 现有字段
    Region string `json:"region,omitempty"`
    Identity FeishuIdentityConfig `json:"identity,omitempty"`
}

type FeishuIdentityConfig struct {
    // 用户缓存容量,默认 5000
    UserCacheSize int `json:"user_cache_size,omitempty"`
    // 用户缓存 TTL(秒),默认 43200 = 12h
    UserCacheTTLSec int `json:"user_cache_ttl_sec,omitempty"`
    // 是否启用群聊 enrichCtx(默认 true)
    EnableGroupEnrich bool `json:"enable_group_enrich"`
    NameLocale string `json:"name_locale,omitempty"`
}
```

## 8. 测试

单测:

- `BotOpenID()` 并发 100 次:只触发 1 次 API(sync.Once)
- `UserCache.GetOrFetch` 并发同 open_id:singleflight 只打一次 API,其他 goroutine 复用
- TTL 过期 → 重新 fetch
- LRU 满 → 最久未用被淘汰
- Resolver 把 SenderName 从 open_id 改写成真名

集成:

- 真实租户冒烟:用 bot 账号在群里,用户 A 发消息,查 session 里的消息 sender 是 A 的真名
- mentions:@张三 @李四 → prompt prefix 里都是真名
- 用户改名 → 12h 后 bot 才感知(可接受)

蓝军:

- 飞书 API 对 open_id 查询返空 → cache 存 tombstone 防反复打 API(TTL 5 min),而不是空 name 反复查
- 租户 bot 被封,GetUserInfo 全报 permission_denied → warn + degrade 到 open_id 占位,不阻塞消息处理
- 用户名包含 `]]>` → CDATA 转义处理(M1 §9)

## 9. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/client.go:393` | 现有 `GetBotOpenID`,包进 sync.Once |
| `internal/channel/feishu/longconn.go:52-62` | 删除异步拉取,改为 `_ = client.BotOpenID()` 预热 |
| `internal/channel/feishu/user_cache.go` | **新建** LRU+TTL+singleflight 缓存 |
| `internal/channel/feishu/resolver.go` | 在 M1 §8 的 Resolve 里调 userCache |
| `internal/channel/feishu/webhook.go:113` | `SenderName: senderID` → 初始留空,交给 resolver 回填 |
| `internal/channel/router.go:247-249` | `enrichCtx` 扩展到群聊 |
| `internal/channel/feishu/client.go:269` | `GetUserInfo` 作为 cache 的 fetcher |
| `internal/config/config.go:881` | `FeishuIdentityConfig` 新字段 |
