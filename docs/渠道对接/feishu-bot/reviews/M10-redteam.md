# M10 · 会话治理 — 红队评审报告

> 基于 `10-session-governance.md` 的对抗性审计 + `internal/channel/router.go` / `internal/master/session_loop.go` 现状检查。

## 方法

- 枚举命令/ACL/灰度/多 agent 每一个断言,构造绕过/污染路径
- 关注 P2P vs 群聊的对称性、tenant 隔离边界、PII 泄露武器化、bot 状态跨生命周期耦合

## P0 发现

### P0-M10-01:命令解析在 P2P 与群聊不对称 — `/etc/hosts` 触发误命令

**证据**:M10 §2.2 `ParseCommand` 说"必须在 @ 之后"防群聊误触。但 P2P 私聊 bot 不需要 @,`strippedContent == rawContent`。

**攻击**:
- 用户 P2P 问 bot:"帮我看下 /etc/hosts 能连通吗"
- `HasPrefix("/")` → ParseCommand 返 `{Name:"etc", Args:["hosts"...]}`
- bot 回"命令不存在:/etc"
- 真实的 agent 处理被跳过 → 用户体验破碎

**补救**:
1. P2P 模式下命令解析必须比群聊**更严格**:
   - 命令必须在**首行**且行末无其他字符 → `strings.HasPrefix(s, "/") && !strings.Contains(s, " ")` 或正则 `^/\w+( +\S+)*\s*$`;
   - P2P 命令**白名单**:只认 `help|status|reset|debug|mute|unmute|model|agent|audit`,其他前缀 `/` 当普通消息;
2. 群聊模式沿用"必须 @ bot 后"策略;
3. M10 §2.2 加对称性规则 + 代码示例。

---

### P0-M10-02:ACL 跨租户 openID 碰撞

**证据**:M10 §3.1 `IsGroupAdmin(chatID, openID)` 查 `ChatManagers.List`。openID 在飞书是 per-app 唯一但**不是全局唯一**,不同 tenant(即不同 app 或不同企业)里同值 openID 指向不同用户。

**攻击**(多租户启用后):
- Tenant A 的群管 Alice openID = ou_abc
- Tenant B 的普通用户 Bob openID 恰好也 = ou_abc
- Bob 在 tenant B 群里发 `/reset`
- Router 若未正确传 tenant_key → ACL 查 tenant A 的 ChatManagers → Bob 被判为群管 → 执行

**补救**:
1. 所有 ACL 查询必须 `(tenant_key, openID)` 联合 key;
2. `IsGroupAdmin(tenantKey, chatID, openID)` 签名;
3. 缓存 key = `fmt.Sprintf("%s:%s:%s", tenantKey, chatID, openID)`;
4. M10 §3.1 + M11 §7 交叉引用。

---

### P0-M10-03:rollout 采样维度错误 — chat 全开/全闭看似 bot 断线

**证据**:M10 §6.3 `rollout.Allow(chatID, tenantKey, senderID)` + `PercentageSample: 10` 用 `hash(chatID) % 100 < 10` 决定。

**攻击/现实场景**:
- 10% 灰度,hash(chatA) = 95 → **Chat A 所有消息全丢**,持续几小时
- Chat A 用户看到"bot 一条都不回",怀疑 bot 挂了,投诉/找运维
- 按 chat_id hash 做采样时,该 chat 非好即坏,没有"10% 消息命中"的直觉

**补救**:
1. 采样维度改成 `hash(chat_id + message_id) % 100 < N` → **每条消息独立决定**,Chat A 里 10% 消息命中,9 条消息里用户能看到 1 条回复,体验 degrade 但不"完全断";
2. 或提供两种模式:`SampleDim: "chat"` (整 chat 要么全开要么全闭,适合观察性实验) vs `"message"` (渐进灰度,适合功能上线);默认 `message`;
3. M10 §6 明确两种采样语义 + 默认值 + trade-off 说明。

---

### P0-M10-04:`/debug on` 被武器化成 PII 永久泄露

**证据**:M10 §8 说 debug echo 原始 payload + "30 分钟自动关"。

