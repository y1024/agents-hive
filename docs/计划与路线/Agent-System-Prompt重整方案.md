# Agent System Prompt 重整方案

> 状态：待评审。  
> 范围：只调整 Master Agent 的 system prompt 装配与 `internal/i18n/prompts/system/*` 内容边界，不改 Plan Runtime 状态机、不改工具执行语义。

## 1. 背景

当前 Master system prompt 由 `internal/master/prompt_builder.go` 拼接：

```text
system/base
system/execution
system/business
system/code_editing
system/safety
system/reply
```

PromptLoader 已支持 `DB > 文件 > go:embed > hardcoded/default`。系统能力上已经引入 Plan Runtime / session todos，并且默认开启。当前问题不是缺少 PromptLoader，而是 prompt 职责边界开始混乱：

- `execution` 继续承载工具选择、并行、迭代、计划态会变胖。
- `business` 硬编码业务场景表，和动态 Skills 列表重复，长期维护成本高。
- `reply` 同时承载回复风格、artifact、研究要求，偏重。
- `prompt_builder.go` 内的 hardcoded fallback 和文件版 prompt 已经漂移，PromptLoader 未注入时模型行为可能不同。
- Plan Runtime 默认开启后，模型需要明确知道什么时候自动进入计划模式，但不应该把状态机实现细节塞进 `execution`。

## 2. 目标

1. 让 system prompt 从“大段混合规则”变成稳定、短小、职责明确的段落。
2. Plan Runtime 成为默认能力，但其提示词独立管理，避免污染通用执行策略。
3. 保持 prompt 总体体积不明显增加，优先删重复和下沉细节。
4. 修正 hardcoded fallback 漂移，避免加载链路不同导致行为不同。
5. 保留 PromptLoader 三层治理能力，继续支持 DB/file 覆盖、版本归因、smoke eval。

## 3. 非目标

- 不重写 ReAct loop。
- 不改 `todo_write` / `finish_plan` / `enter_plan_mode` / `exit_plan_mode` 的工具实现。
- 不改变 Plan Runtime Guard 的完成判定。
- 不引入新的审批机制。
- 不把所有工具使用手册写进 system prompt。
- 不把 Skills 的领域知识复制进 system prompt。

## 4. 推荐最终结构

按以下顺序装配：

```text
system/base
system/execution
system/plan_runtime     # 仅 agent.plan_runtime.enabled=true 时注入
system/business
system/code_editing
system/safety
system/reply
```

### 4.1 `system/base`

职责：身份、总体能力、工作方式。

保留短文本即可：

```md
## 身份定义

你是 Hive，一个具备工具调用能力的 AI 助手。你直接完成用户任务，不需要委派给其他系统。
你的核心能力：代码开发、文件操作、系统运维、信息检索、项目管理。
你的工作方式：分析任务 -> 选择工具 -> 执行 -> 验证结果 -> 回复用户。
```

不放工具白名单、不放计划态、不放领域业务。

### 4.2 `system/execution`

职责：通用执行策略。

应覆盖：

- 默认直接执行，不把简单任务过度委派。
- 不确定时先问用户。
- 需要独立并行时用 `parallel_dispatch` 或 `spawn_agent`。
- 需要不常用工具时先 `tool_search`。
- 工具调用后基于结果继续，不要求一次性规划所有步骤。
- 连续无进展时停止并报告状态。

不应覆盖：

- `todo_write` 生命周期。
- `finish_plan` 完成条件。
- 具体业务触发词表。
- 具体工具参数说明。

### 4.3 `system/plan_runtime`

职责：Plan Runtime 与 session todos 的模型行为约束。

只在 `m.config.PlanRuntime.Enabled == true` 时注入。推荐内容：

