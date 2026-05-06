# Agent 工具发现与候选召回计划

> 状态：待评审  
> 范围：Master Agent 的隐藏工具发现、`tool_search` 检索质量、每轮工具候选召回。  
> 核心约束：**不扩大主 system prompt 的工具清单，不把长尾工具默认暴露给模型，不为每个业务平台写硬编码提示词。**

## 1. 背景

当前系统已经有 `tool_search`，也已经把长尾工具从默认模型候选集中收窄出去：

- `internal/master/tool_visibility.go`：默认只暴露核心工具和少量质量杠杆工具，长尾工具需要被 `tool_search` 发现后才进入会话可见集。
- `internal/tools/tool_search.go`：提供只读工具搜索能力，模型可通过它发现隐藏工具。
- `internal/tools/feishu_tools.go` / `internal/tools/sendim.go`：飞书出站能力实际存在，但不默认可见。

这个设计方向是对的：工具会越来越多，不能把所有工具说明写进 system prompt，也不能让每轮 LLM 请求携带全部工具 schema。

问题在于现在的 `tool_search` 仍是**被动工具**。当用户说“发送给飞书用户:郭松”时，服务器侧没有任何确定性机制把相关隐藏工具召回为候选；模型如果没有主动调用 `tool_search`，系统就不会帮助它发现 `feishu_api` 或 `send_im_message`。这让“隐藏工具发现”退化成“模型自觉搜索”，稳定性不够。

## 2. 问题定义

### 2.1 不是模型能力问题

现代模型具备足够强的自然语言理解能力。它应该能理解“发送给飞书用户:郭松”包含：

- 动作：发送消息
- 平台：飞书
- 目标：用户“郭松”
- 副作用：外部系统出站发送，需要工具执行和权限审批

如果模型没有使用工具，更可能是上下文里的工具候选和发现机制没有给它足够好的操作面，而不是需要在 prompt 里继续写更多“遇到飞书就调用某某工具”的规则。

### 2.2 不是补主 prompt

不能把以下内容继续塞进主 system prompt：

- 飞书发送路径说明
- 微信/Jira/GitHub/Slack 等平台触发词
- 每个长尾工具的使用手册
- “如果用户说 A，就调用 B”式规则表

这会导致 prompt 随工具数量线性膨胀，而且规则会过时、冲突、互相污染。

### 2.3 当前缺口

当前缺口分两层：

1. **候选召回缺失**：每轮开始时没有基于用户原文自动召回隐藏工具候选。
2. **检索质量不足**：`tool_search` 只按 name/description 做简单子串匹配，自然句如“发送给飞书用户:郭松”不一定能匹配到相关工具。

## 3. 目标

1. 保持主 prompt 稳定短小，不新增平台级工具说明。
2. 保持长尾工具默认隐藏，不回退到“全量工具每轮暴露”。
3. 让工具发现从“模型主动想起要搜”升级为“系统自动召回少量相关候选”。
4. 让 `tool_search` 和自动召回共用同一套工具索引，避免两套检索逻辑漂移。
5. 对外部副作用工具保持现有权限审批和 plan mode gate，不绕过安全层。
6. 可以灰度、可观测、可回滚。

## 4. 非目标

- 不新增 `send_feishu_message` 这类平台专用捷径工具。
- 不把 `feishu_api` / `send_im_message` 加入全局默认可见工具。
- 不在 system prompt 里增加飞书、微信或其他平台的特殊说明。
- 不改变 HITL 权限策略。
- 不改变工具实际执行语义。
- 不引入复杂 agent planner 或外部编排框架。

## 5. 推荐架构

### 5.1 工具目录索引层

新增一个工具目录索引，职责是把已注册工具转换成可检索文档。

索引输入来自 `mcphost.Host.ListTools()`，每个工具构建一条 `ToolCatalogEntry`：

```go
type ToolCatalogEntry struct {
    Name        string
    Description string
    Schema      json.RawMessage
    Tags        []string
    Aliases     []string
    Verbs       []string
    Domains     []string
    SideEffect  bool
    Core        bool
}
```

首版不要求 embedding 服务。先做本地轻量索引：

- 工具 name 分词：`send_im_message` -> `send`, `im`, `message`
- description 分词：中文按 rune bigram + 英文 token
- schema 字段名分词：`chat_id`, `content`, `platform`
- 注册时可选元数据：`Domains=["feishu","im"]`, `Verbs=["send","message"]`

后续可以把同一接口升级为 embedding 检索，但首版不绑定外部 provider。

### 5.2 每轮候选召回

在构造 `llm.ChatWithToolsRequest` 前，对最新用户消息做一次只读召回：

