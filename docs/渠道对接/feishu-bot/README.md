# 飞书机器人完整实现方案

> Scope:从零构建**生产级飞书机器人**的全局方案。不是"补 bug",是 ground-up 盘点 + 分模块落地。
> Status:规划 freeze,实施分 Phase。
> 架构前置阅读:[`../feishu.md`](../feishu.md)(当前 channel 层 EventRenderer / PatchCard 流程)

## 铁律(所有模块必须遵守)

1. **SDK-only**:所有飞书服务端交互走 `github.com/larksuite/oapi-sdk-go/v3`(v3.5.3)。禁止 `net/http` 直调 `open.feishu.cn`/`open.larksuite.com`,禁止第三方 wrapper。详见 [`00-feature-matrix.md §0.1`](00-feature-matrix.md)
2. **包依赖叶子化**:`internal/imctx` 仅 stdlib,CI gate `go list -deps` 卡反向依赖
3. **所有能力可热开关 + 可观测**:每能力配 config flag + metric + 结构化日志,不允许"硬开启"

## 文档地图(11 模块)

| 文档 | 模块 | 核心 |
|---|---|---|
| [`00-feature-matrix.md`](00-feature-matrix.md) | **M0 能力盘点** | SDK 57 module 分档 × 非 SDK 维度(安全/可靠/会话/租户/国际化) × "不做"清单 |
| [`01-inbound-parse.md`](01-inbound-parse.md) | M1 消息摄取 | 全 message_type 解析、引用识别、父消息、prompt prefix |
| [`02-interaction-callback.md`](02-interaction-callback.md) | M2 交互回调 | **HITL 死链修复**、卡片按钮/表单、表情/已读/撤回 |
| [`03-lifecycle.md`](03-lifecycle.md) | M3 生命周期 | bot 入群/被踢/P2P 首招呼 + session 清理 |
| [`04-outbound.md`](04-outbound.md) | M4 消息投递 | 图/文件 upload/download、ratelimit、retry |
| [`05-identity.md`](05-identity.md) | M5 身份 | bot open_id 同步、UserCache、真名回填、多语言 |
| [`06-push.md`](06-push.md) | M6 主动推送 | HTTP API、模板、scheduled |
| [`07-observability.md`](07-observability.md) | M7 可观测 | Metrics、feature flags、降级、热重载、健康端点 |
| [`08-security.md`](08-security.md) | **M8 安全合规** | encrypt_key 解密、signature 校验、PII 脱敏、sanitizer(禁 @所有人)、审计 |
| [`09-reliability.md`](09-reliability.md) | **M9 可靠性** | 分布式去重、longconn gap fetch、失败重放队列 |
| [`10-session-governance.md`](10-session-governance.md) | **M10 会话治理** | `/reset`/`/debug`/`/status`/`/mute`、ACL、灰度、一群绑多 agent |
| [`11-multi-tenant.md`](11-multi-tenant.md) | **M11 多租户** | ISV 预留接口(一期 stub,不实现) |
| [`ROADMAP.md`](ROADMAP.md) | 实施 | 分 Phase、测试、回滚、验收、锚点速查 |

## 当前验收口径(给其他 Agent)

> 截至 2026-04-27，当前仓库对飞书通道的**现实目标**不是“11 模块全部做满”，而是：
> **单租户 + webhook-only + 内部直接可用的飞书 MVP**。
>
> 其他 Agent 做验收时，**不要**再按“12 份规划文档全部完工”作为通过标准；应按“核心功能可用 + 明确延期项不阻塞上线”的口径判断。

### 本轮上线验收应看什么

- 能收消息：私聊、群聊、@机器人、基础消息类型
- 能回消息：文本、卡片；图片/文件能力已实现，但只在业务需要时作为强验收项
- 能处理卡片回调：`card.action.trigger` / HITL 按钮可用
- 有基础治理：`/help`、`/status`、`/reset`、`/mute`、`/unmute`、`/model`
- 有基础安全：验签、加密开关、时间窗校验、`@all` 防护
- 有基础可靠性：fail-closed dedup、retry queue、权限降级 fast-fail

