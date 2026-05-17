# Agent 质量整改计划：从「截图发蠢」到根因修复

> 状态：**ARCHIVED** — Phase 1（P0-A tool_choice + P0-B websearch strict）已交付，Phase 2/3 不再按此推进。
> 相关 follow-up：**[Agent-工具召回稳定化计划.md](Agent-工具召回稳定化计划.md)** / **[Agent-长任务续航验收与压缩策略计划.md](Agent-长任务续航验收与压缩策略计划.md)**

### 已交付（不再维护本文档，直接读代码）

| P0 | 状态 | 实际落点 | 说明 |
|----|------|---------|------|
| P0-A tool_choice 门槛 | ✅ 交付（方案加强） | `tool_choice_detector.go` + `react_processor.go:332-356` + `evaluateRequiredGuard` + `shouldSuppressStreamPartial` + `assistantcap.GrantStream` | 未按原 plan 走"探测器 + forced tool_choice"简单版，而是做成 **detectToolChoice + structural lock + required guard**：required 模式主动屏蔽流式 partial，坏回答根本不能 render；LLM 硬抗不调工具时走 breach 计数 + 重试。比原方案严。 |
| P0-B websearch 静默假成功 | ✅ 交付 | `websearch.go:74,149,185-191` | strictMode 零结果转 `IsError=true`；`QualityGuards.WebsearchStrict` 开关有消费者。 |
| 灰度开关 ToolChoiceForce / WebsearchStrict | ✅ 交付 | `config/config.go:301-306` | 定义 + 消费齐全。 |

### 未交付（本文档到此为止，下列各项请到新计划继续）

| 项 | 状态 | 证据 | 去向 |
|----|------|------|------|
| P0-A deferred tool discovery（`tool_search` 元工具 + 工具描述分层） | ❌ 未做 | 全仓零匹配 `tool_search` / `DeferredTool` | 待跨计划对齐 |
| P0-B 统一 `SearchResultEnvelope` + fallback chain（Google/Bing/Brave） | ❌ 未做 | 全仓零匹配 | 待跨计划对齐 |
| **P0-C 流式 tool_calls 回调消费端** | ❌ 未做 | `react_processor.go:358-365` 主回调仍只看 `ContentSoFar`/`ReasoningContent`，无 `chunk.ToolCalls` 分支 | 待跨计划对齐；注意 required 模式的 "屏蔽 partial + 终态广播" **不等价**于原 plan 的"长任务首工具反馈 < 2s" |
| **P0-D middleware pipeline**（`BeforeModel`/`AfterModel`/`WrapToolCall` 接口 + 四件套） | ❌ 未做 | `internal/master/middleware.go` 不存在；接口符号全仓零匹配 | **硬依赖项**，longrun plan 的 L-1/L-2/L-3/L-6 都等这层 |
| **P0-D grounding validator**（`sources []URL` + URL ⊆ sources 校验 + 下一轮重核） | ❌ 未做 | `validateLLMResponse` 仍是 7 行 nil 检查（`react_processor.go:1562-1567`）；`sources`/`citation`/`Grounding` 全仓零匹配 | 待跨计划对齐 |
| §3 提示词重写（任务分类路由 / 动作型工具描述 / 消除 execution vs reply 矛盾） | ❌ 未做 | `prompt_builder.go:113` websearch 仍是名词式；`144-146` 防幻觉三行软引导原封未动 | 待跨计划对齐 |

### ⚠️ 僵尸 flag 遗留

`cfg.Agent.QualityGuards.PostValidation`（`config/config.go:307`）定义了但**全仓零消费者**。留着会误导后来人以为"只是没开"，下个人接手 P0-D 时请决定：接入消费点 / 或先删掉避免误会。

### 本文档的使用方式

- §1 前因 / §1.3 自我撤回 / §2 P0 四条的现状证据 / §7 deer-flow 对照清单 —— **仍然有效**，作为历史背景和调研工件保留
- §2 中的"修什么" / §5 Phase 计划 / §6 待决策 —— **不要再按此推进**，已由上表拆档
- §4 不做什么 —— 其中"永不做"四条仍然有效（不引入 LangGraph、不换 provider 默认、不做静态 DAG）；"本期不做，由 longrun plan 承接"所有条目维持原约定