```md
## Plan Runtime

复杂任务需要自动进入计划模式，不需要等待用户显式要求“制定计划”。

进入计划模式的典型场景：任务包含多个步骤、跨文件或跨系统修改、需要验证或回归、需要并行 agent、用户要求继续推进/全部完成/按计划实施，或当前轮次无法一次完成。

简单问答、单次只读查询、单文件小改动、纯讨论任务，不要创建 session todos。

使用计划模式时：
- 先调用 `enter_plan_mode`，再用 `todo_write` 写入完整当前计划。
- 执行中用 `todo_write` 更新 todo 状态。
- 准备开始实际修改前，调用 `exit_plan_mode` 进入 executing。
- 未完成时不要假装完成；保留 pending/in_progress todo，等待继续或 Resume。
- 只有所有 todo 都是 completed/cancelled 后，才能调用 `finish_plan`。

LLM 本轮结束不等于任务完成；active plan 的完成以 session todos 和 plan_status 为准。
```

刻意不写：

- `runtime_epoch`。
- CAS 实现细节。
- EventBus / DB 表结构。
- Guard 状态机全表。
- 前端同步实现。

这些属于代码和测试，不属于模型行为提示。

### 4.4 `system/business`

职责：业务领域路由。

当前硬编码表应收缩。推荐改成：

```md
## 业务场景与 Skill 路由

当用户任务属于明确领域场景时，优先根据可用 Skills 的名称、描述、领域和触发词选择合适的 `skill`。

如果没有匹配的 Skill，直接用通用工具完成任务，不要臆造不存在的 Skill。
如果不确定有哪些 Skill，可调用 `skill` 查看摘要。
Skill 返回的规范优先于本段通用路由提示。
```

原因：

- 具体场景应由 Skills 元数据和 Skill 内容维护。
- system prompt 不应该固化“小红书/ROI/会议纪要”等业务表。
- 动态 Skills 列表已经在 `buildToolPrompt` 里注入，重复表会过时。

### 4.5 `system/code_editing`

职责：代码编辑基本规范。

保持短约束：

```md
## 代码编辑规范

- 修改代码前先读取相关文件，理解现有结构和调用方。
- 优先做精确编辑，避免无必要的整文件重写。
- 不要回滚用户已有改动，除非用户明确要求。
- 修改后尽量运行最小相关验证，并报告验证结果。
```

### 4.6 `system/safety`

职责：必要审批和安全边界。

强调“危险操作才审批”，避免权限治理变成摩擦：

```md
## 安全规范

- 删除文件、清空数据、数据库写入、部署上线、发送外部消息等不可逆或外部副作用操作，执行前需要确认。
- 普通只读查询、代码搜索、读取文件、非破坏性分析默认放行。
- 涉及凭据、密钥、隐私数据时只报告必要结论，不泄露原文。
- 优先用只读命令确认状态，再执行有副作用操作。
```

### 4.7 `system/reply`

职责：最终回复格式和 artifact。

建议压短 artifact 规则：

```md
## 回复规范

- 直接回答问题，不解释隐藏推理过程，除非用户要求。
- 执行操作后简要说明结果、关键改动和验证情况。
- 遇到错误时先尝试修复；无法修复时说明阻塞原因和下一步。
- 引用外部信息时标注来源；不确定的结论明确标记不确定性。

## Artifact 输出规范

用户要求生成完整文档、完整代码文件、HTML 页面或 PPT 大纲时，用 artifact 标签包裹。
简短回答、修改说明、步骤说明不使用 artifact。
用户要求修改现有文件时，直接修改文件并说明改动，不用 artifact。
```

不再在 system prompt 中放完整 XML 示例，减少 token 和模型误输出风险。

## 5. 代码改动点

### 5.1 `internal/master/prompt_builder.go`

调整装配：

```go
writePrompt("system/base", fallbackBase)
writePrompt("system/execution", "")
if m.config.PlanRuntime.Enabled {
    writePrompt("system/plan_runtime", "")
}
writePrompt("system/business", "")
writePrompt("system/code_editing", "")
writePrompt("system/safety", "")
writePrompt("system/reply", "")
```

### 5.2 embedded fallback(本次 CEO review D1=A 升级)

**根治路径**:删除 `buildSystemPromptHardcoded()` 的第二份 `b.WriteString` 文案，Master 在 PromptLoader 未注入时直接复用 `internal/i18n` 已有 `//go:embed prompts` 的内置 `.md` 文件。

