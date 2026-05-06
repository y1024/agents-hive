# 架构层 Review：5 份 SPEC 风险评估

> **方法**：主线程亲自以架构师视角审视 5 份 spec（避免子 agent 翻车风险）
> **范围**：L0+L1+L2 详细 review；L3+L4+L5 关键问题摘录（远期 spec 启动时再细 review）
> **风险级别**：🔴 高（必须修复）/ 🟡 中（建议修订）/ 🟢 低（可接受，标注）
> **日期**：2026-04-26

---

## §0 总评

5 份 spec 整体框架扎实（DAG 依赖正确 + 接口设计合理 + 蓝军 mutation 思路对）。但有 **15 个具体设计问题** 需要在施工前解决，避免返工。其中 **5 个高风险** 必须修复。

---

## §1 SPEC-LAYER0 风险

### 🔴 高风险

#### R1.1 CheckID uint16 容量不足
**位置**：`SPEC-LAYER0 §1.2.1`
**问题**：CheckID 用 uint16，每段 256 个 ID。W5 BashTool 攻击 vector ~17+ + W6 Permission 8 层级联 + W9 Memory 多个 check + W12 Spec-driven check + 未来扩展，**很可能超过 65536 个 ID**。
**修复**：改 uint32（4 字节，cost 可忽略）+ 命名空间每段 65536 个 ID。

#### R1.2 hive_metrics 高基数 label 风险
**位置**：`SPEC-LAYER0 §1.2.2`
**问题**：`session_id` 作为 metric label 进 hive_metrics 表。spec 说"PG 索引存（不影响）"是 **hand-wave**——PG 高基数 label 上索引性能仍会退化，10K+ session 后查询慢。
**修复**：
- session_id **不进 metric label**，进 trace 的 attributes（trace 表设计就是高基数 OK）
- metric 只保留有限基数 label（check_id / tool_name / result）
- 如要按 session 维度查询，走 hive_traces 表 join

#### R1.3 W2 Timeout cancel reason 区分缺失
**位置**：`SPEC-LAYER0 §2.2.1`
**问题**：`WithToolTimeout` cancel func 在 ctx.Err() == DeadlineExceeded 时上报 timeout metric。但 **如果 ctx parent 被用户主动取消（不是 timeout），ctx.Err() 也是 Canceled**——不应该报 timeout metric。
**修复**：
```go
return ctx, func() {
    select {
    case <-ctx.Done():
        if ctx.Err() == context.DeadlineExceeded {
            // 真 timeout
            metricsWriter.RecordCheck(...)
        }
        // 否则是 user cancel 或 parent cancel，不报 timeout
    default:
        // ctx 还活着但用户主动调 cancel
    }
    cancel()
}
```

### 🟡 中风险

#### R1.4 W3 OnComplete leaky abstraction
**位置**：`SPEC-LAYER0 §3.2.2`
**问题**：`governance.CheckSpawn() → ... defer governance.OnComplete()`。OnComplete 必须 defer 调用，否则 counter 错乱（panic / early return 漏调）。这是 **leaky abstraction**。
**修复**：用 Decision returning release closure 模式：
```go
type SpawnPermission struct {
    Allowed bool
    Release func()  // 调用方 defer Release()，不会忘
}

decision := governance.RequestSpawn(ctx, sessionID)
if !decision.Allowed { return }
defer decision.Release()
```
强制性更强（编译器无法保证 defer，但 Release 函数式更显眼）。

#### R1.5 W2/W3 配置时机不对齐
**位置**：`SPEC-LAYER0 §2.2.2 + §3.2.1`
**问题**：W2 timeout 在 PerToolPolicies hardcoded map 里。W3 capacity 在 config.json 里。**两者本质都是 capacity 配置，应该统一**。
**修复**：W3 配置 schema 加 timeout section（合并）：
```json
{
  "capacity": {
    "spawn": {...},
    "tool_concurrency": {...},
    "tool_timeout": {
      "default_ms": 30000,
      "per_tool": {...}
    },
    "admission": {...}
  }
}
```
W2 实现时只用 hardcoded fallback，W3 完成后切到读 config。

### 🟢 低风险

