# Hive 已有能力清单（防 NIH 重复发明）

> **目的**：在 spec 设计前必读 — 列出 Hive 内部已实现的核心能力，防止把已有 interface 重新发明
> **触发**：RE-REVIEW-POST-FEISHU 发现主线程 + codex 双 review 都漏掉"重新发明现有 channel interface"
> **维护规约**：每次重大功能 ship 后追加更新，作为后续 spec 设计的前置参考
> **日期**：2026-04-27

---

## §0 教训

主线程 ARCH-REVIEW（15 项）+ codex 独立 review（17 项）都没看 Hive 内部 channel 代码，所以同时漏掉了**最大的 NIH 问题**：

> 我 W4 spec 设计的 ChannelAdapter interface（Name / Subscribe / Unsubscribe / Render / Patcher / Acker capability interface）= **Hive 已有的 `channel.ChannelPlugin + channel.EventRenderer + channel.RendererError`**

**根本原因**：spec 设计前没 grep 现有代码，假设"现状空白"。这是 NIH（Not Invented Here）syndrome。

**永久规约**：任何新 interface / 新事件类型 / 新存储抽象的 spec 设计前，**必读本文件**。

---

## §1 Channel 体系（飞书改造完成后）

### 1.1 接口契约（`internal/channel/plugin.go`）

```go
// 必需基础 — 所有 channel 实现
type ChannelPlugin interface {
    Platform() Platform
    Send(ctx, msg OutboundMessage) error
    WebhookHandler() http.HandlerFunc
    Verify(r *http.Request) bool
}

// 流式渲染能力（可选扩展）— EventBus → channel 翻译
type EventRenderer interface {
    ChannelPlugin
    RenderEventStream(ctx, scope SessionScope, eventCh <-chan master.BroadcastMessage) error
}

// 入站控制（可选扩展）— 命令处理 / mute / session override
type InboundController interface {
    ControlInbound(ctx, msg, currentSessionID) (InboundControlResult, error)
}

// 错误兜底机制
type RendererError struct {
    Inner       error
    LastContent string  // renderer 失败时，Router 用 LastContent 走 plugin.Send 兜底
}
func WrapRendererErr(inner error, lastContent string) error
```

### 1.2 飞书已有能力（17,904 行 / 50+ 文件）

| 能力 | 文件 |
|---|---|
| EventRenderer 完整实现 | renderer.go (763 行) |
| dedup（PostgresEventClaimer fail-closed 200ms）| dedup.go |
| gap_fetch（断线重连恢复缺口）| gap_fetch.go + gap_fetch_runner.go |
| reconnect_watchdog（重连监控）| reconnect_watchdog.go |
| reliability_leader_gate（多副本 leader 选举）| reliability_leader_gate.go |
| governance（rollout / mute / debug / multiAgent）| governance.go |
| acl + audit | acl.go + audit.go |
| ratelimit | ratelimit.go |
| retry_queue + webhook_retry | retry_queue.go + webhook_retry.go |
| lifecycle_handler | lifecycle_handler.go |
| client_registry（多 client 管理）| client_registry.go |
| dispatcher_factory | dispatcher_factory.go |
| mention_sanitizer | mention_sanitizer.go |
| prompt_prefix（prompt 注入）| prompt_prefix.go |
| card_builder + card_decode | card_builder.go + card_decode.go |
| chat_state_repo | chat_state_repo.go |
| crypto | crypto.go |
| longconn（长连接管理）| longconn.go |
| user_cache | user_cache.go |
| welcome_sender | welcome_sender.go |
| commands（slash command 处理）| commands.go |
| hitl_bridge（HITL 集成）| hitl_bridge.go |
| health + health_api | health.go + health_api.go |
| resolver（用户/会话解析）| resolver.go |
| webhook | webhook.go |
| tenant + region | tenant.go + region.go |

### 1.3 router 体系（`internal/channel/router.go`）

```go
// 已有
type DedupBackend interface {
    Check(ctx, messageID) (dup bool, err error)
    Stop()
}
const dedupTimeoutDefault = 200 * time.Millisecond  // fail-closed
var ErrDedupBackendDown = errors.New("dedup backend down ...")

// 已有 messageDedup（基于 sync.Mutex + map）默认实现
```

### 1.4 push/ 主动推送（`internal/channel/push/`）

新目录含 service.go + templates.go — 主动推送能力（与响应式 webhook 相对）。

### 1.5 channel 类型常量（`internal/channel/types.go`）

```go
const (
    PlatformDingTalk Platform = "dingtalk"
    PlatformFeishu   Platform = "feishu"
    PlatformWeCom    Platform = "wecom"
    PlatformWeChat   Platform = "wechat"
    PlatformWeChatWechaty Platform = "wechat-wechaty"
    PlatformWeChatPadPro  Platform = "wechat-wechatpadpro"
)

type ChatType string  // direct / group / channel

type MessageProcessor interface { ProcessMessage(...) }
type IMMessageProcessor interface { ... }  // IM 元数据透传扩展
```

---

## §2 Master / Event 体系

### 2.1 EventBus（`internal/master/event_bus.go:90`）

```go
type EventBus struct { ... }  // 进程内订阅广播
//   - Subscribe(sessionID) → eventCh
//   - Publish(event)
//   - 带 critical / non-critical 事件分级（满通道丢弃 non-critical）
```

### 2.2 BroadcastMessage（`internal/master/master.go:77`）

```go
type BroadcastMessage struct {
    Type      string    // "input_received" / "message" / "tool_call" / "input_request" / "error" / "agent_progress" / "agent_status" 等
    SessionID string
    Content   string
    // ...
}
```

### 2.3 ClaimToken（dedup 用）

```go
type ClaimToken interface { ... }  // 飞书 PostgresEventClaimer 用
```

