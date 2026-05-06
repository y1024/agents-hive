# Agent 长时执行能力计划（30 分钟续航）

> 状态：**SUPERSEDED** — 本计划不再推进。
> 后续方案：**[Agent-全局破局与长时续航演进方案.md](./Agent-全局破局与长时续航演进方案.md)**

---

以下为原始内容，保留作历史背景。

---

## 0. TL;DR

**本计划解决的问题**：让 Agent 能**自动执行 30 分钟以上**的复杂任务（100-200 轮 ReAct），而不是每轮答对事实。后者由 remediation plan 解决。

**一句话诊断**：30 分钟续航不是"时间问题"，是**有限上下文 × 非确定性决策序列 × 无分层架构**的叠加约束。当前 agents-hive 主循环是裸 `for`，没有预算门控、无外部记忆层、无 auto-dispatch——这三条都是续航的必要条件。

| P0 | 缺什么 | 对应第一性原理 | 依赖 |
|----|--------|--------------|------|
| L-1 | 主循环 max_turns + token budget 门控 | #1 有限上下文 / #5 确定性外壳 | 独立 |
| L-2 | TodoList 结构化外部记忆 | #2 压缩有损 / #4 记忆层次 | remediation middleware pipeline |
| L-3 | 复杂步骤 auto-dispatch sub-agent | #4 记忆层次（工作内存切分） | remediation middleware pipeline |
| L-4 | 启用 openspec planner（legacy → active） | #5 确定性外壳 | remediation 提示词重写完成后 |
| L-5 | 工具级 timeout / retry / parallel | #3 非确定性坍塌 / #6 串行 I/O | 独立 |
| L-6 | Checkpoint 每 N 轮落盘 | #3 错误回退必需 | 独立 |

**时间线 15-20 天**，四阶段。**验收核心指标**：100-200 轮复杂任务跑完率 0% → ≥ 70%。

---

## 1. 前因：为什么要独立一份计划

### 1.1 两份计划的分工

| 维度 | remediation plan | 本计划 |
|------|-----------------|--------|
| 解决的问题 | 单轮（~30 秒）事实正确性 | 多轮（>10 分钟）自我续航 |
| 症状 | "女娲.skill 胡编 URL" | "跑到第 50 轮上下文爆，或提前停" |
| 核心手段 | tool_choice + grounding + websearch 显式化 | TodoList + budget 门控 + sub-agent 分派 + planner 状态机 |
| 不做什么 | 不动分层架构 | 不动单轮精度 |

**这两类问题是不同维度，彼此不替代**：单轮精度做到 p=0.99，100 步连续正确概率仍只有 37%。必须叠加架构层。

### 1.2 六条第一性原理（压缩版）

详见 remediation plan 的原理讨论，这里只列结论：

1. **有限上下文 × 线性追加** → 必然触壁（30 分钟 ≈ 120K-720K 净增）
2. **压缩非线性丢损**（一次 90% → 三次 73%）
3. **非确定性决策指数坍塌**（p=0.95, 100 步 = 0.6%）
4. **记忆层次**：人类三层（工作/长期/外部），裸 ReAct 一层
5. **确定性外壳包非确定性内核**：预算/状态/retry 必须代码保证
6. **串行 I/O 累加**（90-180 轮理论上限）

### 1.3 agents-hive 代码事实（grep 验证）

| 维度 | 现状 | 位置 |
|------|------|------|
| 主循环 max_turns | **零匹配**（主循环无硬顶） | `internal/master/react_processor.go` |
| Token budget | 仅 planner 有（`tokenBudget int64`） | `internal/specdriven/ingress/runner.go:101` |
| TodoList | **零匹配** | 全仓 |
| Sub-agent auto-dispatch | 存在但手动触发；MaxTurns=50 | `internal/subagent/agentloop.go:96,110` |
| Openspec planner | 代码齐全但 `mode=legacy` 默认关 | `internal/master/session_loop_specdriven_*` |
| 并行 tool call | **零匹配** | 全仓 |
| Checkpoint | 会话级 PostgreSQL 持久化存在，步骤级重放**未验证** | `internal/store/` |

---

## 2. P0 六条（Problem Cards）

### L-1：主循环 max_turns + token budget 门控

**现状**：`react_processor.go` 主循环是裸 `for`，退出条件完全靠 LLM 自己说"答完了"。无 turn 硬顶、无 token 预算感知。

**修什么**：
- 加 `cfg.Agent.LongRun.MaxTurns`（默认 100，可调至 300）
- 加 `cfg.Agent.LongRun.TokenBudget`（默认 150K，留 context 50K 余量）
- 触顶策略：不是硬停，而是触发「**切换到 summarize + todo 续跑**」的状态转移
- 实现为 `BudgetGuardMiddleware`，插在 remediation middleware pipeline 的 `before_model`

**对应原理**：#1（有限上下文）+ #5（确定性外壳）

**验收**：人为构造 200 轮任务 → 不再无脑撞到底，在第 ~90 轮主动触发 summarize 续跑

### L-2：TodoList 结构化外部记忆

