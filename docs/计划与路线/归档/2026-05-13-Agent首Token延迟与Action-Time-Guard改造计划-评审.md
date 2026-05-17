# 计划评审报告:2026-05-13 Agent 首 Token 延迟与 Action-Time Guard

**评审日期:** 2026-05-13
**评审对象:** `docs/计划与路线/2026-05-13-Agent首Token延迟与Action-Time-Guard改造计划.md`
**评审方式:** 全量读取计划点名的源文件 + 跨文件副作用追踪 + 蓝军 mutation
**结论:** ISSUES_OPEN —— 方向对,Phase 0.5 / 1.2 / 2.2 / 4.1 / 5.2 必须按本报告修订

---

## Step 0:范围挑战 + "已经存在的"代码对照

读完计划点名的全部文件,**这个计划的诊断是对的,但严重低估了"已有实现"的覆盖面**。下表列出每条提议 vs 现状:

| 计划提议 | 现状 | 差距 |
|---|---|---|
| Phase 0.1 加 `intent_classify_ms` 分段日志 | `react_processor.go:356/409` 已记 `ReAct 迭代开始`/`发起 LLM 调用`,中间无分段 | 真缺,需要补 |
| Phase 0.2 `create_stream_ms`/`input_count`/`tool_count` | `stream_responses.go:77-84` **已经在记** | 只缺 `base_url`/`service_tier`/`prompt_cache_key_present`,**计划夸大了改动量** |
| Phase 1 拆 2s intent classifier | `react_processor.go:367-372`,timeout 是 `intent_classifier_adapter.go:11` 的硬常量 `2 * time.Second` | 是真瓶颈,但 `turnIntent` 还流到 **L408 工具可见性 + L432-456 ToolChoice + L856/877 PendingExternalSendIntent**,默认值改了行为会跟着塌 |
| Phase 2.1 ActionGuard 类型 | 无 | 真新增 |
| Phase 2.2 shell deny/ask + structured danger + 外部发送 ask | `lifecycle.go:201-336` 的 `createPermissionPromptFn` **已实现 80%**(shell MatchPolicy → deny/ask + StructuredDangerousOperation → ask + strict 兜底) | 只差"外部发送默认 ask"和"unknown MCP 默认 deny";Phase 2 大部分是重新包装 |
| Phase 2.2 #5 `feishu_api send_message` ask | `capability_registry.go:134-141` 的 `structuredDangerousActions["feishu_api"]` **不含 send_message/send_file/send_image** | **真安全缺口**,计划描述太轻 |
| Phase 2.3 在 `executeTool` 接入 guard | `react_processor.go:1620-1704` 当前顺序:plan gate → emit start → quality event → enforce gate → bridge | 接入点存在,但 `enforceToolExecutionGate` 被调了 **3 次**(L1702/1736/1741),计划没说 guard 是否也要 3 处 |
| Phase 2.4 复用 HITL | `lifecycle.go:343-401` 的 `requestHITLPermission` 现成可复用 | 接口就绪 |
| Phase 3.2 收敛 `createPermissionPromptFn` | `master.go:444` 通过 `skills.NewPermissionManager` 注入到 `permMgr`,**在 toolBridge.ExecuteDirect 内部触发** | 关键:ActionGuard 在 `executeTool` 顶层调用 ≠ `permMgr` 在 bridge 内部触发,**会双触发审批卡片** |
| Phase 4.1 默认只暴露 ls/memory/question/skill/tool_search | `capability_registry.go:203-210` **已经是这 5 个** | 计划误诊。21 工具来源是 `tools.go/webfetch.go/...` 共 **16+ 个 `Core: true`** 标志(read_file、write_file、bash、edit、grep、glob、browser_interact 等),`tool_visibility.go:119` 把 `Core` 当成等价默认可见 |
| Phase 4.2 稳定工具排序 | `mcphost/host.go:175 for _, t := range h.tools`(map 遍历) | 真问题,**且直接破坏 OpenAI prompt cache**(工具 schema 是 cache key 一部分) |
| Phase 5.1 service_tier | grep 全仓 0 命中 | 真新增,需确认 openai-go SDK 字段名 |
| Phase 5.2 prompt_cache_key | `message_transform.go:705` **已设置** `PromptCacheKey = openai.String(ctx.Model)`(model 名做 key) **但只走 Chat Completions 路径** | Responses API 路径(`stream_responses.go`)**完全没走 message_transform**,所以 Responses 流式调用**当前根本没有 prompt cache key**。计划应该说"修复 Responses 路径,顺便改 cache key 组成",而不是当作新增 |

