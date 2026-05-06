# Hive Final Plan

> **状态**：唯一权威方案
>
> **依据**：2026-04-28 代码级审计，见 `EVIDENCE/CODE-AUDIT-2026-04-28.md`
>
> **用途**：回答 Hive 现在为什么这样排、未来 3-6 个月做什么、未来能力版图长什么样。
>
> **执行规则**：所有研发排期、施工优先级、里程碑判断，都以本文件为准。
>
> **代码施工入口**：具体代码怎么写、改哪些文件、加哪些测试，见 `IMPLEMENTATION/`。

## 1. 最终判断

Hive 不是能力缺失型系统。代码显示，Hive 已经有 ReAct 主循环、PromptLoader、ToolBridge、Skill overlay、MCP host、ACP server/client、subagent、memory、journal、taskboard、HITL、security、sandbox、AI router、cost/quota、Gateway、Admin UI、多 IM channel 等大量能力。

Hive 当前真正缺的，是把这些能力变成 **可评测、可回归、可治理、可解释的 agent 质量系统**。

所以最终方案是：

1. **质量闭环优先**：先建立主 Agent 的评测、回归、失败归因和上线门禁。
2. **治理一致性优先**：把现有危险操作边界、HITL、成本、观测、沙箱统一到可运营策略。
3. **质量杠杆前置**：完整工作台、multi-agent、ACP、自我优化不能整体后置；它们的最小质量杠杆版必须进入 P0/P1/P3/P4，用来做评测、回放、归因、恢复和失败沉淀。
4. **完整扩张冻结**：除非能直接提升质量指标，否则不继续铺工具、铺 UI、铺生态。
5. **未来版图保留**：memory、multi-agent、ACP、spec-driven、自我改进都保留，但完整产品化和生态化必须经过质量门禁再进入施工。

## 2. 代码现实

这次审计覆盖后端约 690 个 Go 文件、约 169520 行，前端 `frontend/src` 约 129 个 TS/TSX 文件。

关键现实如下：

| 领域 | 代码现状 | 对计划的影响 |
|---|---|---|
| 主 Agent | `internal/master` 已有 ReAct、streaming、tool guard、loop detector、成本预算、事件广播 | 不重建主循环，先做质量评测和失败归因 |
| Prompt | `internal/i18n/prompt_loader.go` 已支持 DB > 文件 > embed、缓存、热失效 | 不重建 Prompt 系统，先做 prompt eval、版本、回归 |
| Tool | `internal/tools` 已有文件、bash、web、batch、task、spawn_agent、skill、question、memory、taskboard、IM 等工具 | 不追工具数量，先测工具选择率和错误调用率 |
| Skill | `internal/skills` 已有 registry、overlay、DB 覆盖、租户隔离、on-demand、permission | 不重建 skill 平台，先做路由质量和生命周期治理 |
| Security | `internal/security`、`internal/skills/permission.go`、`internal/sandbox` 已有规则、HITL、LLM classifier、Docker sandbox | 重点修危险操作边界：安全操作默认放行，删除/删库/force push 等不可逆操作才审批或拒绝 |
| Memory | `internal/memory` 已有 PG、pgvector、混合检索、抽取、注入 | 不从零建 memory，先治理来源、置信度、生命周期和注入污染 |
| Spec-driven | `internal/specdriven` 已有 planner、runner、eval harness、CAS store，但 ReAct 主执行仍主要走 legacy | 作为孵化线，不作为近期能力跃迁主线 |
| Multi-agent/ACP | `internal/subagent`、`internal/acpclient`、`internal/acpserver` 已有可运行骨架 | 不整体后置；近期把最小协作场景纳入 golden tasks、trace linkage、危险操作边界和恢复回放 |
| Channel | `internal/channel` 尤其飞书已很重，含 renderer、dedup、gap fetch、governance、HITL bridge | 不再发明 channel 抽象，复用并治理现有体系 |
| Admin/控制面 | `internal/api/routes.go` 和 `frontend/src/pages/admin` 已有用户、配额、用量、Prompt、Skill、LLM Provider 管理 | 不做完整工作台重建；近期把 SessionReplay、Journal、PromptManager、UsageStats 接入质量调试闭环 |