**现状**：主循环只有 messages[] 一层记忆。长任务中 LLM 只能靠"把 TODO 写进自然语言回复 + 期待自己在后续轮次里还记得"，这在 3-5 次压缩后必失效。

**修什么**：
- 新增 `TodoWrite` / `TodoRead` 两个工具（参考 Claude Code 同名工具）
- 状态持久化到 agent state（不走 LLM summary 通道，即使 compact 也保留）
- 实现为 `TodoListMiddleware`，`before_model` 注入当前 TodoList 到 prompt，`after_model` 解析模型对 TODO 状态的更新

**对应原理**：#2（压缩有损）+ #4（记忆层次——长期记忆层）

**参考 deer-flow**：`agents/middlewares/` 同目录有 TodoList 实现（codex 分析中提到 "TodoList middleware for plan mode"，具体文件 Phase 1 owner 二次校验）

**验收**：模型在第 50 轮能准确复述第 5 轮定下的 TODO 状态

### L-3：复杂步骤 auto-dispatch sub-agent

**现状**：`internal/subagent/agentloop.go` 的 sub-agent 存在（MaxTurns=50），但主循环**不会自动**把复杂步骤派出去。所有 tool output 直接进主 context，哪怕是 20K token 的文件 dump。

**修什么**：
- 加入 `SubagentDispatchMiddleware`（`before_model` / `wrap_tool_call`）
- 触发规则（保守起步）：
  - 预期 tool result 单次 > 5K token（如读大文件、长 grep）
  - 单轮 TODO 带 ≥ 3 步子任务
  - 用户显式请求"研究/探索"类任务
- Sub-agent 返回**结构化 summary**（不是原始 tool output）进主 context
- 利用已有 `Explore` sub-agent 能力

**对应原理**：#4（记忆层次——工作内存切分）

**验收**：同样 100 轮任务，主 context 消耗从 X tokens 降到 X/3（sub-agent 分担）

### L-4：启用 openspec planner（legacy → active）

**现状**：`internal/master/session_loop_specdriven_*` 有完整 planner 代码（test:129 证实 `token_budget=800` 默认），但 `mode=legacy` 默认关，主路径零启用。这是**仓库里已有的确定性状态机**，不启用浪费了 ~6K 行代码。

**修什么**：
- 把 openspec 从"维护债"重新定位为"待激活能力"
- 设计激活门控：`cfg.Agent.LongRun.PlannerMode = "auto" | "always" | "legacy"`
- `auto` 规则：任务预估 > 10 轮或用户显式请求 plan-first 时激活 planner
- 依赖 remediation plan 的提示词重写完成（planner 产出的 plan 要能被新 prompt 正确 consume）

**对应原理**：#5（确定性外壳——状态机）

**风险**：openspec 代码久未主路径使用，可能有腐烂。Phase 1 要做「openspec 现状健康度评估」子任务。

**验收**：用 10 个复杂任务集跑 auto 模式，planner 正确激活率 ≥ 80%

### L-5：工具级 timeout / retry / 并行

**现状**：全仓 grep `ParallelToolCalls` / `ToolTimeout` / `RetryTool` **全部零匹配**。单工具卡 60s = 主循环卡 60s。

**修什么**：
- 每个工具加 `DefaultTimeout`（bash 300s，HTTP 工具 30s，本地 IO 5s）
- 失败 retry：网络类工具 3 次 exponential backoff（1s / 4s / 16s）
- 并行 tool call：LLM 一次返回多个 tool_calls 时并发执行（OpenAI API 本身支持，agents-hive 目前串行）

**对应原理**：#3（非确定性坍塌——retry 降错率）+ #6（串行 I/O 打破上限）

**预期收益**：30 分钟理论上限从 90-180 轮提升到 300+ 轮（tool 并行化 + 失败不拖累主循环）

**验收**：10 工具并发任务端到端耗时从 Y 秒降到 Y/3

### L-6：Checkpoint 每 N 轮落盘

**现状**：会话级持久化存在（PostgreSQL），但"第 50 轮挂了能否从第 45 轮 replay"**没验证过**。

**修什么**：
- 每 N 轮（默认 10）序列化完整 state（messages + TodoList + sub-agent state）到 `session.checkpoints[]`
- 崩溃恢复：从最近一个 checkpoint 继续跑，丢最多 N-1 轮
- 用户显式请求"从第 X 步重做"也走这条路径
- 实现为 `CheckpointMiddleware`（`after_model`）

**对应原理**：#3（错误回退必需）

**验收**：kill -9 main.go 后重启，session 能从最近 checkpoint 续跑，state 完整

---

## 3. 不做什么

- ❌ **不引入 LangGraph 或等价 Python 生态**。agents-hive 是 Go，middleware pipeline 自建
- ❌ **不把 ReAct 改成硬编码 FSM**。planner 是 opt-in 的确定性外壳，内核保持 ReAct
- ❌ **不做静态 multi-agent DAG**（planner→researcher→reporter 这种）。deer-flow 自己都没做，我们也不做
- ❌ **不预先优化**：并行 tool call、checkpoint 这类在 benchmark 未跑前不做激进调优
- ❌ **不做用户态长时任务调度**（cron、queue）。本计划只管"一个任务跑 30 分钟"，不管"多个任务排队"

