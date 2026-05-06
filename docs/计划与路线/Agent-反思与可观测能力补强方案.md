# Agent 反思与可观测能力补强方案

> 状态:待评审(CEO Review 占位 §16,Eng Review 占位 §17,Design Review 占位 §18)。
> 范围:Master ReAct Agent 的**反思(Reflection)** + **可观测追踪(OTel Trace)** + **轨迹回放与结构化输出守卫**三类能力补齐;不改 Plan Runtime 状态机、不改 Skills 体系、不引入新工具家族。
> 版本基线:`qa_up` 分支(2026-04-30),已合 sessiontodo + System-Prompt 重整 + 定时任务方案。

## 1. 背景与定位

### 1.1 触发原因

用户在 2026-04-30 提出"我们的 ReAct 范式 Agent 是否有反思能力,现在有哪些能力,符不符合行业最佳实践"的诉求。我对 `internal/master` + `internal/observability` + `internal/agentquality` 等核心包进行了 10 层 ReAct 能力矩阵审计,得分 **39/46 ≈ 85%**。其中 6/10 层(感知/记忆/规划/工具/执行/学习)已达 2026 行业生产级,但 **Layer 4 反思** 与 **Layer 9 可观测追踪** 是真正的能力空白,**Layer 10 轨迹回放/结构化输出** 已有骨架但未深用。

### 1.2 2026 行业锚点(WebSearch 验证)

不再用 2023 论文(Reflexion / Self-Refine / CRITIC)做 anchor,改用 2026 实际生产模式:

| 来源 | 模式 | 数据点 |
|---|---|---|
| **Anthropic 2026-03 Enterprise Agent Playbook** | sequential / parallel / **evaluator-optimizer**(三大正交模式) | 推荐 evaluator 与 optimizer 解耦为独立子代理,成本 + 1.3× 但 +25–50% 多步任务成功率 |
| **OpenAI o3 / Anthropic Claude 4.7 thinking** | inference-time scaling(推理时扩展) | 2026 年 ≈ 2/3 AI 算力流向推理侧,reasoning_effort 调度成为一线原语 |
| **Hugging Face AI Trends 2026** | reasoning models 主流化 + PRMs(过程奖励模型) | HumanEval 80→91% 当反思 + PRM 联用 |
| **LangGraph 1.0**(2026-Q1 GA) | durable execution + state persistence + HITL | 状态机式 Agent 框架已成事实标准,trace 自动埋 |
| **Microsoft AutoGen v0.6** | multi-agent debate(蓝军/红军) | 适合高风险决策,2026 仍属前沿,ROI 不明确 |

### 1.3 我们当前的位置

| 层 | 能力 | 文件证据 | 评分 |
|---|---|---|---|
| L1 感知 | tool_call + observation 闭环 | `react_processor.go:234 runReActLoop` | 5/5 ✅ |
| L2 记忆 | extractor + governance + pgvec hybrid | `internal/memory/extractor.go` `pgvec_store.go` | 5/5 ✅ |
| L3 规划 | Plan Runtime + sessiontodo + ClaimResume(CAS) | `plan_runtime.go:485` `:833` | 5/5 ✅ |
| L4 **反思** | **仅 loopDetector(5 次重复硬停)+ 无反思 prompt 注入** | `react_processor.go:1628 hard_stop` | **2/5 ❌ P0 缺口** |
| L5 工具调用 | 15+ 工具 + tool_search + spawn_agent + parallel_dispatch | `internal/tools/*` | 5/5 ✅ |
| L6 执行控制 | Plan Runtime Guard + advisory lock + idempotency | sessiontodo 已落 | 5/5 ✅ |
| L7 安全 | sandbox + ast_parser + llm_classifier + SafeExecutor | `internal/sandbox` `internal/security` | 5/5 ✅ |
| L8 学习 | candidate_store + auto_optimization + gate_runner | `internal/agentquality/optimization.go` | 4/5 ✅ |
| L9 **可观测** | **Tracer 接口已建但 master 0 接入 + metrics 已落但无 trace 链路** | `observability.go:11 StartSpan` 但 `internal/master` grep 0 hits | **2/5 ❌ P0 缺口** |
| L10 **轨迹/Schema** | ReplayRef 已落 48 hits,**无 step-rewind / fork-from-step**;structured output 仅 6 文件零散 | `gate_runner.go:132` `llm/responses.go` | **3/5 ⚠ P1 缺口** |

