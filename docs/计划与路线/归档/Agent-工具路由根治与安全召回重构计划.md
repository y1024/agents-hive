# Agent 工具路由根治与安全召回重构计划

> 状态：已完成(P0/P1 本期闭环)；仅保留 P1+ 增量项  
> 优先级：P0  
> 创建日期：2026-05-07  
> CEO Review:2026-05-07 (mode=SCOPE_EXPANSION, 7/7 提案接受)  
> Eng Review:2026-05-08 (3 D-decisions, Codex outside voice 后二次修订:禁止危险 baseline 扩散、rollback 统一到 observe、LLM rewrite 移出 P0)  
> CEO Plan 副本:`~/.gstack/projects/chef-guo-agents-hive/ceo-plans/2026-05-07-tool-routing-2026-sota.md`  
> Test Plan:`~/.gstack/projects/chef-guo-agents-hive/guoss-qa_up-eng-review-test-plan-20260507-185700.md`  
> 取代：`docs/计划与路线/归档/Agent-工具召回稳定化计划.md` 的后续增强项。旧计划解决了配置化和观测，但保留了弱文本召回直接注入可调用工具的架构风险。

## 0. 2026-05-07 CEO Review 决策与升级范围

### 0.0 完成快照(2026-05-08)

本计划本期已完成：

- `skill-creator` 本地 workflow、模板与验证脚本已落地，创建 skill 意图不再落空。
- `skill` 与 MCP 是 typed capability 下的不同 domain：`skill-creator=skill_authoring`，`mcp-builder=mcp_server_building`。
- 弱文本召回不再直接授权；`mode=inject` 也必须经过 `RouteDecision`。
- `tool_search` 明确 discovery-only，并返回 `kind/domain/source/invocation/route_status`。
- description/name/schema sanitizer 已 fail-closed，prompt-injection 命中不会参与授权。
- 普通 skill 调用不再自动执行 bundled `scripts/`。
- `IntentClassifier` 规则 fallback、cache、budget guard、LLM 接口 scaffold 已落地，默认无外部 key 可运行。
- CapabilityGate 已落地为显式 capability 求交；风险标签不会自动授予能力。
- `quality.route_decision`、DecisionSpan、本地 JSONL replay 已落地。
- ReflectionBlock 已按 session/mode 接入 RouteDecision；结构性工具失败会写 block，timeout/network 等瞬态失败不会写。
- route eval checked-in corpus 已扩到 26 条，含 prompt-injection 5+，CI workflow 已新增。

本期不再把以下 P1+ 项作为未完成阻塞：OTEL collector/exporter 部署、LLM description rewrite、YAML capability source-of-truth/codegen、vector recall、LLM rerank、weekly 100+ 对抗扩集、前端 explainability 面板。

### 0.1 真实失败链路(用户实测)

```
用户:  "创建一个 XXX 技能"
召回:  "技能(→skill alias)" + "创建" 命中 mcp-builder description
       ("creating high-quality MCP servers")
注入:  mcp-builder 进入 model-visible tools (mode=inject)
模型:  把"创建技能"错误迁移成 MCP server/tool 实现问题,把 `mcp-builder` 当成 top-1 候选 → 直接调用
执行:  InvokeFull 自动跑 mcp-builder/scripts/* 中所有脚本(无白名单)
```

### 0.2 CEO Review 揭露的 3 个最高杠杆问题

1. **Product Gap P0(致命):** `skill-creator` 在本仓库和 `~/.claude/skills` **都不存在**。原计划假定它是"创建技能"的正确路由对象,但没有它,IntentCreateSkill 路由出空集,用户体验从误调用 → 无响应,**更差**。
2. **2026 P0 安全洞:** MCP tool description 是 prompt-injection 主战场(2025 Q4 已有公开案例:Practical DevSecOps / Elastic Security Labs)。原计划完全没提 description sanitization。
3. **Migration 隐藏断路:** 原计划若直接把默认工具召回模式切到 `observe`,但 `defaultModelVisibleTools` baseline 8 工具不含 feishu_api/send_im_message/wechat_*/bash —— 这些工具今天全靠 per-turn recall 浮上来。**二次修订结论:** 不能通过扩危险 baseline 修,否则只是把风险从 per-turn recall 搬到常驻可见集。正确做法是先保留 legacy/inject 存量行为,在 shadow/observe 中上线 RouteDecision,评测达标后再切默认。

### 0.3 升级范围(D1/D2/D3 决策)

D1(范围):**B - 2026 SOTA 完整版** —— 在原 7 Task 基础上增加 P0 + U1〜U6  
D2(性能/成本):**A - 全量** —— budget guard + intent cache + hybrid sanitize  
D3(评测+capability):**A - 严门 + 细粒度** —— recall<90%/bypass>0% 拦 PR + `<surface>.<action>.<scope>` 命名空间

| ID | 升级项 | 工程量(human / CC+gstack) |
|---|---|---|
| **P0** | 先建 skill-creator 本地工作流(模板向导) | 2 天 / ~30 分 |
| 原 1-7 | 原计划 7 Task(三段式 + observe 默认 + bundled scripts 修复) | 2-3 周 / ~5-7 工作日 |
| **U1** | IntentClassifier hybrid (可配置轻量模型 + 规则提字段 + fallback + cache + budget guard) | 1 周 / ~2 小时 |
| **U2** | Tool description sanitization (P0 regex/normalize/block only; LLM rewrite 为 P1 opt-in) | 2 天 / 30 分 |
| **U3** | Capability-based authz(替换 legacy trust list 与 isSideEffectTool) | 1 周 / ~2 小时 |
| **U4** | Decision span + 本地 replay + 8 metric + 4 alert；OTEL collector/exporter 为 P1 | 3 天 / ~1 小时 |
| **U5** | Reflection 反馈环 wire 回 RouteDecision(per-session,标 mode) | 2 天 / 30 分 |
| **U6** | 红蓝对抗评测框架 + 25+ 种子 case + 4 核心指标 + CI 严门；P1 weekly job 持续扩到 100+ | 1 周 / ~3 小时 |

总工期(human):4-6 周。CC+gstack 压缩:8-12 完整工作日。

### 0.4 关键校准(Section-级发现)