**结论**:计划"最小可交付版本 = Phase 0+1"是对的,但 Phase 2-4 各写成独立大块时,**实际工作量约 40% 是重命名/挪位/补 metric**,真正新增逻辑是 30%,剩 30% 是别处早就修过的(尤其 Phase 4.1)。

---

## Section 1:架构

### 1.1 fast path 拆 classifier 的"副作用面"被低估 [P1, confidence 9/10]

证据:`react_processor.go:367-374` 把 `turnIntent` 算出后,**3 处下游消费**:

```
L408: modelVisibleToolsForSessionWithRecallObservationAndSkillsAndIntent(..., turnIntent)
       └─> tool_visibility.go:84 driven by intent (外部发送候选注入 / mixed read-only 默认)
L432-456: shouldEvaluateToolChoiceForTurn / detectToolChoiceWithIntentAndMessages(latestQuery, ..., turnIntent, ...)
       └─> tool_choice: 命中 external_send 时强制 "required"
L856/877: session.RememberPendingExternalSendIntent(turnIntent)
       └─> 后续 turn 检测未完成外部发送
```

Phase 1.2 的"保守默认 intent":`IntentAnswer + AllowsSideEffects:false + RequiresExternal:false` 会让:
- L432 的 `shouldEvaluateToolChoiceForTurn` 走 auto 分支 → **失去 IM 里 "@bot 发文档给 X" 这种场景的 required 兜底**
- L408 的 `ensureExternalSendCandidates`(tool_visibility.go:202)依赖 `isExplicitExternalSendIntent(intent)`,默认 answer 永远 false → **外部发送候选不再被注入**
- L856 的 PendingExternalSendIntent 不再设置 → **后续 turn 跟踪丢失**

**这是计划没列出的真后果**。计划说"工具执行前再做 action guard"是对的,但 ActionGuard 是 deny/ask/allow,**没法补回 tool_choice 决策**——tool_choice 是给模型看的提示,在 LLM 调用之前就要定。

**推荐**:Phase 1.2 的 fast-path-default-intent 必须**额外**保留一条 cheap rule-based 路径(`router.RuleClassifyIntent` 已存在,见 `intent_classifier.go:230-260`),走纯本地正则。把 `intentClassifierTimeout` 改成 200ms(就是 `DefaultIntentClassifierTimeout`),并把 mode 切到 `IntentClassifierRuleOnly`,**就能去掉 2s 阻塞同时保住 turnIntent 信号**。这条比"硬塞 IntentAnswer 默认"安全得多。

### 1.2 ActionGuard 与 createPermissionPromptFn 双触发风险 [P0, confidence 9/10]

证据:
- `master.go:444`: `m.permMgr = skills.NewPermissionManager(hitlCfg.PermissionRules, m.createPermissionPromptFn())`
- `react_processor.go:1740-1745`: `toolBridge.ExecuteDirect(...)` 内部走 skills.WithDirectExecutionGate
- `createPermissionPromptFn` 由 `skills.PermissionManager.CheckPermission` 触发,**在 bridge 内部 + 异步**

如果 Phase 2.3 在 `executeTool` 顶部加 ActionGuard 触发 HITL,然后又进 `toolBridge.ExecuteDirect`,内部 permMgr **还会再触发一次** `createPermissionPromptFn`。同一个工具调用会发**两次审批卡片**(用户可能在飞书里看到两条卡片,先批一条,工具还没动)。

Phase 3.2 写"短期保留 createPermissionPromptFn 作为兼容层"是不够的——必须明确两层 idempotency:
- 要么 ActionGuard 一旦决定了就**注入一个 "permission_pre_granted" ctx flag**,permMgr 跳过
- 要么 ActionGuard 只做 **deny + 静默 allow**,把 ask 路径完全留给 permMgr(但这违背计划"统一拦截前置"的目标)

**推荐**:Phase 2.3 接入 guard 时,在 ctx 里注入 `actionGuardDecided=true` flag,`createPermissionPromptFn` 第一行检查这个 flag,有就直接返回 `Granted: true`。计划必须显式写这一点。