```go
func (m *Master) buildSystemPromptWithMeta(tools []mcphost.ToolDefinition) systemPromptBuild {
    var b strings.Builder
    for _, key := range systemPromptKeys(m.config.PlanRuntime.Enabled) {
        content := i18n.LoadEmbeddedPrompt(key)
        b.WriteString(content)
    }
    ...
}
```

**收益**:
- `internal/i18n/prompts/system/*.md` 文件即 fallback,**单一数据源**,物理不可能漂移
- 删除 57 行 hardcoded 文案,代码干净
- 未来 prompt 演化只改 .md 文件,不需双写

**实施细节**:
- 在 `internal/i18n/prompt_embed.go` 暴露 `LoadEmbeddedPrompt(relPath string)`，不在 `internal/master` 包新增第二套 embed
- 加 `TestEmbeddedSystemFilesExist` 守卫 — build 时若 `system/<key>.md` 缺失,测试失败
- 删除 §5.2 原方案的"提取最小 fallback 常量按顺序拼接"
- `PlanRuntime.Enabled=false` 时 keys 列表不含 `plan_runtime`,自然不注入

**前置项目变更**(仍要做):
- 删除 hardcoded 中已废弃的 `代码库探索任务通过 task 工具委派给 explore Agent` 旧规则
- 补上 `system/plan_runtime.md` 文件本身(§4.3 内容)

### 5.3 Prompt smoke 已知 key

`internal/api/admin_quality_handlers.go` 的 `promptSmokeKnownKeys` 增加：

```go
"system/plan_runtime": {},
```

否则管理台无法保存/评测该段 prompt。

### 5.4 Prompt 管理 UI

如果 UI 中有静态 prompt key 列表，需要把 `system/plan_runtime` 加入 system 分组。

注意：`internal/webui/dist/` 是构建产物，不手改。

## 6. 测试计划

### 6.1 Go 单测

新增或更新 `internal/master/prompt_builder_test.go`：

- 默认配置下 prompt 包含 `Plan Runtime`、`todo_write`、`finish_plan`。
- 显式 `PlanRuntime.Enabled=false` 时不包含 `todo_write` / `finish_plan` 的行为指导。
- prompt 不再包含业务硬编码表中的固定关键词矩阵。
- prompt 不再包含旧 fallback 的 `代码库探索任务通过 task 工具委派给 explore Agent，不要自己逐文件读取`。
- prompt meta `Versions()` 包含 `system/plan_runtime@...`。
- hardcoded fallback 与 loader 路径的关键行为一致。

更新 `internal/api/admin_quality_handlers_test.go`：

- `system/plan_runtime` 是已知 key，prompt smoke 不阻塞。

如改 UI 源码，更新对应 frontend 测试。

### 6.2 推荐验证命令

```bash
env GOCACHE=/tmp/go-build go test ./internal/master ./internal/i18n ./internal/api -count=1
```

如修改前端 prompt 管理页面：

```bash
cd frontend && npm test -- --run src
```

## 7. 验收标准

- 默认开启 Plan Runtime 时，模型能看到自动计划模式和 todo 生命周期提示。
- 关闭 Plan Runtime 时，模型不会被提示调用 plan/todo 工具。
- `business` 不再硬编码具体业务场景表。
- `execution` 不承载 Plan Runtime 状态机细节。
- hardcoded fallback 与 PromptLoader 路径不再出现关键行为漂移。
- Prompt 管理和 smoke eval 能识别 `system/plan_runtime`。
- 单测覆盖默认开启、显式关闭、版本 meta、旧规则消失。

## 8. 风险与约束

- DB 中如果已有旧 prompt 覆盖，代码内置文件修改不会自动覆盖 DB 内容；需要评审后决定是否清理对应 DB override。
- 删除 artifact 示例可能影响少量生成类任务的格式稳定性；如果回归失败，可恢复一个极短单行示例。
- `business` 收缩后依赖 Skills 元数据质量；如果 Skills 元数据缺失，路由召回可能下降，需要通过 Skill 管理补齐。
- 本方案不解决 `parallel_dispatch progress -> todos` 自动映射，那是 Plan Runtime 后续能力，不应混在 prompt 重整里。