- **Section 1(架构)**: `tool_visibility.go` 应迁出 `internal/master`,放进 `internal/router`。IntentClassifier 默认必须支持纯规则模式;LLM 模型通过配置/现有 router 选择,不得硬编码供应商模型。
- **Section 2(错误)**: `CapabilityGate` 解析失败 fail-closed + emit `quality.route_decision.errors`(原计划缺)。`DescriptionSanitizer` 失败显式 fail-closed(防工程师误改)。
- **Section 3(安全)**: 加 `ToolNamePolicy.Validate()`(name 也是攻击向量)。IntentClassifier 用 system prompt 边界 + user message 作为结构化 input。Schema 字段也走 sanitization。capability overrides/grants/denies 支持 env var override。ReflectionBlocks 必须 per-session,不可全局。
- **Section 4(数据流)**: 空消息 / 50KB 消息 / 重复 tool call / Reflection 列表过长 / catalog 抖动 / mode 切换 reflection 误承袭 —— 7 个 edge case 全部 scope 内修。
- **Section 5(代码质量)**: 必须删 `pruneGenericIMWhenFeishuDomainEntryRecalled`(band-aid)、`isSideEffectTool` hardcoded list(替换为 capability tag)、`defaultModelVisibleTools` map(改配置驱动)。
- **Section 6(测试)**: 首批评测套必须含 25+ 种子 case,其中 prompt-injection 红蓝 case 5+；P1 weekly 扩集到 100+。checked-in prompt-injection corpus bypass_count = 0, recall@5 ≥ 90%, false-positive callable rate ≤ 2%, IntentFrame 准确率 ≥ 92%。CI 严门:不达标拦 PR；weekly 扩集只做趋势告警,确认后的新增 case 再进入 gating corpus。
- **Section 7(性能)**: IntentClassifier 200ms timeout + 强制降级。message_hash + session_id 缓存 10min TTL,预期命中率 30%。日 budget guard 阈值默认 $50,可配置。
- **Section 8(可观测性)**: Decision span schema(intent.kind/confidence/source/degraded, candidates, allowed, blocked, reasons, session_id_hash, trace.id),本期写本地 trace/replay 队列,OTEL collector/exporter 生产接入列 P1。8 metric:`hive_intent_classify_total`、`hive_intent_classify_duration_seconds`、`hive_intent_classify_degraded_total`、`hive_route_decision_total`、`hive_route_decision_blocked_total`、`hive_tool_description_sanitize_total`、`hive_capability_gate_eval_duration_seconds`、`hive_intent_classify_cost_usd_total`。4 alert:Degraded > 10% 5min / Cost > $10 1h / BlockedRate > 30% 10min / SanitizeBlockRate > 20%。
- **Section 9(部署)**: Migration 4 阶段(legacy/shadow → route_decision-observe → route_decision-default → eval-gated),feature flag `agent.tool_routing.engine = legacy|observe|route_decision`。Phase 0 生产默认暂保留 legacy/inject 以避免断路,只新增 shadow observe；RouteDecision 评测达标后才切默认。生产 rollback 统一到 `observe` 或 `tool_recall.mode=off`;`legacy/inject` 仅保留为本地开发调试开关,不作为生产回滚目标。
- **Section 10(长期)**: Reversibility 2/5(主路径数据格式变化)。Capability 命名空间一旦发布难改 → 必须 ADR。
- **Section 11(设计)**: 后端为主。UI explainability(为什么没出现/用了)推 P1,与前端排期。

### 0.5 Migration 4 阶段(关键!)

```
Phase 0 (今天):  legacy mode=inject (默认), 风险存量
                       │
Phase 1 (Task 0/6/1): 止血 + shadow observe
                      不扩危险 baseline;不改变生产默认
                      tool_search 显式 discovery-only
                       │
Phase 2 (Task 2-4 + U1〜U5): IntentFrame + ToolProfile + RouteDecision 上线
                             feature flag agent.tool_routing.engine = "route_decision"
                             route_decision 先 observe,评测通过后默认启用
                       │
Phase 3 (Task 5 + U6):       评测严门 CI 强制
                              新 case 进回归集, recall<90%/bypass>0% 拦 PR
                       │
Phase 4 (P1 增量):            vector recall + LLM rerank + tool plan validator
```

生产回滚顺序:`engine = "observe"` → `tool_recall.mode = "off"`。`engine = "legacy"`/旧 `inject` 只允许本地开发或临时诊断,不得作为生产事故回滚目标。

### 0.6 Capability 命名空间设计(D3 选 A 后必须 ADR)

格式:`<surface>.<action>.<scope>`

- `fs.read.local` / `fs.write.local` / `fs.delete.local`
- `fs.read.workspace` / `fs.write.workspace`
- `external.read.feishu` / `external.write.feishu` / `external.send.feishu`
- `external.read.wechat` / `external.write.wechat`
- `runtime.exec.shell` / `runtime.exec.script`
- `meta.skill.create` / `meta.skill.modify` / `meta.tool.register` / `meta.tool.remove`
- `network.fetch` / `network.search`

工具与 IntentKind 通过 capability 求交集授权,不再用 hardcoded 工具名表。

### 0.8 Eng Review 决策(2026-05-07,Codex outside voice 后)

D1(eng):**B - yaml 配置** —— Capability 映射存 `config/capability.yaml`,启动加载 + JSON schema 校验 + codegen 生成代码常量(yaml 是 source of truth)  
D2(eng):**A - Secondary intent + cassettes** —— IntentFrame 含 `MainIntent` + `SecondaryIntents []IntentKind`;CI 默认 cassettes(零费用零密钥);weekly job 跑真实配置模型 50 条金标准 case 检测模型升级退化  
D3(eng + Codex):**A - 全接 5 条 tension + T2 MVP-first**

| 调整 | Codex 论点 | 落实 |
|---|---|---|
| T1 | Task 1 baseline 扩展 = 把危险面从 per-turn 移到 default,等于没修 | 二次修订:禁止扩危险 baseline;Task 1 改为 shadow observe + discovery-only,生产默认等 RouteDecision 评测通过后再切 |
| T2 | SOTA full 走远了,现有 PermissionRules + 简单 gate 可能 90% 价值 30% 成本 | 改 MVP-first 实施顺序: Phase 0(MVP) → 评测验证 → Phase 1+ 按需补 SOTA U |
| T3 | 4 套策略(DefaultPermissionRules + ToolPolicy + master_direct + Capability)drift 必然 | 加 **Task -1: Policy unification ADR**,在 U3 capability 之前先定 4 套合并/收敛策略 |
| T4 | LLM rewrite description = 把攻击者控制文本喂另一个模型,P0 风险 | U2 默认只用 regex + normalization;LLM rewrite 退 P1 + opt-in flag;**plan 加硬规则**: description 永不用于 authorization,只用于显示 |
| T5 | 50 case 1 周不现实,2-3 周 reality | 改 25 条种子 + 评测框架 1 周 + LLM 对抗扩集 P1 持续补;指标"bypass rate trend"(weekly job)替换"bypass=0 over 10 cases"(theatrical) |

### 0.9 调整后实施顺序(MVP-first)