合计 39/46。本方案专攻 L4 + L9 + L10。

## 2. 目标

1. **L4 反思**: 让 Agent 能在循环/连续失败/产物不达标时**自我评估并改写下一步**,而不是 hard_stop。引入 Anthropic 2026-03 推荐的 **evaluator-optimizer** 子代理模式。
2. **L9 可观测**: master 主循环 + 工具调用 + 子代理 + LLM 流全链路接入 `observability.Tracer`,产出可在 Admin UI 时间线回放的端到端 trace。
3. **L10 轨迹/Schema**: 让 ReplayRef 不只是事件指针,而是支持 **step-by-step rewind / fork-from-step / 结构化产物 schema 守卫**,把 L8 自动优化所需的训练数据补齐。
4. **整体**: 多步任务成功率(以 sessiontodo 完成率衡量)从当前 baseline 提升至 +20% 以上,生产事故定位时长(Mean-Time-To-Diagnose)从分钟级缩到秒级。

## 3. 非目标

- **不**重写 ReAct 主循环(`runReActLoop`)。
- **不**接管 Plan Runtime / sessiontodo 完成判定(`finish_plan` 仍是单一 source of truth)。
- **不**新增 LLM Provider / 不改任何工具的参数语义。
- **不**做 multi-agent debate / LATS(2026 仍前沿,ROI 待验)。
- **不**做 PRM 训练(需要标注数据 + 训练管道,本方案先用启发式评估器)。
- **不**改前端通用 UI 框架(沿用既有 React + ai-elements)。

## 4. 现状审计(代码亲读结论)

### 4.1 L4 反思现状

```text
react_processor.go:234   runReActLoop  // 主循环
react_processor.go:1596  loopDetector  // 唯一反思类机制
react_processor.go:1628  if d.consecutiveSame >= 5: return "hard_stop"  // 直接停,无 prompt
react_processor.go:1640  recordCallResult: 同 fingerprint 连续失败 ≥2 触发 early stop
```

问题:
- `hard_stop` 把 LLM 完全踢出循环,用户层面看到的是"agent 突然不动了",不是"agent 反思后换了路径"。
- `early_stop` 同上,无反思 prompt 注入,无第二意见(second opinion)。
- 没有任何"产物质量评估"环节(写代码后是否编译/测试是否过/产物是否符合 schema)。
- 没有 reasoning effort 调度(简单问题 minimal,复杂任务 high)。

### 4.2 L9 可观测现状

```text
internal/observability/observability.go:11  type Tracer interface { StartSpan(...); RecordSpan(...) }
internal/observability/pg_writer.go         hive_traces 表已存在
internal/observability/labels.go            已有 labels 标准

grep -r "StartSpan|SpanFromContext|otel\." internal/master  →  0 hits
grep -r "tracer\.Start"  internal/master                   →  0 hits
```

问题:
- 基础设施齐备但**无人使用**: master 主循环、`spawn_agent`、`parallel_dispatch`、LLM stream 全部不打 trace。
- 现有 metrics(`hive_metrics` 表)只能告诉你"出了多少错",不能告诉你"这一次会话第 7 步在 LLM 等待时挂了 9.4 秒"。
- 生产 bug 排查只能靠 zap 日志关键词搜索,跨会话/跨子代理串不起来。

### 4.3 L10 轨迹/Schema 现状

```text
ReplayRef                                    48 hits across 20 files (字段已落)
admin_quality_candidates_handlers.go:46     body.ReplayRef = sessionID + ":step-" + index  (生成规则)
agentquality/gate_runner.go                  使用 ReplayRef 关联 quality event
frontend/src/components/replay/qualityCandidate.ts  前端有展示

llm/responses.go                             有 ResponseFormat / JSONSchema 但只在少数路径用
internal/tools/*                             tool args 校验大多走 jsonschema 但 output 不校验
```