### 1.3 21 个工具的真实来源 ≠ HostToolSetDefaultVisible [P2, confidence 9/10]

证据:`tool_visibility.go:114-120`

```go
func isDefaultVisibleTool(tool mcphost.ToolDefinition) bool {
    ...
    return tool.Core || router.IsHostToolInSet(router.HostToolSetDefaultVisible, name)
}
```

`HostToolSetDefaultVisible` 只 5 个(`capability_registry.go:204-210`),但 `tool.Core` 在 16+ 处置 true:`bash, read_file, write_file, edit, multi_edit, grep, glob, browser_interact, webfetch, websearch, question, todo_write, tool_search, promote_todos_to_taskboard, create_handoff_summary, enter_plan_mode, exit_plan_mode` 等。

Phase 4.1 文案"普通 fast path 默认只暴露 ls/memory/question/skill/tool_search"**字面已经是真的**,但实际 21 个是因为 `Core: true` 注入。计划必须明确:
- 改 `isDefaultVisibleTool` 引入 fast_path 分支(`tool.Core` 只在 non-fast-path 生效),**还是**
- 删除部分工具的 `Core: true` 标志(影响面大,bash/edit/read_file 是常用工具)

**推荐**:加 `FastPathDefaultVisible` 第三种集合(只 5-8 个白名单),fast path 开启时**忽略 Core 标志**,只看这个白名单。

---

## Section 2:代码质量 / DRY

### 2.1 ActionGuard 与 createPermissionPromptFn 逻辑重复 [P1, confidence 8/10]

证据:`lifecycle.go:201-336` 与计划 Phase 2.2 列的规则:

| Phase 2.2 规则 | lifecycle.go 现状 |
|---|---|
| shell PolicyDeny → deny | L279-306 已实现 |
| shell PolicyAsk → ask | L308-327 已实现 |
| StructuredDangerousOperation ask | L211-231 已实现 |
| read-only allow | L232(非 shell 默认放行)已实现 |
| 外部发送 ask | **未实现**(structuredDangerousActions 缺 send_*) |
| unknown MCP deny | **未实现** |

**计划该做的不是"新建 ActionGuard 重复 80% 逻辑",而是**:
1. 在 `capability_registry.go:134-141` `structuredDangerousActions["feishu_api"]` 中**加 4 行**:`send_message, send_file, send_image, upload_file`
2. 在 `createPermissionPromptFn` 里增加 unknown MCP fallback 分支
3. ActionGuard 抽象只在**普通工具调用前 deny/ask 决策**这一层提取,不重复实现规则

**推荐**:Phase 2 重写为"扩展现有 createPermissionPromptFn 的规则表 + 抽接口出来",而非"重新实现一套 ActionGuard"。完成度 9/10。

### 2.2 PromptCacheKey 在 Responses 路径丢失 [P2, confidence 9/10]

证据:`message_transform.go:704-708` 只在 Chat Completions 路径设 `ctx.Params.PromptCacheKey`。`stream_responses.go:38-44` 构造 `responses.ResponseNewParams` 时**没有任何 PromptCacheKey 字段调用**。

Phase 5.2 写"新增配置 prompt_cache_key_enabled"暗示是新功能,但实际上**Chat Completions 路径早就在用**(只是 cache key 选成 model 名,粒度太粗)。Phase 5.2 应改成两件事:
- **Fix**:Responses API 路径补设 prompt_cache_key
- **Improve**:cache key 加上 `stable_toolset_hash`(必须先做 Phase 4.2 工具稳定排序,否则 hash 漂)

### 2.3 enforceToolExecutionGate 被调 3 次 [P3, confidence 8/10]

证据:`react_processor.go:1702, 1736, 1741`。计划 Phase 3.1 写"保留 enforceToolExecutionGate"但没解释 3 次调用的存在原因。**蓝军提问**:ActionGuard 也要 3 处吗? 如果只在 1702 处接入,middleware wrap 路径(L1732)的 inner call 会绕过吗? 这影响 spawn_agent/parallel_dispatch 这类执行入口工具。

**推荐**:Phase 3.1 验收里加一条"ActionGuard 接入点的覆盖矩阵:串行/middleware/bridge gate hook 都要走过"。

