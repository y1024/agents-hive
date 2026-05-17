# Agent 首 Token 延迟与 Action-Time Guard 改造实施计划

**目标：** 普通聊天首 token 不再被前置 LLM 意图分类阻塞；高风险工具调用在执行前统一拦截、审批、审计。

**当前瓶颈：**

```text
ReAct 迭代开始 -> 发起 LLM 调用：2056ms
发起 LLM 调用 -> Responses 流式请求已创建：3034ms
首个 Responses event -> 首个文本 chunk：212ms
```

优先级：

1. 先补观测，确认 2s 是否确实来自 intent classifier，3s 是否来自代理。
2. 再移除普通请求的 2s 前置 LLM 分类，但保留本地 intent 信号。
3. 再补齐工具执行前 guard。
4. 再优化工具 prompt 体积和 Responses cache/service tier。

评审修订结论：

- 采纳：fast path 不能硬默认 `IntentAnswer`，必须走 `RuleClassifyIntent + resolveTurnIntent`。
- 采纳：21 个工具的主要来源是 `Core=true`，fast path 必须绕开 Core 默认可见逻辑。
- 采纳：外部发送 action 需要补入 ask，尤其 `feishu_api send_message/send_file/send_image`。
- 采纳：代理对比提前到 Phase 0.3，避免先做应用层优化。
- 校正：Master 主路径的 `ToolBridge.ExecuteDirect` 当前跳过 `PermissionManager.CheckPermission`，不会天然双审批；但 legacy/subagent/CallTool 路径仍需兼容和测试。

---

## Phase 0：补齐延迟观测

**目的：** 先让每段耗时可见，避免后续优化靠猜。

### Step 0.1：记录 ReAct 调用前耗时

**修改文件：**

- `internal/master/react_processor.go`

**实施：**

在 `ReAct 迭代开始` 到 `发起 LLM 调用` 之间增加分段耗时日志：

- `repair_tool_calls_ms`
- `context_compression_ms`
- `intent_classify_ms`
- `tool_visibility_ms`
- `before_model_ms`
- `llm_request_build_ms`

**验收：**

- 同一次请求可以从日志里还原 `ReAct 迭代开始 -> 发起 LLM 调用` 的完整耗时构成。
- 普通 1-message chat 必须能看出是否仍有 `intent_classify_ms ~= 2000`。

### Step 0.2：记录 Responses 建流细节

**修改文件：**

- `internal/llm/stream_responses.go`

**实施：**

保留现有 `create_stream_ms/input_count/tool_count` 日志，补齐缺失字段：

- `create_stream_ms`
- `input_count`
- `tool_count`
- `request_payload_bytes`
- `base_url`
- `api_format`
- `service_tier`
- `prompt_cache_key_present`

**验收：**

- 能区分是本地构建慢、代理建流慢，还是上游首 event 慢。
- `create_stream_ms` 可按 `base_url/model/tool_count` 聚合。

### Step 0.3：提前做代理对比

**目的：** `create_stream_ms=3034` 基本发生在 `NewStreaming` 的 HTTP 建流阶段，先判断是不是代理问题。

**不改业务代码。**

**实施：**

同一请求测三组：

1. 直连 OpenAI。
2. 当前 `http://120.48.38.233:4000`。
3. 当前代理关闭 SSE buffering / 开启 keep-alive 后。

记录：

- `create_stream_ms`
- `first_raw_event_ms`
- `first_text_ms`
- `tool_count`
- `request_payload_bytes`

**验收：**

- 如果代理路径明显慢于直连，先修代理配置，再继续 Phase 5。
- 如果直连也慢，再继续 service tier / prompt cache / 工具体积优化。

### Step 0.4：加最小测试

**修改文件：**

- `internal/master/stream_diagnostics_test.go`
- `internal/llm/stream_diagnostics_test.go`

**实施：**

补测试覆盖：

- first raw event 不等于 first text。
- first token 统计使用首个有文本的 chunk。

**验收命令：**

```bash
go test ./internal/master ./internal/llm -v
```

---

## Phase 1：移除普通请求的前置 LLM 分类，但保留本地 intent 信号

**目的：** 普通聊天不再等待 `intentClassifierTimeout = 2s`，但不能丢掉 external send、create skill、manage tool 这些本地可恢复信号。