问题:
- ReplayRef 是"指针",但后端**没有**"按 step 重放上下文 / 从某 step fork 新分支"的 API。
- 前端有展示但无交互(看不到、点不动)。
- 工具产物(尤其 LLM 文本类输出)没有 schema 守卫,导致 evaluator 拿到的样本格式漂移。

## 5. 能力差距矩阵与 6 项建议(R1'–R4' + OTel + Trajectory)

> 命名遵循已 evolve 的 2026-anchor: R1'/R2' 等是 prime 标记,与早期 R1/R2(2023 论文)区分。

| ID | 能力 | 锚点 | 工作量 | ROI | 优先级 |
|---|---|---|---|---|---|
| **R1'** | reasoning_effort 调度(minimal/medium/high 自动选) | OpenAI o3 / Claude 4.7 thinking | S(3 d) | 高(降本+提质) | **P0** |
| **R2'** | evaluator-optimizer 子代理(产物质量评估 + 改写) | Anthropic 2026-03 三模式之一 | M(8 d) | 高(+25–50% 成功率) | **P0** |
| **R3'** | 测试驱动反馈环(代码类产物自动跑最小验证) | 启发式 PRM 替代 | M(6 d) | 高(代码任务可量化) | P1 |
| **R4'** | loopDetector → 反思 prompt(取代 hard_stop) | Reflexion 思想 + 2026 重构 | S(2 d) | 中(止血+不退化) | **P0** |
| **OTel** | master / 子代理 / LLM stream 全链路 trace | LangGraph 1.0 + observability 已建接口 | M(5 d) | 极高(MTTD) | **P0** |
| **Traj** | step-rewind API + fork-from-step + tool output schema | LangGraph state checkpoint + agentquality | L(10 d) | 中(L8 数据飞轮) | P1 |

合计 P0 ≈ 18 工程日,P1 ≈ 16 工程日。建议两条 worktree 并行。

### 5.1 R1' Reasoning effort 调度

- 在 `internal/master/prompt_builder.go` 后加一个轻量分类器(可硬编码规则起步): 
  - 短问答 / 单工具调用 → `minimal`
  - 多步骤计划 / 跨文件改动 → `medium`  
  - 复杂任务(`enter_plan_mode` 触发 + sessiontodo ≥ 5) → `high`
- 通过 `llm.RequestOptions.ReasoningEffort` 字段透传给支持 thinking 的 provider(Anthropic / OpenAI o-系)。
- 不支持 thinking 的 provider 自动 fallback(no-op)。
- **观测**: 新指标 `hive_reasoning_effort_total{level=minimal|medium|high,provider=...}`。

### 5.2 R2' Evaluator-Optimizer 子代理

- 新增 SubAgent: `evaluator`(打分 0-10 + 给出 actionable feedback)和 `optimizer`(消费 feedback 重写产物)。
- 触发时机(由 ReAct 主循环判断,不是 LLM 自决):
  - 复杂代码改动且 `R3'` 反馈失败时
  - sessiontodo 中标记 `quality_critical=true` 的 step 完成后
  - LLM 返回长 artifact(>500 token)且无 schema 时
- **关键**: evaluator 与 optimizer 是**独立 LLM 调用**,各拿干净上下文,避免主循环偏置(Anthropic 2026-03 强调点)。
- **预算控制**: 每个会话最多触发 3 次 evaluator(`agent.reflection.max_evaluator_per_session=3`)。
- **观测**: trace span `reflection.evaluator` + `reflection.optimizer`,带 score / verdict。

### 5.3 R3' 测试驱动反馈环

- 代码改动后(`edit` / `multiedit` / `apply_patch` / `write_file`),自动尝试运行最小验证:
  - Go: `go vet ./<changed_pkg>` + `go build` + 同包 `go test -run` 仅相关测试
  - 前端: `tsc --noEmit` + `eslint <changed>` + 相关 vitest
