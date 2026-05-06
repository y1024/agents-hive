# 飞书改造完成后的 Spec 重审

> **触发**：用户告知飞书改造完成
> **发现**：Hive channel 架构已实现大量我 spec 重新发明的能力
> **影响**：W4 / W7 / W8 / F3 / F8 部分需要撤销或大幅简化
> **日期**：2026-04-27

---

## §1 飞书改造后 Hive 实际架构（远超我 spec 假设）

### 1.1 channel 包已实现完整接口体系

`internal/channel/plugin.go` 已存在：

```go
// 必需基础（所有 channel 实现）
type ChannelPlugin interface {
    Platform() Platform
    Send(ctx, msg OutboundMessage) error
    WebhookHandler() http.HandlerFunc
    Verify(r *http.Request) bool
}

// 可选扩展：流式渲染能力
type EventRenderer interface {
    ChannelPlugin
    RenderEventStream(ctx, scope SessionScope, eventCh <-chan master.BroadcastMessage) error
}

// 可选扩展：入站控制
type InboundController interface {
    ControlInbound(ctx, msg, currentSessionID) (InboundControlResult, error)
}

// 错误包装：兜底路径
type RendererError struct { Inner error; LastContent string }
func WrapRendererErr(inner error, lastContent string) error
```

**这就是我 W4 spec 设计的 ChannelAdapter interface 的现成版本**，且更精炼（一个 RenderEventStream 方法承担订阅 + 渲染 + 增量 patch + 错误兜底）。

### 1.2 master.EventBus 完整事件流系统

`internal/master/event_bus.go:90` `type EventBus struct` 已存在：
- 全局订阅 + per-session 过滤
- BroadcastMessage 类型（master.go:77）
- Subscribe / Unsubscribe / Publish 完整路径

**这就是我 spec 中"streaming.EventBus"的现成版本**。

### 1.3 飞书已实现完整 EventRenderer

`internal/channel/feishu/renderer.go` **763 行 + 50+ 文件 / 17,904 行总规模**：

| 已实现能力 | 文件 |
|---|---|
| EventRenderer 完整实现（订阅 → 派发 → 渲染 → flush） | renderer.go:79 `feishuRenderer` |
| 卡片增量 PATCH + 重试 | renderer.go:535 `patchWithRetry` |
| 心跳触发 PATCH | renderer.go:211/229 `shouldHeartbeat / scheduleHeartbeat` |
| 卡片 state 管理 | rendererCardState struct |
| 文本 fallback（兜底） | renderer.go:577 `buildTextFallback` |
| Final flush | renderer.go:558 |
| **dedup**（PostgresEventClaimer fail-closed 200ms） | dedup.go |
| **gap_fetch**（断线重连恢复缺口事件） | gap_fetch.go + gap_fetch_runner.go |
| **reconnect_watchdog**（重连监控）| reconnect_watchdog.go |
| **reliability_leader_gate**（多副本 leader 选举）| reliability_leader_gate.go |
| **governance**（rollout / ACL / audit / mute / debug） | governance.go |
| **ratelimit** | ratelimit.go |
| **retry_queue** | retry_queue.go |
| **webhook_retry** | webhook_retry.go |
| **lifecycle_handler** | lifecycle_handler.go |
| **mention_sanitizer** | mention_sanitizer.go |
| **acl** + **audit** | acl.go / audit.go |
| **client_registry**（多 client 管理）| client_registry.go |
| **dispatcher_factory** | dispatcher_factory.go |
| **prompt_prefix** | prompt_prefix.go |

### 1.4 push/ 主动推送服务（新目录）

`internal/channel/push/` 含 service.go + templates.go — **新增主动推送能力**（与现有响应式 webhook 不同）。

---

## §2 我 spec 中重复发明的部分（必须撤销）

### 撤销清单 1：W4 ChannelAdapter 重新发明

我 spec `SPEC-LAYER1-W4.md §2.3` 设计的 `ChannelAdapter` interface（Name / Subscribe / Unsubscribe / Render / Health）+ Patcher / Acker capability interface（F3 修复后）— **这些 Hive 已经有了**：
- `ChannelPlugin` + `EventRenderer` 已覆盖
- 不需要 Subscribe / Render 分离 — RenderEventStream 一个方法搞定
- 不需要 Patcher / Acker 拆分 — RendererError + LastContent 已是兜底机制

**修订**：W4 不再新建 ChannelAdapter interface，而是**扩展现有 EventRenderer**：
- 新增 todos 事件类型（master.BroadcastMessage 添加新 EventType）
- 飞书 renderer dispatchEvent 加 case handle todos
- W4 工期：2 周 → **1 周**（仅工具结构化 + 关注点分离 + 加 todos 事件类型）

### 撤销清单 2：W8 飞书 adapter 包装层

我 spec `SPEC-LAYER2 §4` 设计的 `internal/channels/feishu/adapter.go` 包装层 + `FeishuRenderer interface` 解耦 — **完全不需要**：
- 飞书已是 `EventRenderer` 实现
- 接现有 `feishuRenderer.dispatchEvent()` 加 `handleTodoEvent()` case 即可