#### R1.6 RecordCheck/Tool/Step 三个 helper 重复
3 个 helper 同质（都是 metric record），可合并为一个 typed 接口：
```go
type EventType uint8
const (EventTypeCheck = iota; EventTypeToolCall; EventTypeAgentStep)
func Record(ctx context.Context, eventType EventType, ...)
```
但语义清晰也是优点，**保持现状可接受**。

---

## §2 SPEC-LAYER1-W4 风险

### 🔴 高风险

#### R2.1 ChannelAdapter interface 缺 Unsubscribe
**位置**：`SPEC-LAYER1 §2.3`
**问题**：方法集只有 Subscribe / Render / Ack / Patch / Health。**用户关闭 web tab / 删除飞书 message / IDE 关闭** 后没有 Unsubscribe 路径，goroutine 泄漏。
**修复**：加 `Unsubscribe(ctx, sessionID, eventStream) error`，并约定 Subscribe 在 ctx.Done() 时也自动 unsubscribe。

#### R2.2 W4 一次改 5 个工具风险大
**位置**：`SPEC-LAYER1 §2.1`
**问题**：5 工具结构化改造同时做，2 周内完成。**任何一个出 regression 都阻塞整体**。
**修复**：**先 1 个（bash）作为 reference 模式**，验证通过后再扩散到其他 4 个：
- W4.1（1 周）：bash 单工具改造 + Tool interface 接入测试
- W4.2（1 周）：read/write/edit/web_fetch 4 个并行（已有模式，低风险）

### 🟡 中风险

#### R2.3 W4 W5 之间空 module 衔接不清
**位置**：`SPEC-LAYER1 §2.2`
**问题**：W4 spec 说"W5 在这个骨架上填实际 attack vector 防御代码"，但 **W4 是否建 path_validation / sed_validation / readonly_mode 包的空骨架**？不清楚。
**修复**：W4 验收清单加："W5 用到的 4 个 module（security / path_validation / sed_validation / readonly_mode）必须有空 package 骨架（含 interface 定义），W5 直接填实现，不新建包。"

#### R2.4 channels 与 channel 两包并存迁移路径
**位置**：`SPEC-LAYER1 §2.3`
**问题**：spec 说"`internal/channels/`（复数）做新抽象层" + "现有 `internal/channel/` 不动"。**两套并存的最终目标不明**。
**修复**：明确迁移路径：
- W4: 新建 `channels` 包（interface 层）
- W7: Web adapter 直接用 channels
- W8: feishu adapter 包装现有 channel/feishu/ 代码（不重写）
- 月 6 后（L3 完成）：决定是否把 channel/ 全部包装成 adapter 然后 deprecate channel/ 顶层 router

---

## §3 SPEC-LAYER2 风险

### 🔴 高风险

#### R3.1 W6 8 层 Permission 性能未验证
**位置**：`SPEC-LAYER2 §2.2`
**问题**：每次工具调用走 8 层 Permission 评估。**累计 latency 未做 benchmark**。如每层 1ms，8 层就 8ms。tool call 频繁场景下不可忽视。
**修复**：
- 加 benchmark：1000 工具调用走 8 层，p50/p99 < 5ms
- 必要时加 cache（同 sessionContext 同 command 缓存 N 秒）
- 验收清单加性能门槛

#### R3.2 W6 多层 Ask 触发语义未明确
**位置**：`SPEC-LAYER2 §2.2`
**问题**：spec 说"任何层 Ask（无 Deny 上层）→ final ask"。但 **多层都返回 Ask 时，HITL 触发 1 次还是多次**？多次会让用户 click 多个 approve button 烦死。
**修复**：明确"final ask 只触发 1 次 HITL"，多层 Ask 合并为单一 prompt（含所有层的 reason）。

### 🟡 中风险

#### R3.3 W7 Web Console 工期 1 周不够
**位置**：`SPEC-LAYER2 §3.5`
**问题**：W7 工期 1 周。但需要：React 组件（todos list / item / store）+ WebSocket integration + 后端 web adapter + UI 设计 + E2E 测试。**1 周明显不够**，前端 + 后端各 1 人也要 1.5-2 周。
**修复**：W7 工期改为 **2 周**（前端 1.5w + 后端 0.5w 并行 → 2w 完成），相应调整 Layer 2 总工期。