---

## 0. TL;DR（原始内容，保留作历史）

Agent 答"女娲.skill"时胡编 3 个博客 URL，根因**不是模型不够强**，是 ReAct 循环缺四道闸门。deer-flow 也没全做好，我们得超越它两项。

| P0 | 故障点 | 修什么 | deer-flow 可抄吗？ |
|----|--------|--------|-------------------|
| P0-A | 每轮全量工具 + 零 tool_choice | 加任务探测 + forced tool_choice | ❌ 只能抄 deferred tools，强制必须自建 |
| P0-B | websearch HTTP 200 + 空 DOM = 静默成功 | 零结果转 IsError | ✅ 抄 DDG/InfoQuest 的显式失败 |
| P0-C | chunk.ToolCalls 已累积，主回调不看 | 主回调补 tool_calls 分支 | ✅ 抄 codex_provider + client 透传 |
| P0-D | `validateLLMResponse` 只查 nil | grounding validator（URL ⊆ sources） | ❌ deer-flow 自己也没做，必须自建 |

**时间线 9-13 天，三阶段灰度。**
**验收核心指标**：截图那类事实问答幻觉率 100% → ≤10%，工具调用触发率 0% → ≥90%。

---

## 1. 前因

### 1.1 触发事件

用户截图：问 Agent「女娲.skill / 女娲.skll 是什么」，Agent 返回：

- 三个看起来像博客的 URL（经核实全不存在）
- 部署指令里写 `git clone <仓库地址>`，尖括号是字面占位符
- 全程零工具调用，零检索

**真正要回答的问题**：一个装了 15+ 工具、9+ Provider、声称"生产级"的 Agent，碰到"我不知道，让我查一下"的问题时，为什么优先选"凭印象胡编"？

### 1.2 调查方法

三路并行，每一步都要代码证据，不接受"听起来对"：

1. **agents-hive 自身代码**：4 个 Explore agent 覆盖 40K+ 行，锁定 P0 四条
2. **Claude ↔ codex 蓝军对辩**：3 轮，每轮必须给 file:line_range，我自己撤回三条错误断言（见 §1.3）
3. **deer-flow 对照读**（字节 LangGraph 项目）：codex 在 `/Users/guoss/workspace/company/vast/deer-flow` 逐行读 20+ 文件（576 行输出），Claude 因沙箱限制改为"精读 codex 一手输出 + 反推对照 agents-hive 真实位置"

### 1.3 自我撤回（诚信留痕）

初诊时 Claude 断言过三条，后被 codex 逐条反驳：

| 初始断言 | codex 反证 | 裁决 |
|---------|-----------|------|
| Temperature 默认 1.0 是幻觉源 | `react_processor.go:217` 已硬编码 `const temperature = 0.3` | 撤回 |
| `trigger_keywords` 全仓没人用 | `skill_search.go:131` 真在用 | 撤回 |
| websearch 失败全无信号 | `react_processor.go:1046-1071` 有 IsError 路径捕获 HTTP/timeout | 部分撤回（真正漏洞是 HTTP 200 + 空解析静默成功，保留为 P0-B） |

这三条撤回留在这里，提醒后续评审者：**本计划的 P0 四条是反复 adversarial 验证过的，不是拍脑袋**。

### 1.4 额外澄清

用户之前怀疑三件事，逐一给结论：

- **openspec 让我们变弱了？** 无关。`mode=legacy` 默认值保证主路径零行为变化。~6K 行代码是独立的维护债，本次不动
- **提示词太 low？** 是。3/10（见 §3）。**这是截图症状的头号放大器，贡献约 70%**
- **Skills on-demand 回归？** 不是。`OnDemandEnabled` 默认 false，`skill_search` 本来就没在默认 MCP host 里出现。"女娲.skill" 答不出是**新功能没灰度**，不是升级把通道改坏了

---

## 2. P0 四条（Problem Cards）

> 每张卡自包含：现状证据 / 修什么 / deer-flow 能抄什么 / 必须超越什么 / 验收。
> 读者不用跳来跳去。

---

### P0-A：ReAct 循环无 tool_choice 门槛