**修订**：W8 工期 0.5 周 → **0.2 周**（仅在 dispatchEvent switch 加 todos case + 渲染逻辑），且**不阻塞**（飞书代码已稳定，可立即做）

### 撤销清单 3：F3 Capability interface 拆分

我 ARCH-REVIEW F3 修复（拆 Patcher / Acker capability interface）— **现有 EventRenderer 不需要拆**：
- RenderEventStream 一个方法已涵盖 patch + ack 语义
- RendererError + LastContent 是兜底约定

**修订**：F3 撤销

### 撤销清单 4：F8 重连协议部分

我 spec F8 设计的"client 启动 GET snapshot → 订阅 ?since_plan_version=N"重连协议 — **飞书 gap_fetch + reconnect_watchdog 已实现等价机制**：
- gap_fetch：断线后从 IM 平台拉取缺口事件
- reconnect_watchdog：监控连接健康 + 触发恢复

**修订**：F8 简化为**仅新增 TodoEvent + Version 字段**到 BroadcastMessage，不重做整个 EventBus / 重连协议（复用现有）

---

## §3 现有代码已覆盖的修复点（F1-F16 重新评估）

| F# | 我的修复 | Hive 现状 | 状态 |
|---|---|---|---|
| F1 | W2 timeout RunWithToolTimeout | 与 channel 无关 | ✅ 保留 |
| F2 | W3 Permission Release sync.Once | 飞书 governance ≠ W3 全局 capacity（不同概念）| ✅ 保留 |
| F3 | ChannelAdapter 拆 Patcher/Acker | EventRenderer 已是单方法接口 | ❌ **撤销** |
| F4 | T1.3 验收 session_id 不进 metric | observability 已存在 PG-backed | ✅ 保留 |
| F5 | path-rules 结构化 schema | 与飞书无关 | ✅ 保留 |
| F6 | chrome-mcp SSRF 防御 | 与飞书无关 | ✅ 保留 |
| F7 | W12 单向 export | 与飞书无关 | ✅ 保留 |
| F8 | TodoEvent Version + 重连协议 | 飞书 gap_fetch / reconnect_watchdog 已有 | 🟡 **简化**（只加 TodoEvent + Version 字段）|
| F9 | approve scope 多维绑定 | 飞书 acl.go / audit.go 部分相关但偏 IM 治理 | ✅ 保留（独立 W6 系统）|
| F10 | Bash AST 解析 | 与飞书无关 | ✅ 保留 |
| F11 | Memory append-only journal | 与飞书无关，但 PostgresEventClaimer 模式可参考 | ✅ 保留 |
| F12 | Embedding 索引绑模型 ID | 与飞书无关 | ✅ 保留 |
| F13 | Skills Loader sync.RWMutex | 与飞书无关 | ✅ 保留 |
| F14 | Task version + lease | 与飞书无关 | ✅ 保留 |
| F15 | SteeringInjector queue | 与飞书无关 | ✅ 保留 |
| F16 | W15 拆两阶段 | 与飞书无关 | ✅ 保留 |

**结论**：16 项中 1 项撤销（F3）+ 1 项简化（F8），其余 14 项保留。

---

## §4 IMPLEMENTATION-PLAN 工期与依赖重估

### 4.1 节省工期

| 工作流 | 原工期 | 新工期 | 节省 | 原因 |
|---|---|---|---|---|
| W4 | 2 周 | **1 周** | 1 周 | 撤销 ChannelAdapter 新发明，扩展现有 EventRenderer |
| W7 | 2 周 | **1.5 周** | 0.5 周 | Web 实现现有 EventRenderer 即可（少抽象层）|
| W8 | 0.5 周 | **0.2 周** | 0.3 周 | 飞书 renderer 加 todos case 即可 |
| **小计** | 4.5 周 | **2.7 周** | **~2 周** |

### 4.2 阻塞解除

| 项 | 原状态 | 新状态 |
|---|---|---|
| W8 飞书 adapter 等飞书施工 | ✅ 阻塞 | ❌ **解除阻塞**（飞书已完成）|
| W12 依赖 W8 | （已修正为仅依赖 W7）| 不变 |

### 4.3 时间轴更新

```
原：月 1 → 月 3 (Layer 0+1+2 不含飞书) → 月 6 (含 W8) → ...
新：月 1 → 月 2.5 (Layer 0+1+2 全部，含 W8) → 月 5.5 (Layer 3) → ...
```

**整体节省 ~2 周**（L1+L2 ship 提前到月 2.5）。

---

## §5 飞书改造带来的新借鉴点（Hive 自有的好设计）

飞书改造中引入的几个模式可作为后续工作流的参考：

### 5.1 dedup PostgresEventClaimer fail-closed 200ms 模式
- 适用于：F11 Memory journal 写入冲突 / F14 Task lease 申请
- 参考飞书 dedup.go 的 fail-closed 设计