#### R3.4 W7 Subscribe 阻塞循环 vs 多用户并发
**位置**：`SPEC-LAYER2 §3.2.1`
**问题**：`WebAdapter.Subscribe` 是阻塞循环。但 **Web 端是 WebSocket 连接，多 user 同时连**——每个 connection 一个 Subscribe goroutine 还是单一 goroutine fan-out？
**修复**：明确架构：
- 单一 event subscriber goroutine（per session）
- 多 WebSocket connection 通过 fan-out broker 分发
- WebAdapter 维护 `sync.Map[sessionID][]*websocket.Conn`

#### R3.5 W8 Adapter 包装层与现有飞书代码耦合
**位置**：`SPEC-LAYER2 §4.2`
**问题**：W8 直接 import `internal/channel/feishu`。**强耦合**。如果飞书施工方改了 renderer.go 内部接口，W8 包装层会 break。
**修复**：W8 加 adapter interface 层：
```go
// internal/channels/feishu/types.go
type FeishuRenderer interface {
    PatchCard(ctx context.Context, cardID, json string) error
    // ... 适配 W8 需要的最小接口
}
// internal/channels/feishu/adapter.go 用 FeishuRenderer interface（不直接 import 飞书 renderer 类型）
```

### 🟢 低风险

#### R3.6 W5 Detector 数量"17+"未确定
spec 说"17+ Detector"。应在 W5 启动时**精确列出 attack vector 清单**。但 17+ 是合理 estimate（Claude Code bashSecurity.ts 头 80 行就有 17 种 substitution patterns + 25 个 Zsh 命令），**实施时再细化可接受**。

---

## §4 SPEC-LAYER3 关键问题（远期，简略 review）

### 🟡 中风险

#### R4.1 W9 Local embedding provider 决策
**位置**：`SPEC-LAYER3 §1.2.5`
**问题**：5-provider fallback 中 ProviderLocal placeholder（Go 无 node-llama-cpp 等价物）。
**修复**：W9 启动时决策：A) 用 ollama（subprocess）/ B) 跳过 local，4-provider fallback。

#### R4.2 W12 markdown export 失败兜底"audit log"未具体
**位置**：`SPEC-LAYER3 §4.4 M12.1`
**问题**：fs 不可写时降级为 audit log。**audit log 是 PG 表？文件？**未说。
**修复**：明确 audit log = `hive_logs` 表（已有）的特殊 source 字段（如 `source="specdriven_export_failure"`）。

#### R4.3 W12 A/B 实验统计显著性
**位置**：`SPEC-LAYER3 §4.4 M12.4`
**问题**：spec 说"实验组完成率 > 对照组 ≥ 20%"。**没有样本量 / 置信度 / 显著性测试**。
**修复**：W12 ship 后 dual-flag rollout 阶段加：
- 最小样本量计算（基于预期效应大小 + alpha 0.05 + power 0.8）
- 用 A/B testing framework（如 Optimizely 或自建简单版）

### 🟢 低风险

#### R4.4 W11 collapse threshold 默认值
N 行自动 collapse 的默认值未说。建议 **100 行**（可配置）。

---

## §5 SPEC-LAYER4-5 关键问题（远期，简略 review）

### 🟡 中风险

#### R5.1 W13 工期估算偏乐观
**位置**：`SPEC-LAYER4-5 §1.2`
**问题**：spec 说 25 工具 × 1 周 = 25 周。但 **每个新工具不止编码**，还有：测试 / 文档 / prompt 设计 / E2E 验证 / 集成调试。实际每工具可能 1.5-2 周。
**修复**：工期改为 **2-3 个 quarter（分批）→ 6-9 个月**（已经写了，但具体 25 周 vs 50 周差距大，需要明确）。

#### R5.2 W14 ACP↔MCP bridge 是否对齐 OpenClaw 限制
**位置**：`SPEC-LAYER4-5 §2.2.3`
**问题**：OpenClaw 限制"per-session mcpServers 不支持"。Hive 是否保留同样限制？
**修复**：W14 启动时决策：A) 对齐限制（与 OpenClaw 互通性好）/ B) Hive 自己支持 per-session（独有功能）。

### 🟢 低风险

#### R5.3 W15 Coordinator Mode 切换粒度
spec 是 feature flag + env var。可接受，**未来如需 per-session 切换再加**。