#### 现状与证据

三个 LLM 入口都只传 `tools`，从未设 `tool_choice`：

- `internal/master/react_processor.go:325-331`（主路径）
- `internal/llm/client.go:805`
- `internal/llm/stream_completions.go:148`
- `internal/llm/responses.go:141-149`

已 grep 验证 `internal/llm/` 目录 `ToolChoice|tool_choice` **零匹配**。

```go
// react_processor.go:325
resp, err := sessionLLM.ChatWithToolsStream(ctx, llm.ChatWithToolsRequest{
    SystemPrompt:    systemPrompt,
    Messages:        preparedMessages,
    Tools:           availableTools,  // 全量无筛选
    Temperature:     temperature,
    // 缺 ToolChoice 字段
})
```

**后果**：等同永远 `auto`。截图那类"女娲.skill"问题，模型凭训练语料的模糊印象直接作答，不触发 websearch/skill_search。

#### 修什么

两段改：

**1）探测器 + 强制 tool_choice**（主路径）

```go
// react_processor.go，ChatWithToolsStream 调用前
toolChoice := detectToolChoice(userQuery, preparedMessages)
// 首版走确定性 keyword（零成本，§6 决策 2 待拍）：
//   - 含未见过专有名词 / URL 片段 / 文件路径 → "required"
//   - "X 是什么 / 怎么用" 且 X 不在 skills index → "required"
//   - 明确闲聊 / 致谢 → "none"
//   - 其他 → "auto"
```

配套扩 `llm.ChatWithToolsRequest` 加 `ToolChoice` 字段，三个入口全部透传。

**2）deferred tool discovery**（降 token 污染 + 降误选率）

工具默认只暴露 active 集（read_file/write_file/edit/bash/web_search 等高频）+ 一个 `tool_search` 元工具；长尾工具延迟到 LLM 主动查。

#### deer-flow 能抄什么

| 机制 | deer-flow 参考 | 抄法 |
|------|---------------|------|
| deferred tool discovery | `tools/builtins/tool_search.py:155-193` + `agents/middlewares/deferred_tool_filter_middleware.py:31-44` | 把工具描述分 active/deferred 两层，默认只下发 active，LLM 查 `tool_search` 才 attach deferred schema |
| 动作型工具描述 | `tools/builtins/task_tool.py:46-54` | 每个工具描述改成 `when_to_use / when_not_to_use / args_order` 三段式，替代现在的名词短句 |

#### 必须超越 deer-flow 什么

codex 亲自确认：deer-flow `openai_codex_provider.py:181-192` **没有任何 tool_choice 显式设置**。也没有"研究题必须先搜"的 classifier。

所以 **探测器 + forced tool_choice + no-tool-call 重试**这条路 agents-hive 必须自建。照抄 deer-flow 只能降概率，不能根治。

#### 验收

- 单测：10 条"女娲.skill"类问题 → 100% 触发 required
- 蓝军测：10 条纯闲聊短语（"你好" / "谢谢"）→ 100% 走 none（明确闲聊不消耗工具；
  本行与 §2 §107-115 策略一致 —— 闲聊 → none，非闲聊非强信号 → auto）
- 回归测：前缀启发式被移除后，"好的继续" / "收到请继续" / "ok 再来" 等指令短句走 auto（M6）
- 端到端：截图原问题必须触发 websearch 或 skill_search

---

### P0-B：websearch 静默"假成功"

#### 现状与证据

`internal/tools/websearch.go:112-118, 161-163`：DDG HTTP 200 后走 `parseSearchResults`。若 DOM 被反爬/变动/空白，正则零匹配，函数返回空切片，上层拼出"未找到关于 X 的搜索结果"包进 `textResult` 返回 **`IsError=false`**。

```go
// websearch.go:161-163
results := parseSearchResults(string(body), logger)
return results, nil  // 空结果当成功

// websearch.go:341
if len(results) == 0 {
    return fmt.Sprintf("未找到关于 '%s' 的搜索结果", query)  // textResult 正常路径
}
```

LLM 看到"工具执行成功 + 没搜到"，回退到凭印象答。我们对这类"假成功"**零监控**。

#### 修什么