### 5.2 gap_fetch 断线恢复模式
- 适用于：W7 Web Console 重连后的 todos snapshot 拉取
- 参考飞书 gap_fetch.go

### 5.3 reliability_leader_gate 多副本 leader 选举
- 适用于：W9 nightly distill（多实例部署时只有 leader 跑）/ W3 admission control
- 参考 reliability_leader_gate.go

### 5.4 governance 治理体系（rollout / ACL / audit / mute / debug）
- 适用于：W6 path-scoped rules / F9 approve scope（部分概念可借鉴）
- 参考 governance.go

### 5.5 RendererError + LastContent 兜底模式
- 适用于：所有 W4-W8 channel 错误路径
- 参考 plugin.go RendererError

---

## §6 待修订的 spec / plan 文件

| 文件 | 修订内容 | 工期 |
|---|---|---|
| `SPEC-LAYER1-W4.md` | §2.3 撤销 ChannelAdapter，改为"扩展现有 EventRenderer + 新增 TodoEvent BroadcastMessage 类型"；§2.1 工期 1 周 | 30 分钟 |
| `SPEC-LAYER2-W5-W6-W7-W8.md` | §3 W7 改为"实现现有 EventRenderer interface"；§4 W8 大幅缩减为"飞书 dispatchEvent 加 todos case"，工期 0.2 周 | 30 分钟 |
| `IMPLEMENTATION-PLAN.md` | §1 时间轴更新（L1+L2 ship 提前到月 2.5）；§2 W4/W7/W8 工期更新；§3 W8 阻塞解除 | 15 分钟 |
| `DEPENDENCY-ORDER.md` | §1 DAG L1 节加注"扩展现有 EventRenderer 而非新建" | 10 分钟 |
| `CROSS-REVIEW-SYNTHESIS.md` | §4 F3 标撤销；§4 F8 标简化 | 10 分钟 |
| **新增**：`HIVE-EXISTING-CAPABILITIES.md` | 文档化飞书改造后已有的 capability 清单（防止下次重复发明）| 30 分钟 |

**总修订工期**：~2 小时

---

## §7 战略层观察

### 7.1 主线程 + codex review 都漏掉的事
本次重审揭示**第 3 个独立视角**（Hive 现有代码）—— 单 AI 思维链 + 双 AI 辩论都不够，**还要主动核实"目标系统当前状态"**。

我和 codex 都在审视 spec 设计，但都没有读 Hive 内部 channel 代码（17,904 行飞书代码全没看），所以同时漏掉了"重新发明现有 interface"这个根本问题。

**教训**：spec 设计前必须先扫现有代码，避免 NIH（Not Invented Here）syndrome。

### 7.2 飞书改造的工程质量
飞书 50+ 文件 / 17,904 行 / 含 dedup / gap_fetch / reconnect_watchdog / reliability_leader_gate / governance / ratelimit / retry_queue / acl / audit 完整体系 —— **这个工程质量已经接近 Claude Code BashTool 的标杆**（12,411 行 / 18 文件）。

**意味着**：Hive 在 IM channel 工程深度上**已经是顶级**（不仅仅是国内 IM 数量），这是 FINAL-REPORT §6.1 应该承认的真实领先点（之前我的判断"Hive 仅在数量领先"是错的）。

### 7.3 Hive 真正领先的轴（重新评估）

| 维度 | 状态 |
|---|---|
| 国内 IM 数量覆盖 | ✅ 仍领先 |
| **飞书 channel 工程深度**（17K 行 + dedup + gap_fetch + leader_gate + governance）| ✅ **新增领先**（与 Claude Code BashTool 工程深度同档）|
| Spec-driven ReAct | ⚠️ 仍是赌注（待 W12 dual-flag 验证）|
| SafeExecutor 代码层强制 | ⚠️ Phase 1 已上线，Phase 2 + W5 后才完整 |
| Go 单二进制部署 | ⚠️ 工程取舍非"先进" |

**Hive 真正领先**：从 1 条增加到 **2 条**（IM 数量 + 飞书工程深度）。

---

## §8 决策

| 选项 | |
|---|---|
| **A** | 立即按 §6 修订 5 份文件（~2 小时）+ 把"飞书工程深度"加入 FINAL-REPORT 领先轴 |
| **B** | 仅修 W4/W7/W8 spec（核心修订，~1 小时）|
| **C** | 不动文件，仅在心里调整（不推荐 — IMPLEMENTATION-PLAN 工期不准）|

我推荐 **A**：spec 必须反映现实，否则后续施工会按错误前提走。

---

## §9 文件索引

```
docs/research/
├── RE-REVIEW-POST-FEISHU.md     # ⭐ 本文件
├── ARCH-REVIEW.md                # 主线程一轮 review
├── CROSS-REVIEW-SYNTHESIS.md     # 主线程 × codex 综合
├── CROSS-REVIEW-CODEX-RAW.md     # codex 原文
└── （5 份 spec + IMPLEMENTATION-PLAN + DEPENDENCY-ORDER）
```

---

*— End of Post-Feishu Re-Review —*