---

## Section 3:测试覆盖

### 3.1 测试地图(关键路径)

```
首 Token 延迟 (Phase 0+1)
├── intent classifier 拆除
│   ├── [新加] react_processor_test.go: fast_path 开启时 turnIntent 默认 answer ★
│   ├── [新加] intent_classifier_adapter_test.go: 关闭 fast path 回退旧 2s 行为 ★
│   ├── [GAP] tool_choice 行为回归: external_send 场景关闭 classifier 后还能命中 required? ★★ [→E2E]
│   └── [GAP] PendingExternalSendIntent 跨 turn 状态: 关闭 classifier 后跨 turn 跟踪是否失效 ★★
└── stream diagnostics
    └── [已存在] stream_diagnostics_test.go: first raw event vs first text 区分

Action-Time Guard (Phase 2)
├── action_guard_test.go (Phase 2.1)
│   ├── rm -rf / deny ★★★ (复用 BuiltinDangerousRules 已有断言)
│   ├── git push --force ask ★★★
│   ├── read_file allow ★★
│   ├── feishu_api send_message ask ★★★ (蓝军重点)
│   ├── unknown side-effect MCP deny ★★★
│   └── [GAP] structured dangerous operation 同输入 vs createPermissionPromptFn 一致性 ★★★ ⚠️ REGRESSION
├── react_processor: executeTool 顶层 guard ask 用户拒绝时,toolBridge.ExecuteDirect 不被调用 ★★★
├── [GAP-CRITICAL] ActionGuard ask 与 createPermissionPromptFn 双触发回归测试 ★★★ ⚠️
└── HITL 复用 (Phase 2.4)
    └── [已存在] lifecycle_test.go:31-264 已覆盖 permission fn → HITL 路径

工具收敛 (Phase 4)
├── [GAP] tool_visibility_test.go: fast_path 模式下 21 → ≤8 工具 ★★
├── [GAP] [→稳定排序] 同请求两次 tool schema 顺序一致 ★★ (REGRESSION-PRONE)
└── [GAP] max_model_visible_tools=0 时回退当前行为 ★

总计: 13 关键路径 / 现有覆盖 3 / 需新增 10 (其中 3 个 [REGRESSION])
```

### 3.2 关键 REGRESSION(必须加,无需确认)

1. **ActionGuard ↔ createPermissionPromptFn 等价性测试**:同一输入两条路径决策必须一致(包括 ask 卡片描述、deny 文案)。否则灰度切换时用户会看到不一致的审批表现。
2. **fast path off 回滚测试**:`first_token_fast_path_enabled=false` 时 `intent_classify_ms` 仍约 ~2s 量级,确保回滚路径可用。
3. **外部发送 ask 全覆盖**:`feishu_api send_message/send_file/send_image, im_api send_*, send_im_message` 各一例。

---

## Section 4:性能

### 4.1 3034ms create_stream_ms:**不是应用层问题** [P0, confidence 8/10]

证据:`stream_responses.go:75-83` 的 `streamStart := time.Now()` 在 `c.client.Responses.NewStreaming(ctx, params)` **之前**。但 `create_stream_ms` 实际记录 NewStreaming 返回耗时——openai-go SDK 的 NewStreaming 是**建立 HTTP 连接 + 发送请求**的同步调用。3034ms 几乎全是网络/上游耗时。

用户的 trace baseUrl 是 `http://120.48.38.233:4000`(纯 IP + 4000 端口)→ **强烈像 LiteLLM/oneapi/openai-forward 自部署代理**。这类代理常见问题:
- SSE buffer 没禁用 → 首 event 被 chunk 起来等满 4KB
- 中间链 proxy 也缓冲
- 上游 prefill latency(tools schema 大)

Phase 5.3 "对比直连和代理"**是这个 plan 唯一可能砍掉 3s 的措施**。其他 Phase 5 改造(service_tier、cache_key)就算 100% 见效也只是 2-3 倍 cache 命中率提升,绝对值有限。

**推荐**:
- Phase 5.3 提前到 **Phase 0.5**,放在加观测之后,任何应用层改造**之前**。
- 如果代理是 3s 的主因,**Phase 5.1/5.2 在代理修好之前都是浪费**。

### 4.2 工具数量裁剪的真实收益 [confidence 7/10]