## 3. 真正要追求的质变

未来 3-6 个月，Hive 的质变不是“功能更多”，而是：

- 复杂任务一次完成率可测并持续提升。
- 工具选择错误、空工具调用、重复工具循环可被分类和回归拦截。
- Prompt、tool description、skill 内容的改动都有离线评测和线上指标。
- 长会话压缩、memory 注入、附件上下文不会静默污染模型决策。
- 危险操作边界、HITL、沙箱、成本、quota 在 Web、API、IM、ACP 路径上语义一致。
- 任何失败都能归因到 prompt、tool、skill、context、model、dangerous_operation、runtime 中的一类。
- 失败轨迹能在现有 replay/journal/Admin surface 中被定位，并能沉淀到 DB regression candidate pool。
- subagent/ACP 的最小协作路径可评测、可回放、可恢复，证明它们对成功率或成本有正收益。
- 自动优化不允许系统自动改生产；但允许人工审批后通过可审计 API 应用 prompt、skill、tool description、memory governance policy，并保留 rollout/rollback 记录。危险操作规则仍不得由自动优化直接改写。

如果这一层没有建立，继续做完整工具铺货、完整工作台、multi-agent 全面协作、ACP 生态化或自动上线式自我改进，只会把不稳定性放大。

## 4. 执行优先级

### P0：Agent 质量闭环

这是唯一最高优先级。

范围：

- 主 ReAct golden tasks 与 eval harness。
- Prompt / tool / skill / context 的离线评测。
- 线上质量事件 schema 与失败分类。
- 回归门禁和质量看板。
- 最小质量工作台：把 `quality.*` 事件接入现有 journal/replay/Admin surface，让失败能被定位、筛选、写入 DB regression candidate pool。
- 最小 multi-agent case：只覆盖 spawn/task/parallel_dispatch 是否提升质量，不能扩大协作产品面。
- 最小自我优化入口：失败轨迹自动生成 golden task 候选、prompt diff、skill 草稿、tool description 和 memory governance 建议；生产变更只能人工审批后 apply，并可 rollback。

对应路线图：`ROADMAPS/AGENT-QUALITY.md`

### P1：治理与运行底座

这条线并行推进，但只服务 P0。

范围：

- Observability 指标模型和标签基数治理。
- 危险操作边界、HITL、sandbox 策略一致性。
- timeout、并发、spawn、cost、quota 的统一运行策略。
- Admin/Gateway/API 的运营闭环。
- subagent/ACP 的 trace linkage、session isolation、dangerous operation decision 和 cancel/timeout/recovery 事件。

对应路线图：`ROADMAPS/FOUNDATIONS.md`

### P2：Memory / Context 深治理

这条线与 P0 强相关，但不能混进一份文档里失焦。

范围：

- memory 来源证据、置信度、去重、过期和召回评测。
- compaction 摘要质量、长会话污染控制。
- 注入预算、注入可解释性、向量空间一致性。

对应路线图：`ROADMAPS/MEMORY-AND-CONTEXT.md`

### P3：能力表面最小推进

能力面不是取消，也不是一概延后，而是只推进对 P0 有杠杆的最小子集。

范围：

- 修补直接影响任务成功率的工具和 UI 缺口。
- 用现有 SessionReplay、ReplayGallery、Journal、PromptManager、UsageStats 做质量调试工作台。
- 复用已有 API、Admin、taskboard、artifact、HITL、channel surface。
- 不做大规模工具铺货和完整产品化工作台。

对应路线图：`ROADMAPS/CAPABILITY-SURFACE.md`

### P4：协作生态受控孵化

multi-agent、ACP、remote agent 是明显的质量杠杆，但只能以受控最小版进入近期主线。

范围：

- 本地 subagent 的质量评测、危险操作边界、恢复能力。
- ACP server/client 的稳定性、认证、会话隔离、事件回放。
- golden task 中必须有最小委派、并行委派、远程 ACP 失败/取消/危险操作 case。
- 不做 ACP 生态化扩张。

对应路线图：`ROADMAPS/MULTI-AGENT-AND-ACP.md`