### Step 1.1：增加 fast path 开关

**修改文件：**

- `internal/config/config.go`
- `internal/master/master.go`

**实施：**

新增配置：

```json
{
  "agent": {
    "first_token_fast_path_enabled": true
  }
}
```

Go config 字段建议：

```go
type FirstTokenConfig struct {
    FastPathEnabled bool `json:"fast_path_enabled,omitempty"`
}
```

**验收：**

- 默认开启 fast path。
- 可通过配置关闭，回到旧行为。

### Step 1.2：把 input-time classifier 从普通路径移走

**修改文件：**

- `internal/master/react_processor.go`
- `internal/master/intent_classifier_adapter.go`

**当前问题代码位置：**

```go
turnIntentResult = router.NewIntentClassifier(
    router.WithIntentLLMClassifier(m.intentLLMClassifier(sessionLLM)),
    router.WithIntentClassifierTimeout(intentClassifierTimeout),
).Classify(ctx, session.ID, latestQuery)
```

**实施：**

fast path 开启时：

- 不同步调用 `m.intentLLMClassifier(sessionLLM)`。
- 先走本地规则：

```go
classified := router.RuleClassifyIntent(latestQuery)
turnIntent = resolveTurnIntent(session, latestQuery, classified)
```

- `resolveTurnIntent` 必须继续负责 external send / create skill / manage tool 的本地恢复。
- 外部写入能力不在首轮默认开放。
- 工具执行前再做 action guard。

**验收：**

- 普通聊天日志里没有阻塞式 LLM intent classifier。
- `ReAct 迭代开始 -> 发起 LLM 调用` 在短会话 warm path 下低于 300ms。
- 配置关闭 fast path 后旧逻辑仍可运行。
- "给郭松发一下今天的天气信息" 仍被 `resolveTurnIntent` 恢复成 `IntentExternalWrite`。
- "创建一个跟我打招呼的技能" 仍被恢复成 `IntentCreateSkill`。
- IM 文档引用场景仍能触发 `tool_choice=required`。

### Step 1.3：保留可选高风险预检，但不阻塞普通聊天

**修改文件：**

- `internal/master/intent_classifier_adapter.go`
- `internal/master/react_processor.go`

**实施：**

只在明确需要阻止主模型启动的场景做预检，例如：

- 用户直接要求读取/发送 secrets。
- 用户直接要求执行生产破坏性操作。
- 上下文中已有未完成外部发送确认。

预检不得使用主会话大模型。需要 LLM 时使用独立小模型和短 timeout；默认优先本地规则。

预检 timeout 改为配置：

```json
{
  "agent": {
    "preflight_classifier_timeout_ms": 300
  }
}
```

**验收：**

- 普通聊天不触发预检。
- 高风险预检超时默认进入 ask/deny，不进入 allow。
- 预检失败不会把 ordinary chat 卡 2 秒。

---

## Phase 2：实现 Action-Time Guard

**目的：** 模型可以先流式响应；真正执行工具前统一判断该工具调用是否允许。

### Step 2.1：新增 guard 类型

**新增文件：**

- `internal/master/action_guard.go`
- `internal/master/action_guard_test.go`

**实施：**

定义输入：

```go
type ActionGuardInput struct {
    SessionID       string
    UserID          string
    ToolCallID      string
    ToolName        string
    Arguments       json.RawMessage
    LatestUserQuery string
}
```

定义输出：

```go
type ActionGuardDecision struct {
    Action               string // allow | ask | deny
    Reason               string
    Source               string // policy | safe_executor | classifier
    RequiresConfirmation bool
}
```

定义接口：

```go
type ActionGuard interface {
    Decide(ctx context.Context, in ActionGuardInput) (ActionGuardDecision, error)
}
```

**验收：**

- 类型独立可测。
- 不依赖 LLM。

### Step 2.2：实现确定性 policy

**修改文件：**

- `internal/master/action_guard.go`
- `internal/router/capability_registry.go`
- `internal/security/exec.go`

**实施规则：**

不要复制一套新规则。先把 `createPermissionPromptFn` 里已有的 shell / structured dangerous 逻辑抽成可复用决策函数，然后 ActionGuard 和 legacy permission prompt 都调用同一份函数。

规则：

