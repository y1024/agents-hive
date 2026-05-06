# M8 · 安全与合规

> 当前完全缺失的面。飞书企业版要求消息体加密 + 签名校验;日志里混打真名 + open_id 是 PII 风险;bot 发 @所有人 / 越权 / 权限被撤销三个场景无保护。

## 1. 现状

| 项 | 位置 | 状态 |
|---|---|---|
| `encrypt_key` 消息体解密 | `webhook.go` | 🟡 已接入严格加密开关,可拒绝明文;完整轮转窗口策略仍未完成 |
| `verification_token` 签名校验 | `webhook.go` | 🟡 已 wire 到 SDK dispatcher,仍需继续补更完整的安全观测 |
| PII 日志脱敏 | 分散各处 | 🔥 真名、open_id、email、手机号混打 |
| Content sanitizer(bot 发送内容) | `mention_sanitizer.go` + `plugin.go` | 🟡 已拦截 `@all` / `<at user_id="all">` 等高风险 mention,后续还要补更细粒度规则 |
| Bot 权限撤销感知 | `health.go` + `/health/feishu` | 🟡 已支持 permission-denied 窗口降级和健康暴露,跨副本共享仍未完成 |
| 多租户隔离 | 假设单 app 单租户 | ❌ |
| 审计日志(who called what tool) | `audit.go` | 🟡 已有 JSONL sink 和 push 审计落点,覆盖面仍不完整 |
| Secret 管理 | 从 env 读 | ⚠️ 没有 rotation 机制 |

## 2. Encrypt + Signature

### 2.1 Config

```go
// internal/config/config.go
type FeishuConfig struct {
    ...
    EncryptKey          string `json:"encrypt_key,omitempty"`          // 飞书后台配置的消息加密 key
    VerificationToken   string `json:"verification_token,omitempty"`   // 老签名 token(v1 事件)
    EventEncryptEnabled bool   `json:"event_encrypt_enabled,omitempty"` // true → 必须解密,拒绝明文
}
```

**默认 `EventEncryptEnabled=false`**(向后兼容)。生产建议开启,飞书后台同步勾选"加密推送"。

### 2.2 webhook.go 接入 larkcallback 内置 handler

SDK 已有 `larkevent.NewEventReqHandler` 自动处理 encrypt + signature,但现有代码**手写 JSON 解析绕过了**。改造:

```go
// internal/channel/feishu/webhook.go
import (
    larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
    larkhttpserverext "github.com/larksuite/oapi-sdk-go/v3/event/httpserverext"
    larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

// Handler 构造:
dispatcher := dispatcher.NewEventDispatcher(cfg.VerificationToken, cfg.EncryptKey).
    OnP2MessageReceiveV1(c.handleMessage).
    OnP2CardActionTriggerV1(c.handleCardAction).
    OnP2ChatMemberBotAddedV1(c.handleBotAdded).
    OnP2ChatMemberBotDeletedV1(c.handleBotRemoved).
    OnP1P2PChatCreatedV1(c.handleP2PCreated)
    // ... 所有 lifecycle/message/card 事件都 on 到这里

// HTTP mount:
mux.Handle("/webhook/feishu", larkhttpserverext.NewEventHandlerFunc(dispatcher,
    larkevent.WithLogLevel(larkcore.LogLevelInfo),
))
```

**好处**:
- 签名校验、解密、URL Verification challenge 全 SDK 内置
- longconn 和 webhook 用**同一个 dispatcher**,事件路由逻辑一处维护
- 手写的 `event_type` 分发可删除(M2 §4.2 方案升级)

### 2.3 `EventEncryptEnabled` 严格模式

> ⚠️ **红队修正(2026-04-22)**:SDK v3.5.3 **没有** `isEncrypted()` helper(grep 确认只有 `apaas/v1/model.go` 的 `IsEncrypted` 字段,与 webhook 加密无关)。严格模式自己做 body probe,只接受 `{"encrypt": "..."}` 形态:

```go
// internal/channel/feishu/webhook.go
if cfg.EventEncryptEnabled {
    var probe struct {
        Encrypt string `json:"encrypt"`
    }
    // probe 失败当作未加密处理,交给下一步拒绝
    _ = json.Unmarshal(body, &probe)
    if probe.Encrypt == "" {
        http.Error(w, "encryption required", http.StatusBadRequest)
        return
    }
}
// 交给 SDK dispatcher,内部会调 EventDecrypt(encrypt, secret)
```

防止开了加密后攻击者/误配绕过。注意 `larkevent.EventDecrypt` 返回 `DecryptErr` 时,dispatcher 已经把 HTTP 响应写成 400,无需重复处理。

## 3. PII 脱敏

### 3.1 规则