### P5：未来研究

研究保留，但不默认进入当前排期。

范围：

- GEPA。
- 自动 skill 生成。
- 自我改进。
- 长周期策略学习。
- 失败轨迹到 DB regression candidate、prompt diff 建议、skill 草稿、tool description 建议和 memory governance policy 建议的离线生成；人工审批后的 apply/rollback 是受控恢复路径，不是自动上线。

对应路线图：`ROADMAPS/FUTURE-EXPLORATION.md`

## 5. 立即做 / 延后做 / 不做

### 立即做

- 建主 Agent eval harness，覆盖工具选择、编辑任务、skill 路由、长会话、HITL、安全拦截、IM 场景。
- 把最小 multi-agent/ACP 场景放进 eval：spawn/task/parallel_dispatch、ACP 断线/取消、危险操作桥接。
- 给 ReAct 事件补质量分类：失败类型、重试原因、工具选择、prompt 版本、skill 版本、context 来源。
- 把 `quality.*` 事件接到现有 journal/replay/Admin 页面，形成最小质量工作台。
- 建 `agentquality_candidates` DB 候选池，候选默认 `required=false`，只有人工审核后才能晋升正式 golden case。
- 建 P0 gate：required cases 100%、危险操作误放行 0、失败归因率 >= 90%、工具选择命中率 >= 85%、replay 可定位率 >= 90%、candidate 生成率 >= 80%、required-zero-tool 回归 0、delegation trace 覆盖率 100%。
- 把 PromptLoader 纳入版本、评测、灰度和回滚，不重建 loader。
- 把 tool description 和 tool schema 纳入回归，重点看错误调用率。
- 把 memory/compaction 注入纳入污染检测和召回评测。
- 修正危险操作治理不一致：普通操作默认放行，危险/不可逆操作不能因入口不同而静默执行。
- 治理 observability 标签，禁止高基数字段直接进入通用 metric labels。
- 从失败轨迹生成 DB regression candidate 和 prompt/skill/tool/memory 优化建议；建议必须先人工 review，审批后才允许人工触发 apply，并且必须具备 rollback。

### 延后做

- 大规模新增工具。
- 完整 todos 产品面。
- 大规模 Web/IM 工作台铺开；质量调试工作台不延后。
- multi-agent 全面协作。
- ACP 全量生态化。
- spec-driven 主执行路径替换 ReAct。

### 不做

- 重建 PromptLoader。
- 重建 Memory Store。
- 重建 Channel 抽象。
- 从零重写 MCP/ACP。
- 为了追赶竞品数量而铺工具。
- 没有 eval 的“感觉更智能”改动。

## 6. 未来能力版图

未来能力版图保留，但版图不是排期，也不是另一套 `v1/v2` 实施计划。下面只记录能力关系和后续判断上下文；任何施工优先级仍以第 4、5、8、9 节为准。

### 6.1 Agent 质量层

- Prompt 可版本化、可评测、可回归、可灰度。
- Tool 选择可测、可解释、可优化。
- Skill 发现、路由、安装、升级形成闭环。
- Context 和 memory 注入稳定可靠。
- 失败轨迹进入 DB regression candidate pool，人工确认后纳入 golden tasks。

### 6.2 执行控制层

- Web、API、IM、ACP 路径危险操作语义一致。
- HITL request 只覆盖危险/不可逆操作，并绑定 actor、workspace、tool、input fingerprint。
- Sandbox、timeout、quota、cost、spawn limit 统一受控。
- 失败可回放，恢复路径清晰。
- subagent 和 ACP 的父子 trace、取消、超时、恢复、危险操作决策进入统一事件流。

### 6.3 能力表面层

- 工具面围绕质量指标扩展。
- taskboard、artifact、journal、replay、HITL 先作为质量调试工作台，后续再产品化。
- Channel surface 复用现有 renderer/event bus，不重复造抽象。

### 6.4 协作与生态层

- 本地 subagent 的最小委派场景先进入质量评测，全面协作在质量门禁后扩展。
- 远程 ACP agent 的最小断线/取消/危险操作场景先进入回归，生态化在会话隔离和恢复能力达标后扩展。
- MCP/ACP/external runtime 协同只在真实场景驱动下推进。