1. shell 家族工具先走 `SafeExecutor.MatchPolicyWithRule`。
2. `PolicyDeny` 直接 deny。
3. `PolicyAsk` 直接 ask。
4. `router.StructuredDangerousOperation(tool, args)` 为 true 时 ask。
5. 补齐外部发送类 action 为 ask：
   - `send_im_message`
   - `feishu_api` 的 `send_message/send_file/send_image`
   - `im_api` 的发送类 action
6. read-only 工具 allow：
   - `read_file`
   - `grep`
   - `glob`
   - `ls`
   - `web_search`
   - `web_fetch`
7. unknown/open-world/side-effect MCP 工具 deny。

**验收测试：**

- `rm -rf /` => deny。
- `git push --force origin main` => ask。
- `read_file` => allow。
- `feishu_api {"action":"send_message"}` => ask。
- `feishu_api {"action":"send_file"}` => ask。
- `feishu_api {"action":"send_image"}` => ask。
- `send_im_message` => ask。
- unknown side-effect tool => deny。
- 同一输入在 ActionGuard 和 `createPermissionPromptFn` 兼容层下决策一致。

### Step 2.3：在 `executeTool` 开头接入 guard

**修改文件：**

- `internal/master/react_processor.go`

**实施位置：**

函数：

```go
func (m *Master) executeTool(...)
```

在以下逻辑之后：

- 注入 `sessionID`
- 注入 trace context
- `evaluatePlanToolGate`

在以下逻辑之前：

- `emitToolCallEvent(... Status: "start")`
- 参数 override
- `toolBridge.ExecuteDirect`
- `mcpHost.ExecuteTool`

实现方式：

- 新增 `m.guardToolExecution(ctx, session, toolCallID, toolName, args, ...) (toolResult, bool)`。
- `guardToolExecution` 内部按顺序调用：
  1. `enforceToolExecutionGate`
  2. `ActionGuard.Decide`
  3. HITL ask/deny 处理
- `executeTool` 首次执行前调用一次。
- `ToolBridge.ExecuteDirect(..., skills.WithDirectExecutionGate(...))` 的 gate hook 中也调用同一个 `guardToolExecution`，用于插件改写参数后的二次校验。

**行为：**

```text
allow -> 继续执行工具
ask   -> 发 HITL 请求，未批准则返回 tool error
deny  -> 返回 tool error，不执行工具
```

覆盖矩阵：

- 串行路径：直接调用 `executeTool`。
- 并发路径：`executeToolsConcurrent` 最终调用 `executeTool`。
- 插件改写参数后：`ExecuteDirect` 的 `WithDirectExecutionGate` 再次调用同一个 `guardToolExecution`。

**验收：**

- 被 deny/ask 未批准的工具没有进入 `toolBridge.ExecuteDirect`。
- 串行和并发工具执行路径都生效，因为二者最终都会调用 `executeTool`。
- 插件改写参数后，不会绕过 ActionGuard。
- `enforceToolExecutionGate` 与 ActionGuard 的组合不会出现两个不同错误文案；统一由 `guardToolExecution` 返回 tool error。

### Step 2.4：复用现有 HITL

**修改文件：**

- `internal/master/lifecycle.go`
- `internal/master/action_guard.go`

**实施：**

ActionGuard `ask` 时复用：

```go
m.requestHITLPermission(ctx, req, sessionID)
```

把 concrete action preview 放进 `PermissionRequest.Description`。

避免重复审批：

- Master 主路径使用 `ToolBridge.ExecuteDirect`，当前实现已经跳过 `PermissionManager.CheckPermission`，不会天然双弹审批。
- 其他 legacy / subagent / `ToolBridge.CallTool` 路径仍可能走 `createPermissionPromptFn`，如果这些路径也接入 ActionGuard，必须用 context 标记避免重复 HITL。
- 新增 context 标记建议放在 `internal/toolctx/context.go`，例如 `WithActionGuardDecision` / `HasActionGuardDecision`。

**验收：**

- Web 端能收到审批请求。
- IM 端高风险动作不 silent auto allow。
- 用户拒绝时工具结果为结构化错误。
- 同一 tool call 最多出现一张审批卡。
- IM 真链路验证：触发 ask 后，飞书/IM 用户能收到确认卡片或确认消息。

### Step 2.5：记录 guard 事件

**修改文件：**

