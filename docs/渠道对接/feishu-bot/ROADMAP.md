# 实施路线图

> 11 个模块,分 8 个 Phase 交付。每 Phase 独立可 ship、可回滚。上一阶段合并后再开下一阶段 PR。

## 当前验收边界(给验收 Agent)

> 截至 2026-04-27，当前仓库飞书改造的验收边界已收缩为：
> **单租户 + webhook-only + 内部直接可用 MVP**。
>
> 也就是说，ROADMAP 仍然保留完整长期规划，但**当前验收不要求**把所有 Phase 全做完。
> 其他 Agent 验收时请以“是否满足 MVP 上线口径”为主，而不是以“ROADMAP 所有尾项是否归零”为主。

### 当前上线必须通过

- webhook 路径收消息、回消息
- `card.action.trigger` / HITL 回调
- 基础治理命令：`/help`、`/status`、`/reset`、`/mute`、`/unmute`、`/model`
- 基础安全：验签、加密、时间窗、`@all` 防护
- 基础可靠性：fail-closed dedup、retry queue、降级 fast-fail

### 当前明确可延期

- `/debug`
- `/agent`
- 多租户 / ISV
- longconn 首发生产化
- 重型运维面
- dead-letter 人工 replay 平台
- 完整 observability / 告警闭环
- 一群绑多 agent

## Phase 规划原则

1. **P0 基座 + 安全 + 可靠性 必须最先**(Phase 0):不然生产上线就是事故面
2. **独立性**:每 Phase 代码改动不强依赖未合并的后续 Phase
3. **可观测与可回滚**:每 Phase 合并时配套 metric 上报 + feature flag 降级
4. **蓝军覆盖**:每 Phase 必须有 adversarial test(畸形 payload、并发竞态、飞书 API 错误)
5. **SDK-only**:新代码不允许引入手写 HTTP 调 OpenAPI(CI 拦 diff)

## Phase 0 🔥 P0 基座 + 安全 + 可靠性(webhook-only 首发)