```go
// websearch.go:341 附近
if len(results) == 0 {
    return errorResult(fmt.Sprintf(
        "DDG 返回 HTTP 200 但解析零结果（可能被反爬/DOM 变动），建议换 provider 或换 query：'%s'",
        query,
    )), nil  // IsError=true，ReAct 明确感知工具失败
}
```

加埋点 `websearch_empty_parse_total`，上 Grafana 看真实世界命中率。

#### deer-flow 能抄什么

| 机制 | deer-flow 参考 | 抄法 |
|------|---------------|------|
| 搜索失败显式化 | `community/ddg_search/tools.py:72-79` — 空结果返回 `{"error":"No results found", "query":...}` | 同款：HTTP 层失败、空解析都显式返回 error |
| 多 provider 的错误规范 | `community/infoquest/infoquest_client.py:69-78, 258-268` | HTTP 非 200、空响应、异常、格式错误全都显式返回 `Error: ...`，不装成成功 |

#### 必须超越 deer-flow 什么

deer-flow 每个 provider 自己拼 schema（DDG/Tavily/InfoQuest 三套不同结构）。我们应该更进一步，定一个统一 envelope：

```go
type SearchResultEnvelope struct {
    OK       bool               `json:"ok"`
    Query    string             `json:"query"`
    Provider string             `json:"provider"`
    Results  []SearchResultItem `json:"results"`
    Error    string             `json:"error,omitempty"`
}
```

任何搜索工具只能返回它，禁止裸字符串。这也给 P0-D 的 `sources[]` 聚合打地基。

#### 验收

- 单测：mock DDG 空白 HTML → 工具返回 IsError=true
- 观测：`websearch_empty_parse_total` 上线首周出数据，命中率验证 10%-30% 的估计

**可选扩展（§6 决策 4）**：Phase 2 加 Google/Bing/Brave fallback chain。

---

### P0-C：流式工具调用不可用（消费端漏洞）

#### 现状与证据

这是最"狡猾"的一条。累积端没问题，消费端丢数据。

**累积端 OK**（`stream_completions.go:281-315`，已 grep 验证）：

```go
// stream_completions.go:281
for _, tc := range delta.ToolCalls {
    // ... 累积
}
// stream_completions.go:315
if ... {
    ToolCalls: curToolCalls,  // callback 参数里有
}
```

**消费端漏**（`react_processor.go:332-339`，已读 50 行上下文）：

```go
}, func(chunk llm.StreamChunk) error {
    if chunk.Done {
        return nil
    }
    // 有可见内容或推理内容时才推送
    if chunk.ContentSoFar == "" && chunk.ReasoningContent == "" {
        return nil   // ← chunk.ToolCalls 非空也在这里被吞
    }
    // ... 后面只处理 ContentSoFar 和 ReasoningContent
}
```

**后果**：长上下文 + 多工具链式任务时，用户盯着空白屏幕等几十秒，体感"Agent 卡死"。

#### 修什么

改 `react_processor.go:332-339` 主回调，加 `tool_calls` 分支：

```go
if len(chunk.ToolCalls) > 0 {
    // 流式上报工具调用 preview：EventBus 广播 + UI 展示
    emitToolCallStream(session.ID, chunk.ToolCalls)
    // 不 return，让下面的 content 处理继续
}
if chunk.ContentSoFar == "" && chunk.ReasoningContent == "" && len(chunk.ToolCalls) == 0 {
    return nil
}
```

#### deer-flow 能抄什么

| 机制 | deer-flow 参考 | 抄法 |
|------|---------------|------|
| provider 解析流式 function_call | `models/openai_codex_provider.py:321-356` | 我们的 provider 层已经做了（`stream_completions.go:281-315`），无需改 |
| client 层 tool_calls 事件上抛 | `client.py:629-632` | 对应改我们的 `react_processor.go:332-339` 回调（如上） |

**这是三条借鉴里最直接对齐的一条**：deer-flow 的架构问题和我们一样，解决方案也一样。

#### 必须超越 deer-flow 什么

无。这条纯抄作业。

#### 验收

长任务（≥3 工具调用）首个工具反馈时间：「等完整 resp」→「首个 tool_call chunk」（体感 < 2s）。

---