> ⚠️ **红队修正(2026-04-22)**:原稿允许直接打 `open_id` / `union_id`,但 ROADMAP Phase 0 #12 与 M10 §8 的 sanitizer 都要求脱敏 `open_id`。统一规则:**生产日志一律不出现原始 ID**,只允许 `SafeSenderID()` 返回的 `sid_xxxxxxxx` 形态。原始 ID 仅在 audit 表(行级访问控制 + retention)出现。

生产日志里:
- `open_id` → 🔥 **禁止原文**,只允许 `SafeSenderID(openID)` = `sid_<sha256[:4]>`
- `union_id` → 🔥 **禁止原文**,同上规则(`SafeUnionID`)
- **用户真名** → 🔥 **禁止**
- `email` / `phone` / `mobile` / `employee_no` → 🔥 **禁止**
- 消息 content → 默认 **不打**,仅在 DEBUG 级别打 + 截断 200 字 + 过 sanitizer
- 群名 / 部门名 → 允许打(但消息内容里若包含敏感词不能落日志)
- audit 表:可保留原始 `open_id`(非日志),但必须有 row-level access control 和 retention(详见 §6)

### 3.2 实现

```go
// internal/channel/feishu/piilog.go
func SafeSenderID(openID string) string {
    h := sha256.Sum256([]byte(openID))
    return "sid_" + hex.EncodeToString(h[:4])
}

// 所有日志里,SenderName 一律不出现:
logger.Info("message received",
    zap.String("platform", "feishu"),
    zap.String("chat_id", msg.ChatID),
    zap.String("message_id", msg.MessageID),
    zap.String("sender", feishu.SafeSenderID(msg.SenderID)),  // 不是 SenderName
)
```

### 3.3 CI gate

```bash
# 任何日志里出现 SenderName 直接 fail
grep -rn 'zap\.String("sender_name"' internal/channel/feishu/ && exit 1 || true
grep -rn 'zap\.String("email"' internal/channel/feishu/ && exit 1 || true
grep -rn 'zap\.String("phone"' internal/channel/feishu/ && exit 1 || true
```

## 4. Content Sanitizer

### 4.1 场景

- Agent 输出 `<at user_id="all">大家好</at>` → 飞书渲染为 @所有人 → 打扰全群
- Agent 输出诱导点击的钓鱼链接(场景:prompt injection)
- Agent 把 `@某部门` 标签当文本输出 → 被飞书解析为真 @

### 4.2 OutboundSanitizer

```go
// internal/channel/feishu/sanitizer.go
type SanitizerConfig struct {
    AllowAtAll   bool      // 默认 false
    AllowedMentionOpenIDs []string  // 白名单 open_id(默认空,意味着只能 @ 消息里出现过的人)
    MaxMentionsPerMessage int       // 单条消息最多 @ 几人,默认 10
    StripImagesFromPlaintext bool   // markdown 里的 ![](远程图) 默认 strip(防 SSRF 外链)
}

func (s *Sanitizer) Sanitize(out *channel.OutboundMessage, allowedFromInbound []string) error {
    // 1. 扫 <at user_id=...> 标签
    // 2. user_id="all" / "all_user" → 根据 AllowAtAll 决定 strip 或 allow
    // 3. 白名单外的 user_id → strip
    // 4. 数量超 Max → 截到 Max 条
    // 5. 返回 sanitized content
}
```

### 4.3 注入点

`plugin.go Send` 入口调 `sanitizer.Sanitize`,在 renderer 走 PATCH 之前也调一次(防 agent 中途 inject)。

### 4.4 feature flag

```go
FeishuSecurityConfig.SanitizerEnabled bool  // 默认 true
```

关掉时 log warn,不建议生产关。

## 5. Bot 权限撤销感知

### 5.1 场景

应用管理员在飞书后台停用 bot / 撤销某权限 → 所有 API 调用返 `code=99991663`(permission denied)或 `code=10013`。当前代码静默 warn + 重试 3 次耗尽,用户看不到任何提示。

### 5.2 策略

`Client` 加一个 `healthStatus`:

```go
type HealthStatus struct {
    TenantAccessTokenOK bool      // 最近一次刷新成功
    LastAPIError        error     // 最近一次 API 错误
    PermissionDeniedCount int     // 滚动 5 分钟窗口内 permission_denied 计数
    DegradedMode        bool      // >5 次 permission_denied → 进入降级模式
}
```

降级模式下:
- 新消息收到仍正常解析(保留 inbound 流)
- 所有 outbound 调用快速失败(不重试)
- 健康端点 `/health/feishu` 返 503,触发 ops 告警
- `feishu.bot.degraded` metric 升 1

**恢复路径**:人工修复权限后,config hot reload 触发 `DegradedMode=false` 重新尝试。

## 6. 多租户基础(与 M11 交叉)

本模块只做"**识别 tenant**",完整多 app 路由在 M11。

