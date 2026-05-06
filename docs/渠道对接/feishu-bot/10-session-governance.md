# M10 · 会话治理与命令

> bot 在群里被多人共用时的"管理面":命令、ACL、灰度、chat 级禁用、一群绑多 agent。

## 1. 现状

| 项 | 位置 | 状态 |
|---|---|---|
| `/help` / `/status` / `/reset` | `internal/channel/feishu/{commands,governance,plugin}.go` | ✅ 已实现 |
| `/mute` / `/unmute` | `internal/channel/feishu/{commands,governance,plugin}.go` | ✅ 已实现 |
| `/model <name>` | `internal/channel/feishu/{governance,chat_state_repo}.go` + `channel/router.go` | ✅ 已实现(白名单 + 下一条消息透传到 master) |
| `/audit last <N>` | `internal/channel/feishu/{governance,audit}.go` | ✅ 已实现(JSONL 审计查询,按 chat + tenant 过滤,默认群管 ACL) |
| `/debug` | `internal/channel/feishu/governance.go` | 🟡 明确拒绝,Phase 0 不开放 |
| 命令 ACL(群管才能 reset/mute/model/audit) | `internal/channel/feishu/acl.go` | ✅ 已实现 |
| Chat 级禁用(让 bot 在某群静默) | `internal/channel/feishu/{governance,chat_state_repo}.go` | ✅ 已实现 |
| 按 chat/tenant 灰度 | `internal/channel/feishu/rollout.go` + `chat_state.rollout_mode` | 🟡 已有基础 drop,完整白名单/采样策略未做完 |
| 群名/成员变更感知 | 无 | ❌ |
| 一群绑多 agent(切换 agent profile) | `internal/channel/feishu/governance.go` | 🟡 命令明确拒绝,未开放 |

## 2. 命令体系

### 2.1 命令集

| 命令 | 权限 | 行为 |
|---|---|---|
| `/help` | 所有人 | 返回当前已开放命令列表 |
| `/status` | 所有人 | 返回当前 session ID、lifecycle state、rollout、mute、suppress_outbound |
| `/reset` | 群管理员(群聊)/ 用户自己(私聊) | 清空 session 历史,下一条消息重开 |
| `/debug on`/`off` | 群管理员 | 开关本群 debug echo(后续消息原始 payload echo 回) |
| `/mute` / `/unmute` | 群管理员 | 让 bot 在本群不再响应(`chat_muted` DB 写入) |
| `/model <name>` | 群管理员(白名单内) | 本群临时切模型(触达 `SessionState.pendingModelOverride`) |
| `/agent <profile>` | 群管理员 | 切 agent profile(一群绑多 agent,§4) |
| `/audit last <N>` | 群管理员 | 返回最近 N 条本群审计记录(当前已覆盖命令执行、push API；N 上限 20) |

### 2.2 解析

```go
// internal/channel/feishu/commands.go
type Command struct {
    Name string   // "reset" / "status" / ...
    Args []string
}

// 只解析 bot 被 @ 后紧跟 `/xxx` 的,避免用户聊天误触
func ParseCommand(content string, botAtStripped string) (*Command, bool) {
    s := strings.TrimSpace(botAtStripped)
    if !strings.HasPrefix(s, "/") { return nil, false }
    parts := strings.Fields(s[1:])
    if len(parts) == 0 { return nil, false }
    return &Command{Name: parts[0], Args: parts[1:]}, true
}
```

**必须在 @ 之后**:保证群友聊天说 `帮我看下 /etc/hosts` 不会误触 `/etc`。

### 2.3 路由

`router.go processMessageImpl` 在 `resolver.Resolve` 之后、送进 master 之前:

```go
if cmd, ok := commands.ParseCommand(msg.Content, msg.StrippedContent); ok {
    if err := r.commandHandler.Handle(ctx, cmd, msg); err != nil {
        // reply error
    }
    return  // 命令模式不进 agent
}
```

## 3. 命令 ACL

### 3.1 角色识别 [Phase 2 实证]

> ⚠️ **红队修正(2026-04-22)**:原稿 `a.client.Im.ChatManagers.List(...)` 在 SDK v3.5.3 **不存在**(`service/im/v1/resource.go:473/501` 只暴露 `AddManagers` / `DeleteManagers`,无 `List`)。下面的伪码是**待实证设计**,Phase 2 真正实现时必须先确认可用 API 形态:候选方案 (a) 通过 `ChatMembers` + 另查群主 chat owner;(b) 升级 SDK 到含 `ListManagers` 的版本;(c) 用 OpenAPI 文档 `/open-apis/im/v1/chats/:chat_id/managers` 直查 → 但这违反 SDK-only 铁律,需先给 SDK 提 PR。

不预拉群成员(成本高),只在命令被触发时按需查。**待实证伪码**(API 形态可能变):