### P0-D：零后置校验 + 缺中间件护栏链

#### 现状与证据

`react_processor.go:1478-1484`，就这 7 行：

```go
func validateLLMResponse(resp *llm.ChatWithToolsResponse) error {
    if resp == nil {
        return errs.New(errs.CodePlanExecFailed, "LLM returned nil response")
    }
    return nil
}
```

没有 URL reachability、没有引用来源核验、没有 schema 校验。`prompt_builder.go:144-146` 那句"引用外部信息时标注来源"是软引导，模型忽略零成本。

#### 修什么

两层改动：

**1）Middleware Pipeline（运行时护栏 + 可扩展架构）**

这一条是本计划的**架构基石**。不仅服务 P0-D 质量护栏，也是姊妹计划 `agent-longrun-capability-plan` 的插入点。设计要求：**接口一次定义，本期只落 4 个质量实现，后续长时续航能力以"新增实现"方式扩展，不再改主循环**。

**接口定义**（在 `internal/master/middleware.go`，待新增）：

```go
type Middleware interface {
    Name() string
    BeforeModel(ctx context.Context, state *AgentState) error
    AfterModel(ctx context.Context, state *AgentState, resp *llm.Response) error
    WrapToolCall(ctx context.Context, call *ToolCall, next ToolExecutor) (*ToolResult, error)
    OnInterrupt(ctx context.Context, state *AgentState, reason string) error  // 可选
}

type Pipeline struct {
    middlewares []Middleware  // 按注册顺序执行
}
```

**本期（Phase 2）落地 4 个质量实现**：

| 实现 | Hook | 职责 |
|------|------|------|
| `SkillInjectionMiddleware` | `before_model` | 注入 skill section + 工具裁剪（配合 P0-A deferred） |
| `LoopDetectionMiddleware` | `after_model` | 检测重复 tool_call 模式，强制转向 |
| `DanglingToolCallMiddleware` | `after_model` | tool_calls 完整性检查 + 自动补洞（抄 deer-flow） |
| `ToolErrorHandlingMiddleware` | `wrap_tool_call` | tool error → 结构化 ToolMessage（替代当前 `IsError` 散乱处理） |

**预留给 longrun plan 的扩展点**（本期不实现，仅保证接口兼容）：

| 实现（longrun plan） | Hook | 职责 |
|---------------------|------|------|
| `BudgetGuardMiddleware` | `before_model` | turn/token 预算门控（L-1） |
| `TodoListMiddleware` | `before_model` + `after_model` | 结构化外部记忆（L-2） |
| `SubagentDispatchMiddleware` | `before_model` + `wrap_tool_call` | 复杂步骤分派（L-3） |
| `CheckpointMiddleware` | `after_model` | 每 N 轮落盘（L-6） |

本期只需保证：接口签名不会因为上述扩展再改、注册顺序对 longrun 可配、`AgentState` 字段预留空间（`Todos`、`Checkpoints`、`BudgetUsage` 零值占位即可）。

**2）Grounding validator**（最终事实闸门）

agent state 加字段 `sources []URL`，每次 tool 输出累加。`validateLLMResponse` 升级为多检查器：

- URL 提取 → 必须 ⊆ sources[]，不在就标红
- URL 提取 → HEAD 请求（3s 超时）→ 不可达标红（可选，代价高）
- "文件路径"模式 → glob 核验存在
- **失败处理**：不抹掉结果，而是**追加系统消息进循环下一轮**：「你上一轮回复引用了 X 个不在 sources 里的 URL，请用 websearch 核实」。让模型自修。

#### deer-flow 能抄什么

| 机制 | deer-flow 参考 | 抄法 |
|------|---------------|------|
| tool error → 结构化 message | `agents/middlewares/tool_error_handling_middleware.py:19-65` | `wrap_tool_call` hook 的范本 |
| dangling tool_call 自动补洞 | `agents/middlewares/dangling_tool_call_middleware.py:75-138` | `after_model` hook 的范本 |
| loop detection | `agents/middlewares/loop_detection_middleware.py:347-356` | `after_model` hook 的范本 |

#### 必须超越 deer-flow 什么

**这是本计划最重要的"超越"**。

