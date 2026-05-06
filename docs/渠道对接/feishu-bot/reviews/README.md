# 红队评审总览

> Phase 0 实施前对 M8/M9/M10 的对抗性审计。3 份独立报告 + 本总览整合 P0 并给出 Phase 0 scope delta。

## 方法论

- 证据原则:每个断言带 SDK 源码 line-number / 具体攻击路径,不做空头指控
- 蓝军心智:枚举绕过、边界、合规、跨副本/租户耦合
- SDK 版本锁定:`github.com/larksuite/oapi-sdk-go/v3 v3.5.3`,所有断言对此版本

## 报告文件

| 文件 | 模块 | P0 数 | P1 数 | 最致命发现 |
|---|---|---|---|---|
| [`M8-redteam.md`](M8-redteam.md) | 安全合规 | 12 | 2 | SDK 签名无 replay 窗口 + handler 返 err 触发飞书重试 + dedup 绕过链 |
| [`M9-redteam.md`](M9-redteam.md) | 可靠性 | 14 | 2 | `larkws.WithStatusChangeHandler` **在 SDK v3.5.3 不存在**,M9 §4.3 整段不可实施 |
| [`M10-redteam.md`](M10-redteam.md) | 会话治理 | 12 | 2 | debug 被武器化 + P2P 命令误触 + profile 切换历史污染 |

## 跨模块致命链

**链 A(消息永失)**:飞书重投 × dedup 单阶段 × handler 返 err
- M8 P0-02:handler 返 err → 500 → 飞书重投
- M9 P0-06:ClaimEvent 单阶段,claim 后崩溃 = 消息永失
- 联合效应:replica A claim → 崩溃 → replica B dedup drop → 用户永收不到回复。**上线即事故**。

**链 B(双回复)**:fail-open × 双入口
- M9 P0-05:DB 不可用 fail-open
- M9 P0-13:longconn + webhook 双入口
- 联合效应:双入口消息两次触达 + DB 超时 fail-open → 双副本双回复。

**链 C(PII 全链泄露)**:debug + audit + 日志
- M8 P0-10:debug echo 不过 sanitizer
- M10 P0-04:debug 30 min 重置可永久开
- M10 P1-13:`/audit last` 输出未脱敏
- M8 P0-08:PII grep gate 可绕过
- 联合效应:debug on → 全群 PII 折叠卡片永久留存 + audit 输出给任意群管 + 日志里 open_id 明文。

**链 D(多租户越权)**:ACL openID × ops mute × session_id
- M10 P0-02:ACL 无 tenant_key
- M10 P0-06:ops mute API 跨租户
- M9 P0-12:session_id 未含 tenant_key
- 联合效应:M11 启用多租户时每一层都要回头改。

## Phase 0 scope delta(必改)

原 Phase 0 scope = imctx 叶子 + HITL 死链 + encrypt/signature wire + 分布式 dedup + TenantResolver/ClientRegistry stubs。

**红队后必须加入 Phase 0**:

| # | 项 | 来源 | 理由(不加入后果) |
|---|---|---|---|
| 1 | Webhook timestamp replay 窗口 middleware(±5 min) | M8 P0-01 | 无窗口 = 攻击者无限重放历史 body |
| 2 | SDK handler wrapper 永返 nil + 失败写 retry_queue | M8 P0-02 + M9 P0-06 | 触发飞书 5xx 重投 → 消息永失 |
| 3 | `isEncrypted()` 修正为手写 probe | M8 P0-03 | 照抄文档即 compile error |
| 4 | 未注册 event type metric + 兜底告警 | M8 P0-05 | 未来新事件类型静默丢 |
| 5 | `@all` sanitizer 白名单递归(含 post/卡片/merge_forward) | M8 P0-07 | 绕过路径 5+ 种,只禁 text 无效 |
| 6 | PII CI gate 升级 AST 级 + runtime logger hook | M8 P0-08 | grep regex 易绕过 |
| 7 | Debug echo 强制过 sanitizer(仅 Alice 可见) | M8 P0-10 + M10 P0-04 | debug = PII 泄露武器 |
| 8 | Webhook URL 层防御(signature-missing 直 401) | M8 P0-11 | 扫描器 DoS / 日志爆炸 |
| 9 | `larkws.WithStatusChangeHandler` 不存在 → 自建 watchdog | M9 P0-01 | M9 §4.3 整段失效 |
| 10 | `Im.Message.List` 加 `ContainerIdType` + EndTime | M9 P0-02/03 | API 直接 400 + 大群消息爆炸 |
| 11 | gap fetch 全量 chat 集合(非"最近活跃") | M9 P0-04 | 断线期间静默群永久丢消息 |
| 12 | Dedup 三级 fail 策略(短超时 fail-closed / redis / lru) | M9 P0-05 | DB 抖动即双处理 |
| 13 | 两阶段 claim(processed 标志 + reclaim worker) | M9 P0-06 | claim 后崩溃 = 消息永失 |
| 14 | retry_queue `FOR UPDATE SKIP LOCKED` + 死信 | M9 P0-07/11 | 并发 worker 抢同条;5 次失败石沉 |
| 15 | retry_queue payload 最小化(不存原文) | M9 P0-08 | PII 合规风险 |
| 16 | session_id 格式统一 `im-feishu-{tenantKey}-{chatID}` | M9 P0-12 + M11 §8 | Phase 0 不做后续全表迁移 |
| 17 | P2P 命令白名单 + 命令规范化(lowercase/trim/unicode) | M10 P0-01/12 | P2P 问"/etc/hosts"触发误命令 |
| 18 | ACL 签名带 tenant_key | M10 P0-02 + M11 §7 | M11 启用即越权 |

**仍留 Phase 2/5/7 的项**(文档必须在 Phase 0 改完,实施延后):
- health 跨副本、audit 异步/retention、ops mute 跨租户、rollout 采样维度、bot 被踢清 binding 等。

## 对 README 的矫正项

原 README.md 给 M8/M9/M10 评 🔴,红队后:
- M8 原稿里有**凭空编造的 SDK 符号**(`isEncrypted`) → 🔴 **+ 基础事实错误**
- M9 原稿**基于不存在的 SDK API**(`WithStatusChangeHandler`) → 🔴 **+ 整章失效**
- M10 原稿**未做攻击枚举** → 🔴 **+ 命令/debug/ACL 每个都有绕过**

README 关键判断从"缺几个 handler"升级为:**原稿 M8/M9 含基础事实错误(凭空符号),不是"完成度低"而是"不可直接落地"**。Phase 0 scope 必须以本 redteam 报告为准。

## 建议的实施动作

1. **立即**(开始 Phase 0 前必做):
   - 按本 scope delta 更新 M8/M9/M10 文档主体(每个 P0 在原章节改写)
   - 更新 ROADMAP Phase 0 列表加 18 项
   - 删除文档中所有引用 `larkws.WithStatusChangeHandler` / `isEncrypted` 的段落
2. **Phase 0 实施中**:
   - 每个 P0 对应一个 PR + 单测覆盖攻击路径
   - 关键表(dedup/retry)schema 迁移前做 staging drill(主从切换 + claim 崩溃 + gap fetch 边界)
3. **上线前**:
   - 安全侧做一次**外部**红队演练(HackerOne-style),本报告为内部基线

## 核心判断

**三个模块中最严重的是 M9**——`larkws.WithStatusChangeHandler` 的凭空符号让原稿的可靠性核心(gap fetch 触发时机)完全没有落脚。**Phase 0 在 M9 上的代码量要从"wire dedup"变成"wire dedup + 自建 watchdog + 两阶段 claim + fail 策略"**,工作量 3-4 倍。

**M8 次之**——系统性缺"攻击面枚举",@all 绕过/PII gate 绕过/debug 泄露链多条可组合,补齐后 M8 会从 🔴 升到 🟡,再需要 Phase 5 完善 audit/health 才能到 🟢。