#### R5.4 W16 GEPA 成本控制
spec 没明确 GEPA reflection batch job 的 LLM 成本预算。**远期问题**，W16 启动时再定。

---

## §6 跨 W 依赖正确性 Review

### 🟢 DAG 无循环依赖
检查：W1→W4→W5/W6/W7→W12 链路 + W3→W6 capacity 接入 + W14→W15 ACP→Multi-agent 依赖。**无循环**。

### 🟡 中风险

#### R6.1 W7 与 W8 与 W12 依赖关系
**位置**：跨 spec
**问题**：原 spec 说 W12 依赖 W7+W8。Q1 用户决策后改为只依赖 W7。**但 SPEC-LAYER3 §4.1 仍写"接 W7 Web Console todos UI（不依赖 W8）"**，需确认所有 spec 一致。
**修复**：确认 IMPLEMENTATION-PLAN + SPEC-LAYER3 表述一致（已修正部分，再 grep 一遍）。

---

## §7 风险总结

| 风险级别 | 数量 | 必须在施工前修复？ |
|---|---|---|
| 🔴 高 | 5 | **是**（R1.1 CheckID uint32 / R1.2 metric 高基数 / R1.3 cancel reason / R2.1 Unsubscribe / R2.2 W4 分批 / R3.1 性能 benchmark / R3.2 多层 Ask 合并）|
| 🟡 中 | 8 | 建议（不修也能 ship，但会留技术债）|
| 🟢 低 | 5 | 可接受 |

---

## §8 必须修复的 5+ 个高风险（W1 启动前）

### Action items

1. **R1.1**：CheckID uint16 → uint32 + 命名空间每段 65536（5 分钟改 spec）
2. **R1.2**：session_id 不进 hive_metrics label，挪到 hive_traces attributes（10 分钟改 spec）
3. **R1.3**：WithToolTimeout cancel reason 区分（DeadlineExceeded vs Canceled）（15 分钟改 spec）
4. **R2.1**：ChannelAdapter 加 Unsubscribe 方法 + ctx.Done auto-unsub 约定（10 分钟改 spec）
5. **R2.2**：W4 工具结构化改造分批 — 先 bash 1 周 + 4 工具并行 1 周（10 分钟改 spec + 改 IMPLEMENTATION-PLAN 时间轴）
6. **R3.1**：W6 加性能 benchmark 验收门（5 分钟改 spec）
7. **R3.2**：W6 多层 Ask 合并为单一 HITL prompt（10 分钟改 spec）

合计 **~1 小时改 spec**，然后 W1 可启动。

---

## §9 建议修复的 8 个中风险（可分批做）

施工进入对应 W 之前修：

- R1.4 W3 OnComplete leaky abstraction → release closure 模式（W3 启动前）
- R1.5 W2/W3 配置统一（W3 启动前）
- R2.3 W4-W5 空 module 衔接明确（W4 启动前）
- R2.4 channels/channel 迁移路径明确（W4 启动前）
- R3.3 W7 工期改 2 周（W7 启动前）
- R3.4 W7 Subscribe fan-out 架构明确（W7 启动前）
- R3.5 W8 adapter interface 层（W8 启动前）
- R6.1 W12 依赖一致性 grep 确认（W12 启动前）

---

## §10 不需修复的 5 个低风险

- R1.6 RecordCheck/Tool/Step 3 个 helper（语义清晰可接受）
- R3.6 W5 Detector 数量精确化（W5 启动时再定）
- R4.4 W11 collapse threshold 100 行（合理默认）
- R5.3 W15 Coordinator Mode 切换粒度（远期再加）
- R5.4 W16 GEPA 成本控制（远期问题）

---

## §11 下一步

| 选项 | 工期 |
|---|---|
| **A** | 立即修 7 个高风险（R1.1-R3.2），~1 小时改 spec → 启动 W1 施工 |
| **B** | 修 7 个高风险 + 8 个中风险 → 启动 W1 施工 |
| **C** | 不修任何风险，直接启动 W1（接受技术债 / 后续迭代）|

我推荐 **A**：高风险必修（避免大返工），中风险逐 W 启动前修。

---

*— End of Architecture Review —*