- `internal/master/action_guard.go`
- `internal/master/quality_events.go`
- `internal/agentquality/types.go`

**实施：**

新增或复用 quality event：

```text
action_guard_decision
```

字段：

- `session_id`
- `tool_name`
- `tool_call_id`
- `action`
- `reason`
- `source`
- `latency_ms`

**验收：**

- 每个 ask/deny 都能从日志追溯。
- allow 事件可采样记录，避免日志过大。

---

## Phase 3：收敛旧权限逻辑

**目的：** 避免 `createPermissionPromptFn`、`enforceToolExecutionGate`、ActionGuard 三套逻辑互相打架。

### Step 3.1：保留 `enforceToolExecutionGate` 做 runtime allow-list

**修改文件：**

- `internal/master/react_processor.go`

**实施：**

`enforceToolExecutionGate` 继续负责：

- 当前 turn allowed tools。
- `AllowedToolInputs` 参数约束。
- plan mode gate。

ActionGuard 负责：

- 风险判断。
- 审批。
- deny/ask/allow。

**验收：**

- 不删除现有 `RouteDecision` 执行约束。
- ActionGuard 不替代 allowed-tools gate。

### Step 3.2：把 `createPermissionPromptFn` 的风险判断迁到 ActionGuard

**修改文件：**

- `internal/master/lifecycle.go`
- `internal/master/action_guard.go`

**实施：**

短期保留 `createPermissionPromptFn` 作为兼容层，但风险判断函数只保留一份。

迁移后：

- shell deny/ask 规则只维护一处。
- structured dangerous operation 只维护一处。
- HITL 请求仍走同一个 broker。
- legacy `CallTool` 路径继续走 `PermissionManager`。
- Master `ExecuteDirect` 路径走 ActionGuard，不重复走 `PermissionManager`。

**验收：**

- Master 主路径不绕过 ActionGuard。
- legacy / subagent 路径在未接入 ActionGuard 前继续使用 `PermissionManager`，不得删除原有保护。
- 单元测试证明同一输入在旧 prompt fn 和新 guard 下结果一致。

---

## Phase 4：减少首轮工具负载

**目的：** 降低 prompt/tool schema 体积，减少建流和 prefill 压力。

### Step 4.1：缩小默认可见工具

**修改文件：**

- `internal/master/tool_visibility.go`
- `internal/router/capability_registry.go`

**实施：**

当前 `HostToolSetDefaultVisible` 已经只有少量工具，21 个工具主要来自 `ToolDefinition.Core=true`。fast path 必须显式绕开 `Core` 默认可见逻辑。

新增 fast path 可见集合，普通 fast path 默认只暴露：

- `ls`
- `memory`
- `question`
- `skill`
- `tool_search`
- 必要 read-only 文件/搜索工具

外部写入工具不默认暴露为 callable。

**验收：**

- 普通聊天 `tool_count` 明显低于当前 21。
- 需要外部写入时仍可通过发现/后续 turn 使用。
- `isDefaultVisibleTool` 在 fast path 下不再把所有 `Core=true` 工具自动放入模型可见列表。
- `first_token_fast_path_enabled=false` 或 `max_model_visible_tools=0` 时恢复旧工具暴露行为。

### Step 4.2：稳定工具排序

**修改文件：**

- `internal/master/tool_visibility.go`
- `internal/llm/stream_responses.go`

**实施：**

发送给模型前按稳定 key 排序：

```text
tool.Name
```

不要让 map 遍历顺序影响 tool schema 顺序。

**验收：**

- 同一配置下连续两次请求 tool schema 顺序一致。

### Step 4.3：补 tool_count 预算

**修改文件：**

- `internal/config/config.go`
- `internal/master/tool_visibility.go`

**实施：**

新增配置：

```json
{
  "agent": {
    "max_model_visible_tools": 8
  }
}
```

超过预算时：

- read-only 核心工具优先。
- `tool_search` 必保留。
- 外部写入工具不进入普通 fast path。

**验收：**

- 普通请求不再携带 20+ 工具 schema。
- 命中预算时有日志说明被裁剪工具数量。

---

## Phase 5：Responses 建流优化

**目的：** 处理剩下的 `create_stream_ms=3034`。

### Step 5.1：加 service tier 配置

**修改文件：**