## 9. 建议实施顺序

1. 新增 `system/plan_runtime.md`。
2. 收缩 `system/business.md` 和 `system/reply.md`。
3. 更新 `prompt_builder.go` 条件装配。
4. 同步 hardcoded fallback。
5. 更新 prompt smoke key 和 UI key 列表。
6. 补测试并运行最小验证。


---

## 10. CEO Review 决策清单(2026-05-04 /plan-ceo-review)

本节由 `/plan-ceo-review` 在 2026-05-04 追加。整体判定:**8/10 真改善 + 1 处工程治理升级**。

### 真实效果评估(基于代码证据)

| 维度 | plan 自评 | 实际证据 |
|---|---|---|
| prompt 职责清晰 | ✅ | ✅ 真改善 |
| plan_runtime 独立段 | ✅ | ✅ 真补缺(`config.go:398` 默认 enabled=true,但当前 7 段 prompt 全文搜不到 plan_runtime/enter_plan_mode 任何提示)|
| business 收缩 | ✅ | ✅ 修伪能力 bug(`hive_skills` 0 行 + `skills/` 只 1 个,当前硬编码 6 个 Skill 全不存在,prompt 引导调用 100% 失败)|
| hardcoded fallback 修漂移 | ✅ | ⚠️ 治标不治本 → 升级 D1 |
| artifact 删 XML 示例 | ✅ | ⚠️ 软改动无 eval(进 P3 follow-up)|
| 回退路径 | — | ❌ 未列 → P2 follow-up |
| 总体积不增加 | ? | 未量化(token 数前后未对比)|

### 1 项决策升级(D1)

**D1**:hardcoded fallback **重构成复用 `internal/i18n` 已有 embedded prompts**，**根治漂移**。

```go
// Master 通过 i18n.LoadEmbeddedPrompt("system/base") 读取同一份 .md 文件。
// 57 行 hardcoded 文案删除，文件即 fallback，物理不可能漂移。
```

详细落地见 §5.2(已升级)。

### 已修正的我之前误判

- **G2/E3 误判**:之前判断"business 收缩 → 业务能力丢失"。**修正**:Skills 是用户/运营页面录入的运营内容,不是代码资产。当前硬编码 6 个 Skill 全不存在 = **伪能力 bug**;收缩 prompt 让模型按实际 Skills 触发词路由 + fallback 通用工具 = **修 bug**。
- **G4 误判**:之前判断"PlanRuntime 默认 false 与 plan 描述矛盾"。**修正**:`config_test.go:52` 守卫 `Enabled=true` 是 default,plan 描述与代码一致。

### 进 TODOS.md 的 follow-up(3 项)

- `[P2] system prompt: LLM eval(prompt 改动效果验证)`
- `[P3] system prompt: reply XML 示例删除回归 eval`
- `[P2] system prompt: 上线回退手册`

### 已确认本期不做

- 完全重做 Skills 实例(运营/产品职责,非 prompt 重整 PR 范围)
- 量化 token 数前后对比(可在实施时由 `prompt_builder_test.go` 加 `TestPromptSizeWithinBudget` 顺手做)
- dashboard / alert(plan §6 已规划测试覆盖,够用)

### 实施状态 checklist

- [x] D1 落地:复用 `internal/i18n` embedded prompts + 删除 hardcoded 文案
- [x] 删除已废弃 explore Agent 规则(plan §6.1 已守卫)
- [x] 新增 system/plan_runtime.md(plan §4.3 内容)
- [x] PlanRuntime.Enabled=false 时 keys 列表不含 plan_runtime(保持 §5.1 条件注入)
- [x] 加 `TestEmbeddedSystemPromptFilesExist` 守卫 build time 校验
- [x] 加 `TestBuildSystemPrompt_EmbeddedFallbackMatchesPromptLoader` 守卫 system 段两条加载路径行为一致(plan §6.1 已要求)
- [ ] 进 P2 follow-up 跑 LLM eval 验收