- 失败结果走 R2'(evaluator + optimizer 改写)。
- **关键**: 不强制全套 CI(那是 `/qa` 的事),只做最小快速反馈,目标 < 10 秒。
- 配置开关 `agent.reflection.test_driven=true`(默认 true,可单 session 关)。

### 5.4 R4' loopDetector → 反思 prompt

- 当前: `consecutiveSame >= 5 → hard_stop`
- 改为:
  - `>= 3 warn → ` 不再只 log,而是注入一段 reflection prompt 给 LLM("你已对相同输入重复 3 次,请分析为什么没进展并改变策略;若确实没有可行路径,调用 finish_plan 或 ask_user")
  - `>= 5` 才硬停,且硬停时同时**保留 reflection prompt 作为最后一条 system note**,让用户能看到 agent 的"绝望理由"。
- 同步修改 `recordCallResult`: 连续失败 ≥ 2 注入 "工具 X 用 Y 参数已连续失败 N 次,先用 read_file 确认前置条件再重试,或换工具" 提示。
- **观测**: 新指标 `hive_reflection_injected_total{trigger=batch_loop|call_failure}`。

### 5.5 OTel 全链路 trace

- 在 `internal/master/react_processor.go::runReActLoop` 顶层 `StartSpan("react.loop")`。
- 每轮 LLM 调用 + 工具调用 + spawn_agent + parallel_dispatch 起子 span。
- LLM stream 时长 + token 数计为 span attribute。
- trace_id 通过 ctx 透传到 `internal/acpclient` / `internal/llm` / `internal/tools`,跨子代理保留 parent_span_id。
- **关键**: 利旧 `observability.Tracer`,**不**引入 OpenTelemetry SDK 依赖(项目里没 go.opentelemetry.io,不要为这个加重)。
- Admin UI 新增 `/admin/trace/<session_id>` 时间线视图(纵向 swimlane: master / subagent / llm / tool)。

### 5.6 Trajectory: step-rewind + fork-from-step + tool output schema

- 后端新增:
  - `POST /api/v1/sessions/<id>/replay/<step>`: 返回该 step 时的完整上下文快照(messages + sessiontodo + memory snapshot)。
  - `POST /api/v1/sessions/<id>/fork?from_step=<n>&prompt=<...>`: 从某步 fork 一个新会话(复用记忆但重走规划)。
- 工具产物 schema:
  - 在 `internal/tools/` 每个工具注册时支持可选 `OutputSchema *jsonschema.Schema`。
  - 返回时若声明了 schema 但产物不符,走 R2' optimizer 改写一次。
- 前端 `frontend/src/components/replay/AgentTreeView.tsx`(已新建未提交)接 step-rewind API,实现"点 step 看上下文 / 拖滑块定位 / fork 按钮"。

## 6. 数据模型与配置

### 6.1 配置(config.json)

```json
{
  "agent": {
    "reflection": {
      "enabled": true,
      "reasoning_effort": {
        "auto": true,
        "default_level": "medium"
      },
      "evaluator_optimizer": {
        "enabled": true,
        "max_per_session": 3,
        "trigger_on_artifact_min_tokens": 500
      },
      "test_driven": {
        "enabled": true,
        "go_vet_timeout_sec": 10,
        "tsc_timeout_sec": 15
      },
      "loop_detector_prompt_inject": true
    },
    "tracing": {
      "enabled": true,
      "sample_rate": 1.0,
      "max_span_per_session": 2000
    },
    "trajectory": {
      "step_rewind_enabled": true,
      "fork_enabled": true,
      "tool_output_schema_strict": false
    }
  }
}
```

所有开关默认开,可一键关回当前行为(回滚路径)。

### 6.2 PG 表(增量,不破坏)

复用 `hive_traces` + `hive_metrics`。新增**反思事件**:

```sql
CREATE TABLE hive_reflection_events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    step_index INT NOT NULL,
    trace_id TEXT,
    trigger TEXT NOT NULL,           -- batch_loop / call_failure / artifact_eval / test_driven
    evaluator_score INT,             -- 0-10, NULL if not evaluator
    evaluator_feedback TEXT,
    optimizer_applied BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_refl_session ON hive_reflection_events(session_id, step_index);
```