- `internal/config/config.go`
- `internal/llm/stream_responses.go`
- `internal/llm/responses.go`

**实施：**

新增配置：

```json
{
  "llm": {
    "interactive_service_tier": "priority"
  }
}
```

对交互式主聊天请求透传 service tier。provider 不支持时忽略并记录 debug。

**验收：**

- 日志显示请求的 `service_tier`。
- 不支持该字段的 provider 不报错或可回退。

### Step 5.2：加 prompt cache key

**修改文件：**

- `internal/config/config.go`
- `internal/llm/stream_responses.go`
- `internal/llm/message_transform.go`

**实施：**

修复点：

- Chat Completions 路径已有 `PromptCacheKey`，但 key 只有 model 粒度，需要改进。
- Responses 流式路径当前没有设置 prompt cache key，需要补上。

新增配置：

```json
{
  "llm": {
    "prompt_cache_key_enabled": true
  }
}
```

cache key 使用：

```text
hash(user_id || tenant_salt) + model + prompt_versions + stable_toolset_hash
```

不能包含原始用户输入和 secrets。
不能直接写明文 `user_id`。
`stable_toolset_hash` 依赖 Phase 4.2 的稳定排序，Phase 4.2 未完成前不要启用该 hash。

**验收：**

- 请求日志显示 cache key 是否开启。
- usage 中如果有 cached tokens，要记录。
- Responses API 路径和 Chat Completions 路径都设置一致策略。
- cache key 不包含明文用户 ID。

---

## Phase 6：测试与回滚

### Step 6.1：核心测试

**新增/修改文件：**

- `internal/master/action_guard_test.go`
- `internal/master/react_processor_test.go`
- `internal/router/capability_gate_test.go`

**必须覆盖：**

```text
普通聊天不触发 input-time LLM classifier
rule-only + resolveTurnIntent 保留 external_send/create_skill 信号
external_send 场景仍能触发必要的 tool_choice required
PendingExternalSendIntent 跨 turn 不丢失
read_file allow
grep allow
feishu send ask
feishu send_file/send_image ask
send_im_message ask
rm -rf / deny
unknown side-effect tool deny
structured dangerous operation ask
fast path 关闭后旧逻辑可运行
ActionGuard 和 legacy permission 决策一致
同一 tool call 不重复弹审批
fast path 模式下 model-visible tools <= max_model_visible_tools
同请求两次工具 schema 顺序一致
```

**验收命令：**

```bash
go test ./internal/master ./internal/router ./internal/security -v
```

### Step 6.2：全量测试

**验收命令：**

```bash
go test ./... -v
```

### Step 6.3：回滚开关

**修改文件：**

- `internal/config/config.go`

**必须支持：**

```json
{
  "agent": {
    "first_token_fast_path_enabled": false,
    "action_guard_enabled": false,
    "max_model_visible_tools": 0
  },
  "security": {
    "permission_mode": "strict"
  }
}
```

**验收：**

- 关闭 fast path 后恢复旧 intent classifier 行为。
- 关闭 action guard 后仍保留旧 `enforceToolExecutionGate` 和 HITL。
- `strict` 模式仍能强制审批。
- 关闭 `max_model_visible_tools` 限制后，已有 session 的 `AllowedTools` 在下一轮重新按旧逻辑计算，避免历史会话工具突然失效。

---

## 实施顺序

按这个顺序做，不要并行大改：

1. Phase 0：只加观测，不改行为。
2. Phase 0.3：先对比直连和代理，确认 3s 建流瓶颈。
3. Phase 1：fast path 移除 2s 前置 LLM 分类，但保留本地 intent 信号。
4. Phase 2：ActionGuard 最小可用，只覆盖 shell/read/external send/unknown。
5. Phase 3：收敛旧权限逻辑，避免重复判断。
6. Phase 4：减少默认工具数量。
7. Phase 5：优化 Responses cache/service tier。
8. Phase 6：补测试和回滚验证。

最小可交付版本只包含 Phase 0 + Phase 1：

- 改动小。
- 风险低。
- 能直接验证首 token 是否减少约 2 秒。

完整版本包含 Phase 0-6：

- 改动中等偏大。
- 主要集中在 `internal/master`、`internal/router`、`internal/llm`、`internal/config`。
- 不需要重写 agent 架构。