```go
// ⚠️ 待 Phase 2 用真实 SDK 符号重写,本伪码不可直接落地
func (a *ACL) IsGroupAdmin(ctx context.Context, chatID, openID string) (bool, error) {
    // 1) 拉群成员判断是否在群里
    // 2) 单独调群管理员查询接口(具体 SDK 符号 Phase 2 实证)
    // 3) 命中即 true
    return false, errors.New("ACL.IsGroupAdmin pending Phase 2 SDK verification")
}
```

5 分钟缓存(不会频繁变),miss 时走 API。

### 3.2 ACL 配置

```go
type CommandACLConfig struct {
    // 如果用户开 p2p 和 bot 聊天,所有命令都允许(用户是自己 session 的主人)
    P2PAllowAll bool  // 默认 true
    // 群聊时,以下命令只有群管能用:
    GroupAdminOnlyCommands []string  // 默认 ["reset", "debug", "mute", "model", "agent", "audit"]
    // 例外:某些 user_id 总是被允许(运维团队)
    SuperAdminOpenIDs []string
}
```

## 4. 一群绑多 agent

### 4.1 场景

不同群用不同 agent profile:
- 研发群 → `code-assistant` agent(代码工具集)
- 客服群 → `customer-support` agent(工单/FAQ 工具集)
- 数据群 → `data-analyst` agent(SQL/图表工具集)

### 4.2 数据模型

```sql
CREATE TABLE IF NOT EXISTS feishu_chat_agent_binding (
    chat_id      TEXT PRIMARY KEY,
    tenant_key   TEXT NOT NULL,
    agent_profile TEXT NOT NULL,  -- 指向 agent registry 里的一个 profile id
    muted        BOOLEAN NOT NULL DEFAULT FALSE,
    debug_mode   BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by   TEXT  -- 最后修改的 open_id
);
```

### 4.3 Session 创建时路由

```go
// router.go ensureSession:
binding, _ := r.chatBindingRepo.Get(ctx, msg.ChatID)
profile := binding.AgentProfile
if profile == "" { profile = r.defaultAgentProfile }
session := r.master.EnsureSession(sessionID, master.SessionOpts{AgentProfile: profile})
```

### 4.4 `/agent <profile>` 实现

- 群管输入 → 更新 `feishu_chat_agent_binding.agent_profile`
- 发卡片确认:"本群 agent 已切换为 `code-assistant`。若需要保留上下文,请先 `/reset`"
- **不自动 reset**:用户可能希望历史上下文继续

## 5. Chat 级禁用(`/mute`)

### 5.1 作用

- `feishu_chat_agent_binding.muted = true` → Router 在 `processMessageImpl` 第一件事就 return
- 消息仍正常入库,仅不触发 agent
- `/unmute` 解除

### 5.2 场景

- 群员投诉 bot 打扰 → 群管秒速 `/mute`
- bot 出 bug 刷屏 → ops 侧全局 mute 某群

### 5.3 全局 ops 紧急 mute

除了群管 `/mute`,运维侧通过 API 可对任意 chat_id mute:

```
POST /api/v1/channels/feishu/mute
{"chat_id": "oc_xxx", "reason": "incident-2026-04-22"}
```

需 super admin scope。

## 6. 灰度

### 6.1 维度

- **chat_id whitelist**:仅白名单群响应(POC 阶段用)
- **tenant_key whitelist**:多租户下仅部分租户启用
- **user_id whitelist**:仅部分用户的消息触发 agent(内测)

### 6.2 Config

```go
type FeishuRolloutConfig struct {
    Mode string  // "all" / "whitelist" / "blacklist",默认 "all"
    ChatWhitelist   []string  // chat_id 列表
    TenantWhitelist []string  // tenant_key 列表
    UserWhitelist   []string  // open_id 列表(仅这些人发的消息会被处理)
    ChatBlacklist   []string
    // 全灰度,按消息 hash %100 取样,例如 10 = 10%
    PercentageSample int
}
```

### 6.3 决策点

在 `router.go processMessageImpl` 最前面:

```go
if !r.rollout.Allow(msg.ChatID, msg.TenantKey, msg.SenderID) {
    metrics.Counter("feishu.rollout.drop", 1)
    return  // 静默丢(不回复,避免让白名单外用户感知 bot 存在)
}
```

## 7. 群变更感知(可选)

### 7.1 事件

- `im.chat.updated_v1` → 群名/群主/公告变更
- `im.chat.member.user.added_v1/deleted_v1` → 群成员进出(§M3 已列)
- `im.chat.disbanded_v1` → 群解散

### 7.2 一期 action

- 群解散 → 清 `feishu_chat_agent_binding` + `Router.Unbind` + `master.EndSession`
- 群名变更 → 仅 log,不 action
- 新成员加入 → 可选:发一句"欢迎 @新人,/help 查看 bot 能力"(默认关,防打扰)

## 8. Debug Mode [Phase 2 — Phase 0 不实施]