codex 读 `ThreadState` 结构（`agents/thread_state.py:1-55`），**没有 `sources/citations/grounding_map` 任何字段**。deer-flow 的 citation 本质是：搜索工具把 JSON 塞进 ToolMessage → LLM 自己从结果里抽 URL → 按 prompt 生成 `[citation:Title](URL)` 内联链接。前端 `markdown-content.tsx:38-44` 只是渲染时识别 `citation:` 前缀转 badge。

**没有后端 `[1][2] → URL` 映射表**，**没有独立 reporter**，**没有 citation reducer**，**没有 final grounding validator**。deer-flow 靠"prompt 规定 + LLM 自觉"防 URL 幻觉，理论上仍可能编。

所以：**`sources []URL` + final validator 这一层 agents-hive 必须自建**。这是我们**超越 deer-flow** 的地方，不是抄。

#### 验收

- 单测：构造"回答里带非 sources URL"的响应 → validator 标红 + 触发下一轮重核
- 端到端：截图原问题 → 回答中 URL 必须全部来自 tool output

---

## 3. 提示词重写（跨 P0，独立章节）

### 3.1 现状评分：3/10

位置：`prompt_builder.go:144-146, 269-294` + `skills/*/execution.md, reply.md`

问题清单：

1. **套娃**：技能列表埋在工具列表之后，巨型 prompt 尾部，注意力衰减
2. **工具描述名词式**：现状 `"搜索网络内容"`、`"搜索本地 + marketplace 的 skill"`；应改为动作导向
3. **规则自相矛盾**：`execution.md:13`「聚焦核心问题」vs `reply.md:8`「全面完整回答」— 模型选最省力那条
4. **全中文**对非中文原生模型指令遵循 -7% ~ -12%（有公开 eval）
5. **缺任务分类路由**：没有显式"识别这是事实问答 / 代码 / 规划 / 闲聊"的分流

### 3.2 改法

1. **任务分类路由置顶**：
   ```
   第一步：判断问题类型
   - 事实问答（X 是什么 / 谁做的）→ 必须 websearch + skill_search 后回答
   - 代码生成 → 先 glob/grep 看现有代码
   - 规划 → 先列 TODO，确认后再动
   ```
2. **工具描述改动作型**（抄 deer-flow `task_tool.py:46-54`）：
   - 现状：`"搜索网络内容"`
   - 改后：`"当用户提到不确定的仓库 / URL / 未见过的术语时必须调用 websearch"`
3. **消除 execution vs reply 矛盾**：统一为"先聚焦核心 → 核心解决后再扩展"
4. **关键指令双语**（§6 决策 3 待拍）：对非中文原生模型给英文锚点

### 3.3 验收

prompt-quality agent 重跑评分：3/10 → ≥ 7/10。

---

## 4. 不做什么（范围边界）

> 下列条目严格区分"永不做"和"本期不做"。后者是与 longrun plan 的分工边界，不是放弃。

**永不做：**

- ❌ **不引入 LangGraph / Python agent 框架依赖**。middleware pipeline 在 `react_processor.go` 外围自建（Go interface）
- ❌ **不换 LLM provider 默认值**。Temperature 已 0.3，不是元凶
- ❌ **不做静态 multi-agent DAG**（planner→researcher→reporter 这种）。codex 确认 deer-flow 自己也舍弃了（`ThreadState` 只有 `messages/artifacts/todos/viewed_images`）

**本期不做，由 longrun plan 承接：**

- ⏸ **不重写/重激活 openspec planner**。代码 ~6K 行保持 `mode=legacy` 默认值。longrun plan L-4 负责"激活健康度评估 + auto 模式设计"
- ⏸ **不加主循环 max_turns / token budget**。longrun plan L-1 负责（作为 `BudgetGuardMiddleware` 注册到本期定义的 pipeline）
- ⏸ **不加 TodoList 外部记忆层**。longrun plan L-2 负责（作为 `TodoListMiddleware`）
- ⏸ **不改 sub-agent 自动分派**。longrun plan L-3 负责
- ⏸ **不加工具级 timeout/retry/parallel**。longrun plan L-5 负责
- ⏸ **不加 step-level checkpoint**。longrun plan L-6 负责