**攻击链**:
- 恶意群管 Alice `/debug on` → 30 min 内群内所有 PII 被折叠卡片 echo 回群
- T+29 min Alice 再发 `/debug on` → 计时重置
- 效果:debug **永久开启**
- 同时:(a) 其他群员不易察觉(折叠卡片默认收起);(b) audit log 能追溯但运维不看实时;(c) 敏感消息(身份证、手机号、token)全量留在消息历史里,任何群员 `/audit last 100` 能拉出

**补救**(多重):
1. **Debug echo 必须先过 sanitizer**(M8-redteam P0-M8-10),不 echo 原始 PII;
2. **Debug 累计时长硬限**:`(tenant, chat)` 每日 debug 累计 ≤ 60 min,超过当日锁死;
3. **Debug 只对触发者可见**:用飞书卡片 `only_visible_to` 功能,debug 卡片**只 Alice 看得到**,不污染群历史;
4. **频繁开启告警**:3 次/小时 同一 chat `/debug on` → 告警 + 30 min 冷却;
5. M10 §8.3 改写,M8 §3 加 debug-path 必过 sanitizer。

---

### P0-M10-05:multi-agent 切换不 reset → session 历史污染

**证据**:M10 §4.4 "不自动 reset,用户可能希望历史上下文继续"。

**污染路径**:
- Chat X 先绑 `agent=code-assistant`,session 里有 system prompt + 20 轮代码讨论
- 群管 `/agent customer-support` 切到客服 profile
- 客服 agent 的 system prompt 是 "你是贴心客服",但 session.history 里第一条还是 "你是代码助手" + 20 轮 git 对话
- 新消息 "订单怎么退" → LLM 看到混合 prompt → 行为不可预测(可能还在讨论代码,或者混乱自称"代码客服")
- 且上一个 agent 的工具调用历史还在 session,新 agent 不一定有那些工具 → tool_call 被引用但没实现 → runtime error

**补救**(两选一):
1. **方案 A**(推荐):profile 切换**强制 reset**,M10 §4.4 改写。用户若需保留历史 → 运维命令 `/agent --keep-history` 明示承担风险;
2. **方案 B**:session_id 维度加 profile 版本,`session_id = "im-feishu-{tenant}-{chat}-{profile_id}"`,切 profile 自动开新 session,旧 session 保留只读;
3. 无论哪种,M10 §4.4 明写"profile 切换 = session 重置默认",交叉引用 M9-redteam P0-M9-12 的 session_id 规范。

---

### P0-M10-06:ops mute API 跨租户越权

**证据**:M10 §5.3 `POST /api/v1/channels/feishu/mute {chat_id}` 需 super admin scope。但 super admin 一般是**平台级**,能操作所有 tenant 的 chat。

**攻击/合规**:
- 平台 SRE(super admin)意外/故意 mute tenant A 的 chat
- 被 tenant A 合规追责:"你们怎么能 mute 我的客服群"
- 或:SRE 账号被攻破 → 攻击者可 mute 任意租户 chat → 业务中断

**补救**:
1. mute API 必须带 `tenant_key` 参数,super admin 也必须显式指定;
2. 权限分层:`platform_admin`(跨 tenant,仅事故响应,需审批 + 2FA) vs `tenant_admin`(仅本租户);
3. 每次 mute 全量 audit log + 钉钉/飞书告警通知 tenant 管理员;
4. M10 §5.3 + M11 §7 新增"跨租户操作需审批"章节。

---

### P0-M10-07:被 `/mute` 的群静默丢消息,用户以为 bot 挂

**证据**:M10 §5.1 "Router 在 processMessageImpl 第一件事就 return"。用户发消息 bot 无任何反应。

**攻击/体验破碎**:
- 群管 Alice `/mute` 后 Alice 离职,接替的 Bob 不知情
- 群员 `@bot 帮我查 X` bot 沉默
- 群员 ping 几次 → 升级到 Bob:"bot 坏了"
- Bob 找运维,运维看 log:正常 mute 中
- 全链路几小时浪费

**补救**:
1. **首次 mute 消息必须回一条 ephemeral 提示**:"本群已被群管 `<Alice>` 静默。发 `/unmute` 解除。";
2. 该提示**每人每天最多一次**(避免刷屏 — 用 `feishu_mute_notify_log(chat_id, user_id, last_notified)` 记 24h);
3. Metric `feishu.mute.message_dropped{chat_id_hash}` 便于运维看 mute 期间流量;
4. M10 §5 新增 §5.4 "静默反馈"。