### 6.5 自我改进层

- 轨迹记录进入 eval 数据集。
- 高价值失败自动沉淀为 DB regression candidate。
- Skill 和 prompt 优化由评测收益驱动。
- 自动优化只允许自动生成建议、case、diff 草稿；生产变更必须人工确认并通过 eval 后由人工触发 apply。已应用建议必须可 rollback，不能静默长期漂移。
- 长周期策略学习只在基础质量稳定后推进。

## 7. 专题路线图职责

- `ROADMAPS/AGENT-QUALITY.md`：定义 P0 主线，回答如何让主 Agent 稳定做对，并把失败变成可回放、可回归的样例。
- `ROADMAPS/FOUNDATIONS.md`：定义 P1 治理底座，回答如何让质量、subagent、ACP、危险操作、成本可测、可控、可运营。
- `ROADMAPS/MEMORY-AND-CONTEXT.md`：定义 P2 深治理，回答长会话和记忆如何不污染决策。
- `ROADMAPS/CAPABILITY-SURFACE.md`：定义 P3 能力面最小推进，回答现有 replay/Admin/channel surface 如何先服务质量调试。
- `ROADMAPS/MULTI-AGENT-AND-ACP.md`：定义 P4 协作生态孵化，回答最小协作如何先证明质量收益、何时再扩大复杂度。
- `ROADMAPS/FUTURE-EXPLORATION.md`：定义 P5 研究线，回答哪些自动优化只能生成建议，哪些可在人工审批后进入 apply/rollback。

## 8. 代码级施工计划

路线图只定义方向，不直接承担代码施工。执行 P0/P1/P2/P3/P4/P5 时必须进入：

- `IMPLEMENTATION/P0-AGENT-QUALITY-CODE.md`：主 Agent eval、quality event、prompt/tool/skill/context 回归门禁、最小质量工作台、失败转 DB regression candidate、P0 gate 阈值。
- `IMPLEMENTATION/P1-FOUNDATIONS-CODE.md`：observability label、危险操作边界、runtime policy、cost/quality 关联、subagent/ACP trace 和运行边界。
- `IMPLEMENTATION/P2-MEMORY-CONTEXT-CODE.md`：memory governance、结构化注入、context build 事件、memory eval。
- `IMPLEMENTATION/P3-QUALITY-WORKBENCH-CODE.md`：质量工作台、聚类、真实 replay run、batch eval、dashboard、weekly report。
- `IMPLEMENTATION/P2-MEMORY-PRODUCTION-CODE.md`：memory 生产治理、policy defaults、prune 运营入口。
- `IMPLEMENTATION/P4-MULTI-AGENT-ACP-CODE.md`：multi-agent/ACP trace、safe fs/terminal capability、危险操作边界。
- `IMPLEMENTATION/P5-AUTO-OPTIMIZATION-CODE.md`：建议生成、审批、人工 apply、rollout/rollback。

施工规则：

1. 先按 `IMPLEMENTATION/README.md` 选择对应代码级计划。
2. 每个任务先写测试，再改实现。
3. 不允许只按 `ROADMAPS/*.md` 直接开工，路线图粒度不够。
4. 新增代码计划必须写清文件路径、类型/函数签名、测试文件和验证命令。

## 9. 执行规则

1. 讨论总优先级，只看本文件。
2. 讨论某条线怎么做，只看对应路线图。
3. 需要代码证据，先看 `EVIDENCE/CODE-AUDIT-2026-04-28.md`。
4. `EVIDENCE/GAP-INVENTORY.md` 已过期，只能作为历史输入，不能作为施工依据。
5. `ARCHIVE/` 只保留历史，不再承担当前执行职责。
6. 新计划必须先回答“代码里是否已有”、“复用哪个模块”、“用什么 eval 验收”、“失败如何回滚”。
7. 不再使用 `v1/v2` 方案说法；版本号只能用于外部产品/协议/数据 schema 的客观版本，不得表达竞争实施计划。
8. 真正写代码时必须引用 `IMPLEMENTATION/` 对应文档，不能只引用思想层判断。