**本期暂不做，独立立项：**

- ⏸ **不默认开 `OnDemandEnabled`**。需要先做 marketplace 稳定性 + 安全 review，另行立项

---

## 5. 交付计划

### 5.1 阶段与时间线

| 阶段 | 内容 | 预估 | Owner |
|------|------|------|-------|
| Phase 1 — 止血 | P0-A 探测器首版 + P0-B 显式化 | 2-3 天 | 待定 |
| Phase 2 — 补强 | P0-C 回调 + P0-D 护栏链 + grounding validator + 提示词重写 + deferred tools | 5-7 天 | 待定 |
| Phase 3 — 观测与灰度 | 埋点 / 面板 / 三开关灰度 | 2-3 天 | 待定 |
| **合计** | | **9-13 天** | |

### 5.2 灰度开关

三个 config flag，任一关闭即恢复旧行为：

- `cfg.Agent.QualityGuards.ToolChoiceForce` — P0-A + deferred
- `cfg.Agent.QualityGuards.WebsearchStrict` — P0-B + envelope
- `cfg.Agent.QualityGuards.PostValidation` — P0-D 护栏链 + grounding validator

### 5.3 验收矩阵

| 指标 | 现状 | 目标 |
|------|------|------|
| "女娲.skill"类事实问答幻觉率 | ~100% | ≤ 10% |
| 事实问答触发工具调用比例 | ~0% | ≥ 90% |
| prompt-quality agent 评分 | 3/10 | ≥ 7/10 |
| 长任务首个工具反馈延迟 | 等完整 resp | < 2s |
| websearch 静默空结果监控 | 无 | 有指标 + 告警 |
| 回答中 URL 可追溯到 tool output 比例 | 不可度量 | ≥ 95% |

### 5.4 风险与回滚

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| tool_choice=required 导致闲聊也被迫调工具 | 中 | 用户体感变差 | 蓝军 10 条闲聊必须全走 auto |
| websearch 改 IsError=true 导致弱成功的查询全部失败 | 中 | 搜索召回下降 | Phase 2 可选 fallback chain；看 metric |
| 提示词重写引入新矛盾 | 中 | 回归更严重 | 每轮改动跑 prompt-quality agent + 回放旧问题集 |
| Grounding validator 增加 LLM 轮次 | 高 | token 开销上涨 20-30% | 只在 `tool_choice=required` 的轮次做校验，闲聊轮次跳过 |
| codex 引用的 deer-flow file:line_range 飘动 | 中 | Phase 2 设计文档作废 | 开工前 owner 手动二次校验（§7 清单） |

**全局回滚**：上述三开关全部关掉。

### 5.5 与 longrun plan 的协作关系

本计划解决**单轮精度**（~30 秒内答对一个事实问题）。姊妹计划 `agent-longrun-capability-plan` 解决**多轮续航**（30 分钟 / 100-200 轮自驱动）。两者必须对齐：

**本计划对 longrun plan 的硬承诺：**

1. Phase 2 交付的 `Middleware` interface 一次定稿，longrun 6 个 P0 全部以新增实现方式扩展，不再改主循环
2. `AgentState` 扩展字段（`Todos []TodoItem` / `Checkpoints []CheckpointRef` / `BudgetUsage BudgetState`）本期以零值占位形式提前落地，避免 longrun 启动时再改核心 struct
3. Phase 1/2 完工即通知 longrun owner，不等 Phase 3 观测

**longrun plan 对本计划的反向依赖：**

1. L-2 TodoList 写入规则将复用本计划 §3 提示词重写里的"任务分类路由"——plan 类任务才要求 TodoList 开场
2. L-4 openspec 激活门控可能复用 P0-A 的 `detectToolChoice` 探测器（§6 决策 5）

**何时两个计划可以并行？** 本计划 Phase 2 中段（middleware interface 定稿后）即可并行。longrun 不必等本计划完全收尾。

**何时必须串行？** longrun L-4（openspec 激活）必须等本计划 §3 提示词重写上线，否则 planner 产出的 plan 结构无法被新 prompt 正确 consume。

---

## 6. 待决策（请拍板）