```go
// 每条 event 必带 tenant_key,Client 加:
func (c *Client) VerifyTenantKey(expected string) error {
    // 若 event.TenantKey != c.tenantKey,log error + drop(或 M11 路由到正确 client)
}
```

一期单 app 场景下只做 `VerifyTenantKey`,不一致直接 drop + metric。

## 7. 审计日志

### 7.1 范围

- 每次 Agent 调用 feishu tool(get_doc / read_sheet / upload_file / send_message 等)→ 审计一行
- 每次 HITL 按钮点击 → 审计一行
- 每次 push API 调用 → 审计一行

### 7.2 格式

```json
{
  "ts": "2026-04-22T10:20:30Z",
  "platform": "feishu",
  "action": "tool.call",
  "tool": "get_doc_content",
  "actor": {"type": "agent", "session_id": "im-feishu-xxx"},
  "target": {"doc_token": "DxxxxxYYYY"},
  "outcome": "ok",
  "duration_ms": 142,
  "tenant_key": "xxx"
}
```

### 7.3 落地

写到单独文件(`audit.log`)或单独 Postgres 表(`feishu_audit`)。不混入普通 app log。

Retention 30 天默认(config 可调)。

## 8. Secret 管理

### 8.1 当前

`FeishuConfig.AppID` / `AppSecret` / `EncryptKey` / `VerificationToken` 从 env 读一次,进程生命周期内不变。

### 8.2 目标

支持 rotation:
- config hot reload 时,检测到 AppSecret / EncryptKey 变化 → `Client` 重建内部 SDK client(**不重建整个 Plugin**,保留 session/dedup)
- 老 token 维持 5 分钟 grace period(并发中的 in-flight 请求能完成)

### 8.3 实现

```go
type Client struct {
    ...
    mu      sync.RWMutex
    sdkClient *lark.Client  // 热可换
}

func (c *Client) ReloadFromConfig(cfg config.FeishuConfig) error {
    newSDK := lark.NewClient(cfg.AppID, cfg.AppSecret, ...)
    c.mu.Lock()
    oldSDK := c.sdkClient
    c.sdkClient = newSDK
    c.mu.Unlock()
    // grace: 老 SDK 不主动 close,GC 会回收(飞书 SDK 无显式 close)
    _ = oldSDK
    return nil
}
```

每个 SDK 调用先 `c.mu.RLock()` 拿当前 sdkClient。

## 9. Config

```go
type FeishuSecurityConfig struct {
    EventEncryptEnabled bool   `json:"event_encrypt_enabled,omitempty"`  // 默认 false,生产建议 true
    SanitizerEnabled    bool   `json:"sanitizer_enabled"`                // 默认 true
    AllowAtAll          bool   `json:"allow_at_all,omitempty"`           // 默认 false
    MaxMentionsPerMessage int  `json:"max_mentions_per_message,omitempty"` // 默认 10
    AuditEnabled        bool   `json:"audit_enabled"`                    // 默认 true
    AuditRetentionDays  int    `json:"audit_retention_days,omitempty"`   // 默认 30
    PermissionDegradeThreshold int `json:"permission_degrade_threshold,omitempty"` // 默认 5(5 分钟内 permission_denied 超过此数 → 降级)
}
```

## 10. 测试

单测:
- encrypt_key 设置后,明文 event → 解析失败(`EventEncryptEnabled=true` 严格模式)
- signature 错 → handler 返 401,event 不进 dispatcher
- Sanitizer 对 `<at user_id="all">` 默认 strip;`AllowAtAll=true` 保留
- Sanitizer 对不在白名单的 user_id strip
- PII 日志:build tag 下扫 log 里不能出现 sender_name/email/phone
- HealthStatus 滚动窗口 5 分钟超 5 次 permission_denied → 降级

集成:
- 生产配置 `EventEncryptEnabled=true`,收一条加密 event → 完整解析
- 故意把 bot 权限撤销 → 5 分钟内 degraded,`/health/feishu` 503

蓝军:
- 伪造 signature 请求 → 401
- Agent prompt injection 诱导输出 `<at user_id="all">` → sanitizer 拦住
- 日志侧通道:构造含真名的 log call → CI grep gate fail

## 11. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/webhook.go` | 重写为 larkcallback + dispatcher,内置签名/加密 |
| `internal/channel/feishu/sanitizer.go` | **新建** |
| `internal/channel/feishu/piilog.go` | **新建**,`SafeSenderID` 等工具 |
| `internal/channel/feishu/health.go` | **新建**,`HealthStatus` + `/health/feishu` 端点 |
| `internal/channel/feishu/audit.go` | **新建**,审计落盘/DB |
| `internal/channel/feishu/client.go` | `ReloadFromConfig` 支持 secret rotation;所有 API 调用前 RLock |
| `internal/config/config.go` | `FeishuSecurityConfig` 新字段 |
| `internal/api/handlers.go` | 注册 `/health/feishu` |