```
Phase 0 (MVP, 1-2 周):
  - Task 0:    skill-creator 本地工作流(P0,product gap)
  - Task 6:    skill bundled scripts 白名单(P0 安全洞)
  - Task 1':   Task 1 改为 shadow observe
                1a: 保留生产默认 legacy/inject,新增 shadow observe 事件
                1b: 不扩危险 baseline;tool_search 标 discovery-only
  - Task -1:   Policy unification ADR(在 capability 之前定 4 套合并策略)
  - 微调:     PermissionRules 补漏(若 ADR 建议)+ tool_search 显式标 "discovery only"
                                        ↓
              Phase 0 验收: 真实失败链路(创建技能 → mcp-builder)是否复现?
              修复成功 → 继续 Phase 1 补 SOTA;不够 → 评估 SOTA 必要性

Phase 1 (SOTA 骨架, 2-3 周):
  - Task 2:    internal/router/{intent,profile,decision} 包结构 + Stub 实现
  - Task U2':  DescriptionSanitizer regex only(LLM rewrite → P1 opt-in)
  - Task U3:   Capability based authz(yaml 配置, codegen, ADR 已定收敛策略)
  - Task 3:    替换 per-turn inject 为 RouteDecision

Phase 2 (智能层 + 观测, 1-2 周):
  - Task U1:   IntentClassifier hybrid(可配置轻量模型 + 规则 fallback + cache + budget + breaker)
  - Task 4:    扩展质量事件(EventRouteDecision)
  - Task U4:   Decision span + OTEL emit(注:OTEL collector 部署是独立 infra 工作,本期只 emit + 本地 trace)
  - Task U5:   Reflection 反馈环

Phase 3 (评测 + CI 严门, 1-2 周):
  - Task 5+U6':  25 条种子 + 评测框架 + recall/bypass/accuracy 三指标 + cassettes CI gate
  - Task U6 P1:  LLM 对抗扩集(weekly job)持续扩到 100+

Phase 4 (文档 + 归档):
  - Task 7:    架构文档 + 迁移说明 + 本计划归档

总工期(human, 调整后):6-9 周(Phase 0 验收后可能缩到 3-4 周如果 MVP 够用)
```

### 0.10 Codex 反馈中已反驳/部分反驳的项

- **#3(plan 自相矛盾,§4.1 说规则,U1 说 LLM)**:Codex 误读。我们 Phase 1/2 才接 LLM,§4.1 描述的是 IntentFrame 作为类型的"第一个版本",U1 在后续 Phase 2 才上线。Plan §0.9 实施顺序明确分阶段。**不是矛盾,是 Codex 在 plan 内部读单段时缺上下文。**
- **#9(rollback 到 legacy 不安全)**:接受指出。但**rollback 不应回 legacy = 回原 inject**。改:rollback 应回 **observe**(本 Phase 0 的安全态),不是 legacy(漏洞态)。Plan 加这一条。
- **#15(OTEL 是独立项目)**:接受。本期 Task U4 改成"emit decision span 到本地 trace + 异步队列",OTEL collector 部署/exporter 配置 → 列 P1 TODO,**不**捆绑本计划完工。
- **#16(Reflection blocks 隐藏恢复路径)**:接受。Task U5 加约束:transient failure(网络/timeout)不进 ReflectionBlocks,只有 4xx/auth/schema_invalid 这种结构性失败进。
- **#17(skill-creator 写 ~/.claude/skills 战略错误)**:接受。Task 0 默认输出到 `./skills/<name>/`(本仓库),全局安装通过 `--global` flag 显式 opt-in。
- **#19(2026-05-08 二次审阅 baseline/rollback/skill-creator/LLM rewrite/模型硬编码)**:全部接受。禁止危险 baseline 扩展;rollback 统一 observe/off;skill-creator P0 只交付 instruction workflow,不承诺单次 skill 调用写盘;P0 description sanitizer 不调 LLM;IntentClassifier 模型通过配置/现有 router 选择。

### 0.7 不在本计划范围(已转 TODOS)

| 项 | 优先级 | 估时 | 依赖 |
|---|---|---|---|
| Vector recall(embeddings) | P1 | 1w / 4h | 本计划完工 |
| LLM Reranker(候选 → top-5) | P1 | 3d / 1h | Vector recall |
| Tool Plan Validator | P1 | 2w / 6h | RouteDecision 稳定 |
| Capability budget per session | P1 | 3d / 1h | Capability 模型完工 |
| LLM 对抗扩集自动化(每周生成) | P1 | 1w / 4h | U6 评测框架 |
| Cost/latency 路由优化 | P2 | 1w / 3h | 全部 P0 完工 |
| RouteDecision admin UI | P2 | 2w / 8h | 本地 replay/OTEL trace 数据稳定 |
| Frontend explainability 面板 | P2 | 1w / 4h | RouteDecision 数据稳定 |

## 1. 问题定义

当前 `internal/tools/tool_search.go` 的 `RecallToolCatalog()` 用 `name / description / schema / alias / n-gram` 做文本召回，`internal/master/tool_visibility.go` 在 `mode=inject` 下把命中的隐藏工具直接加入本轮 model-visible tools。

这导致主意图、工具描述词和模型自选实现方案混在同一排序空间里。例如用户只说“创建一个打招呼的 skill”，系统却可能因为 `mcp-builder` description 里的 “creating high-quality MCP servers” 命中“创建/creating”,把 `mcp-builder` 暴露给模型；模型随后把“创建 skill”错误迁移成“创建 MCP server/tool”,并误调用 `mcp-builder`。这个问题不能通过继续扩 prompt 修复；prompt 规则会膨胀，且无法形成可验证的硬边界。

重要校准：**skill 作为可调用 tool/capability 入口是正确方向**，这与 2026 主流 Agent SDK 实践一致。错误不在于“skill 被当成工具”，而在于当前 catalog 缺少 `kind/domain/capability/risk` 等类型边界，导致 skill workflow、MCP tool、builtin tool、custom tool 在同一个无类型弱文本召回池里竞争，并且召回命中可以直接变成 callable。

根因：

- 召回层没有区分“用户要完成的任务”与“实现约束/上下文词/工具描述词”。
- catalog 没有一等类型字段，无法区分 `skill_workflow`、`mcp_tool`、`builtin_tool`、`custom_tool`、`agent`。
- 发现结果、推荐结果、可调用工具集三者耦合。
- 工具风险只靠少量硬编码工具名，缺少统一元数据和默认保守策略。
- `agentquality` 目前多为静态 fixture，缺少真实 `RecallToolCatalog()` 与 model-visible 路径的误召回评测。

## 2. 2026 最佳实践基线

本计划只采纳能落到宿主层硬约束的实践，不把安全和路由正确性寄托在 prompt 上。

- OpenAI function calling 文档支持用 `allowed_tools` 在每轮限制模型可调用子集，并建议启用 strict schema；这说明“给模型哪些工具”应由宿主显式控制，而不是把所有潜在候选暴露出去。  
  来源：https://developers.openai.com/api/docs/guides/function-calling