> ⚠️ **CEO 决议(2026-04-22)**:`/debug` 属于红队链 C(PII 全链泄露)的主要武器化通道,Phase 0 **不首发**。Phase 0 上线期间群里没有任何"把原始 payload 回群"的通道,链 C 主要放大器不存在。
>
> Phase 2 真正上线 `/debug` 时必须满足以下**全部**硬约束,少一项都不允许进 main:

### 8.0 Phase 2 上线硬约束(红队修正)

1. **强制过 sanitizer**:debug echo 的所有字段先过 `@all` 白名单递归 + PII mask(`open_id` → `SafeSenderID`、`phone` / `email` / `mobile` → `***`)。
2. **only-visible-to-caller**:debug 输出**不能广播全群**。具体技术形态(临时卡片 / 私聊回弹 / 仅对调用者可见的卡片字段) **Phase 2 实现前必须先用真实 SDK 验证**(v3.5.3 SDK 树未直接验证 `visibility=private` 字段)。若验证不通过,fallback 方案:debug 输出**改投私聊**给开启者,而不是回到当前群。退化方案不允许是"普通群卡片",否则直接回到链 C。
3. **Hard cap 30 min 不可重置**:一次开启只有 30 min,倒计时结束后**禁止**立即再开(冷却 10 min)。原稿"每 30 分钟自动关"实际可被循环开启,必须改成硬上限。
4. **audit log 独立存储**:谁开 / 何时开 / 何时关 落到 audit 表,`/audit` 命令查询时仍走 sanitizer,不返回原始 payload。
5. **Phase 0 guard**:`FeishuSessionGovernanceConfig.DebugEnabled=true` 在 Phase 0 直接 `log.Fatal("/debug is Phase 2 only")`。

### 8.1 开关(Phase 2)

群管 `/debug on` → `feishu_chat_agent_binding.debug_mode = true`

### 8.2 行为(Phase 2)

每条消息处理时,在 agent 调用之前,bot 对**开启 debug 的群管**(而非全群)回一条折叠卡片:
- **脱敏后的** feishu event payload(open_id → Safe、手机号/邮箱 → `***`)
- parser 提取的 References / Mentions / ParentID(脱敏)
- resolver 拉取到的父消息内容(脱敏)
- 即将送到 master 的 prompt prefix(折叠 + 脱敏)

便于调试"为什么 agent 看不懂我的消息"。

### 8.3 风险(原稿缓解不足)

消息包含敏感内容时 debug echo 会外泄。**原稿仅靠"群管权限 + 30 分钟自动关"不足**:群管本身可能被钓鱼、可重复开启、卡片广播全群。必须叠加 8.0 的全部 5 项约束,Phase 2 才能开放。

## 9. Config

```go
type FeishuSessionGovernanceConfig struct {
    // 命令
    CommandACL CommandACLConfig `json:"command_acl"`
    // 灰度
    Rollout FeishuRolloutConfig `json:"rollout"`
    // Debug(Phase 2 才启用,Phase 0 DebugEnabled=true 启动 fatal)
    DebugEnabled         bool `json:"debug_enabled,omitempty"`          // 默认 false
    DebugHardCapMinutes  int  `json:"debug_hard_cap_minutes,omitempty"` // 默认 30,到期冷却 10 min,不可重置
    DebugCooldownMinutes int  `json:"debug_cooldown_minutes,omitempty"` // 默认 10
    // 一群绑多 agent
    MultiAgentEnabled bool `json:"multi_agent_enabled"`  // 默认 false(一期保守)
    DefaultAgentProfile string `json:"default_agent_profile,omitempty"`
}
```

## 10. 测试

单测:
- 命令必须在 @ 之后才识别
- ACL 非管理员 `/reset` 群聊 → 拒绝 + 提示
- `/mute` → 后续消息 drop
- 多 agent binding:chat A 用 profile X,chat B 用 profile Y,互不污染
- rollout whitelist:非白名单 chat 静默 drop

集成:
- 群管 `/reset` → session 清;普通群员 `/reset` → 被拒
- `/debug on` → 下一条消息 bot 先回 debug 卡片,30 分钟后自动关
- `/agent code-assistant` → 切 profile,下条消息跑新 agent

蓝军:
- 伪造 openID 冒充群管 `/reset` → ACL 查真实管理员列表拒绝
- 群管 `/debug on` 把敏感消息 debug echo → audit log 可追溯,ops 可紧急 mute

## 11. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/commands.go` | **新建**,命令解析 |
| `internal/channel/feishu/command_handler.go` | **新建**,命令执行 |
| `internal/channel/feishu/acl.go` | **新建**,ACL + 群管缓存 |
| `internal/channel/feishu/rollout.go` | **新建**,灰度决策 |
| `internal/channel/feishu/chat_binding_repo.go` | **新建**,DB 访问层 |
| `migrations/<ts>_feishu_chat_binding.sql` | **新建** |
| `internal/channel/router.go` | processMessageImpl 最前加 rollout/mute 检查;cmd 分支 |
| `internal/api/handlers.go` | 加 ops mute API |
| `internal/config/config.go` | `FeishuSessionGovernanceConfig` |
