# Agent 记忆反馈闭环与治理计划

> 状态：COMPLETED  
> 类型：记忆质量 / 用户反馈学习 / 注入治理  
> 目标：让记忆系统不只是保存事实，而是能可靠保存用户反馈、偏好和纠错信号，并安全影响后续行为。  
> 非目标：不做跨用户记忆共享，不做记忆编辑 UI，不做“自我进化”大而空的自动改造。

## 完成记录

> 完成日期：2026-05-07

本计划已按可验证闭环完成：

- `internal/memory/extractor.go` 支持 feedback 专用提取入口、结构化 JSON 提取器、坏 JSON 降级规则提取、非 bullet feedback 提取和治理元数据写入。
- `internal/master/reflection_evaluator.go` 已把 evaluator verdict feedback 写入 memory feedback 闭环。
- `internal/memory/injector.go` 已拆分 `## 工作方式反馈` 与 `## 相关记忆`，feedback 独立查询、独立 token 预算，并优先注入。
- `internal/config` 与 `internal/bootstrap` 已接入 feedback topK、普通记忆 topK、独立 token 预算、最低置信度和最低相关性配置。
- `internal/memory/governance_ops.go` 已覆盖 dry-run/apply、过期、低置信、跨用户风险、容量清理和人工高置信保护。
- `internal/memory/eval/testdata/` 已新增 feedback 优先级、过滤、跨用户和误分类负例，并支持期望注入顺序断言。
- `docs/运维手册/memory-feedback-governance.md` 已补充运维配置、治理统计、prune 流程和排障字段。

验证命令见本文件“完成定义”和运维手册；归档后如需扩展记忆编辑 UI、跨用户共享或自动策略优化，应另立新计划。

## 1. 问题

当前记忆系统已经具备基础能力：记忆类型、搜索、注入、治理元数据、过期/低置信过滤、评估 harness、向量和混合搜索基础。

真正还缺的是反馈闭环：

- 用户纠正、偏好、否定工具行为等高价值信号没有稳定进入 `feedback` 记忆。
- `feedback` 记忆没有独立优先注入通道，容易和普通 project/user/reference 记忆混在一起。
- 摘要提取仍偏规则化，非 bullet 格式或隐含偏好容易漏掉。
- 记忆是否真的改善下一次行为，缺少固定回归集。
- 旧记忆、低置信记忆、过期记忆的治理已有基础，但还没有形成自动运行策略和报告。

本计划把旧“AI 自我进化”收窄成一个可执行目标：

> 从会话中提取 feedback 记忆，在下一次相关任务中优先注入，并用评估集证明它没有污染上下文。

## 2. 成功标准

1. 用户纠正和偏好能稳定写入 `MemoryTypeFeedback`。
2. feedback 记忆注入在普通相关记忆之前，并有独立 token 预算。
3. 低置信、过期、跨用户风险记忆不会进入注入文本。
4. 提取器支持结构化 LLM 提取，并保留规则提取作为降级。
5. 记忆评估集覆盖正例、负例、过期、跨用户、feedback 优先级。
6. 有治理报告能说明删除/跳过了哪些记忆以及原因。

## 3. 数据模型约定

继续使用现有 `MemoryType`：

- `user`：用户偏好、长期习惯、沟通风格。
- `project`：项目事实、阶段目标、技术决策。
- `reference`：文件、链接、外部系统指针。
- `feedback`：用户纠错、AI 行为改进点、已验证/被否定的方法。

`feedback` 只记录能影响后续行为的内容，不记录普通任务事实。

示例：

```text
用户明确不想先听方案，要求直接改文件。
用户指出“完成”必须以测试输出为准，不能只说应该可以。
用户否定了某个工具路径，下次遇到同类任务应先用 rg 查证。
```

不应记录：

```text
用户今天问了 README 在哪里。
模型运行了一次 go test。
某次会话中出现了一个临时文件名。
```

## 4. 实施任务

### Task 1：feedback 记忆写入入口

涉及文件：

- `internal/memory/feedback.go`
- `internal/memory/feedback_test.go`
- `internal/master/quality_events.go`
- `internal/master/reflection_note.go`
- `internal/master/reflection_evaluator.go`

实现要求：

- 新增 `FeedbackExtractor` 或等价服务，输入为会话片段、reflection event 或 evaluator verdict。
- 只输出结构化 `MemoryRecord{Type: MemoryTypeFeedback}`。
- 写入时必须带 governance metadata：
  - `source`
  - `confidence`
  - `source_user_id`
  - `source_message`
  - `run_id`
  - `expires_at`
- 低置信 feedback 可以先进入候选，不直接注入。

验收：

- 用户纠错样例能生成 feedback 记忆。
- 普通任务事实不会被误分类成 feedback。
- 写入的 feedback 带 governance 字段。

### Task 2：feedback 优先注入

涉及文件：

- `internal/memory/injector.go`
- `internal/memory/injection_result.go`
- `internal/memory/injector_test.go`

实现要求：

- 注入文本拆成两个区块：

```markdown
## 工作方式反馈

- [feedback] ...

## 相关记忆

- [project] ...
- [user] ...
```