- OpenAI Skills 文档把 skill 定义为 `SKILL.md` manifest + 文件 bundle，并由模型基于 `name/description/path` 判断是否使用；因此 skill 可以是 tool/capability 入口，但必须由宿主控制可见性、授权与执行语义。  
  来源：https://developers.openai.com/api/docs/guides/tools-skills
- OpenAI tool_search 文档使用 deferred tools 按需加载工具定义，并建议把 deferred functions 分组进 namespace / MCP servers；本计划采纳“延迟发现 + 分组/命名空间”思想，但在 RouteDecision/capability 完成前保持 discovery-only，不让搜索结果直接授权。  
  来源：https://developers.openai.com/api/docs/guides/tools-tool-search
- OpenAI Agents SDK 将 tools 分为 hosted tools、function tools、agents as tools、MCP servers、sandbox capabilities，并把 skills 归入 sandbox capabilities；这支持“统一 capability 入口 + typed kind”的设计。  
  来源：https://openai.github.io/openai-agents-js/guides/tools/
- Anthropic tool use 文档强调工具描述要写清楚何时使用、何时不使用，但这是提高模型选择质量，不是替代宿主路由和权限边界。  
  来源：https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools
- MCP 2025-06-18 规范定义工具 `annotations`、`inputSchema`、`outputSchema`；客户端必须把来自不可信服务器的 annotations 视为不可信。  
  来源：https://modelcontextprotocol.io/specification/2025-06-18/server/tools
- MCP 2026 tool annotations 文章明确：annotations 是风险词汇，不是 enforcement；宿主必须自己做信任、组合风险、审批和隔离。  
  来源：https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/

## 3. 目标架构

把当前“一步文本召回并注入”改成 typed capability routing：

```text
User input
  -> IntentFrame      // 结构化主意图、对象、约束、否定、副作用意图
  -> CandidateRecall  // 只读候选，允许 lexical/vector/LLM rerank，但候选必须带 kind/domain/capability/risk
  -> RouteDecision    // 宿主确定本轮 allowed tools
  -> Model tools      // 仅暴露 RouteDecision 允许的工具
```

一等模型：

```text
CapabilityEntry
  name: string
  kind: skill_workflow | mcp_tool | builtin_tool | custom_tool | agent | sandbox_capability
  domain: skill_authoring | mcp_server_building | file_ops | messaging | memory | web | orchestration | ...
  source: builtin | local_skill | marketplace_skill | mcp_server | custom_dir | plugin
  capabilities: []Capability
  risk: read_only | local_write | external_write | runtime_exec | destructive
  invocation: direct_tool | skill_tool | agent_tool | discovery_only
```

这里保留行业通用做法：`skill` 仍是模型可调用 tool，具体 skill 作为 `kind=skill_workflow` 的 capability entry 被 `skill` 工具承载；但具体 skill 是否可见、是否推荐、是否 callable，由 RouteDecision 和 capability gate 决定。

硬规则：

- catalog 必须 typed：每个候选都要有 `kind/domain/source/invocation/risk`，未知字段必须显式标 `unknown` 并走保守策略，不得混入默认安全类。
- skill 是 tool/capability 入口，具体 skill 只能作为 `kind=skill_workflow` 的 capability entry 通过统一 `skill` 工具承载；不得把 skill 从工具体系中移除，也不得把 skill description 当成 MCP/custom tool 直接暴露。
- 默认不再把弱召回结果直接加入可调用工具。
- `tool_search` 仍可做只读发现，但发现结果不等于可调用。
- 只有 `RouteDecision` 允许的工具才能进 model-visible tools。
- `tool_search` 输出不得包含授权语义；`discoverable/recommended/blocked` 只用于 discovery/recommendation，`callable` 只能来自 RouteDecision。
- skill bundled `scripts/` 默认不执行，只作为 bundled resources；脚本执行必须来自显式 hooks 或未来独立白名单机制，并继续经过权限/能力门控。
- 所有外部 MCP/custom tool 默认 `trusted=false`、`read_only=false`、`open_world=true`、`destructive=true`，除非本地信任策略显式覆盖。
- 对“创建 skill / 修改 skill / 优化 skill”等元任务，正确路由对象是 `skill-creator` 这一类 skill 工作流；工具 description 里的 `MCP/server/tool` 等实现词不得把主意图改写成 `mcp-builder` 路由。若用户明确提出 MCP 作为实现约束,它也只能作为 constraint,不得单独提升 `mcp-builder` 为主路由。
- 对“创建 MCP server / 封装外部 API 为 MCP / 调试 MCP transport”等任务，`mcp-builder` 可以作为 `kind=skill_workflow, domain=mcp_server_building` 的候选；这与“创建 skill”域不同。
- 对“不要发送 / 只是写文案 / 解释概念 / 计划阶段”等否定或非执行意图，外部发送/写操作不得进入 allowed tools。

## 4. 新增核心模型

### 4.1 IntentFrame

新增 `internal/router/intent.go`：

```go
type IntentKind string

const (
	IntentUnknown       IntentKind = "unknown"
	IntentAnswer        IntentKind = "answer"
	IntentRead          IntentKind = "read"
	IntentWriteLocal    IntentKind = "write_local"
	IntentExternalRead  IntentKind = "external_read"
	IntentExternalWrite IntentKind = "external_write"
	IntentCreateSkill   IntentKind = "create_skill"
	IntentModifySkill   IntentKind = "modify_skill"
	IntentManageTool    IntentKind = "manage_tool"
	IntentPlan          IntentKind = "plan"
)

type IntentFrame struct {
	Kind              IntentKind
	Subject           string
	Constraints       []string
	NegatedActions    []string
	RequiresExternal  bool
	AllowsSideEffects bool
	Confidence        float64
	Signals           []string
}
```

第一版用确定性解析 + 少量结构化规则，不调用 LLM。后续可在 `observe` 模式接 LLM classifier，但不得直接影响 allowed tools，必须先进评测。

### 4.2 ToolProfile

新增 `internal/router/tool_profile.go`，把 MCP annotations、内置工具元数据、配置覆盖统一成宿主可信 profile：

```go
type TrustLevel string

const (
	TrustBuiltIn TrustLevel = "built_in"
	TrustLocal   TrustLevel = "local"
	TrustTrusted TrustLevel = "trusted"
	TrustUnknown TrustLevel = "unknown"
)

type ToolProfile struct {
	Name              string
	Kind              string // skill_workflow | mcp_tool | builtin_tool | custom_tool | agent | sandbox_capability
	Domain            string // skill_authoring | mcp_server_building | file_ops | messaging | ...
	Source            string // builtin | local_skill | marketplace_skill | mcp_server | custom_dir | plugin
	Invocation         string // direct_tool | skill_tool | agent_tool | discovery_only
	Trust             TrustLevel
	ReadOnly          bool
	Destructive       bool
	Idempotent        bool
	OpenWorld         bool
	SideEffect        bool
	CapabilityTags    []string
	AllowedIntentKinds []IntentKind
}
```