```go
recall := toolCatalog.Recall(ctx, ToolRecallRequest{
    Query: latestUserQuery,
    Limit: 5,
    IncludeHidden: true,
})
```

召回结果不直接执行工具，只把 Top-K 工具临时加入本轮可见工具候选：

```go
visible := modelVisibleToolsForSession(session, availableTools)
visible = mergeToolCandidates(visible, recall.Tools)
```

注意：这不是把所有隐藏工具暴露给模型，只是把当前用户消息相关的少量工具作为候选给模型选择。

### 5.3 `tool_search` 复用同一索引

`tool_search` 不再自己做子串扫描。它调用同一个 `ToolCatalog.Recall`：

```go
hits := catalog.Recall(ctx, ToolRecallRequest{
    Query: in.Query,
    Limit: in.Limit,
    IncludeHidden: true,
})
```

这样自动候选召回和模型主动搜索使用同一套排序、同义词、元数据，不会出现“自动召回找得到，tool_search 找不到”或反过来的漂移。

### 5.4 会话发现状态保持不变

现有 `session.RecordDiscoveredTools()` 机制保留：

- 自动召回只影响当前轮候选，不永久污染会话。
- 模型显式调用 `tool_search` 并成功返回后，仍记录 discovered tools，使后续轮次可见。

这样区分：

- **临时候选**：系统基于当前消息召回，当前轮可用。
- **已发现工具**：模型通过 `tool_search` 明确发现，后续轮次可见。

### 5.5 安全边界

候选召回不得绕过任何安全层：

- `EvaluatePlanToolGate` 仍然过滤 plan mode 不允许的工具。
- `PermissionManager` / HITL 仍然审批副作用工具。
- 工具执行仍走 `executeTool` 主路径。
- 召回层只返回工具定义，不执行工具、不访问外部系统。

## 6. 关键设计原则

### 6.1 工具复杂度进索引，不进 prompt

工具越多，复杂度应增长在：

- 工具元数据
- 工具目录索引
- 检索排序
- 观测和评估集

而不是增长在主 system prompt。

### 6.2 模型负责判断，系统负责候选供给

模型仍然负责判断是否需要调用工具、调用哪个工具、如何组合工具。

系统只负责把“可能相关的隐藏工具”放进当前轮候选区，避免模型在没有候选工具的情况下凭空回答。

### 6.3 首版保持本地、确定、可测

首版不依赖 embedding API，不增加网络依赖。用本地 token/bigram/元数据打分实现，保证测试稳定。

## 7. 实施计划

### Phase 1：抽象工具目录接口

**目标**：建立独立于 `tool_search` 的工具目录索引接口。

涉及文件：

- 新增：`internal/tools/catalog.go`
- 新增：`internal/tools/catalog_test.go`
- 修改：`internal/tools/tool_search.go`

接口草案：

```go
type ToolCatalog interface {
    Recall(ctx context.Context, req ToolRecallRequest) ([]ToolRecallHit, error)
}

type ToolRecallRequest struct {
    Query         string
    Limit         int
    IncludeHidden bool
}

type ToolRecallHit struct {
    Tool  mcphost.ToolDefinition
    Score float64
    Why   []string
}
```

验收：

- 空 query 能列出工具。
- “飞书 发送 用户”能召回 `feishu_api` 和 `send_im_message`。
- “读文件”能召回 `read_file`。
- “删除工具”能召回 `remove_tool`，但不执行。

### Phase 2：增强工具检索质量

**目标**：让自然语言请求能匹配工具，不依赖模型手动改写 query。

涉及文件：

- 修改：`internal/tools/catalog.go`
- 修改：`internal/tools/catalog_test.go`

首版匹配策略：

- name token 命中高权重。
- description 命中中权重。
- schema 字段名命中中权重。
- 中文 bigram 命中低权重。
- alias/domain/verb 命中高权重。

基础元数据可以先从工具名和描述推断，必要时在注册工具时附加轻量元数据，但不要写进主 prompt。

验收查询：

| Query | 期望 Top-K |
|-------|------------|
| `发送给飞书用户:郭松` | `feishu_api` / `send_im_message` |
| `给 IM 群发一条通知` | `send_im_message` |
| `查一下飞书文档内容` | `feishu_api` |
| `创建飞书任务` | `feishu_api` |
| `搜索代码里的函数` | `grep` |
| `读取 README` | `read_file` |

### Phase 3：每轮自动候选召回

**目标**：在不扩大默认工具集的前提下，把相关隐藏工具加入当前轮候选。

涉及文件：

- 修改：`internal/master/react_processor.go`
- 修改：`internal/master/tool_visibility.go`
- 新增或修改：`internal/master/tool_visibility_test.go`

行为：