21 工具的 schema 体积通常 5-10KB(JSON Schema 含描述)。LLM prefill 速度 ≈ 5000 tokens/s 量级,5KB ≈ 1500 tokens ≈ 300ms。**Phase 4.1 收敛工具最多省 100-200ms**——远不及 Phase 1 的 2s。计划"实施顺序"里 Phase 4 排第 5 是对的。

---

## 蓝军 mutation 拷打(给计划找 5 个能"破"的点)

### M1:fast path 关闭 → IM 场景 tool_choice 不走 required → 飞书 IM "@bot 把文档发到 X 群"被模型口头分析而非真发

**Mutation**:Phase 1.2 默认 `IntentAnswer + RequiresExternal:false` 直接关掉了 `detectToolChoiceWithIntentAndMessages`(react_processor.go:432-468)的 `tool_choice_required_total` 路径。复现:用户在飞书 IM 说"发份文档到 X 群",fast path 默认 intent=answer → 不触发 required → 模型可能不调工具 → 没发出去也没报错。

**缓解**:必须保留 rule-only intent 走 200ms 本地分类(我在 1.1 推荐的),不能纯默认。

### M2:ActionGuard ask 在 IM 触发后,前端没收到卡片回路

**Mutation**:Phase 2.4 "复用 m.requestHITLPermission" 没说**广播路径**。`requestHITLPermission`(lifecycle.go:343-401)走 `m.eventBus.BroadcastInputRequest(inputReq)`,在 web 端会显示卡片。但**IM 路径**(飞书)的卡片广播是在 hitl_broker 内部+外部 sender,不一定能在 react_processor 执行流的 ctx 里跑通(broadcast 必须有 sessionID 关联,且当前 trace 是异步)。

**缓解**:Phase 2.4 验收必须**真跑一次 IM 用户对话 + 触发 ask + 卡片在飞书里出现**,不能只跑 web 端单测。

### M3:Phase 4.1 工具骤减→历史会话失效

**Mutation**:已运行的 IM/web 会话里,**`session.AllowedTools` 是被 `tool_visibility.go:108` 设置的**。如果热加载 fast_path,旧 session 的 AllowedTools 缓存还是 21 个的快照,模型记忆里可能引用了 bash/edit,模型再选 bash 时 `enforceToolExecutionGate` 会拒,**用户体验是工具"突然消失"**。

**缓解**:回滚开关 `max_model_visible_tools=0` 必须不仅对新 turn 生效,还要重置已有 session 的 AllowedTools。Phase 6.3 没说这一点。

### M4:prompt_cache_key 改成 `user_id + model + ...` 暴露 user_id

**Mutation**:Phase 5.2 写"cache key 使用 user_id + model + prompt_versions + stable_toolset_hash"。但 OpenAI 把 prompt_cache_key 当**公开 metadata**——multi-tenant 场景里 user_id 直接放到 key 里,假设有日志/审计看到 key,可能泄漏租户身份。

**缓解**:Phase 5.2 必须改成 `hash(user_id || tenant_salt) + model + toolset_hash`。计划原文要改。

### M5:context_compression_ms 比 intent_classify_ms 大,优化错地方

**Mutation**:计划假设 2s 是 intent classifier。但 `react_processor.go:363` `m.prepareMessagesWithCompression(ctx, session, msgsCopy)` 是另一个潜在长耗时点——尤其首 token 阶段 session 大、需要 compression 的场景。计划没列 compression timing。

**缓解**:Phase 0.1 验收里加一项:`context_compression_ms` 必须打出来,**先看真实数据再决定 Phase 1 紧不紧急**。Phase 0 不是只覆盖 intent,要全段。

---

## NOT in scope(显式不做)

- Responses API 之外的 provider(deepseek/qwen/anthropic 等):本计划只优化 OpenAI/兼容代理路径。
- ReAct 主循环本身的并行化或预测式调度:本计划不动迭代结构。
- 模型自身 prefill/decoding 优化:无法在应用层做。
- 安全规则白名单的 LLM 动态学习:Phase 2 全部走静态规则,不引入二级 LLM。

## What already exists(避免重建轮子)