MCP annotations 只能作为输入信号；最终 profile 由宿主根据信任策略计算。

### 4.3 RouteDecision

新增 `internal/router/decision.go`：

```go
type RouteDecision struct {
	Intent       IntentFrame
	AllowedTools []string
	VisibleOnly  []string
	BlockedTools []BlockedTool
	Mode         string // none | discover | allow
	Reason       string
}

type BlockedTool struct {
	Name   string
	Reason string
}
```

`VisibleOnly` 用于 UI/观测展示候选，但不会给模型执行。

## 5. 路由策略

### P0 策略

1. Phase 0 生产默认暂保留 legacy/inject,但只新增 shadow observe 和质量事件；RouteDecision 评测达标后,再把 observe/route_decision 设为默认。`inject` 配置保留为本地诊断开关,标记 deprecated,不得作为生产回滚目标。
2. 基线可见工具只包含经 CapabilityProfile 标记为 `non_side_effect` 或 `discovery_only` 的最小集合。`batch`、`parallel_dispatch`、`task` 这类可间接触发执行的工具是否常驻,必须由 Policy unification ADR 明确风险、限制和 capability gate 后才能进入 baseline。
3. 对隐藏工具，只有满足以下条件才能进入 `AllowedTools`：
   - IntentFrame 与 ToolProfile 的 `AllowedIntentKinds` 匹配。
   - 工具不是 side-effect，或用户明确允许 side effect。
   - 工具不是 open-world external write，或 HITL/permission 层可确认。
   - 当前 plan mode gate 允许。
   - 召回分数只作为辅助，不是充分条件。
4. `tool_search` 返回候选时增加 `route_status`，但只允许 discovery 语义：
   - `discoverable`
   - `recommended`
   - `blocked`
5. `callable` 只能由 RouteDecision 事件或独立字段展示,且必须标注“不是 `tool_search` 授权结果”。弱文本召回命中只能产生 `recommended`,不能直接产生 `callable`。

### P1 策略

1. 加 embedding/vector recall 或 LLM rerank，只用于排序候选，不直接授权。
2. 给每个候选记录 `why`: intent match、capability tag、risk block、negation block、plan gate。
3. 对 MCP/custom tools 引入配置文件信任策略：

```json
{
  "agent": {
    "tool_routing": {
      "engine": "observe",
      "capability_overrides": {
        "feishu_api": {
          "trust": "trusted",
          "grants": ["external.read.feishu", "external.send.feishu"],
          "denies": ["runtime.exec.shell"]
        },
        "mcp-builder": {
          "trust": "local",
          "grants": ["meta.tool.register"],
          "denies": ["meta.skill.create", "runtime.exec.script"]
        }
      }
    }
  }
}
```

## 6. 实施任务

> **顺序变化(2026-05-08 二次修订):** Task 0 (P0 skill-creator) 与 Task 6 必须最先做。Task 1 不再扩危险 baseline,只做 shadow/observe 与 discovery-only 标注。Task 2-7 之间穿插 U1〜U6。详见 §0.5 Migration 阶段。

### Task 0(P0 - 新增): 建 skill-creator 本地工作流

状态：已完成。已新增本地 `skills/skill-creator` instruction workflow、模板和验证脚本；普通 skill 调用不会自动写盘。

文件:

- 新增 `skills/skill-creator/SKILL.md`(模板向导)
- 新增 `skills/skill-creator/templates/skill-template.md`
- 新增 `skills/skill-creator/scripts/validate.sh`(仅作为 bundled resource,不在普通 skill 调用时自动执行)

动作:

- 模板向导说明 skill name、description、是否带 scripts、所需 capabilities 的采集格式
- `skill-creator` P0 只作为 instruction workflow:返回目录结构、frontmatter、模板内容和后续需要调用的显式文件写入步骤
- 默认建议输出到 `./skills/<name>/`;全局安装必须由用户显式要求 `--global`
- IntentCreateSkill 路由命中后,RouteDecision.AllowedTools = [`skill`] 且 arguments 指向 `skill-creator`
- 若要真正写盘,必须由模型后续显式调用 `write_file`/`apply_patch` 或未来单独的 `skill_scaffold` 工具;禁止依赖 skill 调用自动执行 scripts

理由:**没有这个工作流,IntentCreateSkill 即使识别准确路由也是空集。**这是 product gap,不是技术细节。

验收:

```bash
go test ./internal/skills ./internal/tools -run 'SkillCreator|SkillRoute' -count=1
```

手工验收:

- 调用 `skill` 的 `skill-creator` 返回创建 `./skills/foo/SKILL.md` 所需的确定性模板和显式写文件步骤。
- 不产生任何文件写入副作用。
- “创建一个跟我打招呼的 skill”不会调用 `mcp-builder`。
- 防御性 case:“创建一个 skill,并提到 MCP 作为实现约束”也不会让 `mcp-builder` 覆盖 `IntentCreateSkill` 主意图。

### Task 1：冻结当前风险，shadow observe

状态：已完成。`tool_search` 已明确 discovery-only，并输出 `kind/domain/source/invocation/route_status`；发现态只记录审计状态，不会把工具变成 model-visible/callable。`quality.route_decision` 和 DecisionSpan 已在 Task 4/U4 落地。

文件：

- `internal/config/defaults.go`
- `internal/config/config_test.go`
- `internal/master/tool_visibility.go`
- `internal/tools/tool_search.go`
- `README.md`

动作：

- 生产默认暂不从 `inject` 直接切 `observe`,避免主路径突然断。
- 新增 shadow/observe 质量事件:在旧路径继续工作时,并行计算“如果 observe/RouteDecision 生效会发生什么”,但不改变 model-visible tools。
- 保留显式配置 `inject`,但 README 标记为 legacy/高风险/仅诊断。
- `tool_search` 输出和文档明确标注 discovery-only:搜索结果不是授权,不会让工具可调用。
- `tool_search` 返回结果增加 `kind/domain/source/invocation/route_status` 字段。Phase 0 若无法完整推断,至少按来源填 `builtin_tool`、`skill_workflow`、`mcp_tool`、`custom_tool`、`unknown`；缺失时必须标 `unknown` 而不是混入默认安全类。
- `mcp-builder` 这类 skill 结果必须显示为 `kind=skill_workflow, domain=mcp_server_building, invocation=skill_tool`,不得显示成 `mcp_tool`。
- 禁止把 `feishu_api`、`send_im_message`、微信写操作、`bash`、`write_file`、`apply_patch`、`create_tool`、`remove_tool` 等危险工具加入 `defaultModelVisibleTools`。
- 增加测试:默认配置不扩危险 baseline;shadow observe 不改变 visible tools;quality event 能记录 shadow 候选。

验收：

```bash
go test ./internal/config ./internal/master ./internal/tools -run 'ToolRecall|ModelVisibleTools|ToolSearch' -count=1
```