- feedback 单独查询，单独 token budget，排在普通记忆前。
- feedback 同样经过 user 隔离、expires_at、confidence 过滤。
- 普通记忆继续保留现有 token budget 和治理统计。

验收：

- 同时存在 feedback 和 project 记忆时，feedback 在前。
- 过期 feedback 不注入。
- 跨用户 feedback 不注入。
- 低置信 feedback 不注入。
- token 超限时能分别统计 feedback 和普通记忆跳过数量。

### Task 3：相关性阈值与治理配置

涉及文件：

- `internal/config/config.go`
- `internal/config/defaults.go`
- `internal/bootstrap/helpers.go`
- `internal/memory/injector.go`
- `internal/memory/eval/*`

建议配置：

```json
{
  "memory": {
    "inject_min_confidence": 0.5,
    "inject_min_score": 0.3,
    "feedback_top_k": 3,
    "memory_top_k": 8,
    "feedback_max_tokens": 600,
    "memory_max_tokens": 1800
  }
}
```

实现要求：

- `MinScore` 真正进入 store search 或 hybrid search 的过滤链路。
- feedback 和普通记忆的 topK/token 预算可分开配置。
- 评估集能覆盖 min score 过低导致污染的场景。

验收：

- 低相关记忆不会注入。
- 配置缺省时有明确默认值。
- 配置非法值不会导致无限注入或全部注入。

### Task 4：LLM 结构化提取器

涉及文件：

- `internal/memory/extractor.go`
- `internal/memory/extractor_llm.go`
- `internal/memory/extractor_test.go`
- `internal/memory/eval/testdata/`

实现要求：

- 新增 LLM 提取路径，输出 JSON 数组：

```json
[
  {
    "type": "feedback",
    "content": "用户要求完成声明必须附带实际验证命令和输出。",
    "confidence": 0.85,
    "evidence": "用户说：不要再说应该可以，必须跑测试。"
  }
]
```

- LLM 提取失败时降级到现有规则提取。
- LLM 输出必须校验：
  - type 合法。
  - content 非空。
  - confidence 在 0 到 1。
  - evidence 不写入注入文本，只存 metadata。
- 不允许把完整会话无限塞给提取器，必须有 token / message 上限。

验收：

- 非 bullet 摘要也能提取记忆。
- LLM 返回坏 JSON 时回退规则提取。
- feedback/user/project/reference 分类有表驱动测试。

### Task 5：记忆治理作业

涉及文件：

- `internal/memory/governance_ops.go`
- `internal/memory/governance_ops_test.go`
- `internal/api` 管理端点或现有 admin quality/memory handler
- `docs/运维手册/`

实现要求：

- 支持 dry-run 治理报告。
- 按原因分类：
  - expired
  - low_confidence
  - cross_user_risk
  - capacity
- 不自动删除人工标记的高置信记忆。
- 删除/跳过结果可被日志或管理接口查看。

验收：

- dry-run 不删除数据。
- apply 模式只删除计划内 ID。
- capacity 清理优先保留 access_count 高、updated_at 新的记忆。

### Task 6：记忆质量评估集

涉及文件：

- `internal/memory/eval/testdata/`
- `internal/memory/eval/harness.go`
- `internal/memory/eval/runner.go`
- `internal/memory/eval/*_test.go`

新增 case：

- feedback 优先注入。
- 过期 feedback 不注入。
- 跨用户 feedback 不注入。
- 低置信 feedback 不注入。
- 非 bullet 摘要可提取。
- 用户纠错能生成 feedback。
- 普通项目事实不会误进 feedback。

验收：

- `go test ./internal/memory/... -run 'Feedback|Injector|Extractor|Governance|Eval' -count=1`
- 失败时输出 case id 和失败原因。

## 5. 不做

- 不做跨用户记忆共享。
- 不做记忆编辑 UI。
- 不做每周自动“人格进化”批处理。
- 不让 LLM 自己改 prompt、skill 或系统策略。
- 不把 feedback 记忆无条件放进所有会话。

这些以后如需推进，必须基于评估数据另立计划。

## 6. 风险

### 6.1 feedback 污染行为

错误 feedback 比普通错误记忆更危险，因为它会影响工作方式。

应对：

- feedback 需要更高 confidence。
- evidence 进入 metadata，方便回放。
- 低置信 feedback 先不注入。

### 6.2 隐私与跨用户泄漏

feedback 往往包含用户偏好和纠错上下文。

应对：

- 所有注入必须检查 user_id。
- governance 中保留 source_user_id。
- eval 固定覆盖跨用户负例。

### 6.3 LLM 提取成本和不稳定

LLM 提取会增加成本，也可能返回坏 JSON。

应对：

- 提取只在 compaction / 会话结束 / quality event 后异步触发。
- 有 token 上限。
- 有规则提取降级。

## 7. 完成定义

该计划完成后，旧“记忆系统演进”方向可以归档。完成必须满足：

- feedback 写入入口落地。
- feedback 优先注入落地。
- 相关性、置信度、过期、跨用户过滤都有测试。
- LLM 提取器有降级路径。
- memory eval 覆盖新增正负样例。
- 有治理 dry-run 报告。