1. **Phase 顺序**：1→2→3 顺跑 vs 1+3 并行（观测先行，拿真实世界数据指导 Phase 2 取舍）
2. **tool_choice 探测首版**：确定性 keyword（0 成本）vs 小模型分类器（更准，多一次 LLM 调用）
3. **提示词语言策略**：保留全中文 vs 关键锚点双语（后者指令遵循更高，但与项目"所有注释和日志使用中文"规范冲突）
4. **Phase 2 是否包含 websearch fallback chain**（Google/Bing/Brave）
5. **是否同期启动 longrun plan**：
   - **同期启动**（推荐）：longrun owner 在本计划 Phase 1 开工时就介入，与本计划 Phase 2 一起定 middleware interface，避免事后返工
   - **延后启动**：本计划 Phase 3 观测出数据后再开 longrun。代价是 middleware interface 可能要二次修改
   - **本计划的硬约束**：无论同期与否，Phase 2 middleware interface 必须按 §2 P0-D 的扩展点列表设计，不能为"本期只需 4 个"而简化 `AgentState`

---

## 7. 参考与调研工件

### 7.1 本仓库关键位置（已实锤）

- `internal/master/react_processor.go:217`（Temperature=0.3 硬编码）
- `internal/master/react_processor.go:325-339`（P0-A 主阀门 + P0-C 回调消费端）
- `internal/master/react_processor.go:1478-1484`（P0-D 贫血 validator）
- `internal/tools/websearch.go:112-118, 161-163, 341`（P0-B 静默假成功）
- `internal/llm/stream_completions.go:281-315`（P0-C 累积端，已 OK）
- `internal/tools/skill_search.go:131`（`trigger_keywords` 确实在用，已撤回错误断言）
- `internal/skills/spec_resolver.go:46`（Skills on-demand feature flag）

### 7.2 deer-flow 对照清单（codex 一手读，开工前需二次校验）

> **所有 file:line_range 来自 codex 576 行一手输出**；Phase 2 开工前 owner 必须亲自打开这些文件确认没飘动。
> codex 原始输出：`/tmp/deer-flow-analysis/codex-output.md`（576 行）

**主循环 / 中间件架构：**
- `backend/packages/harness/deerflow/agents/factory.py:61-147`
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py:205-358`
- `backend/packages/harness/deerflow/agents/thread_state.py:1-55`（关键：**无 sources/citations/grounding_map 字段**）

**护栏中间件三件套**（P0-D 借鉴）：
- `agents/middlewares/tool_error_handling_middleware.py:19-65`
- `agents/middlewares/dangling_tool_call_middleware.py:75-138`
- `agents/middlewares/loop_detection_middleware.py:347-356`

**Deferred tool discovery**（P0-A 借鉴）：
- `tools/builtins/tool_search.py:155-193`
- `agents/middlewares/deferred_tool_filter_middleware.py:1-60`
- `tools/builtins/task_tool.py:46-54`（动作型描述范本）

**流式 tool_call 透传**（P0-C 借鉴）：
- `models/openai_codex_provider.py:177-430`（重点 321-356）
- `client.py:260-323, 629-632`

**搜索错误显式化**（P0-B 借鉴）：
- `community/ddg_search/tools.py:15-95`（72-79 空结果 error）
- `community/infoquest/infoquest_client.py:45-404`（69-78 HTTP 错误、258-268 成功路径）

**Prompt 架构**（§3 提示词借鉴）：
- `agents/lead_agent/prompt.py:151-727`
- `subagents/builtins/general_purpose.py:1-50`

### 7.3 相关现有文档

- `docs/架构设计/安全权限模型.md`
- `docs/架构设计/skills/Skill-按需加载总览.md`
- `docs/架构设计/skills/Skill-Feature-Flag矩阵.md`
- `CHANGELOG.md` v2.4 条目

---

## 评审问题模板

请审阅者填：

- [ ] P0 四条排序和切分合理吗？
- [ ] Phase 切分能接受吗？需要拆更细？
- [ ] 验收指标是否可度量？
- [ ] 时间线是否现实？
- [ ] deer-flow 借鉴与超越的分界是否认同（抄 #B #C 的错误显式化和流式透传；自建 #A 的 forced tool_choice 和 #D 的 grounding validator）？