### Task 2：引入 `internal/router` 结构化意图与工具画像

状态：已完成。已引入 `IntentFrame`、`ToolProfile`、`CapabilityEntry`、`RouteDecision`、description sanitizer、tool name policy、typed profile inference；U1 已补规则 fallback/cache/budget guard/LLM 接口 scaffold。

文件：

- 新增 `internal/router/intent.go`
- 新增 `internal/router/tool_profile.go`
- 新增 `internal/router/decision.go`
- 新增 `internal/router/capability_entry.go`
- 新增 `internal/router/intent_test.go`
- 新增 `internal/router/tool_profile_test.go`

验收用例：

- “创建一个跟我打招呼的 skill” → `IntentCreateSkill`，subject 包含问候/打招呼。
- “创建一个 skill，MCP 作为实现背景或约束” → `IntentCreateSkill`，constraint 可包含 `MCP`，但主意图不能变成 MCP/tool/server builder。
- “创建一个 MCP server 接入 GitHub API” → `IntentManageTool` 或 `IntentExternalRead/Write + domain=mcp_server_building`，`mcp-builder` 可推荐但仍走 `skill_tool` invocation。
- `mcp-builder` ToolProfile → `Kind=skill_workflow`, `Domain=mcp_server_building`, `Invocation=skill_tool`, `CapabilityTags` 不包含 `meta.skill.create`。
- `skill-creator` ToolProfile → `Kind=skill_workflow`, `Domain=skill_authoring`, `Invocation=skill_tool`, `CapabilityTags` 包含 `meta.skill.create`。
- “帮我写飞书通知文案，不要发送” → `IntentAnswer` 或 `IntentWriteLocal`，`NegatedActions` 包含 send。
- “发送给飞书用户郭松” → `IntentExternalWrite`，`AllowsSideEffects=true`。
- unknown MCP tool 默认 profile 为 unknown/open-world/destructive。

验收：

```bash
go test ./internal/router -count=1
```

### Task 3：替换 per-turn inject 为 RouteDecision

状态：已完成。每轮弱召回只产生候选，`mode=inject` 也必须经过 `RouteDecision` 才能进入 model-visible；具体 skill workflow 通过统一 `skill` 工具承载，并用 `AllowedToolInputs["skill"]["name"]` 做执行前硬约束。

文件：

- `internal/master/tool_visibility.go`
- `internal/master/tool_visibility_test.go`
- `internal/tools/tool_search.go`
- `internal/router/capability_entry.go`
- `internal/router/tool_profile.go`

动作：

- `perTurnRecalledToolSet()` 不再直接返回可注入 set。
- 新增 `BuildRouteDecision(catalog, query, recallCfg)`。
- `BuildRouteDecision` 的输入不再是裸 `ToolDefinition` 排序结果,而是 `CapabilityEntry + ToolProfile + IntentFrame`。
- `mode=observe`：只记录候选，不加入 tools。
- `mode=inject`：只允许 RouteDecision 判定为 callable 的候选加入。
- `kind=skill_workflow` 的候选只能通过统一 `skill` 工具调用,具体 skill name 作为参数；不得把 skill description 当成独立 MCP/custom tool 直接暴露。
- `kind=mcp_tool` 的候选必须有 MCP server/source/profile;不得把名称里包含 MCP 的 skill workflow 误归类为 mcp_tool。

必须新增回归：

- `mcp-builder` 不因 `creating`/`skill`/`MCP` 这类 description 或上下文词进入 callable。
- “创建 MCP server 接入外部 API”可推荐 `mcp-builder`,但显示为 skill workflow,不是 MCP tool。
- `skill`/`skill-creator` 路径保持可见。
- “不要发送”不注入 `feishu_api` / `send_im_message`。
- 飞书发送正例可推荐，但是否 callable 由 risk/HITL 策略决定。

验收：

```bash
go test ./internal/master ./internal/tools -run 'ToolRecall|RouteDecision|ModelVisibleTools|ToolSearch' -count=1
```

### Task 4：扩展质量事件

状态：已完成。`quality.route_decision` 已写入 master 质量事件流，包含 intent、callable/recommended/blocked、blocked reasons 和 routing confidence；DecisionSpan replay 归 U4。

文件：

- `internal/agentquality/types.go`
- `internal/master/quality_events.go`
- `internal/master/react_processor.go`

新增字段：

- `intent_kind`
- `route_mode`
- `callable_tools`
- `recommended_tools`
- `blocked_tools`
- `blocked_reasons`
- `routing_confidence`

验收：

```bash
go test ./internal/master ./internal/agentquality -run 'ToolRecall|RouteDecision|QualityEvent' -count=1
```

### Task 5：真实召回评测 runner

状态：已完成。已落地 checked-in JSON corpus、RouteDecision runner 和 gate metrics；真实召回路径由 master/tool_search/router 组合测试覆盖。

文件：

- 新增 `internal/agentquality/toolrouting_runner.go`
- 新增 `internal/agentquality/toolrouting_runner_test.go`
- 新增 `internal/agentquality/testdata/aq11_skill_create_greeting.json`
- 新增 `internal/agentquality/testdata/aq14_skill_create_mcp_context.json`
- 新增 `internal/agentquality/testdata/aq12_negated_external_send.json`
- 新增 `internal/agentquality/testdata/aq13_external_send_positive.json`

指标门槛：

- P0 误 callable 率：0。
- “创建打招呼 skill”误选 `mcp-builder`：0。
- “创建 skill + MCP 上下文/约束”误选 `mcp-builder`：0。
- negated send 误 callable：0。
- 正例推荐命中率：>= 90%。
- 所有 unknown MCP/custom tool 未配置时 callable：0。

验收：

```bash
go test ./internal/agentquality ./internal/master ./internal/tools ./internal/router -run 'ToolRouting|AgentQuality|RouteDecision' -count=1
```

### Task 6：修复 skill bundled scripts 自动执行

状态：已完成。普通 `InvokeFull()` 不再自动执行 `scripts/`，`skill` 工具调用链不再传入自动脚本 runner；`scripts/` 默认只是 bundled resources。

文件：

- `internal/skills/registry.go`
- `internal/skills/skill.go`
- `internal/skills/registry_test.go`

动作：

- 普通 `InvokeFull()` 不再自动执行 `scripts/` 目录所有文件。
- 只有显式 frontmatter `hooks` 或未来显式 `run-scripts` 白名单可执行。
- `scripts/` 目录仅作为 bundled resources。

回归：

- `mcp-builder/scripts/connections.py` 这种 helper 不会在 skill 调用时自动跑。

验收：

```bash
go test ./internal/skills ./internal/tools -run 'InvokeFull|Script|Skill' -count=1
```

### Task U1(新增): IntentClassifier hybrid 实现