---

### P0-M10-08:`IsGroupAdmin` API 高频调用触发飞书限流

**证据**:M10 §3.1 每命令触发一次查询,5 min cache。但:
- bot 同时驻 100 群,命令风暴时 100 并发查询
- 飞书 `ChatManagers.List` 30 QPS/tenant → 超配
- 被限流时 ACL 查失败 → fail-open(允许)还是 fail-closed(拒绝)未声明

**补救**:
1. 冷启动预热:bot 启动后按 chat_id batch pre-fetch managers,塞 cache;
2. 订阅 `im.chat.manager_added/removed` 事件主动 invalidate,不靠 TTL;
3. API 失败时 **fail-closed**(权限不足)+ 告警,绝不 fail-open;
4. M10 §3.1 加缓存策略细节 + 失败模式。

---

### P0-M10-09:bot 被踢后 mute 状态残留

**证据**:M10 §4/§5 `feishu_chat_agent_binding` 含 `muted`,bot 被踢出群时 M3 lifecycle 的 `bot_removed` 应清 binding。但 M3 文档未提到 binding 清理,M10 §5 也未交叉引用。

**现实**:
- Bot 在 chat X 被群管 mute
- Bot 被踢出 chat X(另一个群管的操作)
- 若干天后 bot 又被加回 chat X → binding 仍在,muted=true → bot 继续沉默
- 谁都不知道为啥

**补救**:
1. M3 lifecycle `bot_removed` handler 必须清 `feishu_chat_agent_binding` 整行(或 set status=evicted);
2. `bot_added` 时检查是否有旧 binding → 如有 `evicted` 状态,启动 welcome 时提示"本群历史有 /mute 设置,已重置";
3. M10 §5 + M3 §lifecycle 交叉引用。

---

### P0-M10-10:`/agent <profile>` 切到不存在 profile → bot 静默

**证据**:M10 §4.4 `/agent code-assistant` 更新 `agent_profile`。未校验 profile 是否存在/启用。

**攻击/失误**:
- 群管输错 `/agent code-assistent`(拼错)→ binding 更新成 `code-assistent`
- `ensureSession` 找不到 profile → 下游空 session → 所有消息 drop 或 error

**补救**:
1. `/agent` handler 先查 profile registry,profile 不存在/disabled 回错误卡片 + 列当前可用 profiles + 不改 binding;
2. 启动时 load profile registry 入 memory,`/agent` 走 memory 查;
3. 从 binding 读 profile 时若找不到 → fallback 到 `DefaultAgentProfile` + metric `feishu.profile.fallback`;
4. M10 §4.4 新增校验规则。

---

### P0-M10-11:rollout whitelist 外 P2P 用户困惑

**证据**:M10 §6.3 "静默丢不回复"。但 P2P(1 对 1)场景:
- 非白名单用户私聊 bot
- Bot 永远不回
- 用户以为 bot 离线,去重启/找运维

**补救**:
1. P2P 非白名单 → 回一条明确提示"您不在本 bot 灰度内,申请访问请联系 <ops@xxx>";
2. 群聊场景继续静默丢(避免泄露 bot 存在);
3. M10 §6.3 补 P2P 差异化策略。

---

### P0-M10-12:ParseCommand 规范化缺失 — 大小写/空格/unicode 绕过

**证据**:M10 §2.2 `parts := strings.Fields(s[1:])` + `parts[0]` 作 Name,未规范化。

**绕过示例**(某些安全关键命令)::
- `/Reset` → Name=`Reset` ≠ `reset` → 不匹配,但用户觉得都一样 → 权限检查 miss 全开
- `/reset ` (尾空格或 tab) → Fields 已 trim,OK
- `/reset\u200b` (零宽字符后缀) → Name=`reset\u200b` ≠ `reset` → 未识别
- `/RESET` / `/ReSeT` 用大小写把规则搅乱
- 若命令 handler 里用 switch case → 某些 miss 某些 hit

**补救**:
```go
name := strings.ToLower(strings.TrimSpace(parts[0]))
name = strings.Map(func(r rune) rune {
    if unicode.IsControl(r) || r == '\u200b' || r == '\ufeff' { return -1 }
    return r
}, name)
// 然后再查 command registry
```
M10 §2.2 code 示例补 normalize 步骤 + 单测覆盖大小写/零宽/尾空格。