> **CEO 决议(2026-04-22)**:Phase 0 只支持 **webhook 生产入口**,longconn 延后 Phase 2。
> **作废原 5 项清单**:imctx 叶子 + HITL 死链 + encrypt/signature wire + 分布式 dedup + TenantResolver/ClientRegistry stubs。红队证明此 scope 漏掉:两阶段 claim、ack 语义、URL 层防御、replay 窗口、handler 不能返 err、session_id 格式、sanitizer 递归、PII AST gate。
> **作废红队原 18 项扩展**:包含大量 longconn / gap-fetch / `/debug` / ops 项目,不在 webhook-only 路径上,推到 Phase 2+。
> **最终 14 项合并结论**(codex 复核后从 12 增补 #13/#14):正确性闭环 + 公网安全边界 + 多租户前向兼容,其他一律 P1 推迟。

**目标**:搭 `imctx` 叶子包 + 修 HITL 死链 + 接通 webhook 安全链路(encrypt/signature/replay 窗口/URL 层 401)+ 两阶段 claim + fail-closed dedup + session_id 多租户前向格式 + handler 永不返 err + @all sanitizer 递归 + PII AST gate。这是后续所有 Phase 的公共基座。

### Phase 0 红队后 14 项清单(codex 复核增补 #13/#14)

| # | 项 | 来源 | 不做则 |
|---|---|---|---|
| 1 | `internal/imctx` 叶子包 + CI gate(`go list -deps` 无反向) | 基座 | 包依赖环,后续 Phase 全受阻 |
| 2 | HITL `card.action.trigger` 注册 + `FeishuHITLBridge` → `master.HITLBroker.SubmitInput` | M2 | 当前死链,按钮按下无反应 |
| 3 | **删除文档中所有 `WithStatusChangeHandler` / `isEncrypted()` 引用**(本 Phase 不依赖) | M8/M9 红队 | 照抄文档即 compile error |
| 4 | webhook 重写为 `larkcallback.NewEventHandlerFunc` + dispatcher(wire encrypt/signature/URL verify) | M8 P0-01 | 线上公网裸奔 |
| 5 | webhook URL 层:缺 signature 头直接 401,不走 SDK 5xx 路径 | M8 P0-11 | 扫描器 DoS + 日志爆炸 |
| 6 | webhook timestamp replay 窗口 ±5 min(URL 层中间件) | M8 P0-01 | 攻击者可无限重放历史 body |
| 7 | SDK handler wrapper 永返 `nil` + 业务失败落盘 `retry_queue`(必须写,但 worker 可 Phase 5 完整化) | M8 P0-02 + M9 P0-06 | Feishu 5xx 重投 → 消息永失 / 双处理 |
| 8 | 两阶段 claim(`claimed_at` + `processed` + `processed_at`) + reclaim worker 清 stale claim | M9 P0-06 | claim 后崩溃 → 消息永失(链 A) |
| 9 | dedup **fail-closed** 短超时(200ms timeout + 返 nil 让飞书重投),禁用 fail-open | M9 P0-05 | DB 抖动即双处理 / 双回复 |
| 10 | `session_id` 格式固化 `im-feishu-{tenantKey}-{chatID}`,第一天就带 `tenantKey` | M9 P0-12 + M11 §8 | Phase 0 不做 → 后续全表迁移 |
| 11 | `@all` sanitizer 白名单递归:text / post / card / merge_forward / 嵌套载体全覆盖 | M8 P0-07 | 绕过路径 5+ 种,只禁 text 无效 |
| 12 | PII CI gate(Phase 0 grep 级即可)+ `SafeSenderID` 强制路径(所有 logger.* 调用禁原文 `open_id` / `union_id` / `email` / `phone` / `mobile`) | M8 P0-08 | grep + runtime hook 双层兜底,AST 级升级延后 Phase 5 |
| 13 | **删除** `FailOpenOnDedupError` config 字段(代码层面禁 fail-open,不留运维脚枪) | 红队 codex 复核 | 留开关 = 一次救火就回到链 B |
| 14 | session_id 多租户格式**全文一致**(M9 §3.1 / 11 §8 / 所有 BuildSessionID 调用点) | 红队 codex 复核 | 不一致即"格式锁了但实施漏" |

### Phase 0 **不做**(红队建议但推到后续 Phase)

以下项**红队 scope 里有,但 webhook-only + 首发最小面**原则下延后:

- longconn watchdog / gap fetch / `Im.Message.List` ContainerIdType 修正 → Phase 2(当 longconn 真开放时)
- `retry_queue` `FOR UPDATE SKIP LOCKED` / 死信 / payload 最小化 → Phase 5(M9 retry_queue worker 完整化一并做)
- `/debug` / `/audit` / ops mute 命令(含 sanitizer、可见性、hard cap) → Phase 2(M10 会话治理整体延后)
- health 跨副本 / rollout 采样维度 / bot 被踢清 binding / audit retention → Phase 5
- P2P 命令白名单 + 规范化 → Phase 2(无 `/debug` 则 P2P 命令攻击面本身很小)
- ACL tenant_key 完整实现 → Phase 2(Phase 0 只冻结 session_id 格式,ACL 细节延后)
- 未注册 event 类型 metric / 兜底告警 → Phase 5(可观测闭环)

### 改动清单

| 文件 | 动作 | 来自 12 项 |
|---|---|---|
| `internal/imctx/*.go` | **新建**,所有共享类型(stdlib only) | #1 |
| `internal/channel/types.go` | 扩 `InboundMessage`;加 `CardAction`/`InteractionHandler`/`InboundContextResolver` 接口 | #2 |
| `internal/channel/router.go` | 加 `SetInteractionHandler`/`HandleCardAction`/`SetContextResolver`/`SetTenantResolver`/`SetClientRegistry` | #2,#10 |
| `internal/channel/feishu/webhook.go` | **重写** larkcallback dispatcher,**包装**成 `handler wrapper` 永返 nil,业务失败入 retry_queue | #4,#7 |
| `internal/channel/feishu/webhook_middleware.go` | **新建** URL 层:signature-missing → 401;timestamp 窗口 ±5 min | #5,#6 |
| `internal/channel/feishu/longconn.go` | Phase 0 **仅保留注册点,默认 disabled**;`LongconnEnabled=true` 启动报错 | — |
| `internal/channel/feishu/hitl_bridge.go` | **新建** `FeishuHITLBridge` → `master.HITLBroker.SubmitInput` | #2 |
| `internal/channel/feishu/dedup.go` | **新建** `DistributedDedup`:两阶段 claim + `MarkProcessed` + fail-closed 短超时 | #8,#9 |
| `internal/channel/feishu/reclaim_worker.go` | **新建** 清 stale `claimed_at` | #8 |
| `internal/channel/feishu/retry_queue_min.go` | **新建** 最小 insert-only API(worker 在 Phase 5) | #7 |
| `internal/channel/feishu/sanitizer.go` | **新建** `@all` 白名单递归:text/post/card/merge_forward | #11 |
| `internal/channel/feishu/piilog.go` | **新建** `SafeSenderID` 等脱敏 + runtime logger hook | #12 |
| `migrations/<ts>_feishu_dedup.sql` | **新建** `feishu_event_dedup`(含 `claimed_at` / `processed` / `processed_at`) | #8 |
| `migrations/<ts>_feishu_retry_queue_min.sql` | **新建** `feishu_outbound_retry_queue` 骨架(Phase 5 worker 拓展) | #7 |
| `internal/channel/feishu/tenant.go` | **新建** `TenantResolver` + `SingleTenantResolver` stub(固化 session_id 格式) | #10 |
| `internal/channel/feishu/client_registry.go` | **新建** `ClientRegistry` + `SingleClientRegistry` stub | #10 |
| `internal/channel/feishu/session_id.go` | **新建** 统一构造 `im-feishu-{tenantKey}-{chatID}` | #10 |
| `internal/bootstrap/server.go:377` | 装配 middleware + dispatcher + registry + dedup + hitl bridge + reclaim worker | 基座 |
| `internal/config/config.go:881` | `FeishuSecurityConfig` + `FeishuReliabilityConfig`(含 `LongconnEnabled=false` fatal guard) | 基座 |
| `docs/渠道对接/feishu-bot/08-security.md` | **修正** 删除 `isEncrypted(body)` 伪码,改为手写 JSON probe | #3 |
| `docs/渠道对接/feishu-bot/09-reliability.md §4` | **修正** 标注 Phase 2 only + 删除 `WithStatusChangeHandler` 代码块 | #3 |
| `docs/渠道对接/feishu-bot/09-reliability.md §2` | **修正** 两阶段 claim + fail-closed 细则(已完成) | #8,#9 |

### CI gate

```bash
# 1. imctx 叶子(#1)
go list -deps ./internal/imctx/... | grep 'chef-guo/agents-hive/internal/' && exit 1 || true

# 2. SDK-only diff gate
git diff origin/main -- 'internal/channel/feishu/**' \
  | grep -E '^\+.*"(open\.feishu\.cn|open\.larksuite\.com)/(open-apis|api)' && exit 1 || true

# 3. 禁用凭空符号(#3)— 只检源码,不检 reviews/(红队报告需要引用符号来解释错误)
grep -rn 'WithStatusChangeHandler\|larkws\.Status\b\|StatusReconnecting\|StatusConnected' internal/channel/feishu/ && exit 1 || true
grep -rn 'isEncrypted(' internal/channel/feishu/ && exit 1 || true

# 4. PII AST gate(#12,升级自原 grep)
go run ./internal/tools/piigate ./internal/channel/feishu/... || exit 1

# 5. handler 永不返 err(#7)— 静态检查
go run ./internal/tools/handlergate ./internal/channel/feishu/... || exit 1

# 6. session_id 格式(#10)— 源码层禁止别的拼法
grep -rn 'session_id.*:=.*chatID' internal/channel/feishu/ | grep -v 'BuildSessionID' && exit 1 || true
```

### 测试

- 单测:
  - `DistributedDedup` 并发 1000 次 claim 同 event → 恰好 1 次 `claimed=true`(#8)
  - claim 后模拟 handler panic,reclaim worker 90s 后清 `claimed_at` → 新 event 可重新 claim(#8)
  - signature 缺失 → URL 层 401,未进 SDK(#5)
  - timestamp 超过 ±5 min → 401(#6)
  - HITL bridge 对 approve/reject 调 `SubmitInput`(#2)
  - `@all` sanitizer:text / post 含 `<at user_id="all">` / card `elements[].content` 含 @ / merge_forward 嵌套 → 全拦截(#11)
  - `SafeSenderID` 不可反推(#12)
  - `BuildSessionID("tenant-x", "chat-y")` === `im-feishu-tenant-x-chat-y`(#10)
  - handler wrapper:业务 panic / 业务 return err → wrapper 永返 nil,写 retry_queue(#7)
- 集成:真实租户加密 event → 完整解析;HITL 按钮点击 → agent resume
- 蓝军:
  - 伪造 signature → 401
  - 重放 5 分钟前的合法 body → 401
  - 重复点击按钮只触发一次
  - DB 断电 200ms timeout → fail-closed 返 nil,飞书重投另一副本处理
  - 副本 A claim 成功后 `kill -9`,副本 B 90s 后 reclaim 成功
  - text 用全角、post block 嵌套、card 嵌套 → sanitizer 全拦

### 验收

- [ ] HITL 卡片按钮点击后 agent 正常 resume(当前:死链)
- [ ] `EventEncryptEnabled=true` 开启后,加密 event 被完整解析
- [ ] 伪造 signature / 超时 timestamp → URL 层 401,日志无 5xx
- [ ] 3 副本部署同时收 webhook,只有 1 副本 `MarkProcessed`,另 2 副本 `feishu.event.deduped` +1
- [ ] 副本 kill -9 → 90s 内 reclaim worker 清 claim,重投 event 被新副本处理(0 消息永失)
- [ ] DB 200ms timeout → `feishu.dedup.fail_closed` +1,handler 返 nil,飞书重投
- [ ] 日志里 grep 不到 `open_id` / `union_id` / `email` / `phone` / `mobile` 字段
- [ ] session_id 全部形如 `im-feishu-{tenantKey}-{chatID}`
- [ ] CI 6 条 gate 全过
- [ ] `LongconnEnabled=true` 启动报错退出

### 回滚

- `Router.SetInteractionHandler(nil)` → HITL 按钮回到死链状态(无倒退)
- `FeishuSecurityConfig.EventEncryptEnabled=false` → 退回明文(需飞书后台同步关)
- `FeishuReliabilityConfig.DistributedDedupEnabled=false` → 退回内存 LRU dedup(**仅灾难恢复,不允许长期开此态**)
- `FeishuSecurityConfig.SanitizerEnabled=false` → 禁用 @all 拦截(**不推荐,会回到链 C 风险面**)

---

## Phase 1:M1 消息摄取 + M4 资源下载

**目标**:Agent 能看懂全部消息形态 + 能主动下载消息里的图/文件。

### 改动清单

| 文件 | 动作 | 模块 |
|---|---|---|
| `internal/channel/feishu/types.go` | `Message` 加 `ParentID`/`RootID`/`Mentions` | M1 |
| `internal/channel/feishu/webhook.go` | 注入 ParentID/Mentions | M1 |
| `internal/channel/feishu/longconn.go` | 同上(webhook/longconn 对齐) | M1 |
| `internal/channel/feishu/parser.go` | **新建** `ExtractInboundMessage`;支持 text/post/image/file/audio/media/sticker/location/share_chat/share_user/merge_forward/system 全分类 | M1 |
| `internal/channel/feishu/plugin.go:251-284` | `HandleInbound` 调 parser | M1 |
| `internal/channel/feishu/resolver.go` | **新建** `ContextResolver`:bot 自反射、父消息、wiki 解析 | M1 |
| `internal/channel/feishu/download.go` | **新建** `DownloadMessageResource`(`client.Im.MessageResource.Get`) | M4 |
| `internal/channel/router.go` | `processMessageImpl` 调 resolver,构造 `IMMessageContext` | M1 |
| `internal/channel/types.go` `IMMessageProcessor.ProcessMessageFromIM` | 签名加 `imCtx` | M1 |
| `internal/master/session_message.go` | 透传 imCtx 到 `SessionRequest.IMContext` | M1 |
| `internal/master/session.go` `SessionState` | 加 `pendingIMContext` + `SetPendingData`/`ConsumePendingIMContext` | M1 |
| `internal/master/session_loop.go:229` | sem gate 之后写 pending(race-free) | M1 |
| `internal/master/react_processor.go` | plugin hook 之后 consume prefix | M1 |
| `internal/tools/feishu_tools.go` | 加 `download_message_resource` action | M4 |
| `internal/config/config.go` | `FeishuInboundConfig` | M1 |

### 关键不变式

- prefix **写入点必须在 session_loop.go:229**(sem gate 后)
- prefix **consume 必须在 react_processor plugin hook 之后**(否则被 plugin 覆盖)
- CDATA escape 处理 `]]>` 嵌套
- parser 对未知 message_type **保守降级**为文本占位符,不 panic

### 测试

- 单测:12 种 message_type 全覆盖;bot 自反射 drop;wiki → token 解析缓存;prefix race(100 并发)
- 集成:用户贴 docx/sheet/bitable/wiki 链接 → agent 读到;PDF 附件 → agent 调 `download_message_resource` 拿 bytes
- 蓝军:prompt 含 `]]>` → CDATA 合法;父消息是 bot 自己 → drop;post 格式畸形 → 降级不 panic

### 验收

- [ ] Agent 识别 5 种云文档 URL + 处理 merge_forward/share
- [ ] 用户回复某消息 @bot → prefix 含父消息摘要
- [ ] `feishu.inbound.refs_count` / `resolver.duration_ms` 上报

### 回滚

- `Feishu.Inbound.EnableContextResolver=false` → agent 只看原始 content

---

## Phase 2A:M3 生命周期 + M10 最小会话治理(webhook-only)

**目标**:在 **不开放 longconn** 的前提下，交付可上线的 webhook-only 生命周期与最小治理面:bot 入群招呼、被踢清理、`/help` `/status` `/reset`、tenant-scoped ACL、deterministic rollout、persistent mute、出站 suppression。

> **红队后新增范围**(Phase 0 推迟项):longconn watchdog / gap fetch / `Im.Message.List` ContainerIdType 修正 / `/debug` / `/audit` / ops mute 命令 sanitizer & hard cap / P2P 命令白名单 + 规范化 / ACL tenant_key 完整实现 / bot 被踢清 binding。
>
> **强约束**:开放 longconn 前 **必须** 先完成 webhook ⇋ longconn 单入口切换机制(不能同进程并存,红队链 B),客户只能在 `Ingress=webhook | longconn` 二选一。

### 改动清单

| 文件 | 动作 | 模块 |
|---|---|---|
| `internal/channel/feishu/lifecycle_handler.go` | **新建** | M3 |
| `internal/channel/feishu/card_builder.go` | 加 BuildWelcomeCard/BuildP2PWelcomeCard | M3 |
| `internal/channel/feishu/commands.go` | **新建** `/help` `/status` `/reset` 命令解析与 normalize | M10 |
| `internal/channel/feishu/acl.go` | **新建** tenant-scoped `/reset` allowlist ACL | M10 |
| `internal/channel/feishu/rollout.go` | **新建** 灰度决策 | M10 |
| `internal/channel/feishu/chat_state_repo.go` | **新建** chat_state DB 访问 | M10 |
| `migrations/20260424000002_feishu_chat_state.sql` | **新建** | M10 |
| `internal/channel/router.go` | processMessageImpl 加 lifecycle/rollout/mute/cmd 治理分支 | M10 |
| `internal/master/public_api.go` | `TerminateSession` 导出并复用到 `/reset`/`bot_removed` | M3 |

### 测试

- 单测:lifecycle 两路解析一致;`bot_removed` → `TerminateSession`;命令 normalize/ACL;rollout deny/mute/evicted drop
- 集成:bot 进群 welcome;`/reset` 群聊拒绝非 allowlist;`bot_removed` 后 inbound/outbound 均阻断
- 蓝军:伪造 openID 冒充群管 → tenant ACL 拒绝;旧 `bot_removed` 不覆盖新 `bot_added`

### 验收

- [ ] bot 被踢后 master session 不占 sem
- [ ] `/reset` 群聊拒绝非管;私聊允许
- [ ] rollout 非白名单 chat 不回复
- [ ] `bot_removed` 后该 chat 不再进入旧 session

### 回滚

- `Lifecycle.Enabled=false` → handler skip
- `Rollout.Mode=all` → 不灰度

---

## Phase 2B:longconn 补偿与扩展治理

**目标**:在 Phase 2A webhook-only 稳定后,按子阶段逐步开放 longconn 入口、断线探测、重连补偿与扩展治理。Phase 2B 不再作为一个“大包”落地,而是拆成 2B.1 / 2B.2 / 2B.3 顺序交付。

### Phase 2B.1:single-ingress switching(已完成)

**边界**:

- 显式 `ingress_mode=webhook|longconn`
- 常驻 Feishu webhook 路由 + runtime gate
- hot reload 先关旧入口,再起新入口,不允许 dual ingress
- 切换失败保持 fail-closed,并返回显式错误给运维
- 本子阶段**不包含** watchdog / gap fetch / `/debug` / `/audit`

**验收清单**:

- [x] `ingress_mode` 成为 Feishu 运行时单一真相源,不再依赖 `WebhookURL` 推导入口模式
- [x] Feishu webhook 路由物理常驻,但 `longconn` 模式下 runtime gate 稳定拒绝请求
- [x] `webhook -> longconn` 与 `longconn -> webhook` 热切换过程中 **never dual ingress**
- [x] 切换失败时保持 fail-closed,不会自动放开另一入口“兜底”
- [x] 本子阶段不引入 SDK status callback 依赖,也不宣称已有 gap fetch 补偿

### Phase 2B.2:reconnect watchdog(已完成)

**边界**:

- 仅实现 longconn 断线/恢复探测与状态机
- 必须是**自建 watchdog**,不能依赖不存在的 SDK status callback
- 产出 reconnecting / recovered / fail-closed 的可观测信号
- 多副本场景下仅允许一个副本持有 watchdog leader 身份
- 本子阶段**不包含** gap fetch 回补,也不包含 `/debug` / `/audit`

**验收清单**:

- [x] watchdog 不依赖 `WithStatusChangeHandler` / `larkws.Status*` 这类 SDK 不存在能力
- [x] 无 event 流量达到阈值时,系统进入明确的 reconnecting 状态
- [x] 恢复判定有结构化日志和 `ReliabilityStatus()` 可查
- [x] 多副本下只有一个副本执行恢复回放 leader 逻辑
- [x] watchdog 本身不拉消息,只负责探测、状态推进和触发后续 gap fetch 回调

### Phase 2B.3:gap fetch(已完成)

**边界**:

- 仅实现 longconn 恢复后的消息回补链路
- 必须复用标准 dedup / claim 流程,不允许旁路处理
- 明确 `Im.Message.List` 请求参数边界,避免无界拉取
- 本子阶段不默认包含 `/debug` / `/audit` / ops mute / P2P 命令扩展

**验收清单**:

- [x] gap fetch 只在 watchdog 判定“已恢复”后触发,不会在不确定状态下并发乱拉
- [x] `Im.Message.List` 请求显式带 `ContainerIdType` 与 `EndTime`,不能依赖 SDK 默认值
- [x] 回补窗口受 `GapFetchMaxWindow` 约束,超窗会截断并记录告警/状态
- [x] 回补结果逐条进入标准 dedup/dispatcher 链路,已处理 event 被安全跳过
- [x] gap fetch 失败时不切回双入口,而是保持当前入口语义并暴露可观测错误

### Phase 2B 尾项:扩展治理(待排期)

以下项保留在 Phase 2B 后续尾项,但**不与 2B.2 watchdog / 2B.3 gap fetch 绑定交付**:

- `/debug` `/audit`
- ops mute 命令与 sanitizer / hard cap
- P2P 白名单与命令规范化
- 其他 longconn 可靠性增强项

---

## Phase 3:M5 身份

**目标**:Agent 看到真名而非 open_id;bot open_id 启动期同步就绪;多语言字段。

**当前状态(2026-04-25)**:已完成主链路: `Client.BotOpenID()` sync.Once 缓存、启动期预热、resolver 真名回填、mentions 真名回填、群聊 enrich 与 `IMContextValue` 注入、Identity config 基础接线、`get_chat_admins(chat_id)` 群管理员按需查询、`/reset` 群管理员 ACL、以及基于 `Region` 的默认 locale 选择。当前唯一剩余尾项是线上验证 `feishu.user_cache.hit/miss` 命中率。

### 改动清单

详见 [`05-identity.md §9`](05-identity.md)。要点:

- `Client.BotOpenID()` sync.Once 同步;删除 longconn 异步拉取;bootstrap 预热
- `user_cache.go` LRU+TTL(12h)+singleflight 包 `client.Contact.User.Get`
- `webhook.go:113` SenderName 留空由 resolver 回填
- resolver 里补 `msg.SenderName` 和 `msg.Mentions[i].Name`
- `router.go:247-249` enrichCtx 扩群聊 + `IMContextValue`
- `feishu_api.get_chat_admins(chat_id)` 复用 `GetChatInfo` 暴露群主/用户管理员/机器人管理员摘要
- `/reset` 群聊命令按群主/管理员判定，`reset_allowlist` 作为 super-admin 兜底
- 多语言字段默认按 `cfg.Region` 选 locale（未显式配置 `name_locale` 时）
- Feishu vs Lark endpoint:SDK 层按 `cfg.Region` 切

### 测试

- 单测:BotOpenID 并发 100 只 1 次 API;singleflight 去重;LRU 淘汰;TTL 过期;真名回填
- 集成:群里用户 A 发消息,session sender 是真名;mentions 真名
- 蓝军:GetUserInfo 返空 → tombstone(5 分钟 TTL);permission_denied → degrade

### 验收

- [ ] `feishu.user_cache.hit` : `miss` > 10:1
- [x] bot open_id 5s 内就绪,webhook/longconn 一致

### 回滚

- `UserCacheSize=0` → 每次打 API
- `EnableGroupEnrich=false` → 群聊不 enrich

---

## Phase 4:M4 投递完整 + 云文档深度

**目标**:图/文件上传下载 + 限流 + 重试 + docx/sheets/bitable/wiki 读写。

### 改动清单

| 文件 | 动作 | 模块 |
|---|---|---|
| `internal/channel/feishu/upload.go` | **新建** `UploadImage`/`UploadFile`(`client.Im.Image.Create` / `client.Im.File.Create`) | M4 |
| `internal/channel/feishu/ratelimit.go` | **新建** global + per-chat token bucket | M4 |
| `internal/channel/feishu/retry.go` | **新建** `withRetry` 指数退避 | M4 |
| `internal/channel/feishu/client.go` | SendImage/SendFile/Delete(bot 撤回);所有核心 API 包 withRetry + Wait | M4 |
| `internal/channel/types.go` | `MsgType` 加 Image/File | M4 |
| `internal/channel/feishu/plugin.go` | Send 分支扩展 Image/File | M4 |
| `internal/tools/feishu_tools.go` | 加 upload_image/upload_file/send_image/send_file + docx 写/sheets 读写/bitable 读写/wiki 读 | M4 + 云文档 |
| `internal/channel/feishu/drive_client.go` | **新建**,大文件走 drive(`client.Drive.File.UploadAll`) | M4 |

### Agent 工具扩展(走 SDK)

| action | SDK | 所属 |
|---|---|---|
| `upload_image` | `larkim.NewCreateImageReqBuilder` | M4 |
| `upload_file` | `larkim.NewCreateFileReqBuilder` | M4 |
| `send_image` / `send_file` | `larkim.NewCreateMessageReqBuilder` + msg_type | M4 |
| `download_message_resource` | `client.Im.MessageResource.Get` | Phase 1 已做 |
| `docx_create` / `docx_update_block` | `client.Docx.Document.Create` / `DocumentBlock.Update` | 云文档 |
| `sheets_read_range` / `sheets_write_range` | `client.Sheets.SpreadsheetSheet.*` | 云文档 |
| `bitable_create_record` / `bitable_list_records` | `client.Bitable.AppTableRecord.*` | 云文档 |
| `wiki_list_nodes` / `wiki_get_node` | `client.Wiki.SpaceNode.*` | 云文档 |

### 测试

- 单测:withRetry 对 99991400/5xx 重试;对 230xx 立即失败;RateLimiter 全局 ≤45/s;SendImage >10MB 预检失败
- 集成:Agent upload_image + send_image → 对端收到;download PDF → OCR tool 分析;Agent 创建 docx 文档并写内容
- 蓝军:5xx 连续命中 → warn + 降级 NotifyError;文件 header 伪造 → SDK 报错立即失败

### 验收

- [ ] 并发 200 条消息:observed QPS ≤ 45,P99 < 2s
- [ ] Agent 真正能收发图/文件
- [ ] Agent 能读写 docx/sheets/bitable/wiki

### 回滚

- `EnableBinaryTransfer=false` → 图文工具消失
- `GlobalQPS=0` 禁限流(不建议)

---

## Phase 5:M7 可观测闭环 + M9 失败重放 + M8 审计完整

**目标**:metric/log/flag/audit 全覆盖;失败重放队列 on;健康端点。

### 改动清单

- `internal/channel/feishu/metrics_names.go` **新建**,集中所有 `feishu.*` metric 常量
- `internal/channel/feishu/reloadable.go` **新建** `Reloadable` 接口
- 所有模块(Resolver/UserCache/HITLBridge/PushService/LifecycleHandler/CommandHandler/ACL)实现 `ReloadFromConfig`
- `internal/channel/feishu/health.go` **新建**,`/health/feishu` 端点
- `internal/channel/feishu/audit.go` **新建**,审计落盘/DB
- `internal/channel/feishu/retry_queue.go` **新建**,失败重放 worker + Postgres
- `internal/channel/feishu/gap_fetch.go` **新建**,longconn 重连后补偿
- `migrations/<ts>_feishu_retry_queue.sql` **新建**
- `internal/bootstrap/server.go:981` 配置 hot reload 触发 Reloadable
- 结构化日志字段统一(`platform`/`chat_id`/`message_id`/`sender`/`event_type`/`phase`/`tenant_key`)

### 测试

- 单测:grep `metrics.Counter("feishu.` 全命中常量;ReloadFromConfig 热切 in-flight 请求正常;retry_queue 5 次耗尽入 dead letter
- 集成:全链路跑一遍,metric endpoint 所有指标齐;热改 `Renderer.Disabled=true` 后续消息走 legacy
- 蓝军:retry_queue 10 万行不 block;gap fetch 5 万条爆群 → 分页

### 验收

- [ ] Grafana 仪表盘(消息数/延迟/错误码/限流/degraded)完整
- [x] `/health/feishu` 返 bot 身份 + token 状态 + degraded 标志
- [x] DB 改 channel_configs 30s 内热生效
- [~] push/scheduled/welcome 失败入 retry_queue,5 分钟后自动重试

> 2026-04-26 实际状态:
> - `welcome` / `push` 失败已入 retry_queue，worker 已接通并可自动重放。
> - `scheduled` 现在已有真实 `CronCreate` / 持久化调度入口 / 启动恢复，基础闭环已落地。
> - 但多副本只跑一次语义、leader gate、完整 metrics/dashboard 仍未完成，因此不能宣称 distributed fully done。

### 回滚

- 按 [`07-observability.md §4`](07-observability.md) 5 级降级逐级退

---

## Phase 6:M6 主动推送

**目标**:开放 HTTP push API + 定时 + 模板。

### 改动清单

详见 [`06-push.md §5`](06-push.md)。要点:

- `PushService`(复用 Router.GetPlugin)
- HTTP API `POST /api/v1/channels/push` + `push:write` scope
- HTTP API `POST/GET/DELETE /api/v1/channels/push/schedules`
- `push_templates.go` text/template
- scheduled 走 master.CronCreate,`scheduled_push:<template>:<k>=<v>` 前缀
- 幂等性 5 分钟 + per-chat 10/min 限流
- `FeishuPushConfig.Enabled=false`(显式 opt-in)
- 与 M8 sanitizer 联动:push content 过 sanitizer,禁 @所有人
- 与 M9 retry_queue 联动:失败自动入队
- `scheduled_pushes` 表持久化 + server 启动恢复 enabled schedule

### 验收

- [x] `curl POST /api/v1/channels/push` → 对端群收到
- [x] scheduled cron 日报按时,且服务重启后自动恢复
- [ ] `feishu.push.rate_limited` 负载测试上报

> 2026-04-26 实际状态:
> - HTTP push API、模板渲染、`scheduled_push:<template>:<k>=<v>` 前缀解析、幂等和 per-chat rate limit 已落地。
> - auth 启用时，已支持 JWT claims `push:write` / `admin` scope 写 push，同时保留 `role=admin` 兼容兜底。
> - `scheduled cron` 的单机持久化闭环已完成，但分布式只跑一次、leader gate、完整观测仍未 fully done。

### 回滚

- `Push.Enabled=false` → API 404

---

## Phase 7:M11 多租户接口 + 国际化 + 可选 AI 增强

**目标**:多租户接口就位(stub 实现)+ Feishu/Lark 切换 + 按需接入 STT/minutes/approval 深度。

### 改动清单

- 所有新建 DB 表已带 `tenant_key` 列(Phase 0-6 已做)
- `ClientRegistry` / `TenantResolver` 已就位(Phase 0 stub)
- Phase 7 新增:`feishu_tenants` 表(真正用时)+ ISV 安装流程
- Region 配置:`FeishuConfig.Region` = "cn" / "intl" → SDK endpoint 切
- 可选:`speech_to_text`(收到语音触发 STT)
- 可选:`approval` 深度(Agent 触发审批 → 监听审批结果)

### 验收

- [ ] 开 `Region=intl` 连 Lark 实例,功能对等
- [ ] 所有 metric 带 `tenant_key_hash` label
- [ ] Session ID 格式 `im-feishu-{tenant_key}-{chat_id}`

---

## 跨 Phase 测试总览

| 类型 | 覆盖 |
|---|---|
| **单测** | imctx、parser、resolver、bridge、cache、ratelimit、retry、template、sanitizer、dedup、acl、rollout、gap_fetch |
| **集成(真实租户)** | 引用识别 / HITL / 生命周期 / 图文 / 真名 / push / 命令 / 灰度 / 审计全链路 |
| **并发/性能** | ratelimit 50/s / QPS 突刺 / 多副本 dedup / push 洪流 / retry queue 10 万行 |
| **蓝军** | 畸形 payload / bot 自反射 / CDATA 嵌套 / @所有人 / token 盗用 / 超大文件 / 签名伪造 / permission_denied |
| **热重载** | 每个 flag true → false → true 切换,无 panic / 无泄漏 / in-flight 正常完成 |
| **包依赖** | `go list -deps ./internal/imctx/...` 无反向 |
| **SDK-only** | diff gate 无 OpenAPI 直调 |
| **PII gate** | log 里无 sender_name/email/phone |

## 全局回滚清单(按影响面)

1. Phase 5 metric/log 代码可 revert
2. Phase 7 Region 切回 cn / 关 STT
3. Phase 6 `Push.Enabled=false`
4. Phase 4 `EnableBinaryTransfer=false`
5. Phase 3 `UserCacheSize=0` / `EnableGroupEnrich=false`
6. Phase 2 `Lifecycle.Enabled=false` / `Rollout.Mode=all`
7. Phase 1 `Inbound.EnableContextResolver=false`
8. Phase 0 `DistributedDedupEnabled=false` / `EventEncryptEnabled=false` / `SetInteractionHandler(nil)`
9. 全线下:`Feishu.Enabled=false`

**所有回滚 100% 通过 `channel_configs` 表 DB 改配 + hot reload 完成,不改代码、不重启**。

## 全局验收标准

### 功能

- [ ] 用户 @bot "这个文档说什么" + 贴 5 种云文档链接 → Agent 读到真实内容
- [ ] HITL 按钮点击 → agent resume(修当前死链)
- [ ] bot 被拉进群 → welcome 卡片;被踢 → session 清零
- [ ] Agent upload_image + send_image → 对端收到图
- [ ] 用户发 PDF → Agent 自动 download_message_resource + OCR
- [ ] 消息里 sender 显示真名(非 open_id)
- [ ] `POST /api/v1/channels/push` 成功
- [ ] scheduled 日报按时送达
- [ ] `/reset` / `/debug` / `/mute` 命令可用 + ACL 生效
- [ ] 一群绑 agent profile,不同群跑不同 agent

### 系统

- [ ] Grafana 能看所有 `feishu.*` metric
- [ ] DB 改 config 任一 flag 30s 热生效
- [ ] 3 副本部署同一事件不重复处理
- [ ] 加密 event 正常解析
- [ ] 日志 grep 不到 PII
- [ ] Bot 发送内容通过 sanitizer(无 @所有人)
- [ ] Degraded 状态通过 `/health/feishu` 可诊断
- [ ] 失败消息进 retry_queue 5 分钟内重试

### 工程

- [ ] CI: imctx 叶子 / SDK-only / PII / 11 模块单测 / 集成 全绿
- [ ] 所有新代码覆盖率 > 70%

## 代码锚点速查(全)

### 叶子包 imctx

| 位置 | Phase | 作用 |
|---|---|---|
| `internal/imctx/context.go` | 0 | `IMMessageContext` 等所有共享类型 |
| `internal/imctx/doc_ref.go` | 0 | URL 正则 + `ExtractDocRefs` |

### channel 层

| 位置 | Phase | 作用 |
|---|---|---|
| `internal/channel/types.go` | 0-4 | InboundMessage/OutboundMessage/MsgType/IMMessageProcessor/接口声明 |
| `internal/channel/router.go` | 0-3,5 | Resolver/InteractionHandler/LifecycleHandler/TenantResolver/ClientRegistry setter + dedup + enrichCtx + cmd 分支 |
| `internal/channel/feishu/webhook.go` | 0 | 重写为 larkcallback dispatcher(含 encrypt/signature) |
| `internal/channel/feishu/longconn.go` | 0-3 | dispatcher 复用 + card action + lifecycle + 断线 gap + 删除 async botOpenID |
| `internal/channel/feishu/parser.go` | 1 | **新建** 全形态解析 |
| `internal/channel/feishu/resolver.go` | 1,3 | **新建** ContextResolver + 真名回填 |
| `internal/channel/feishu/hitl_bridge.go` | 0 | **新建** |
| `internal/channel/feishu/lifecycle_handler.go` | 2 | **新建** |
| `internal/channel/feishu/commands.go` | 2 | **新建** |
| `internal/channel/feishu/command_handler.go` | 2 | **新建** |
| `internal/channel/feishu/acl.go` | 2 | **新建** |
| `internal/channel/feishu/rollout.go` | 2 | **新建** |
| `internal/channel/feishu/chat_binding_repo.go` | 2 | **新建** |
| `internal/channel/feishu/user_cache.go` | 3 | **新建** LRU+TTL+singleflight |
| `internal/channel/feishu/upload.go` | 4 | **新建** |
| `internal/channel/feishu/download.go` | 1 | **新建** |
| `internal/channel/feishu/drive_client.go` | 4 | **新建** 大文件 drive 上传 |
| `internal/channel/feishu/ratelimit.go` | 4 | **新建** |
| `internal/channel/feishu/retry.go` | 4 | **新建** |
| `internal/channel/feishu/dedup.go` | 0 | **新建** 分布式 |
| `internal/channel/feishu/retry_queue.go` | 5 | **新建** 失败重放 |
| `internal/channel/feishu/gap_fetch.go` | 5 | **新建** longconn 补偿 |
| `internal/channel/feishu/sanitizer.go` | 0 | **新建** 内容消毒 |
| `internal/channel/feishu/piilog.go` | 0 | **新建** PII 脱敏工具 |
| `internal/channel/feishu/health.go` | 5 | **新建** 健康端点 + `HealthStatus` |
| `internal/channel/feishu/audit.go` | 5 | **新建** 审计 |
| `internal/channel/feishu/metrics_names.go` | 5 | **新建** metric 常量 |
| `internal/channel/feishu/reloadable.go` | 5 | **新建** Reloadable 接口 |
| `internal/channel/feishu/tenant.go` | 0 | **新建** TenantResolver stub |
| `internal/channel/feishu/client_registry.go` | 0 | **新建** ClientRegistry stub |
| `internal/channel/feishu/client.go` | 各 Phase | 加 SDK 方法 + ReloadFromConfig + sync.Once |
| `internal/channel/feishu/card_builder.go` | 2 | 加 Welcome 卡片 |
| `internal/channel/feishu/plugin.go` | 1,4 | Send 分支扩展 + HandleInbound 调 parser + sanitizer |

### master 侧

| 位置 | Phase | 作用 |
|---|---|---|
| `internal/master/session.go` | 1 | SessionState 加 pendingIMContext + SetPendingData/Consume |
| `internal/master/session_message.go` | 1 | ProcessMessageFromIM 签名加 imCtx |
| `internal/master/session_loop.go:229` | 1 | sem gate 后写 pending |
| `internal/master/react_processor.go` | 1 | plugin hook 后 consume prefix |
| `internal/master/public_api.go` | 0,2 | HITL SubmitInput / EndSession 导出 |
| `internal/master/master.go` + CronCreate | 6 | PushDispatcher |

### 新模块

| 位置 | Phase | 作用 |
|---|---|---|
| `internal/channel/push/service.go` | 6 | **新建** |
| `internal/channel/push/templates.go` | 6 | **新建** |
| `internal/api/push_handler.go` | 6 | **新建** |
| `internal/api/handlers.go` | 2,5,6 | 注册 mute API / health 端点 / push 路由 |
| `internal/tools/feishu_tools.go` | 1,4 | 加 12+ 个 action(全走 SDK) |
| `internal/observability/metrics.go` | 5 | 注册 `feishu.*` |
| `internal/config/config.go:881` | 0-7 | `FeishuSecurityConfig` / `ReliabilityConfig` / `InboundConfig` / `LifecycleConfig` / `OutboundConfig` / `IdentityConfig` / `PushConfig` / `SessionGovernanceConfig` / `RolloutConfig` |
| `migrations/<ts>_feishu_dedup.sql` | 0 | dedup 表 |
| `migrations/<ts>_feishu_chat_binding.sql` | 2 | chat binding 表 |
| `migrations/<ts>_feishu_retry_queue.sql` | 5 | retry queue 表 |
| `migrations/<ts>_feishu_tenants.sql` | 7 | tenant 表(预留) |