迁移走 `internal/store/postgres_migrate.go` 的现有顺序(类似 sessiontodo)。

## 7. 阶段拆分(三阶段 + 两 worktree)

| 阶段 | 范围 | 工程日 | Worktree |
|---|---|---|---|
| **Phase 1 — Reflection 基础**(P0) | R4'(loopDetector 反思 prompt) + R1'(reasoning_effort 调度) | 5 d | `wt-reflection` |
| **Phase 2 — OTel 全链路**(P0) | OTel master + 子代理 + LLM + 工具 + Admin UI | 5 d | `wt-trace` |
| **Phase 3 — Evaluator-Optimizer + R3'**(P0 收尾) | R2' 子代理 + R3' 测试驱动 + 反思事件表 | 8 d | `wt-reflection`(续) |
| **Phase 4 — Trajectory 深用**(P1) | step-rewind / fork / OutputSchema / 前端 | 10 d | `wt-trace`(续) |

Phase 1 + 2 可完全并行(不同 worktree,代码不冲突)。Phase 3 依赖 Phase 1 落地(共享 reflection event 表)。Phase 4 依赖 Phase 2(trace_id 是 step 索引核心)。

## 8. 测试计划

### 8.1 单元测试(Go)

- `internal/master/reflection_test.go`: R4' loop 进入 prompt 注入分支不退化 hard_stop 阈值。
- `internal/master/reasoning_effort_test.go`: R1' 分类器在 minimal/medium/high 三档分别命中预期。
- `internal/agentquality/evaluator_test.go`: R2' evaluator 输出 schema 满足 `{score: int, feedback: string}`。
- `internal/observability/master_trace_test.go`: master 主循环至少触发 1 个 react.loop span。

### 8.2 集成测试(需 PG)

- 端到端跑一个 5-step 任务,断言 `hive_traces` 行数 ≥ 5,父子 span 关系正确。
- 故意触发 loop(同样 grep 5 次),断言出现 reflection_events 行 + 不退化 hard_stop。
- 跑一个代码改动任务,断言 R3' 触发了 `go vet` 且失败时 R2' 介入。

### 8.3 验收命令

```bash
env GOCACHE=/tmp/go-build go test \
  ./internal/master \
  ./internal/observability \
  ./internal/agentquality \
  -count=1 -race
```

前端:

```bash
cd frontend && npm test -- --run \
  src/components/replay/AgentTreeView.test.tsx \
  src/pages/admin/Trace.test.tsx
```

### 8.4 蓝军 mutation(per memory note)

- 把 R4' 注入 prompt 临时删除,验证集成测试**应该红**(loop 退化为 hard_stop)。
- 把 OTel `StartSpan` 调用全部 nop,验证 trace 端到端测试**应该红**。
- 把 evaluator score < 3 的兜底逻辑改成"不接受任何分数",验证 R2' 测试**应该红**。

## 9. 验收标准

1. 5-step 任务 sessiontodo 完成率从 baseline X 提升至 X + 20%(对比一周生产数据)。
2. 生产 bug 出现时,通过 `/admin/trace/<session_id>` 能在 < 30 秒内定位卡顿/失败子 span。
3. 触发循环 ≥ 3 次的会话中,有 ≥ 60% 在反思 prompt 注入后**不进入** hard_stop(成功改路径)。
4. evaluator 介入的代码改动会话中,优化后版本通过率 ≥ 1.5× 原版本。
5. 配置项 `agent.reflection.enabled=false` 时所有反思代码路径短路,行为完全等价于本方案前。