---

## 4. 交付计划

### 4.1 依赖 / 前置

- **硬依赖**：remediation plan Phase 2 完工——其 `Middleware` interface + Pipeline 容器是本计划 L-2/L-3/L-6 的插入点
- **软依赖**：remediation plan 提示词重写完成——L-4 planner 激活需要新 prompt 能正确 consume plan 结构

### 4.2 四阶段

| 阶段 | 内容 | 预估 | 前置 |
|------|------|------|------|
| Phase 1 — 基础设施 | L-1 budget 门控 + L-5 工具 timeout/retry/并行 | 4-5 天 | 独立 |
| Phase 2 — 外部记忆 | L-2 TodoList + L-6 Checkpoint | 4-5 天 | remediation Phase 2 完工 |
| Phase 3 — 分层架构 | L-3 sub-agent auto-dispatch | 3-4 天 | Phase 2 完工 |
| Phase 4 — 状态机激活 | L-4 openspec planner（含健康度评估子任务） | 4-6 天 | remediation 提示词重写完工 |
| **合计** | | **15-20 天** | |

### 4.3 验收矩阵

| 指标 | 现状 | 目标 |
|------|------|------|
| 100-200 轮复杂任务跑完率 | 0%（会爆 context 或提前停） | ≥ 70% |
| 30 分钟连续执行成功率 | ~0% | ≥ 50% |
| 主 context 峰值占用（100 轮任务） | ~满（爆） | ≤ 60% |
| 工具并行化加速比（10 工具任务） | 1x | ≥ 2.5x |
| 崩溃后 checkpoint 恢复成功率 | 不支持 | ≥ 95% |
| 单步错误回退发生率 | 0%（无回退） | 有埋点，真实数据 |

### 4.4 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| openspec 代码腐烂（久不用） | 高 | L-4 延期或废弃 | Phase 1 并行做健康度评估 |
| TodoList 引入 LLM 纪律负担，模型不写 todo | 中 | L-2 形同虚设 | Prompt 强制规则 + 少轮次 reward test |
| Sub-agent dispatch 过度，主循环"啥也不干" | 中 | UX 倒退 | Phase 3 保守触发规则 |
| 并行 tool call 引入工具副作用冲突 | 低 | 数据错乱 | 并行仅限只读工具（read/grep/lsp），写工具串行 |
| Checkpoint 落盘拖慢主循环 | 中 | 每轮 +100ms | 异步落盘，不阻塞主循环 |

---

## 5. 待决策

1. **TodoList 实现层级**：L-2 工具 (`TodoWrite`/`TodoRead`) 模式 vs 原生 middleware 模式
   - 工具模式：LLM 可见、易调试，但多一次 tool_call 成本
   - Middleware 模式：隐式维护，成本低，但模型不可见导致写入率低
2. **Openspec 激活策略**：L-4 `auto` 触发阈值（多少轮以上启用 planner）
3. **Sub-agent dispatch 边界**：L-3 是否让 LLM 自己决定 dispatch（通过工具），还是完全由 middleware 自动
4. **Checkpoint 粒度**：L-6 每 N 轮 = 10 vs 5 vs 动态（按 token 增量）
5. **是否复用 remediation plan P0-A 的 tool_choice 探测器**：用同一套 task classifier 判断"是否需要 planner / sub-agent"

---

## 6. 参考

### 6.1 本仓库关键位置

- `internal/master/react_processor.go`（主循环，本计划 L-1 主改）
- `internal/subagent/agentloop.go:96,110`（MaxTurns=50 上限）
- `internal/master/session_loop_specdriven_*`（openspec planner，L-4 激活对象）
- `internal/specdriven/ingress/runner.go:101`（现存 tokenBudget 参考实现）
- `internal/store/`（session 持久化，L-6 扩展基础）

### 6.2 对照系统

- **Claude Code**（`TodoWrite` 工具 + sub-agent dispatch 作为 L-2/L-3 实现参考）
- **deer-flow**（`agents/middlewares/` 的 TodoList middleware + SubagentExecutor，remediation plan §7.2 清单已列）
- **LangGraph** `checkpointer` 抽象（L-6 设计参考，**不引入依赖**，只看设计）

### 6.3 对 remediation plan 的反向修订

本计划定稿后，remediation plan 需同步 4 处修订（见 remediation plan 讨论记录）：

1. §4 「不做什么」中 "不重构 DAG" 和 "不改 openspec" 的口径改为"本期不做"
2. §2 P0-D middleware 设计改为 "pipeline + 首批 4 实现"，预留扩展点
3. §5 新增 §5.5 "与 longrun plan 的协作关系"
4. §6 新增决策项 "是否同期启动 longrun plan"

---

## 评审问题模板

- [ ] L-1 到 L-6 的优先级排序合理吗？
- [ ] Phase 依赖关系是否最小化？可否更多并行？
- [ ] 验收指标是否可度量（特别是"跑完率"的定义需要任务集基准）？
- [ ] openspec 激活是否值得做？还是直接放弃？
- [ ] 是否同意 L-2 的"工具模式"作为默认实现？