**M10 第三**——主要是"功能列表式"规划遗漏攻击向量,命令/debug/ACL 每个都得重写细节。好在 M10 主体在 Phase 2,Phase 0 只需冻结 session_id 规范 + ACL 签名 + 命令规范化三个接口,不阻塞。

---

## CEO 决议 (2026-04-22)

本次决议基于 plan-ceo-review(Claude)与 codex(独立工程判断)双向辩论结果综合成稿。**辩论产物保留在 `/tmp/codex-redteam-out.txt`**(codex 原文)与本仓库对话历史。

### 决议要点

1. **Phase 0 = webhook-only 首发**。客户私有化 ingress 场景下 webhook 覆盖主要流量,longconn 属于"内网无公网"极端情况的补丁,推迟 Phase 2。
2. **禁止同进程 webhook + longconn 并存**。不管哪个 Phase,生产运行时必须 `Ingress=webhook XOR longconn`,从根源消除红队链 B 的双入口前提。
3. **`/debug` 不在 Phase 0 首发**。Phase 0 不打开任何"把原始 payload 回群"的通道,链 C 的主要放大器在首发期不存在。Phase 2 实现时必须带 sanitizer + only-visible-to-caller + hard cap 30 min(不可重置)。
4. **Phase 0 收敛为 12 项**(不是原 5 项,也不是红队 23 项)。原则:**正确性闭环 + 公网安全边界 + 多租户前向兼容**,其余一律 P1 推迟。完整清单见 `ROADMAP.md § Phase 0 红队后 12 项清单`。

### 三条致命链的处理

- **链 A(消息永失)**:**完全采纳**。Phase 0 实现两阶段 claim + `MarkProcessed` + reclaim worker + handler 永返 nil + fail-closed 短超时。不依赖"把 retry_queue 整个前拉",只要 claim 语义正确 + handler 不返 err 即可堵住。
- **链 B(双回复)**:**从根上断掉**。Phase 0 webhook-only,Phase 2 起 `Ingress` 二选一,消除双入口前提。fail-open 一律改 fail-closed 短超时。
- **链 C(PII 全链泄露)**:**分层缓解**。Phase 0 不开 `/debug`、`/audit`,消除主要武器化通道;PII gate 升级 AST 级 + runtime logger hook,阻断"grep 绕过"。Phase 2 真正开放 `/debug` / `/audit` 时必须带 sanitizer + only-visible + hard cap,单独 PR review。

### 对红队报告的保留意见(不全盘接受)

与 codex 独立判断一致,本仓库保留以下修正:

- "M9 §4.3 整段失效" → 准确说法是"依赖 `WithStatusChangeHandler` 的重连触发方案失效",M9 dedup/幂等部分并未失效。
- "链 B 必然双回复" → 前提是**生产双入口并存**,这是部署决策不是 SDK 铁律。Phase 0 webhook-only 即不成立。
- "23 项都不做就上线即事故" → 混了 A/B 级:两阶段 claim / URL 层 401 属 A 级(不做必爆);未注册 event metric / AST PII gate / retry payload 最小化 / health 跨副本 / rollout 采样维度 属 B 级(强 hardening,不阻塞首发)。
- `retry_queue` 非唯一正解 → 真正必须的是 durable handoff + reclaim 语义,不是特定表名 / 实现套路。
- 部分 API/QPS 断言(`Im.Message.List` "一定 400"、某些 tenant QPS 数字)在允许阅读材料里无法独立证实,**不照单全收**,Phase 2 真正做 gap fetch 时再实证。

### 红队未覆盖但关键的 insight(Codex 补充)

> **核心不变量不是"要不要 retry_queue",而是"claim != processed,ack 不能绑定在业务成功上"。**

这个判断被采纳为 Phase 0 两阶段 claim + handler 永返 nil 的理论基础。

### 文档更新映射