1. 从 prepared messages 提取最新用户文本。
2. 调用工具目录召回 Top-K。
3. 与 `modelVisibleToolsForSession` 的结果合并。
4. 继续经过 plan mode gate。
5. 只影响当前轮，不写入 discovered tools。

验收：

- 未调用 `tool_search` 时，“发送给飞书用户:郭松”这一轮的 LLM 工具候选包含飞书相关工具。
- 普通闲聊不召回副作用工具。
- plan mode 下仍不暴露发送类工具。
- 已发现工具仍按原机制跨轮可见。

### Phase 4：观测与灰度

**目标**：可评估召回是否提高工具使用率，并能快速回滚。

涉及文件：

- 修改：`internal/config/config.go`
- 修改：`internal/config/defaults.go`
- 修改：`internal/master/react_processor.go`
- 修改：质量事件或日志相关文件

配置建议：

```json
{
  "agent": {
    "tool_recall": {
      "enabled": false,
      "limit": 5,
      "min_score": 0.35
    }
  }
}
```

观测字段：

- `tool_recall.enabled`
- `tool_recall.query_preview`
- `tool_recall.candidate_count`
- `tool_recall.candidate_names`
- `tool_recall.selected_tool`
- `tool_recall.model_used_recalled_tool`

灰度：

1. 默认关闭，只在测试配置打开。
2. 开启 read-only 观测模式：记录召回结果但不注入 LLM tools。
3. 小流量开启注入。
4. 观察误召回、副作用审批量、工具调用成功率。

### Phase 5：Agent Quality 回归集

**目标**：把这类问题沉淀为质量用例，防止后续工具增加后退化。

涉及文件：

- 新增：`internal/agentquality/testdata/aq_tool_recall_feishu_send.json`
- 修改：`internal/agentquality` 相关 loader 或 runner 测试
- 可选：新增 `cmd/agentquality` fixture

用例至少覆盖：

- “发送给飞书用户:郭松”
- “把刚才结论发给飞书用户郭松”
- “通知飞书群 oc_xxx：任务完成”
- “给微信联系人张三发消息”（用于验证跨平台泛化，不写平台 prompt）

验收指标：

- 召回 Top-K 命中率 >= 90%。
- 模型最终工具调用命中率 >= 80%。
- 普通非工具请求误召回副作用工具率 <= 5%。

## 8. 风险与应对

### 8.1 误召回副作用工具

风险：用户只是讨论“飞书消息怎么写”，系统召回发送工具。

应对：

- 召回只提供候选，不执行。
- 副作用工具仍走 HITL。
- min_score 和 Top-K 控制候选数量。
- 观测模式先上线。

### 8.2 工具元数据维护成本

风险：每个工具都要手写 tags/aliases，维护成本变高。

应对：

- 首版从 name/description/schema 自动抽取。
- 只有召回效果差的工具才补元数据。
- 元数据与工具注册同处维护，不进 prompt。

### 8.3 候选过多污染模型判断

风险：Top-K 太大导致模型误选。

应对：

- 默认 Top-K=5。
- Core tools 仍按原逻辑可见，不重复计入召回上限。
- 对副作用工具提高 min_score。

### 8.4 与 `tool_search` discovered 状态混淆

风险：自动召回后工具跨轮持续可见，造成隐性扩权。

应对：

- 自动召回不写 `session.RecordDiscoveredTools()`。
- 只有显式 `tool_search` 成功才记录 discovered tools。

## 9. 开放问题

1. `ToolCatalog` 应放在 `internal/tools` 还是 `internal/master`？
   - 倾向 `internal/tools`：`tool_search` 和 Master 都需要用。
   - 注意避免 tools 反向依赖 master。

2. 工具元数据应扩展 `mcphost.ToolDefinition`，还是维护 sidecar registry？
   - 倾向先 sidecar：减少对 MCP host 基础结构的侵入。
   - 如果后续多个模块都需要元数据，再提升到 ToolDefinition。

3. 自动召回命中的工具是否应在 UI/日志里展示？
   - 倾向展示在质量日志，不直接打扰用户。
   - Debug UI 可显示“候选工具召回”用于排障。

4. 是否需要 embedding 检索？
   - 首版不需要。
   - 当工具数量超过数百、关键词召回不够时再引入。

## 10. 审核重点

请重点审核以下问题：

1. 这个方案是否真正避免了主 prompt 膨胀？
2. 自动候选召回是否会破坏“隐藏工具需要发现”的安全边界？
3. `tool_search` 与自动召回复用同一索引是否会引入包依赖问题？
4. 首版本地检索是否足够，还是应直接做 embedding？
5. `ToolCatalog` 的归属和接口是否合理？
6. 观测和灰度是否足够支撑线上试运行？