状态：已完成。已新增 `IntentClassifier` 规则 fallback、可插拔 LLM classifier 接口、10min session/message cache、200ms timeout 和 daily budget guard；默认无外部 LLM key 时纯规则运行。

文件:

- 新增 `internal/router/intent_classifier.go`(可配置 LLM call + 规则 fallback + cache)
- 新增 `internal/router/intent_classifier_test.go`
- 新增 `internal/router/intent_budget.go`(日 budget guard)
- 新增 `internal/router/intent_cache.go`(message_hash + session_id → IntentFrame, 10min TTL)

动作:

- LLM call 走 `internal/llm` / 现有 task router;模型由配置 `agent.tool_routing.intent_classifier_model` 或现有 routing profile 决定,不得在代码或计划中硬编码具体供应商模型。
- 默认必须可在无外部 LLM key 的环境下以纯规则 fallback 运行。
- 用 system prompt 定义 IntentFrame schema, **user message 进结构化 input field**(不允许 user 控制 classifier 行为, F3.2)
- LLM 失败/超时/budget 超限 → fallback 到规则提取(subject/constraint/negation 关键词表)
- 规则 fallback 必须有独立测试,不可只 mock LLM
- Cache key = sha256(message + session_id),TTL 10min
- Budget guard:每日 USD 累计阈值默认 $50,可配置;超阈值自动降级规则模式 + Sentry 告警

验收:

```bash
go test ./internal/router -run 'IntentClassifier|IntentCache|IntentBudget' -count=1
```

### Task U2(新增): Tool Description Sanitization (P0 regex/normalize/block)

状态：已完成。已实现 normalization/长度限制、ToolNamePolicy、description/name/schema prompt-injection regex block、RawDescription 审计字段和 fail-closed profile。

文件:

- 新增 `internal/router/description_sanitizer.go`
- 新增 `internal/router/description_sanitizer_test.go`

动作:

- P0 只做 regex 黑名单 + normalization + 长度限制 + fail-closed block。匹配 `Use this tool whenever`, `ignore previous instructions`, `Important:` 等高置信度 prompt-injection 模式时直接 block 或降为 display-only,不得调用另一个 LLM 重写。
- LLM rewrite 仅作为 P1 opt-in 实验,不能作为安全边界,不能进入 P0 完成定义。
- 规范化后的 description 仅用于显示/审计/排序解释;**description 永不参与 authorization**。
- 原始 description 入 ToolProfile.RawDescription(仅审计可见,不进 prompt)
- ★ schema 字段也 sanitize(F3.3, schema 是 tool_search.go 现成 collect 的攻击面)
- ★ 工具 name 也走 ToolNamePolicy.Validate(F3.1, regex 白名单 `[a-z][a-z0-9_]*`,长度 ≤ 64)
- Sanitize 失败 fail-closed(F2 G2.2),工具被 block

验收:

```bash
go test ./internal/router -run 'Sanitize|ToolNamePolicy' -count=1
```

### Task U3(新增): Capability-based Authorization

状态：已完成本期闭环。已定义基础 capability 标签、RequiredCapabilities、CapabilityGate、ProfileHasSideEffect，并删除 master `isSideEffectTool` 硬编码；YAML source-of-truth/codegen 与完整 ADR 属 P1+ 增量。

文件:

- 新增 `internal/router/capability.go`(Capability 类型 + 命名空间)
- 修改 `internal/router/tool_profile.go`(加 RequiredCapabilities/GrantedCapabilities)
- 修改 `internal/router/decision.go`(CapabilityGate 求交)
- 删除 `internal/master/tool_visibility.go` 中 `isSideEffectTool`(F5.2)
- 删除 `internal/master/tool_visibility.go` 中 `pruneGenericIMWhenFeishuDomainEntryRecalled`(F5.1, 迁 ToolProfile.DomainEntry)

动作:

- 命名空间 `<surface>.<action>.<scope>`(详见 §0.6)
- Session 持有 GrantedCapabilities, PlanMode 持有 PlanModeCapabilities
- IntentFrame 计算 RequiredCapabilities
- CapabilityGate.Check = Intent.Required ∩ Tool.Granted ∩ Session.Granted ∩ PlanMode.Allowed ∩ ¬ReflectionBlocks
- Capability 配置必须支持 env var override(F3.4)
- 必须先做 ADR(架构决策记录)定 namespace,改命名要 schema migration

验收:

```bash
go test ./internal/router -run 'Capability|Gate' -count=1
go test ./internal/master -run 'PrunedIM|SideEffectTool' -count=1  # 应该删掉相关测试
```

### Task U4(新增): Decision Span + 本地 replay + 8 metric + 4 alert

状态：已完成本期闭环。已新增 DecisionSpan、本地内存/JSONL replay、RouteDecisionSummary，并在 master 路由事件路径写入本地 replay log；OTEL collector/exporter 和生产 alert 配置属 P1+ infra 工作。

文件:

- 修改 `internal/agentquality/types.go`(加 EventRouteDecision)
- 修改 `internal/master/quality_events.go`(emit decision span)
- 新增 `internal/router/decision_span.go`(OTEL 兼容 emit)
- 新增 `internal/router/replay.go`(给定 trace_id 重建完整 RouteDecision 链)

decision span schema 见 §0.4 Section 8 部分。8 metric + 4 alert 见 §0.4 Section 8。

验收:

```bash
go test ./internal/agentquality ./internal/router ./internal/master -run 'DecisionSpan|RouteDecision' -count=1
```

### Task U5(新增): Reflection 反馈环 wire 回 RouteDecision

状态：已完成。已新增 per-session ReflectionBlock、LRU=10、mode gate、结构性失败过滤，并 wire 到 RouteDecision；工具结构性终端失败会写 block，timeout/network 不写。

文件:

- 修改 `internal/master/reflection_evaluator.go`(reflection 失败 → 写 ReflectionBlock)
- 修改 `internal/router/decision.go`(读 session.ReflectionBlocks,加入 gate)
- 修改 `internal/master/session_state.go`(ReflectionBlocks per-session, LRU 限 10 条, 标 mode F4.7)

动作:

- 模型反复调失败工具 → ReflectionFolder.Fold 把工具加 BlockedTools
- ReflectionBlocks per-session,跨 mode 不承袭(plan 阶段的 block 不带到 exec)
- LRU 限制最近 10 条(F4.4 性能考虑)

验收:

```bash
go test ./internal/master ./internal/router -run 'Reflection|Folder' -count=1
```

### Task U6(新增): 红蓝对抗评测框架 + 25+ case + CI 严门

状态：已完成。checked-in corpus 已扩到 26 条，包含 prompt-injection 5+、false-match、skill vs MCP、tool_search discovery-only、unknown MCP fail-closed、plan gate；新增 gate metrics 和 `.github/workflows/route-eval.yml`。

文件:

- 新增 `internal/agentquality/route_eval_runner.go`(真实跑 IntentClassifier + RouteDecision)
- 新增 `internal/agentquality/route_eval_runner_test.go`
- 新增 `internal/agentquality/testdata/route_eval/positive_*.json`(8 条正例)
- 新增 `internal/agentquality/testdata/route_eval/negation_*.json`(5 条否定)
- 新增 `internal/agentquality/testdata/route_eval/false_match_*.json`(5 条误命中,含"创建打招呼 skill" 与 "创建 skill + MCP 上下文" 两类 mcp-builder 不应路由 case)
- 新增 `internal/agentquality/testdata/route_eval/kind_domain_*.json`(至少 4 条 typed catalog case:skill_workflow vs mcp_tool vs builtin_tool vs custom_tool)
- 新增 `internal/agentquality/testdata/route_eval/prompt_injection_*.json`(5 条 description/schema/name 攻击)
- 新增 `internal/agentquality/testdata/route_eval/plan_gate_*.json`(2 条 plan mode 边界)
- 新增 CI workflow `.github/workflows/route-eval.yml`(PR gate)

核心指标:

- recall@5 ≥ 90%(下不达拦 PR)
- false-positive callable rate ≤ 2%(超过拦 PR)
- kind/domain classification accuracy ≥ 95%(下不达拦 PR)
- checked-in prompt-injection corpus bypass_count = 0(任何 bypass 拦 PR);weekly 扩集只做趋势告警,确认后的新增 case 再进入 gating corpus
- IntentFrame 准确率(分主意图)≥ 92%(下不达拦 PR)

验收:

```bash
go test ./internal/agentquality -run 'RouteEval|PromptInjection|FalseMatch' -count=1
.github/workflows/route-eval.yml 在 PR 上跑指标, 不达标拦 PR
```

### Task 7：文档和迁移

状态：已完成。README 已补 typed routing 运行约束，`docs/架构设计/Tool-Routing.md` 已新增架构说明；本计划已归档，保留当前完成记录。

文件：

- `README.md`
- `docs/架构设计/Tool-Routing.md`
- 本计划已移入 `docs/计划与路线/归档/`

必须写清楚：

- skill 是行业通用的 tool/capability 入口,不是要从工具体系里移除。
- 具体 capability entry 必须带 `kind/domain/source/invocation/risk`。
- catalog 是 typed source of truth；缺失类型字段必须显式 `unknown` 并保守处理。
- `tool_search`/lexical recall 只做 discovery/recommendation,不能产生授权语义。
- callable tools 必须经过 RouteDecision。
- prompt 不是路由控制面。
- MCP annotations 是不可信 hint，宿主 profile 才是执行依据。
- scripts 默认不执行；skill bundled `scripts/` 只作为资源,除非有显式 hooks 或未来白名单执行路径。
- 生产回滚只允许 `observe` 或 `off`;旧 `inject`/`legacy` 仅作为本地诊断开关,不作为生产回滚路径。

## 7. 完成定义

- 默认配置不会把弱召回工具直接注入 model-visible tools。
- `mcp-builder` 误调用场景有自动化回归。
- “否定发送”和“计划阶段发送”不会进入 callable。
- unknown MCP/custom tools 默认不可自动 callable。
- `quality.route_decision` 能解释每个候选为何 recommended/callable/blocked；`quality.tool_recall` 只记录 discovery/recommendation。
- typed catalog 覆盖 skill workflow、MCP tool、builtin tool、custom tool、agent；缺失元数据时默认 blocked 或 visible-only。
- `tool_search` 在任何模式下都不能单独授权执行；callable 只能来自 RouteDecision。
- skill 调用不再自动执行 bundled scripts。
- `go test ./internal/router ./internal/tools ./internal/master ./internal/agentquality ./internal/skills -count=1` 通过。

## 8. 实施顺序

更新后的实施顺序(详见 §0.5 Migration 阶段):

1. **Task 0 + Task 6**:止血(P0 product gap + bundled scripts 安全洞)
2. **Task 1**:shadow observe + discovery-only 标注,不扩危险 baseline
3. **Task U2 + Task U3 + Task 2**:Phase 2 上线 sanitization + capability + IntentFrame/ToolProfile
4. **Task U1**:IntentClassifier hybrid(可配置模型 + 规则 fallback + cache + budget guard)
5. **Task 3 + Task 4 + Task U4 + Task U5**:RouteDecision + 质量事件 + decision span + reflection 反馈环
6. **Task 5 + Task U6**:Phase 3 真实评测 runner + 红蓝对抗集 + CI 严门
7. **Task 7**:文档,本计划归档,Capability ADR 入库

## 9. CEO Review 完成定义(2026-05-07 后)

除原 §7 完成定义外,补:

- skill-creator 本地 instruction workflow 可用,IntentCreateSkill 路由非空,且普通 skill 调用不产生写盘副作用
- IntentClassifier 200ms timeout + budget guard + cache 三件套均有独立测试
- description/name/schema sanitize P0(regex + normalize + block)在 checked-in prompt-injection 红蓝集 bypass_count=0;LLM rewrite 不属于 P0 完成定义
- capability 命名空间和基础 gate 入库；YAML/codegen ADR 属 P1+ 增量
- decision span 可本地重放;OTEL collector/exporter 生产接入为 P1 TODO
- reflection block per-session per-mode 验证通过
- CI route-eval workflow 在 PR 上跑且严门生效

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| CEO Review | `/plan-ceo-review` | Scope & strategy | 1 | CLEAR | mode=SCOPE_EXPANSION, 7/7 提案接受;3 高杠杆揭露(skill-creator gap / description sanitize / migration 断路) |
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | CLEAR | 3 D-decisions: yaml capability config + Secondary intent + cassettes CI;Section 1-4 共发现 ~14 P1/P2 issues,全部 scope 内修 |
| Codex (Outside) | `/codex` | Independent 2nd opinion | 1 | issues_found | 18 条批评, 5 条 tension 全接(T1/T3/T4/T5)+ T2 改 MVP-first |
| Design Review | `/plan-design-review` | UI/UX gaps | 0 | — | 未运行(后端为主, UI explainability 推 P1) |
| DX Review | `/plan-devex-review` | Developer experience gaps | 0 | — | 未运行 |

**CODEX:** 18 条独立批评中 5 条接受 + 实施顺序改 MVP-first;3 条已反驳(plan 内部一致性误读 / OTEL 拆分 / rollback 改回 observe)
**CROSS-MODEL:** 一致认为 Capability migration 风险大(Eng F1.2 + Codex #5/#7),决议加 Task -1 Policy unification ADR
**UNRESOLVED:** 0
**VERDICT:** CEO + ENG + CODEX CLEARED — ready to implement(按 §0.9 调整后实施顺序: Phase 0 MVP → Phase 1-3 SOTA → Phase 4 归档)