| 计划提议 | 已存在 |
|---|---|
| `SafeExecutor.MatchPolicyWithRule` | `security/exec.go:104` 完整存在 |
| HITL 审批入口 | `lifecycle.go:343 requestHITLPermission` 完整 |
| Shell deny/ask 规则 | `lifecycle.go:201 createPermissionPromptFn` + `BuiltinDangerousRules` |
| Structured dangerous op | `capability_registry.go:428 StructuredDangerousOperation` |
| 默认可见工具 5 个 | `capability_registry.go:203 HostToolSetDefaultVisible` |
| Rule-based intent 分类(200ms 默认 timeout) | `intent_classifier.go:230 RuleClassifyIntent` |
| Intent cache | `intent_classifier.go:134 IntentCache.Get` |
| Stream diagnostics 框架 | `stream_diagnostics.go` |
| prompt_cache_key | `message_transform.go:705`(仅 Chat Completions) |
| Quality event `EventPermissionDecision` | `lifecycle.go:212` + `agentquality` package |

## 失败模式 + 关键 gap

| 路径 | 失败方式 | 测试 | 错误处理 | 用户可见 |
|---|---|---|---|---|
| fast_path 默认 intent=answer + 外部发送 query | 模型不调 send 工具 | **GAP** | 无 | **静默**(高危) |
| ActionGuard ask 与 permMgr 双触发 | 两张审批卡 | **GAP** | 无 | 明显 |
| Phase 4.1 收敛后历史 session 的 AllowedTools 失效 | 模型选老工具被 deny | **GAP** | enforceGate 返回 tool error | "工具突然消失"(明显) |
| prompt_cache_key 包含 user_id | 元数据泄漏 | **GAP** | 无 | 静默(合规风险) |
| context_compression_ms 是真凶但被忽略 | 优化错地方,首 token 改不下 | **GAP** | 无 | 静默 |

**3 个 critical gaps**:静默外部发送丢失、双卡片、context_compression 黑盒。

---

## 推荐的实施修订

1. **Phase 0 加 `context_compression_ms`、`prepare_messages_ms`、`remove_orphans_ms`** —— 全段而非只 intent。验收必须看到 compression 占比再排 Phase 1 优先级。
2. **Phase 1 拆 classifier 改为"切到 rule-only 200ms"** —— 不是粗暴默认 IntentAnswer。保留 `RuleClassifyIntent`,保住 tool_choice 与 PendingExternalSendIntent 信号。
3. **Phase 2 重写为"扩展 createPermissionPromptFn 规则表 + 抽 ActionGuard 接口"** —— 不重新实现 80% 已有逻辑;`structuredDangerousActions["feishu_api"]` **本周就加 send_* 4 行**(独立 PR,P0 安全修复)。
4. **Phase 3.2 显式 ctx flag `actionGuardDecided`** —— 杜绝双审批。
5. **Phase 4.1 加 FastPathDefaultVisible 第三集合** —— 不动 Core 标志,fast path 模式忽略 Core。
6. **Phase 5.3 前置到 Phase 0.5(对比直连/代理)** —— 3s 是代理还是上游决定后续全部 Phase 5。
7. **Phase 5.2 prompt_cache_key:先修 Responses 路径丢失(P1),再讨论 key 组成(P2),user_id 必须 hash**。
8. **Phase 6.3 回滚开关验收加"已有 session 的 AllowedTools 重置"**。

## Completion Summary

- Step 0 范围挑战:**已完成,大量复用现有代码,Phase 2/4 工作量被高估 30-40%**
- 架构问题:**3 个**(intent 副作用面、双触发、Core 标志混淆)
- 代码质量问题:**3 个**(规则重复实现、Responses 路径缓存丢失、enforceGate 3 处覆盖)
- 测试 gap:**10 个新增 / 3 critical regression**
- 性能问题:**1 个 P0**(create_stream_ms 真凶可能是代理)
- NOT in scope:已写
- 已存在的:已写(10 项)
- 失败模式:**3 个 critical silent failures**
- 蓝军 mutation:**5 个可破点全部列出**
- 优先级建议:**8 条修订**

**Verdict**:**ISSUES_OPEN**——计划方向对,但 Phase 0.5 / 1.2 / 2.2 / 4.1 / 5.2 必须按上述修订改,否则上线后高概率出 M1(静默外部发送丢失)+ M2(双审批卡)+ M5(优化错地方)。