---

### P1-M10-13:audit `/audit last N` 泄露其他用户 PII

**证据**:M10 §2.1 群管能 `/audit last 10` 看审计日志。日志里含 tool_call 参数、消息内容、userID。

**风险**:群管(≠ 超管)能看其他成员的消息被 agent 如何处理 → 信息泄露。

**补救**:
1. `/audit last` 输出必须脱敏(和 debug echo 一样走 sanitizer);
2. 只显示 `action_type + tool_name + status`,不显示 params/content;
3. 详细 audit 只允许 super admin 通过 ops API 拉(带审批);
4. M10 §2.1 + §8 明确权限分级。

---

### P1-M10-14:rollout Mode 字符串手写易拼错

**证据**:M10 §6.2 `Mode string` 取值 `"all"|"whitelist"|"blacklist"` 默认 `"all"`。Config 里拼错成 `"whiteList"` → 行为不可预测(若代码 default 走 allow → 全开,否则全闭)。

**补救**:
1. 定义 `type RolloutMode string` + 常量 enum + `Validate()` 启动时检查;
2. 非法值 → 启动失败 + 清晰 error;
3. M10 §6.2 + config validation 章节。

---

## 修正后 Phase 0 / Phase 2 必须加的工作量(Δ)

| 项 | Phase | 文档改动 | 代码改动 |
|---|---|---|---|
| P0-M10-01 P2P 命令白名单 | P0 基础(防误触) | M10 §2.2 | commands.go ParseCommand |
| P0-M10-02 ACL 带 tenant_key | P0 基础(多租户前置) | M10 §3.1 + M11 §7 | acl.go 签名 |
| P0-M10-03 采样维度 | P2 | M10 §6 | rollout.go hash 改 message_id |
| P0-M10-04 debug 过 sanitizer + 硬限 | P0(借 M8 之力) | M10 §8 + M8 §3 | debug handler + daily quota |
| P0-M10-05 profile 切 = reset | P2 | M10 §4.4 | command_handler + session_id 规范 |
| P0-M10-06 ops mute 跨租户 | P7(M11 启用前做好准备) | M10 §5.3 + M11 §7 | API handler permission |
| P0-M10-07 mute 反馈 | P2 | M10 §5.4 | Router mute drop 前 notify once |
| P0-M10-08 ACL 缓存策略 | P2 | M10 §3.1 | 事件订阅 + prewarm |
| P0-M10-09 被踢清 binding | P2(M3 联动) | M10 §5 + M3 | lifecycle handler cleanup |
| P0-M10-10 profile 校验 | P2 | M10 §4.4 | command handler validate |
| P0-M10-11 P2P 白名单外友好提示 | P2 | M10 §6.3 | rollout.Allow 分场景 |
| P0-M10-12 命令规范化 | P0 | M10 §2.2 | ParseCommand normalize |
| P1-M10-13 audit 脱敏 | P5 | M10 §2 + §8 | audit output sanitize |
| P1-M10-14 enum 校验 | P2 | M10 §6.2 | config validation |

## 核心判断

M10 原稿把**命令/ACL/灰度/多 agent 绑定**写成了"功能列表",但未做"攻击枚举",导致:
- 命令解析在 P2P 里误触(P0-M10-01)
- ACL 跨租户不安全(P0-M10-02)
- debug 武器化(P0-M10-04)
- profile 污染(P0-M10-05)
这些都是**上线后立刻被内部/外部用户意外触发**的问题,不是"罕见攻击"。

**Phase 0 必须做的 M10 项**(即使 M10 主体在 Phase 2):
1. P0-M10-02 ACL 带 tenant_key(和 M11 §7 一起做前置);
2. P0-M10-04 debug sanitizer 规范(跟 M8 §3 同步,防止 Phase 2 实施时埋坑);
3. P0-M10-12 命令规范化(纯文本工具函数,早做早好);
4. 其他(P0-M10-01/03/05/07/08/09/10/11)在 Phase 2 落 M10 时必须全做,不能只做 "命令表"。

Phase 0 不动 M10 主体,但必须 freeze session_id + ACL 签名 + debug 路径三项前置接口,否则 Phase 2 还要回头改 Phase 0 产物。