### 当前明确不作为上线阻塞项

- `/debug`
- `/agent`
- 多租户 / ISV 安装流
- “很重”的运维平台化能力
- dead-letter 人工 replay 平台
- 完整 observability / 告警闭环
- longconn 作为首发生产入口
- 一群绑多 agent
- 群名/成员变更感知的完整治理闭环

### 验收结论应该怎么写

- 可以写：“当前版本满足单租户 webhook-only 的飞书 MVP 上线口径”
- 不应写：“11 个模块已全部完成”
- 不应写：“多租户、`/debug`、`/agent`、完整运维面已完成”

## 愿景:一个"完整"的飞书机器人

从用户角度能做到的事:

1. **听得懂** — text / post 富文本 / 图片 / 文件 / 语音 / 合并转发 / 卡片 / share_chat / share_user / 系统消息(M1)
2. **看得到** — 用户引用的 docx / sheet / bitable / wiki,bot 能识别并主动读取(M1 + 云文档深度)
3. **点得通** — 审批按钮、卡片表单、HITL 按钮,点完即生效(M2,修当前死链)
4. **活得像人** — bot 入群招呼、被踢清后台、P2P 首建自报家门(M3)
5. **发得出** — text/md/卡片/图/文件、长消息分片、失败降级 + 指数退避(M4)
6. **认得人** — 说"小明",不再是一串 open_id;多语言真名;群管识别(M5)
7. **主动推** — 定时播报、长任务完成通知、外部 webhook → 飞书(M6)
8. **看得见** — SRE 能从 metric 看每环节延迟/成功率/降级比例,健康端点一秒诊断(M7)
9. **安全** — 消息加密、签名校验、PII 脱敏、禁 @所有人、审计可追溯(M8)
10. **可靠** — 多副本部署不重复、断线补偿、失败重放(M9)
11. **可治** — 群管能 `/reset`/`/mute`、一群绑专属 agent、灰度可控(M10)
12. **可扩** — 未来多租户 / ISV 场景零 Router 改动(M11 预留)

## 现状总览

> 注意：下表是**完整长期方案**视角下的完成度，不等于当前 MVP 上线阻塞判断。
> 当前真实验收请以上文“当前验收口径(给其他 Agent)”为准。

按模块的"生产就绪度"评分(🔴 严重缺失 / 🟡 部分实现 / 🟢 完整):

| 模块 | 就绪度 | 最严重问题 |
|---|---|---|
| M1 消息摄取 | 🔴 | 引用识别完全缺失、消息形态只认 4 种(post/audio/video/share/merge_forward 都当占位符) |
| M2 交互回调 | 🔴 | **P0 死链**:`card.action.trigger` 未注册,HITL 按钮按了无反应 |
| M3 生命周期 | 🔴 | 5 个 handler 全空,bot_added/removed 未注册,session 不清理 |
| M4 消息投递 | 🟡 | 图/文件 upload/download、ratelimit、retry 已落地;仍缺更完整 metric 与文档收口 |
| M5 身份 | 🔴 | SenderName 直接等于 open_id,bot_openid 异步可丢 |
| M6 主动推送 | 🔴 | 零能力 |
| M7 可观测 | 🟡 | 已有部分 feishu-specific metric 与 `/health/feishu`; lifecycle/HITL/API 错误码面仍不完整 |
| **M8 安全** | 🟡 | encrypt/signature/strict-encrypt/sanitizer/审计与 permission-degrade 已有落地,但跨副本 health / 更完整审计 / key 轮转窗口仍未完成 |
| **M9 可靠性** | 🟡 | 两阶段 claim、webhook fail-closed、retry_queue、longconn watchdog/gap-fetch 已有主体实现;运维工具与剩余观测仍待补齐 |
| **M10 会话治理** | 🟡 | MVP 所需命令与 ACL 已基本具备;`/debug`、`/agent`、更重治理面明确延期 |
| **M11 多租户** | 🟡 | 当前只保留前向兼容 stub,**不属于本轮 MVP 上线阻塞项** |