---

## §3 Observability 体系（`internal/observability/`）

```go
type Tracer interface {
    StartSpan(ctx, traceID, spanID, parentSpanID, operation, service, sessionID) SpanContext
    RecordSpan(ctx, span Span) error
}

type MetricsWriter interface {
    Record(ctx, metric Metric) error
}

type LogWriter interface {
    Write(ctx, entry LogEntry) error
}

type SpanContext struct { ... }
//   - 写 hive_traces / hive_metrics / hive_logs（PG-backed）
//   - nil 安全 + fire-and-forget 异步写入
```

---

## §4 Security 体系（`internal/security/`）

```go
// SafeExecutor + MatchPolicy（Allow / Ask / Deny）
// builtin_rules.go 19 条 hardcoded destructive 规则
// AST parser（ast_parser.go 351 行）
// LLM classifier（llm_classifier.go 118 行）
// arity / env / exec / parser / patterns 各自模块
```

---

## §5 Memory 体系（`internal/memory/`）

```go
// pg_store.go (677 行) — Postgres 持久化
// pgvec_store.go (138 行) — pgvector 向量存储
// hybrid.go (137 行) — 混合检索
// extractor.go (223 行) — memory 提取
// injector.go (133 行) — memory 注入到 prompt
// vecindex.go (188 行) + vecstore.go (28 行) — 向量索引
// embedding.go (172 行) — embedding 接口
```

---

## §6 Skills 体系（`internal/skills/`）

```go
// finder.go (449 行) + discovery.go (390 行) — skill 发现
// executor.go (88 行) + hooks.go (57 行) — skill 执行
// metrics.go (131 行) — skill metric
// admin.go (70 行) — admin API
// on_demand_api.go — on-demand loading 基础
```

---

## §7 MCP 体系（`internal/mcphost/`）

```go
// 26 文件 / 完整 MCP host
// client.go (615 行) + host.go (321 行)
// transport.go + transport_http.go + transport_sse.go + transport_stdio.go (3 种 transport)
// oauth.go (357 行) + token_store.go
// hitl.go (86 行) — HITL 集成
// prompt.go + resource.go + toolset.go
```

---

## §8 ACP 体系

### 8.1 Server（`internal/acpserver/`，已有）

```go
// agent.go + commands.go + permission.go + stream.go
// authenticate_test.go + integration_test.go
// mcp_passthrough.go — ACP 透传 MCP
// 用 coder/acp-go-sdk v0.6.3
// IDE 接入 Hive 的 server 角色
```

### 8.2 Client（`internal/acpclient/`）

```go
// client_impl.go + pool.go + remote_agent.go + transport.go + types.go
// 当前是 Hive 主动连接其他 ACP 端的 client（待 W14 扩展）
```

### 8.3 A2A Bridge（`internal/a2abridge/`）

```go
// adapter.go + transport.go — Agent-to-Agent 协议桥接
```

---

## §9 Spec-driven 体系（`internal/specdriven/`）

```go
// 完整 14 文件 + 7 测试
// compare.go + types.go + metrics.go
// continuation/ + eval/ + ingress/ + intake/ + planner/
// Phase 1 已上线（SafeExecutor 权限极简）
// Phase 2 默认 OFF（mode=legacy / continuation=off），等 W12 大重构
```

---

## §10 Subagent 体系（`internal/subagent/`）

```go
// factory.go (54-79 行 maxPerSession=3 / maxGlobal=30，hardcoded，待 W3 配置化)
// agentloop.go (203-217 行 stream callback drop tool_call，待 W5/W7 修)
```

---

## §11 Tools 体系（`internal/tools/`）

15+ 工具单文件（待 W4 改成结构化目录）：
- applypatch / batch / browser / create_tool / custom_loader
- feishu_tools / file_tracker / filelock / shell / spawn_agent / parallel_dispatch
- read_file / write_file / edit / webfetch / websearch
- task / tools.go (978 行 dispatcher)

---

## §12 Streaming（`internal/streaming/`）

```go
// websocket.go + 主动推送
// maxConnsPerIP = 5 (hardcoded，待配置化)
```

---

## §13 LLM Provider（`internal/llm/`）

9+ provider（OpenAI / Anthropic / Bedrock / Gemini / Groq / Mistral / DeepSeek / Azure / 自定义）。

---

## §14 Spec 设计前的 grep checklist

任何新功能 spec 之前**必跑**：

```bash
# 1. 看是否有现成 interface
grep -rn "type.*interface" internal/<related-domain>/ --include="*.go"

# 2. 看是否有现成事件类型
grep -rn "BroadcastMessage\|EventType\|BroadcastType" internal/master/ --include="*.go"

# 3. 看是否有现成 plugin / adapter / registry 抽象
grep -rn "type.*Plugin\|type.*Adapter\|type.*Registry" internal/ --include="*.go"

# 4. 看是否有现成 dedup / retry / circuit breaker
grep -rn "Dedup\|Retry\|CircuitBreaker\|Backoff" internal/ --include="*.go"

# 5. 看现有数据持久化层
ls internal/store/ internal/<domain>/
```

---

## §15 维护规约

每次重大功能 ship 后追加新 §：
- 新模块 → 在合适的 § 下补能力清单
- 新 interface → §1.1 / §2 等核心接口章节追加
- 现有 interface 演进 → 更新对应 § 的描述

**触发**：
- 月度 retro
- spec 设计前 review

---

## §16 推荐使用流程

设计新 spec 之前：
1. 读本文件全部
2. grep checklist §14
3. 找到现有 interface / 事件类型 / 抽象 → 优先复用
4. 实在没有 → 设计新 interface（再次 codex review 看是否真没有）

---

*— End of Hive Existing Capabilities —*