| 文档 | 更新内容 |
|---|---|
| `08-security.md` §3.2 | 删除 `isEncrypted(body)` 伪码,改为 anonymous struct JSON probe(已完成) |
| `09-reliability.md §2.4` | 新增"两阶段 claim + fail-closed 短超时"细则(已完成) |
| `09-reliability.md §4` | 整节标注 Phase 2,删除 `WithStatusChangeHandler` 代码块,补 SDK 限制 + Phase 2 自建 watchdog 思路(已完成) |
| `ROADMAP.md Phase 0` | 替换原 5 项扩动清单为 12 项 webhook-only 清单 + CI gate 6 条 + 验收 10 项(已完成) |
| `ROADMAP.md Phase 2` | 合并 longconn watchdog / gap fetch / `/debug` / `/audit` / ops mute / P2P 规范化(进行中) |
| `reviews/README.md` | 本节(完成) |
| 主 `README.md` | M8/M9 评分附"原稿含凭空符号"标注 + Phase 0 描述改为 webhook-only(进行中) |

### Codex 复核(2026-04-22 第二轮)

文档落地后再次让 codex 独立复核 7 份改动,verdict: **APPROVE_WITH_FIXES**(7 处真问题)。已全部修复:

| # | 问题 | 修复 |
|---|---|---|
| 1 | `10-session-governance.md:79` ACL 伪码用 `Im.ChatManagers.List(...)`,SDK v3.5.3 只有 `AddManagers/DeleteManagers`,无 `List` | §3.1 标 Phase 2 实证 + 给候选方案,Phase 0 不阻塞 |
| 2 | `09-reliability.md §7` `LongconnGapFetchEnabled=true` / `FailOpenOnDedupError=true` 默认值与 webhook-only / fail-closed 决议冲突 | LongconnGapFetchEnabled 默认 false;`FailOpenOnDedupError` **整字段删除**,新增 `DedupClaimTimeout` / `DedupReclaimAfter` |
| 3 | `09-reliability.md §3.1` 还写 "session_id 按 chat_id 派生",与 M11 多租户格式冲突 | 改为引用 `BuildSessionID(tenantKey, chatID)` |
| 4 | `08-security.md §3.1` 允许打 `open_id/union_id`,但 ROADMAP/M10 要求脱敏 | 统一规则:日志一律 `SafeSenderID`,原始 ID 仅 audit 表(行级访问) |
| 5 | `11-multi-tenant.md §7` 写 "(event_id, tenant_key) 复合保证",但实际 schema `event_id PRIMARY KEY` | 文案改"依赖 event_id 全局唯一,tenant_key 仅审计/metric label" |
| 6 | `09-reliability.md §5.2` retry_queue schema 缺 `tenant_key` + `content TEXT NOT NULL` 是新 PII 泄露面 | 补 `tenant_key NOT NULL DEFAULT 'default'` + 标注 Phase 5 必须 payload 最小化 |
| 7 | `10-session-governance.md §8.0` "visibility=private 临时卡片" 在 SDK v3.5.3 未实证 | 改为"具体形态 Phase 2 实证;退化方案改投私聊,禁止落到普通群卡片" |

**Phase 0 scope 复核结论**:
- Codex 异议: PII AST gate 是 B 级 hardening,不该作 Phase 0 硬阻塞。**采纳**:Phase 0 退回 grep 级 + runtime `SafeSenderID` hook,AST 级延后 Phase 5。
- Codex 增补: 删除 `FailOpenOnDedupError` 配置(运维脚枪)+ session_id 多租户格式全文一致。**采纳**:列入 Phase 0 #13/#14。
- 最终 Phase 0 = **14 项**(不是 12)。

**Codex 没覆盖但需后续注意**:
- `/debug` 在 Phase 2 实现时,若 SDK 不支持 only-visible-to-caller 字段,fallback **必须**是私聊,不能退化为普通群卡片。
- retry_queue Phase 5 worker 完整化时,`content` 列必须改 schema 为 `template_id + template_args_json`,否则即使日志层守住 PII,DB 表本身就是泄露面。
- Phase 2 真做 longconn 时,SDK 升级或自建 watchdog 必须先于"开 `LongconnEnabled=true`"。