**关键判断**:原稿 M8/M9 含**基础事实错误**(凭空 SDK 符号),不是"完成度低"而是"不可直接落地"。经 plan-ceo-review × codex 双向辩论(辩论 + 文档落地后 codex 二次复核)后,**Phase 0 从原 5 项扩到 14 项**(不是红队提议的 23 项,也不是初稿的 12 项 —— codex 复核时补了 `FailOpenOnDedupError` 配置删除 + session_id 全文一致性两项),并决议 **webhook-only 首发**(longconn 延后 Phase 2)。完整决议见 [reviews/README.md § CEO 决议 (2026-04-22)](reviews/README.md#ceo-决议-2026-04-22)。

## 顶层架构

```
                      ┌─────── 飞书 Open Platform ───────┐
                      │                                   │
               HTTPS Webhook                  WebSocket 长连(开发/私有化)
                      │                                   │
                      ▼                                   ▼
         larkevent.Dispatcher (M8 签名/解密)  ← 统一 event 路由(M2 §4.2)
                      │
                      ▼
         M9 分布式 dedup (ClaimEvent @ Postgres)
                      │
        ┌─────────────┼─────────────────┬────────────────┐
        ▼             ▼                 ▼                ▼
   im.message.* → M1 ContextResolver    im.chat.member.* → M3 LifecycleHandler
        │                               card.action.trigger → M2 HITLBridge
        ▼
  channel.Router (M10 rollout/mute 检查 → cmd 分支 → agent 分支)
        │
        ▼
  master.ProcessMessageFromIM(带 imctx.IMMessageContext)
        │
  session_loop(sem 栅栏) → react_processor(plugin hook → prefix consume)
        │
        ▼
  agent 输出 → M8 sanitizer → M4 ratelimit + withRetry → EventRenderer
        │
        ▼                                            ┌─── M7 metric/log
  text / markdown / card / image / file              │
        │                                            └─── M8 audit log
        ▼
   客户端收到

  旁路:M6 PushService(HTTP API)→ 复用 M4 下游
        M9 失败重放 worker → 扫 Postgres 重试
```

## 包结构与依赖约束

### `internal/imctx` 叶子包

定义 `channel`、`master`、`channel/feishu` 三方共享类型,**只用 stdlib**。

核心类型(详见各模块):
- `imctx.DocRef` / `imctx.ReferenceType` / `NormalizeDocType`
- `imctx.Mention`
- `imctx.IMMessageContext`
- `imctx.CardAction`(M2)
- `imctx.LifecyclePayload`(M3)

### 依赖图

```
              ┌─────────────┐
              │  internal/  │
              │   imctx     │  stdlib only
              └──────┬──────┘
                     │ imported by
       ┌─────────────┼─────────────────┐
       ▼             ▼                 ▼
  channel/*    master/*          channel/feishu/*
       │                               │
       └───→ channel → master(既有)   │
                                       │
                                  channel/feishu → channel(既有)
                                       │
                                       └──→ imctx(新增)
```

现有方向不变,新代码沿既有方向扩展,**禁止反向**。CI gate:

```bash
go list -deps ./internal/imctx/... | grep 'chef-guo/agents-hive/internal/' && exit 1 || true
```

## 目标 / 非目标 / 显式不做

### 目标

- 11 模块生产可用,每个都有 flag + metric + 降级路径
- 破坏性变更在单一 Phase 完成并带测试
- 任何 API enrichment 在 dedup/debounce 下游
- 零包依赖环

### 非目标(一期)

- 图片 OCR / 多模态(Agent 自己的 vision 工具覆盖)
- 消息编辑跟踪(飞书 API 对 bot 开放有限)
- ISV 安装流程(M11 §5 预留,一期不实现)
- **webhook + longconn 同进程并存**(红队链 B:双入口 × fail-open 会双投 → CEO 2026-04-22 决议:生产运行时 `Ingress=webhook XOR longconn`,Phase 0 固定 webhook,Phase 2 才开放 longconn 分支)
- `/debug` 与 `/agent` 首发开放
- 多租户真实路由与 tenant config overlay
- dead-letter 人工 replay 平台
- 完整 observability / 告警 / 运维面

### 显式不做(写进来防止"再来一轮重构")

完整清单见 [`00-feature-matrix.md §3.4`](00-feature-matrix.md)。高频被误以为要做的:

| 项 | 理由 |
|---|---|
| OpenAPI 直调 | 铁律 §1 |
| 第三方 Feishu wrapper | 铁律 §1 |
| 绕过 SDK 自建 token 管理 | SDK 已管理,绕过造成分叉 |
| `feishu.aily` / `document_ai` / `translation` | 与 Agent 自身能力重复 |
| `hire`/`corehr`/`ehr`/`okr`/`payroll` 等 HR 域 | 场景不直接 |
| `mail` | 一期不接,留独立 MailChannel 通道 |
| DocCache / Preload | 真实流量未证明热点,降低 tool_call 透明度 |
| 整个 Plugin 重建式热更 | 会丢 session/dedup/cache 状态 |

## 实施路径

详见 [`ROADMAP.md`](ROADMAP.md)。**11 模块,分 8 个 Phase**,每 Phase 独立可 ship。

优先级(按"不做就挂" → "体验完整化"):

- **Phase 0** 🔥 **P0 基座 + 安全 + 可靠性(webhook-only 首发)**:imctx 叶子包、HITL 死链、webhook encrypt/signature/URL 层 401/replay 窗口、**两阶段 claim + fail-closed dedup + handler 永返 nil**、session_id 多租户前向格式、@all sanitizer 递归、PII grep gate + SafeSenderID 强路径、删除 FailOpenOnDedupError 配置。共 14 项,见 [ROADMAP.md Phase 0](ROADMAP.md#phase-0--p0-基座--安全--可靠性webhook-only-首发)
- **Phase 1** 消息摄取 + 资源下载(M1 + M4 部分)
- **Phase 2** 生命周期 + 会话治理(M3 + M10)
- **Phase 3** 身份真名(M5)
- **Phase 4** 投递完整(M4 剩余 + 云文档深度)
- **Phase 5** 可观测闭环(M7)
- **Phase 6** 主动推送(M6)
- **Phase 7** 多租户预留 + 国际化 + 可选 AI 增强(M11 + 剩余可选)

Phase 0 是"**必须先做**"——否则生产上线就是 P0 事故面。其他 Phase 可按业务优先级调序。

## 关键代码锚点(全局)

完整表见 [`ROADMAP.md §代码锚点速查`](ROADMAP.md)。最高频前排:

| 位置 | 作用 |
|---|---|
| `internal/channel/types.go:8` | `channel → master` 既有方向,imctx 设计的物理约束 |
| `internal/channel/types.go:53-55` | `IMMessageProcessor.ProcessMessageFromIM` 扩参点 |
| `internal/channel/router.go:203,208,216,333` | HandleMessage / dedup / debounce / dispatchProcess |
| `internal/channel/feishu/webhook.go` | 重写为 larkcallback dispatcher(M8) |
| `internal/channel/feishu/longconn.go:69-102` | 所有 event handler 注册点 |
| `internal/channel/feishu/client.go:41-891` | API 能力边界(待补 upload/download) |
| `internal/master/session_loop.go:215,229` | sem 栅栏 + pending 写入(race-free) |
| `internal/master/react_processor.go:82-98` | plugin hook → prefix consume 时序 |
| `internal/bootstrap/server.go:377,981` | 实际接线点(不是 cmd/agents/main.go) |