## 10. 风险与回滚

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Evaluator 误判导致 optimizer 把好产物改坏 | 中 | 中 | `max_per_session=3` 限流 + 必须保留改写前快照 + score 阈值守卫 |
| OTel 全埋导致 PG 写入压力 | 中 | 中 | 异步 fire-and-forget + sample_rate 配置 + `max_span_per_session=2000` |
| reasoning_effort=high 在 OpenAI o3 路径成本爆炸 | 中 | 高 | 仅限 plan_runtime 已激活的会话 + 单会话上限 + 默认 medium |
| R3' `go vet` 触发宿主机 GOPATH 污染 | 低 | 中 | 走 sandbox(已有 `internal/sandbox`)且只对 changed 包跑 |
| trace_id 跨子代理透传破坏既有 ctx 用法 | 低 | 高 | 优先在 `acpclient.Transport` 单点接入,其余路径渐进迁移 |

回滚路径: 单一开关 `agent.reflection.enabled=false` + `agent.tracing.enabled=false`,两个开关拍下,行为完全回退。

## 11. Follow-up(本期不做)

- [P2] PRM(过程奖励模型)训练管道 — 需要标注数据,先用 R2' 启发式评估器跑半年攒数据。
- [P2] multi-agent debate(蓝军/红军)— 2026 仍前沿,Anthropic 也未推荐生产用。
- [P2] LATS(Language Agent Tree Search)— 推理时扩展极致版,先观察 Anthropic 是否官方推。
- [P3] 反思事件 → 自动 prompt 优化(R5'`Reflexion` 全闭环)— L8 已有 candidate_store,后续接入。
- [P3] OTel 兼容 OpenTelemetry SDK 导出(用户可挂自家 Jaeger/Tempo)— 当前自有 hive_traces 够用。

## 12. 实施顺序建议

1. Phase 1 起 worktree `wt-reflection`,先落 R4'(2 d)。
2. 同时另一台机/另一窗起 `wt-trace`,落 OTel master span 接入(3 d)。
3. R4' 落地后立即生产灰度 1 周,验证不退化。
4. 灰度通过 → R1' reasoning_effort(3 d)。
5. OTel master 接入完成 → 推子代理 + LLM stream span(2 d) + Admin UI 时间线(3 d)。
6. R2' + R3' 进入 Phase 3,与 Phase 4 trajectory(P1)并行。
7. 全部完成 → README 增加 Reflection / Trace bullet,docs/运维手册新增排障章节。

## 13. 业务驱动场景(占位 — 待用户/产品填充)

> 本节空着,等 OV(Outside Voice)讨论时填。沿用定时任务方案 OV10 教训:**先有具体场景再排能力优先级**,反过来一定塌方。
> 候选场景示例(待 ICP 验证):
> - 长链路代码迁移(改 5 个文件,中途某个 build 失败应自动反思而不是停)。
> - 跨子代理大任务(Master + 3 个 explore + 1 个 quality_eval),生产事故时定位需要全链 trace。
> - 高风险自动化(批量 PR 评审),evaluator-optimizer 二次审能直接降事故。

## 14. PromptLoader 与 i18n(对齐 System-Prompt 重整方案)

R2' evaluator / optimizer 子代理的 system prompt 走 `i18n.LoadEmbeddedPrompt`,新增:

```
internal/i18n/prompts/system/reflection_evaluator.md
internal/i18n/prompts/system/reflection_optimizer.md
internal/i18n/prompts/system/reasoning_effort_classifier.md  (R1' 启发式可选)
```

`promptSmokeKnownKeys` 在 `internal/api/admin_quality_handlers.go` 新增对应 key。

## 15. 跟既有方案的边界

| 既有方案 | 关系 | 边界 |
|---|---|---|
| sessiontodo / Plan Runtime | **复用**: reflection event 引用 step_index | reflection 不接管 finish_plan |
| System-Prompt 重整 | **复用**: i18n 嵌入 + 文件即 fallback | 反思 prompt 是新文件,不动现有 7 段 |
| Agent-质量护栏治理 | **复用**: candidate_store + ReplayRef | reflection 给 candidate 喂数据,不改其评测器 |
| Agent-工具发现与候选召回 | 无依赖 | — |
| 定时任务系统 | 无依赖 | reflection 不影响 cron tick |
| Agent-长时运行能力 | **协同**: trace 帮助定位长任务卡点 | 不改长任务的 checkpoint 语义 |

---

## 16. CEO Review 决策清单(占位)

> 本节由 `/plan-ceo-review` 在评审日填写。整体判定预期:**真改善 + 至少 1 处工程治理升级**。

### 决策点(待评审填写 D1–DN)

- D1: R4' loopDetector → 反思 prompt 是否真的不退化 hard_stop 安全网?
- D2: R2' evaluator-optimizer 触发条件 是 LLM 自决 还是 主循环硬规则?
- D3: R3' 测试驱动 是否在 sandbox 内跑 还是 主进程跑?
- D4: OTel 是 PG 自有 还是 接 OpenTelemetry SDK?
- D5: Trajectory step-rewind 是 P1 还是该提到 P0(取决于 L8 数据需求)?
- D6: 业务驱动场景 §13 是否需要先做 office-hours 再决定 P0/P1 边界?
- D7: 反思事件表 是否需要 retention 策略(默认无,可能膨胀)?

### 可能的升级项(待蓝军挑战)

- U1: R2' 是否要支持"用户在 UI 看到 evaluator 反馈并人工 override optimizer"的 HITL 入口?
- U2: trace_id 是否要与既有 sessionID 强绑定(便于 admin UI 路由)?

## 17. Eng Review 决策清单(占位)

- ER1: trace_id 透传是用 context.WithValue 还是新 struct? 跨进程子代理(acpclient)如何保留 parent_span?
- ER2: evaluator/optimizer 调用的 LLM 计费如何打 label? 是否单独限流?
- ER3: 反思事件表的 step_index 与 sessiontodo.step_index 是否要做外键?(默认不做,弱一致即可)
- ER4: Admin UI trace 时间线是用现有 ECharts 还是新引入(如 d3-timeline)? 性能 budget?
- ER5: R3' `go vet` 失败信号如何 normalize 给 evaluator?(纯文本 vs 结构化)

## 18. Design Review 决策清单(占位)

- DR1: Admin UI trace 时间线在 DESIGN.md 现有色板下的 swimlane 如何呈现?
- DR2: reflection event 是否在 chat 主流中以"Agent 反思了"的轻量条目展示给用户?
- DR3: fork-from-step 按钮放在 step 详情页还是 trace 时间线上?
- DR4: reasoning_effort=high 的视觉提示(thinking 中的"深度思考"标识)如何设计?

---

## 附:GSTACK REVIEW REPORT(框架占位)

```text
PLAN: docs/计划与路线/Agent-反思与可观测能力补强方案.md
SCOPE: SELECTIVE EXPANSION (feature enhancement on existing system)
INDUSTRY ANCHOR (2026):
  - Anthropic 2026-03 Enterprise Agent Playbook (sequential / parallel / evaluator-optimizer)
  - LangGraph 1.0 (durable execution + state persistence + HITL)
  - Hugging Face AI Trends 2026 (reasoning models + PRMs, HumanEval 80→91%)
  - OpenAI o3 / Claude 4.7 thinking (inference-time scaling, 2/3 of 2026 AI compute)

EVIDENCE (file:line):
  - L4 gap: react_processor.go:1628 hard_stop 无 prompt 注入
  - L9 gap: internal/master grep "StartSpan|otel\." = 0 hits 但 observability.go:11 已有 Tracer
  - L10 gap: ReplayRef 48 hits but no step-rewind/fork API

PROPOSALS: R1' R2' R3' R4' + OTel + Trajectory (6)
P0: R1' + R2' + R4' + OTel  (≈18 d)
P1: R3' + Trajectory       (≈16 d)
P2 follow-up: PRM / debate / LATS / Reflexion 全闭环 / OTel SDK 导出

PHASES: 4
RISKS: 5 (evaluator 误判 / OTel 写压 / o3 成本 / R3' 沙箱 / trace_id ctx)
ROLLBACK: agent.reflection.enabled=false + agent.tracing.enabled=false 双开关

PENDING REVIEW: §13 业务场景占位 + §16-§18 OV/ER/DR 决策点
```
