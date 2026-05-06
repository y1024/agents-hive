# Memory And Context Roadmap

> **定位**：长会话与记忆深治理
>
> **优先级**：P2
>
> **目标**：让 memory、compaction、context injection 提升任务成功率，而不是制造隐性污染
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 代码前提

这条路线不是从零建设 memory。

代码里已经有：

- `internal/memory/pg_store.go`：Postgres memory store，user isolation，access count，异步 embedding 双写。
- `internal/memory/pgvec_store.go`、`vecindex.go`：pgvector/内存向量索引。
- `internal/memory/hybrid.go`：FTS + vector 的 RRF 混合检索。
- `internal/memory/extractor.go`：从 compaction summary 自动抽取 memory。
- `internal/memory/injector.go`：按 user message 检索并注入相关记忆，带 topK 和 token budget。
- `internal/compaction`：LLM summary、truncate、session memory、tool budget 等压缩能力。
- `internal/master/prompt_builder.go`：用户输入、附件转换、system prompt、tool prompt 和上下文组装入口。

所以本路线图的重点是治理现有链路，而不是再建一个 memory 系统。

## 2. 当前核心风险

### 2.1 Memory 写入缺少证据链

Extractor 主要基于 summary bullet 和关键词分类，能抽取事实，但缺少：

- 来源 span/message。
- 置信度。
- 事实类型 schema。
- 生命周期。
- 冲突处理。
- 用户可审计入口。

这会导致低质量 summary 被长期写入 memory，并在未来会话中污染模型。

### 2.2 Memory 注入缺少污染控制

Injector 会把检索结果格式化为 `## 相关记忆` 注入上下文，但目前需要加强：

- 相关性阈值。
- 过期策略。
- 冲突记忆处理。
- 跨用户/跨 session 泄漏回归。
- 注入内容对工具选择的影响评测。

### 2.3 Compaction 不是质量黑盒

Compaction 已存在，但必须回答：

- 压缩后事实是否丢失。
- tool result 是否被错误总结。
- 用户偏好是否被误当成指令。
- 旧上下文是否污染当前任务。
- spec state / task state 是否被保留。

### 2.4 Embedding 双写一致性

`pg_store.go` 中 memory 保存后异步生成 embedding。异步双写是合理的，但必须可观测：

- embedding backlog。
- embedding 失败率。
- vector store 与 memory store 一致性。
- provider/model 切换后的向量空间兼容性。

## 3. 施工顺序

### 3.1 Memory record 增强

在不破坏现有 store 的前提下增强 metadata：

- `source_session_id`
- `source_message_id`
- `source_span_id`
- `source_kind`
- `confidence`
- `extracted_by`
- `prompt_version`
- `expires_at`
- `supersedes`

先写入 metadata，不急着改主表 schema。

### 3.2 Memory eval

建立 memory 专用评测：

- 事实召回：给定历史任务，能否召回正确事实。
- 污染检测：无关 memory 是否被注入。
- 冲突检测：新旧事实冲突时是否优先新事实。
- 隔离检测：不同 user 的 memory 不能互相召回。
- 注入影响：注入 memory 后工具选择和最终答案是否更好。

### 3.3 Compaction eval

建立压缩前后对比：

- 用户偏好保留。
- 文件路径保留。
- 待办和未完成事项保留。
- 已完成事项不再误导当前任务。
- tool error 和失败原因保留。
- spec/task state 不丢失。

### 3.4 注入策略治理

先做保守策略：

- topK 和 maxTokens 按任务类型配置。
- 低置信度 memory 默认不注入，只作为候选。
- 过期 memory 默认不注入。
- 冲突 memory 带冲突说明，不静默合并。
- 注入内容必须带来源摘要，便于调试。

### 3.5 Embedding 一致性治理

必须补：

- embedding provider/model 写入 metadata。
- vector dimension 校验。
- provider 切换时禁止静默混用向量空间。
- embedding failure/backlog metric。
- 纯 FTS fallback 的质量对比。

## 4. 不做什么

- 不重建 memory store。
- 不引入第二套长期记忆系统。
- 不为了“更智能”扩大默认注入量。
- 不静默切换 embedding provider。
- 不让 compaction summary 直接变成高置信长期事实。

## 5. 验收指标

- Memory 相关 golden tasks 通过率。
- 长会话事实召回率。
- 无关 memory 注入率。
- 跨用户 memory 泄漏率为 0。
- Compaction 后事实丢失率。
- Embedding backlog 和失败率可见。
- Memory 注入后任务成功率提升，而不是只提升 token 消耗。

## 6. 和主线的关系

- 近期服务 `AGENT-QUALITY.md`：只做能提升主 Agent 成功率的 memory/context 治理。
- 中期形成独立系统能力：memory 生命周期、审计、冲突处理。
- 长期支撑 multi-agent、ACP、spec-driven 和自我改进。
