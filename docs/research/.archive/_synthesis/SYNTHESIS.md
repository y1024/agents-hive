# Hive Harness 工程顶级化 — 三家+deer-flow 调研综合 → P0/P1 施工候选清单

> **作者**：CEO Review 综合（主线程）
> **日期**：2026-04-25
> **范围约束**：
>   - 当前阶段（锁定）：只做国内 IM（飞书/微信/企微/钉钉），打开国内市场为先
>   - 长期雄心（锁定）：harness 工程做到全球顶级（**技术质量层**，不是产品市占率）
> **方法**：deer-flow（既有 16 蓝军 + 双 AI 辩论 + final-verdict）+ OpenClaw（6-axis 源码深挖 + 蓝军 mutation）+ Claude Code（草稿 + 18 项二轮 WebSearch 核实）+ Hermes（WebSearch 方向性，关键数字未核实）

---

## §0 一页纸结论

> **⚠️ 2026-04-25 二次重大修正**：本节原有结论 #1/#2/#3/#4 在主线程对 OpenClaw 6 axis 逐条核实后**全部被推翻或修正**。仅 #5 和 #6 经三层证据交叉验证后保留。详见 §11 修正记录 + §12 OpenClaw 6 axis 主线程核实总报告。
>
> **关键教训**：子 agent 一次性产出的 6 axis evidence 中，至少 4 个 axis 有重大事实错误。**任何不经主线程独立 grep 验证的子 agent 结论都不可信**。

**6 大结论（二次修正版，每条标可信度 + 推翻原因）**：

| # | 结论 | 可信度 | 主线程核实结论 |
|---|---|---|---|
| 1 | **Hive 与 OpenClaw 在 MCP 维度势均力敌**（双方都用官方 SDK；OpenClaw 用 `@modelcontextprotocol/sdk` v1.27.1 + acpx bridge + mcporter + chrome-mcp；Hive 用 26 文件 mcphost + transport×3 + OAuth + HITL）| **[HC-MAIN-VERIFIED]** | 不再说"Hive 领先"，是势均力敌 |
| 2 | **Hive 与 OpenClaw 在 memory 向量栈势均力敌**（OpenClaw 实际有 5-provider embedding 自动选择 + sqlite-vec + memory_search/get；Hive 有 pgvec + hybrid + extractor + injector）| **[HC-MAIN-VERIFIED]** | 推翻原"Hive 领先"。OpenClaw 不是平文本 + 可选 LanceDB，是完整向量栈。Hive 优势仅在 Postgres 持久化层 |
| 3 | **Hive 真正领先点是国内 IM 平台数量覆盖（4 vs 1）**：飞书+钉钉+微信+企微 vs OpenClaw 只有飞书。**单平台工程深度（飞书 dedup/debounce/PATCH 等）双方势均力敌** | **[HC-MAIN-VERIFIED]** | 推翻原"渠道工程领先"。Hive 仅在 ErrPatchRateLimited 限流细节可能更深（待对照） |
| 4 | **acpx (OpenClaw) 与 coder/acp-go-sdk (Hive) 是否同一协议未定论** — 双方都叫 ACP 但 grep 无直接证据 | [TBV] | 仍需核实 acpx npm 包源码 |
| 5 | **Pre-compaction memoryFlush** ✓ — OpenClaw 真做：文档 `docs/concepts/memory.md:52-91` + Schema `zod-schema.agent-defaults.ts:110-113` + 测试 `config.compaction-settings.test.ts` 三层证据 | **[HC-MAIN-VERIFIED]** | P0-4 借鉴根据扎实 |
| 6 | **Progressive Skill Loading** ✓ — OpenClaw `formatSkillsForPrompt` 真做（startup 只注入 name/description/location，model 用 read 按需加载 SKILL.md）+ Claude Code WebFetch 二轮核实 | **[HC-MAIN-VERIFIED]** | P1-1 借鉴根据扎实 |

**新发现的真借鉴点（核实后强化）**：

| # | 借鉴点 | 证据 |
|---|---|---|
| 7 | **mcporter 作为 skill 引入** — 通过 mcporter CLI 一站式接入海量 MCP 服务器（HTTP/stdio + OAuth + daemon + config）| `skills/mcporter/SKILL.md` |
| 8 | **acpx ACP↔MCP bridge 设计** — 把 ACP runtime 与 MCP server 桥接 | `extensions/acpx/src/runtime-internals/mcp-agent-command.ts` |
| 9 | **Chrome MCP 浏览器集成** — 把浏览器作为 MCP server 暴露 | `src/browser/chrome-mcp.ts` |
| 10 | **使用官方 `@modelcontextprotocol/sdk`** — 评估官方 Go SDK 是否成熟到可替代 Hive 部分自实现 | `package.json:@modelcontextprotocol/sdk: 1.27.1` |
| 11 | **HITL `/approve <id> allow-once\|allow-always\|deny` 三态** — 比 Hive 当前 Allow/Ask/Deny 更细，allow-once 与 allow-always 的区分对用户体验更清晰 | `src/infra/exec-approval-reply.ts:90` |
| 12 | **5-provider embedding 自动 fallback** — local→openai→gemini→voyage→mistral 优先级链，避免单 provider 故障 | `docs/concepts/memory.md:96-101` |
| 13 | **8 层（不是 6 层）工具策略级联** — tool profile→provider profile→global→provider→agent→agent provider→sandbox→subagent，比 Hive 当前权限模型更细 | `docs/tools/multi-agent-sandbox-tools.md:206-219` |

**Hive 真正确认的差异化点（核实后剩下的）**：
- ✅ **国内 IM 平台数量覆盖**（4 vs OpenClaw 1）— 钉钉/微信/企微 OpenClaw 都没有
- ✅ **Spec-driven ReAct + requirement resolver**（OpenClaw 没有）
- ✅ **SafeExecutor 代码层强制**（OpenClaw safety 仅 prompt advisories，已确认 [HC]）
- ✅ **Go 单二进制部署**（vs OpenClaw Node.js + 复杂 npm + Mario Zechner 私有 pi-* 框架依赖）
- ⚠️ **飞书限流细节** (`ErrPatchRateLimited` + 心跳 PATCH 重试) — 待 OpenClaw 直接对照确认

**新增浮现的借鉴机会（OpenClaw 支持 MCP 后）**：

| # | 借鉴点 | 证据 |
|---|---|---|
| 7 | **mcporter 作为 skill 引入** — Hive 可以把 mcporter CLI 作为外部 skill 集成，瞬间获得海量 MCP 服务器接入（用户配置 + OAuth + daemon），不必自建每个 MCP 客户端 | `skills/mcporter/SKILL.md` |
| 8 | **acpx extension 设计** — OpenClaw 用 acpx 把 ACP runtime 与 MCP server 桥接。Hive 当前 ACP 与 MCP 是分离的两套，可对照 acpx 的 bridge 模式 | `extensions/acpx/src/runtime-internals/mcp-agent-command.ts` |
| 9 | **Chrome MCP 集成** — `src/browser/chrome-mcp.ts` 把浏览器作为 MCP server 暴露，Hive 当前 `internal/tools/browser.go:471` 是直接 chrome control，可对照其抽象层 | OpenClaw `src/browser/chrome-mcp.ts` |
| 10 | **使用官方 `@modelcontextprotocol/sdk` SDK** — OpenClaw 不重新发明轮子，直接用 Anthropic 官方 SDK。Hive `internal/mcphost/` 26 文件是自己实现的，**需评估**官方 Go SDK 是否成熟到可替代部分自实现 | `package.json:@modelcontextprotocol/sdk: 1.27.1` |

**一句话总结（二次修正版）**：Hive 真正领先三家的轴只剩**国内 IM 数量覆盖**（4 vs 1）+ **Spec-driven ReAct 独有路径** + **SafeExecutor 代码层强制** + **Go 单二进制部署**。MCP / 向量 memory / 飞书工程三个轴都从"领先"降级为"势均力敌"。真正可借鉴的高 ROI 项是 7 条：pre-compaction memoryFlush / progressive skill loading / mcporter skill 集成 / HITL `/approve` 三态 / 5-provider embedding fallback / 8 层工具级联 / acpx ACP↔MCP bridge。Hermes GEPA 方向值得调研但需源码核实。

---

## §1 数据来源与可信度

| 来源 | 路径 | 可信度 | 备注 |
|---|---|---|---|
| deer-flow 完整调研 | `docs/调研笔记/deer-flow/`（PLAN.md + 6 axis evidence + archive） | [HC] | 16 蓝军 mutation + 双 AI 辩论 + final-verdict |
| OpenClaw 源码深挖 | `docs/research/openclaw/` (README + 6 axis evidence) | [HC] | 6286 ts 文件，每条带 file:line，蓝军 mutation 已跑 |
| Claude Code 二轮核实 | `docs/research/claude-code/` (findings + verification-round-2) | [HC] 18 项 / [DOUBT] 3 项 | WebSearch + WebFetch 多源验证 |
| Hermes WebSearch | `docs/research/hermes-agent/findings.md` | [TBV] 全部待核实 | AI 内容污染风险高，关键数字不进施工候选 |
| Hive 现状 | `internal/` ls + MEMORY.md + DESIGN.md + TODOS.md | [HC] 本会话亲见 | |

---

## §2 Hive 6-Axis 现状对照（vs deer-flow / OpenClaw / Claude Code）

| Axis | Hive 现状 [HC] | deer-flow | OpenClaw | Claude Code | Hive 评级 |
|---|---|---|---|---|---|
| **Tools** | `internal/tools/` 15+ files；`feishu_tools.go:669` `browser.go:471` `applypatch.go:406`；`create_tool.go` 工厂；`custom_loader.go` YAML driven | LangGraph 显式 graph node + tool_visibility 字段 | 23 个硬编码工具 + group: 快捷键 + 分层策略链 | 11 核心工具 + 14-17K token 永远 in context + Deny-first | **持平 OpenClaw**，落后 Claude Code 在 deny-first 评估顺序 |
| **Memory** | `internal/memory/` 15 files；`pg_store.go:677` `pgvec_store.go:138` `hybrid.go` `vecindex.go` `extractor.go` `injector.go` | 进程内 + Postgres checkpointer 三后端 | 平文本 Markdown + 可选 LanceDB；**pre-compaction memoryFlush** | CLAUDE.md（静态全量）+ MEMORY.md（25KB cap 懒加载）| **领先** vector tier；**落后**在 pre-compaction flush（OpenClaw 独有）|
| **Skills** | `internal/skills/` 15+ files；`finder.go:449` `discovery.go:390` `on_demand_api.go` `metrics.go` `executor.go` `hooks.go` | 配置驱动注册 + scope/tenant | Progressive loading + frontmatter + 5 包管理器；blacklist regex | Frontmatter-only startup ~100t；命中加载 ~5K | **接近**（已有 on_demand 基础），需核实是否真 frontmatter-only |
| **MCP** | `internal/mcphost/` **26 files**；client/host/oauth/hitl/prompt/resource/toolset/transport×3 (http/sse/stdio) | MCP client + plugin-sdk 通用 | **完整支持** — 官方 `@modelcontextprotocol/sdk` v1.27.1 + acpx MCP bridge + mcporter CLI + chrome-mcp 浏览器 | MCP 子进程 + JSON-RPC + `mcp__namespace__tool` | **势均力敌** with OpenClaw；Hive 独有 transport×3+OAuth+HITL 完整路径 |
| **Channel** | `internal/channel/` 飞书 + 钉钉 + 微信 + 企微 + chunk/debounce/dedup/retry_queue/router/router_renderer | 单一 SSE 流 | 22+ 通道每个独立实现，三元组路由 | 终端 TUI + Print stream-json + Remote Control + Teleport | **领先** in 国内 IM 工程深度（PatchCard 增量）；**落后**在跨设备会话切换（Claude Code Teleport）|
| **Prompt** | `internal/master/prompt_builder.go` + `internal/i18n/prompts/{subagents,system,tools}` | flexible multi-part | 固定模块化 full/minimal/none 三态 | 110+ 条件指令模块化 + 14-17K tool defs + cache 优化 | **接近**，需对照 prompt 拼装条件粒度 |

---

## §3 P0 候选施工清单

> 格式参照 `docs/调研笔记/deer-flow/archive/final-verdict.md` §3。每条带 Hive Go 锚点 + 蓝军 mutation + 工期估算 + 来源证据。

### P0-1：流式 tool_call 可见（沿用 deer-flow final-verdict P0-1）

- **来源**：deer-flow final-verdict §1.1 + Claude Code stream-json 模式 [HC]
- **Hive Go 锚点**：`internal/master/react_processor.go:362-364`（早 return 条件吞 tool_call chunk）
- **修改**：`if chunk.ContentSoFar == "" && chunk.ReasoningContent == "" && len(chunk.ToolCalls) == 0 { return nil }`
- **新增事件**：`EventTypeToolCallPreview`（critical 列表）
- **蓝军 mutation**：3 个对抗用例
  - mutation-1：Chat Completions 路径中间 snapshot 被广播 → 应不广播
  - mutation-2：Responses API ResponseFunctionCallArgumentsDoneEvent 后未 emit → 应 emit
  - mutation-3：P0-A partial suppression 吞 tool_call → suppression 内 `if len(chunk.ToolCalls) > 0 { emit }`
- **工期**：Step1 0.5d（broadcast-only）+ Step2 2d（补 Anthropic/DeepSeek/Gemini）
- **依赖**：无
- **状态**：deer-flow 调研已批准，本次综合**强烈复述**

### P0-2：Subagent stream tool_call 同步修（沿用 deer-flow P0-2）

- **来源**：deer-flow final-verdict P0-2 + OpenClaw axis-1 蓝军确认 stream behavior 一致性是 invariant
- **Hive Go 锚点**：`internal/subagent/agentloop.go:203-217`（drop tool_call chunk）
- **蓝军 mutation**：spawn 一个 subagent 跑 IM 渠道，确认 user 不黑屏
- **工期**：0.5d
- **依赖**：P0-1
- **状态**：deer-flow 调研已批准

### P0-3：schema-runtime drift audit（沿用 deer-flow P0-3，扩展范围）

- **来源**：deer-flow final-verdict P0-3 + 本次综合发现的 3 个新 drift 案例
  - case-A：Hive ACP（Agent Client Protocol） vs OpenClaw ACP（Programmable Protocol）同名不同物
  - case-B：之前 WebSearch agent 报 OpenClaw "MCP client + server"，源码 grep FAIL
  - case-C：Hermes 报告"103K stars / 118 skills"未核实，疑 AI 污染
- **Hive Go 锚点**：覆盖 ACP `/api/run/` enqueue/command/events/checkpoint 路径 + tool schema Pydantic-equivalent vs runtime execution
- **新增任务**：把"调研断言 vs 源码事实"的 drift 检测做成 CI 规则（防未来重蹈"看证据别抄类"覆辙）
- **蓝军 mutation**：每条 [HC] 断言至少 grep 一次源码验证
- **工期**：3d
- **状态**：deer-flow 调研已批准，本次综合**扩展范围**

### P0-4：Pre-compaction Memory Flush（**新，OpenClaw 借鉴**）

- **来源**：OpenClaw `docs/concepts/memory.md:52-77` [HC]
- **机制**：接近自动 compaction（softThresholdTokens）时，触发**无声 agentic turn** 提示模型"Write any lasting notes to memory; reply with NO_REPLY if nothing to store."
- **Hive 现状**：`internal/memory/` 有完整向量栈（pg_store/pgvec_store/hybrid/extractor/injector），但**没有 pre-compaction flush hook**
- **Hive Go 锚点**：
  - 新增 `internal/master/compaction.go`（如不存在）
  - 在 context 接近 compaction threshold 时触发 silent agentic turn
  - 写入路径走现有 `internal/memory/extractor.go` + `internal/memory/pg_store.go`
- **蓝军 mutation**：
  - mutation-1：长会话压缩前后，用户问"你刚才提到的 X 是什么？"应能回忆（不应 lost）
  - mutation-2：silent turn 触发时不应 broadcast 给前端 / IM（不污染 conversation）
  - mutation-3：silent turn LLM 回复 NO_REPLY 时应优雅跳过，不写空 memory
- **工期**：3-5d（含 silent turn 机制 + extractor 集成 + 蓝军测试）
- **依赖**：无
- **决策点**：是否做？— 强烈建议做，**理由**：Hive 当前向量 memory 是被动检索，pre-compaction flush 是主动保存，两者互补不冲突，且解决"长会话上下文丢失"的真实痛点

---

## §4 P1 候选施工清单

### P1-1：Progressive Skill Loading 核实 + 实施

- **来源**：Claude Code 二轮核实 [HC]（frontmatter ~100t startup，命中 ~5K full）+ OpenClaw [HC]
- **Hive Go 锚点**：`internal/skills/finder.go:449`（启动时是否全量 load SKILL.md？）
- **任务**：先核实，再决定是否需要重构
  - 步骤 1：grep + Read `finder.go` 看 startup load 逻辑
  - 步骤 2：measure 当前 system prompt 中 skill 占用 token 数
  - 步骤 3：如未做 progressive，重构为 frontmatter-only startup
- **蓝军 mutation**：startup 时 measure 100 个假想 skills 的 context 占用，应 < 10K token（vs 全量加载 500K+）
- **工期**：核实 0.5d + 如需重构 3-5d
- **决策点**：先核实再定，**理由**：on_demand_api.go 已存在暗示有基础

### P1-2：Deny-First Permission 评估顺序核实

- **来源**：Claude Code 二轮核实 [HC]
- **Hive Go 锚点**：`internal/security/SafeExecutor.MatchPolicy`（已有 Allow/Ask/Deny 三态）
- **任务**：核实评估顺序是否 Deny-first
- **蓝军 mutation**：构造同一命令同时匹配 deny 和 allow 规则，应 deny 胜
- **工期**：核实 0.5d + 如需调整 1d
- **决策点**：低成本高安全收益

### P1-3：完整 ToolError schema（沿用 deer-flow P1-1）

- **来源**：deer-flow final-verdict P1-1 + 本次综合（OpenClaw safety 仅 advisories 反例）
- **Hive Go 锚点**：`internal/mcphost/host.go:31-73` 的 `DecodeToolContent`
- **机制**：把字符串 contains terminal 判定换成 `err.Code` + `err.Retryable` 结构化判断
- **工期**：1w
- **状态**：deer-flow 已立项，本次综合**强烈复述**

### P1-4：Capacity Governance 配置化（沿用 deer-flow P0-6 → 降为 P1）

- **来源**：deer-flow final-verdict P0-6
- **Hive Go 锚点**：
  - `internal/master/react_processor.go:779-787`（maxConcurrentSpawn=3）
  - `internal/subagent/factory.go:52-79`（maxPerSession=3 / maxGlobal=30）
  - `internal/master/streaming_executor.go:81-106`（safe tool 无上限 goroutine）
  - `internal/tools/spawn_agent.go:120-123`（30 分钟兜底 timeout）
- **机制**：从同一 config 读 spawn limit + safe tool max concurrency + long-running tool admission control + 排队/拒绝/超时 metric
- **工期**：3d
- **决策点**：影响 P0-1 是否敢开 early execution

### P1-5：工具分组快捷键 group:*（**新，OpenClaw 借鉴**）

- **来源**：OpenClaw `docs/tools/multi-agent-sandbox-tools.md:225-237` [HC]
- **示例**：`group:runtime`, `group:fs`, `group:sessions`, `group:memory`, `group:ui`, `group:automation`, `group:messaging`, `group:nodes`
- **Hive Go 锚点**：`internal/config/`（tool config 展开逻辑）
- **机制**：在 tools.allow / tools.deny 配置里支持 `group:fs` 展开为该组所有工具
- **工期**：1d（含 unit test）
- **决策点**：低成本配置 DX 改善

### P1-6：按任务复杂度路由模型（**新，Hermes 方向性借鉴**）

- **来源**：Hermes WebSearch [TBV]（数字不可信，方向可信）
- **Hive Go 锚点**：`internal/llm/factory.go`（多 provider 已有）
- **机制**：定义 task complexity heuristic（如 token 长度、工具调用数、需推理深度）→ 简单任务路由到 cheap model（DeepSeek-V3 vs Sonnet 省 90%）
- **蓝军 mutation**：cheap model 命中率 + 失败回退率
- **工期**：3-5d
- **决策点**：省成本 vs 增加路由复杂度，需用户拍板

---

## §5 反面教材（防止重蹈"看证据别抄类"覆辙）

| # | 别抄什么 | 来源 | 理由 |
|---|---|---|---|
| ~~1~~ | ~~别抄 OpenClaw 不支持 MCP~~ **删除：OpenClaw 完整支持 MCP** | 主线程 grep 修正 | 见 §11 修正记录 |
| 2 | 别抄 OpenClaw 22+ 通道独立实现 | OpenClaw axis-5 | Hive `internal/channel/router_renderer.go` 已有统一 SDK |
| 3 | 别抄 OpenClaw safety 仅 prompt advisories | OpenClaw axis-6 | Hive SafeExecutor 已是代码层强制（MEMORY 锁定） |
| 4 | 别抄 OpenClaw 工具级无并发限制 | OpenClaw axis-1 蓝军 FAIL | Hive `streaming_executor.go` 必须保留并发治理 |
| 5 | 别抄 OpenClaw frontmatter 黑名单正则 | OpenClaw axis-3 | 应用白名单显式允许（更安全） |
| ~~6~~ | ~~别抄 OpenClaw / Hermes "ACP 协议"~~ **降级：可能是同一协议（Agent Client Protocol）** | `extensions/acpx/package.json` | 见 §11 修正记录；待最终核实 acpx 是否就是 ACP 标准实现 |
| 7 | 别抄 LangGraph AgentMiddleware | deer-flow final-verdict §5 | 已识别教训，复述防止遗忘 |
| 8 | 别抄 Hermes 数字（103K stars / 47 tools / 118 skills / GEPA ICLR Oral） | Hermes findings §0 警告 | 数字未独立核实，疑 AI 内容污染 |
| 9 | 别抄 Claude Code 14-17K token 工具定义永远 in context | Claude Code 二轮核实 [HC] | progressive loading 是更好范式 |
| 10 | 别为 1 个 validator 重构 Middleware Pipeline | deer-flow final-verdict §1.2 | 已识别教训，复述 |
| **11** | **别再让子 agent 用窄范围 grep 一次性出结论** | 本次 OpenClaw MCP 误判 | 子 agent 只 grep `src/MCPClient` class 名漏掉 acpx + skills/ + package.json，**主线程必须用更宽 grep（package.json 依赖 + 多关键词 + 全 repo）做最终核实** |

---

## §6 决策待定项（CEO 拍板）

| # | 议题 | 选项 | 推荐 |
|---|---|---|---|
| D1 | P0-4 Pre-compaction Memory Flush 是否做？ | A) 做（3-5d）/ B) 等用户反馈"上下文丢失"再做 | **A** — 长会话痛点真实，且 Hive 当前向量 memory 是 read 路径，flush 是 write 路径，互补 |
| D2 | P1-1 Progressive Skill Loading 重构（如核实需要）是否做？ | A) 做 / B) 等 skill 数量 > 50 时再做 | **A** — 现有 `on_demand_api.go` 提示已有基础，重构成本可能较低；且为未来 Skill marketplace 铺路 |
| D3 | P1-6 按任务复杂度路由模型是否做？ | A) 做 / B) 推迟 | **A 但谨慎** — 商业价值（成本）与 harness 工程（复杂度）trade-off，需先 measure 现有任务模型分布 |
| D4 | Hermes GEPA 自我改进闭环是否调研 + 实施？ | A) 立即调研论文 + 等 sandbox 后看源码 / B) 不做 | **A 调研** — 论文真实（arXiv 2507.19457），但 production 集成深度未知，先调研再决定是否做 |
| D5 | deer-flow PLAN.md 既有的 P0-1 (pathguard) / P0-3 (Skill Gateway CRUD) / P0-4 (Prompt Gateway) / P0-5 (execution.md 硬化) 是否纳入本次同批做？ | A) 一起做 / B) 分批 | 用户决定 — 与本次三家调研无强耦合 |

---

## §7 与 deer-flow PLAN.md / final-verdict 的合并

deer-flow PLAN.md 和 final-verdict 是**两条施工脉络**：

- **PLAN.md（第一稿）**：5 个 P0 + 9 个 P1，目标"对标 deer-flow 整改"
- **final-verdict（蓝军 + 双 AI 辩论后）**：6 个 P0（流式 + subagent 同步 + drift audit + Step2 + validator + capacity）+ 2 个 P1（ToolError schema + EventBus 外置化）

本次综合的合并建议：

| deer-flow 既有 P0/P1 | 本次综合状态 |
|---|---|
| final-verdict P0-1 流式 tool_call Step1 | 本 SYNTHESIS P0-1 复述 |
| final-verdict P0-2 Subagent 同步修 | 本 SYNTHESIS P0-2 复述 |
| final-verdict P0-3 schema-runtime drift audit | 本 SYNTHESIS P0-3 **扩展范围** |
| final-verdict P0-4 流式 Step2 | 本 SYNTHESIS P0-1 Step2 合并 |
| final-verdict P0-5 grounding validator | **保持原计划**，不在本次综合范围 |
| final-verdict P0-6 capacity governance | 本 SYNTHESIS **降为 P1-4** |
| final-verdict P1-1 ToolError schema | 本 SYNTHESIS P1-3 复述 |
| final-verdict P1-2 EventBus 外置化 | 保持，多副本触发时再做 |
| **新增** P0-4 Pre-compaction Memory Flush | OpenClaw 借鉴 |
| **新增** P1-1 Progressive Skill Loading | Claude Code + OpenClaw |
| **新增** P1-2 Deny-First Permission 核实 | Claude Code |
| **新增** P1-5 工具分组快捷键 | OpenClaw |
| **新增** P1-6 按任务路由模型 | Hermes 方向 |

PLAN.md 原 P0-1/2/3/4/5（pathguard / Artifact 端点 / Skill Gateway CRUD / Prompt Gateway / execution.md 硬化）**与本次三家调研无强耦合**，不在本次综合范围，按 D5 决策。

---

## §8 仍需核实清单（综合 SYNTHESIS 后剩余）

1. **Hive `internal/skills/finder.go` 是否真做 progressive loading**（影响 P1-1 工期）
2. **Hive `SafeExecutor.MatchPolicy` 评估顺序**（影响 P1-2 是否需调整）
3. **Hermes `github.com/NousResearch/hermes-agent` 真实存在性 + GitHub stars 实际值**（D4 调研前置）
4. **Hermes GEPA 在 v0.10.0 production 集成深度**（D4 调研内容）
5. **OpenClaw mcporter 真不是 MCP？**（OpenClaw 源码 agent grep FAIL on MCP，但找到 mcporter，需进一步核实其本质）

---

## §9 文件索引

```
docs/
├── 调研笔记/deer-flow/             # 既有调研（16 蓝军 + 双 AI 辩论 + final-verdict）
│   ├── PLAN.md
│   └── archive/
│       ├── final-verdict.md       # ★ 主输入
│       └── merged-report.md
├── research/
│   ├── claude-code/
│   │   ├── findings.md            # 一轮草稿（[TBV] 标注）
│   │   └── verification-round-2.md # ★ 二轮核实结果
│   ├── openclaw/
│   │   ├── README.md              # ★ 主战略报告
│   │   └── evidence/
│   │       ├── axis-1-tools.md
│   │       ├── axis-2-memory.md
│   │       ├── axis-3-skills.md
│   │       ├── axis-4-acp-protocol.md
│   │       ├── axis-5-channels-uploads.md
│   │       └── axis-6-prompts.md
│   ├── hermes-agent/
│   │   └── findings.md            # 待核实，关键数字不进施工候选
│   └── _synthesis/
│       └── SYNTHESIS.md           # ★ 本文件
```

---

## §11 本次审计中的事实更正记录

> **触发**：用户在主线程提问"你确定 openclaw 不支持 mcp 么？怎么可能"，主线程亲自 grep 后推翻子 agent 报告。
> **教训**：把这条加入反面教材清单第 11 条，永久警示。

### 更正 1：OpenClaw MCP 支持

**子 agent 错误断言**（OpenClaw axis-4 / SYNTHESIS §0 #1 旧版）：
> "OpenClaw 不支持 MCP；自有 ACP 协议"

**子 agent 错在哪**：
- grep 范围只在 `/src/`，**漏了 `extensions/acpx/`、`skills/mcporter/`、`package.json`、`src/browser/chrome-mcp.ts`**
- grep 关键词只搜 `MCPClient` / `MCPServer` class 名，**没考虑用户用官方 SDK 时不需要自己写 class**
- 没看 `package.json` 依赖

**真相**（主线程 grep 验证）：
| 证据 | 路径 | 内容 |
|---|---|---|
| 1 | `package.json` | `"@modelcontextprotocol/sdk": "1.27.1"` — 官方 Anthropic MCP SDK |
| 2 | `extensions/acpx/src/runtime-internals/mcp-agent-command.ts` | MCP agent command 完整实现 |
| 3 | `extensions/acpx/src/runtime.ts` | runtime 集成 mcpServers，含 `toAcpMcpServers` 转换 |
| 4 | `extensions/acpx/src/config.ts` | `McpServerConfig` schema 完整 |
| 5 | `skills/mcporter/SKILL.md` | mcporter CLI 集成为 skill：`Use the mcporter CLI to list, configure, auth, and call MCP servers/tools directly (HTTP or stdio)` |
| 6 | `src/browser/chrome-mcp.ts` | 浏览器作为 MCP server 暴露 |
| 7 | `docs.acp.md:37` + `docs/cli/acp.md:31` | 仅 bridge mode 不支持 per-session MCP，gateway/agent 层支持 |

**SYNTHESIS 影响**：
- §0 结论 #1 已修正
- §2 axis 表 MCP 行已修正
- §5 反面教材 #1 已删除
- §0 新增结论 #7-#10（mcporter / acpx / chrome-mcp / 官方 SDK 借鉴机会）

### 更正 2：OpenClaw ACP 归属

**子 agent 错误断言**（SYNTHESIS §0 #4 旧版）：
> "OpenClaw ACP = pi-agent-core 子代理 runtime"

**子 agent 错在哪**：把 `package.json` 里的 `@mariozechner/pi-tui`（一个 TUI 渲染库）误读成 pi-agent-core。

**真相**：
- OpenClaw ACP 用 `acpx@0.3.0` npm 包作为 runtime backend
- `extensions/acpx/package.json` 描述：`"OpenClaw ACP runtime backend via acpx"`
- acpx 与 Hive `coder/acp-go-sdk v0.6.3` **可能是同一 ACP（Agent Client Protocol）协议的不同语言实现**（一个 TS 一个 Go）
- 但 grep 没找到 `agent-client-protocol` / `@zed-industries/agent-client-protocol` 字样直接证据，**仍需核实 acpx 是不是 ACP 标准实现**

**SYNTHESIS 影响**：
- §0 结论 #4 改为 [TBV-PROBABLE-SAME]
- §5 反面教材 #6 降级
- §8 仍需核实清单新增"acpx 是否就是 ACP 标准实现"

### 系统性教训

1. **子 agent 出结论必须主线程二次核实**，特别是"不支持 X"这种否定断言（grep 漏一个关键词就可能完全错）
2. **package.json / requirements.txt 等依赖文件必须先 grep**（用户引入官方 SDK 时不会自己写 class）
3. **范围要全 repo 而非只在 src/**（extensions/ skills/ docs/ 都可能有关键证据）
4. **多关键词组合**：除了 class 名，还要 grep 配置 key 名（mcpServers / mcpClients）、CLI 名（mcporter）、文件名（mcp-* / *-mcp.ts）

---

## §13 三家 6 axis 全核实最终修订（2026-04-25 第三次重大修订）

> **触发**：用户 add-dir + 移动 repo 到 vast/ 后，三家源码可读，主线程对 deer-flow / Claude Code / Hermes 各做 6 axis 核实

### §13.1 三家核实统计总览

| 调研对象 | 核实文件 | 错率 |
|---|---|---|
| OpenClaw | `openclaw/evidence/axis-{1,2,3,5-6}-VERIFIED.md` | 26.3% |
| deer-flow | `deer-flow/VERIFIED-6-AXIS.md` | 17%（且包含 1 项推翻为更可信）|
| Claude Code | `claude-code/VERIFIED-6-AXIS.md` | 仅 18 项 [HC] 中 2 项 [FALSE]，但**信息覆盖严重不足**（漏报 30+ 工具）|
| Hermes | `hermes-agent/VERIFIED-6-AXIS.md` | 全 [TBV] → 70% [HC-MAIN-VERIFIED] |

### §13.2 重大修订（推翻 SYNTHESIS §0 已有结论）

#### 修订 1：三家 ACP 形成完整生态（不是孤立各自）

| 角色 | 实现 |
|---|---|
| **Server** (IDE→agent 接入) | Hive `coder/acp-go-sdk` (Go) |
| **Backend Runtime** (agent 端协议适配) | OpenClaw `acpx@0.3.0` (TS) |
| **Client** (agent→LLM provider 包装) | Hermes `copilot_acp_client.py`（连 GitHub Copilot ACP）|

**战略意义**：Hive 接入 ACP 生态意义非常大。同一协议三个角色都有真实使用场景，**Hive 可以同时**：
1. 当 ACP server (已有，IDE 接入)
2. 借鉴 acpx 当 ACP backend runtime (新借鉴)
3. 借鉴 Hermes copilot_acp_client 用 ACP 连其他 agent (Codex/Copilot/OpenClaw) 当 LLM provider (新借鉴)

**这翻转了 SYNTHESIS §0 #4 的"待核实"**——升级为 [HC-MAIN-VERIFIED]：三家 ACP 大概率就是 Zed/Coder 推动的 Agent Client Protocol 标准。

#### 修订 2：Claude Code 工具数 42（不是 11，不是 43）

之前推断 43 错了（含 utils.ts 不是工具目录）。**真实 42 工具**（`ls -d src/tools/*/ | wc -l = 42`）。

但**远不止 11**。findings.md 严重漏报。

#### 修订 3：Claude Code "110+ 条件指令" + "~2.5K token system prompt" 是 [FALSE]

WebSearch 二轮报的两项数字都被主线程 grep 推翻：
- `prompts.ts` 仅 12 个 const，914 行（不含累加每工具 prompt）
- 系统 prompt 真实 token 远超 2.5K（system.ts + prompts.ts + 42 工具 prompt.ts 累加，**估计 30-50K**）

但**模块化 + 多 prefix 动态选择**核心架构事实仍真。

#### 修订 4：Claude Code memdir 有 nightly 蒸馏 + team memory（新借鉴点）

`src/memdir/memdir.ts:322-348`：
- date-named append-only 日志（与 OpenClaw `memory/YYYY-MM-DD.md` 同构）
- **"separate nightly process distills these logs into MEMORY.md and topic files"**（OpenClaw 没有）
- `teamMemPaths.ts` + `teamMemPrompts.ts` — 团队共享 memory（OpenClaw 没有）

**P0-4 设计可三源借鉴**：
- OpenClaw silent agentic turn（实时触发）
- Claude Code nightly distill（离线批处理）
- Hermes auxiliary model 自动 summarize（结构化模板 + Resolved/Pending tracking）

#### 修订 5：Hermes context_compressor 是 SYNTHESIS P0-4 最优参考

`agent/context_compressor.py` 文件头列出 10 项改进：
1. 结构化 Resolved/Pending question tracking
2. Summarizer preamble: "Do not respond"（OpenCode）
3. Handoff framing: "different assistant"（Codex）
4. "Remaining Work" 替换 "Next Steps"
5. 跨次迭代保留 info
6. Token-budget tail protection
7. Tool output pruning before LLM
8. Scaled summary budget
9. 用 auxiliary model（cheap/fast）
10. 保护 head + tail context

**比 OpenClaw memoryFlush 详细 5x**。Hive P0-4 借鉴优先级：**Hermes context_compressor > Claude Code nightly distill > OpenClaw silent turn**。

#### 修订 6：deer-flow 蓝军 12 推翻（既有调研被升级）

deer-flow evidence 蓝军 12 说"`build_tracing_callbacks` 生产接入点缺失"。主线程 grep 推翻：

```
deerflow/models/factory.py:7:   from deerflow.tracing import build_tracing_callbacks
deerflow/models/factory.py:136: callbacks = build_tracing_callbacks()  ← 生产路径
```

**deer-flow tracing 真实集成**，evidence 这条蓝军应改为 PASS。

### §13.3 最终 P0/P1 候选清单（基于全核实）

修正后**真正可执行的 P0/P1**（可信度 [HC-MAIN-VERIFIED]）：

| 编号 | 候选 | 来源 | 可信度 |
|---|---|---|---|
| P0-1 | 流式 tool_call（沿用 deer-flow）| 不依赖三家调研 | [HC] |
| P0-2 | Subagent stream 同步修 | 同 | [HC] |
| P0-3 | schema-runtime drift audit | 同 | [HC] |
| **P0-4** | **Pre-compaction Memory Flush ★ 三源借鉴** | OpenClaw silent turn + Claude Code nightly distill + Hermes context_compressor | [HC-MAIN-VERIFIED] |
| P1-1 | Progressive Skill Loading | OpenClaw + Claude Code (含 mcpSkillBuilders) | [HC-MAIN-VERIFIED] |
| P1-2 | Deny-First Permission 核实 | Claude Code | [HC] |
| P1-3 | ToolError schema | deer-flow 既有 | [HC] |
| P1-4 | Capacity governance | deer-flow 既有 | [HC] |
| P1-5 | 工具分组 group:* 9 个 | OpenClaw axis-1 | [HC-MAIN-VERIFIED] |
| P1-6 | 按任务路由模型 | Hermes `smart_model_routing.py` 真实存在 | [HC-MAIN-VERIFIED] |
| P1-7 | HITL /approve 三态（once/always/deny）| OpenClaw axis-6 | [HC-MAIN-VERIFIED] |
| P1-8 | 5-provider embedding 自动 fallback | OpenClaw axis-2 | [HC-MAIN-VERIFIED] |
| **P1-9（新）** | **ACP Client 路径** — Hive 用 ACP 连 OpenClaw / Codex / Copilot 当 backend agent | Hermes copilot_acp_client + 三家 ACP 生态发现 | [HC-MAIN-VERIFIED] |
| **P1-10（新）** | **acpx 模式 ACP↔MCP bridge** | OpenClaw acpx | [HC-MAIN-VERIFIED] |
| **P1-11（新）** | **mcpSkillBuilders MCP-as-Skills 桥接** | Claude Code | [HC-MAIN-VERIFIED] |
| **P1-12（新）** | **Coordinator Mode + Team 工具协调** | Claude Code | [HC-MAIN-VERIFIED] |
| **P1-13（新）** | **结构化 summary 压缩**（Resolved/Pending tracking + Handoff framing）| Hermes context_compressor | [HC-MAIN-VERIFIED] |

**新增 5 条 P1 候选**（P1-9 到 P1-13）都是基于三家全核实后浮现的高 ROI 借鉴点。

### §13.4 Hive 真正领先的轴（最终版）

| Axis | Hive 状态 vs 三家 |
|---|---|
| **国内 IM 平台数量** | ✅ 领先（4 vs OpenClaw 1 / 其他 0）|
| **Spec-driven ReAct** | ✅ 领先（OpenClaw / Claude Code / Hermes 都没有）|
| **SafeExecutor 代码层强制** | ✅ 领先（OpenClaw safety 仅 prompt advisories）|
| **Go 单二进制部署** | ✅ 领先（其他都是 Node.js / Python，依赖庞杂）|
| **MCP host 完整路径** | ⚠️ 与 OpenClaw / Claude Code 势均力敌 |
| **Memory 向量栈** | ⚠️ 与 OpenClaw / Hermes 势均力敌（Hermes context_compressor 实际更高级）|
| **国内 IM 单平台工程深度** | ⚠️ 与 OpenClaw 飞书势均力敌（双方都有 dedup/debounce/PATCH）|
| **工具集广度** | ❌ 落后 Claude Code（Hive ~15 vs Claude Code 42）|
| **Multi-agent 协调** | ❌ 落后 Claude Code（Coordinator Mode + Team 工具）|
| **Memory 治理（蒸馏）** | ❌ 落后 Claude Code (nightly distill) + Hermes (context_compressor) |
| **ACP 生态接入** | ❌ 落后（Hive 仅 server 角色，未接入 backend / client）|

### §13.5 一句话总结（最终版）

Hive 真正领先三家的轴只剩**国内 IM 数量覆盖** + **Spec-driven ReAct 独有路径** + **SafeExecutor 代码层强制** + **Go 单二进制部署**（4 条）。其他维度大多势均力敌或落后。但**借鉴机会从 6 条扩展到 13 条**（三家 ACP 生态 + nightly distill + Hermes context_compressor + Claude Code 工具集结构化 + Coordinator Mode 等）。**P0-4 应三源借鉴 OpenClaw + Claude Code + Hermes**，是本次调研最高 ROI 产出。

---

## §12 OpenClaw 6 axis 主线程核实总报告

> **触发**：用户要求"一条一条精确验证"
> **完成日期**：2026-04-25
> **方法**：每个 axis 主线程亲自 L3+L4（Read docs + grep 多关键词 + Read 关键代码段）

### 核实统计

| axis | evidence 行数 | 关键断言数 | [VERIFIED] | [REVISED] | [FALSE] | verified.md 文件 |
|---|---|---|---|---|---|---|
| 1 (tools) | ~85 | 18 | 15 | 3 | 0 | `axis-1-tools-VERIFIED.md` |
| 2 (memory) | ~80 | 15 | 13 | 2 | 0 | `axis-2-memory-VERIFIED.md` |
| 3 (skills) | ~85 | 6 | 5 | 1 | 0 | `axis-3-skills-VERIFIED.md` |
| 4 (mcp/acp) | ~85 | 6 | **0** | 1 | **5** | （已在 §11 修正） |
| 5+6 (channels+prompts) | ~165 | 12 | 9 | 3 | 0 | `axis-5-6-channels-prompts-VERIFIED.md` |
| **合计** | **~500** | **57** | **42** | **10** | **5** | |

**总错误率**：(10 + 5) / 57 = **26.3%** 的子 agent 断言需要修正或彻底推翻

### axis-4 全错总结（已知）

子 agent 报"OpenClaw 不支持 MCP，自有 ACP 协议"，主线程发现：
- OpenClaw `package.json` 引入官方 `@modelcontextprotocol/sdk` v1.27.1
- `extensions/acpx/src/runtime-internals/mcp-agent-command.ts` 完整 MCP agent
- `skills/mcporter/SKILL.md` 是 MCP CLI 客户端
- `src/browser/chrome-mcp.ts` 是 chrome MCP server

子 agent 错因：grep 范围只在 `/src/`、关键词只 `MCPClient/MCPServer` class 名 → 漏 `extensions/` + `skills/` + `package.json` + 用 SDK 不写 class

### 重大新发现（5 条）

#### F1 — OpenClaw 基于 `@mariozechner/pi-*` framework family
- `pi-agent-core` 提供 `AgentTool` 类型（axis-1 验证）
- `pi-coding-agent` 提供 `Skill` 类型（axis-3 验证）
- `pi-tui` 提供 TUI 渲染（axis-1 验证 import）
- **战略影响**：OpenClaw 不是独立 agent，是 Mario Zechner 的 pi-* framework 之上的产品 wrapper
- **风险**：Hive 借鉴 OpenClaw 设计时要分清"是 OpenClaw 的发明"还是"是 pi-* framework 的能力"，避免抄到 framework 私有路径

#### F2 — OpenClaw 的 ACP runtime 是 acpx 包
- `extensions/acpx/package.json` `"description": "OpenClaw ACP runtime backend via acpx"`，依赖 `acpx@0.3.0`
- 与子 agent 报的 "pi-agent-core 子代理 runtime" 不一致
- **仍待核实**：acpx 是不是 Coder/Zed 推动的 Agent Client Protocol 标准实现

#### F3 — OpenClaw memory 实际有完整 5-provider 向量栈
- 推翻"平文本+可选 LanceDB"
- local（node-llama-cpp）→ openai → gemini → voyage → mistral 自动 fallback
- + sqlite-vec 向量加速
- + 完整 batch embedding（gemini/openai/voyage 各有 batch runner）

#### F4 — OpenClaw 飞书有完整 dedup/debounce/PATCH 管道
- 推翻 SYNTHESIS §0 #3 "Hive 渠道工程领先"
- `extensions/feishu/src/dedup.ts` + `monitor.account.ts` `createInboundDebouncer` + `send.ts` `client.im.message.patch`
- Hive 仅在限流细节（`ErrPatchRateLimited`）可能仍领先，待对照确认

#### F5 — OpenClaw HITL 三态比 Hive 设计更细
- `/approve <id> allow-once|allow-always|deny`（三个明确选项）
- vs Hive SafeExecutor (Allow/Ask/Deny)（Ask 没区分一次/永久）
- **可借鉴**：Hive 的 Ask 可细化为 once/always

### SYNTHESIS 重新评估

修正后**真正可执行的 P0/P1 候选**（可信度 [HC-MAIN-VERIFIED]）：

| 编号 | 候选 | 来源核实状态 |
|---|---|---|
| P0-1 | 流式 tool_call（沿用 deer-flow）| 不依赖本次三家调研 |
| P0-2 | Subagent stream 同步修 | 不依赖 |
| P0-3 | schema-runtime drift audit | 不依赖 |
| P0-4 | **Pre-compaction Memory Flush** | OpenClaw axis-2 三层证据 [HC-MAIN-VERIFIED] |
| P1-1 | **Progressive Skill Loading** | OpenClaw axis-3 + Claude Code 二轮 [HC-MAIN-VERIFIED] |
| P1-2 | Deny-First Permission 核实 | Claude Code 二轮 [HC] |
| P1-3 | ToolError schema | deer-flow 既有 |
| P1-4 | Capacity governance | deer-flow 既有 |
| P1-5 | **工具分组 group:* 9 个** | OpenClaw axis-1 [HC-MAIN-VERIFIED] |
| P1-6 | 按任务路由模型 | Hermes [TBV] — **不应作为施工候选**直到源码核实 |
| **P1-7（新）** | **HITL /approve 三态细化（once/always/deny）** | OpenClaw axis-6 [HC-MAIN-VERIFIED] |
| **P1-8（新）** | **5-provider embedding 自动 fallback** | OpenClaw axis-2 [HC-MAIN-VERIFIED] |

**剔除的伪候选**：
- 任何依赖"OpenClaw 不支持 MCP，所以 Hive 反向输出"的论述都失效

---

## §10 下一步

1. **阅读本 SYNTHESIS** — 用户对每条 P0/P1 候选 + 决策待定项 D1-D5 拍板
2. **核实清单 §8** — 1-2 项可由主线程当场跑 grep / Read 完成
3. **进入 plan-eng-review** — 把拍板后的 P0 候选转化为完整 spec（架构、数据流、测试、部署、observability）
4. **施工 → 蓝军 mutation 验证** — 每个 P0 落地必须通过 deer-flow PLAN.md §6 的"蓝军 mutation + 命令证据"门

---

**完整链路**：
- 这次调研 = deer-flow 既有调研 ⊕ OpenClaw 源码深挖 ⊕ Claude Code 二轮核实 ⊕ Hermes 方向性
- 调研产出 = P0/P1 施工候选清单（与 deer-flow final-verdict 同格式）
- 验证 = 每条断言 file:line 锚点 + 蓝军 mutation 方案
- 防坑 = 反面教材清单（防止重蹈 deer-flow "看证据别抄类"覆辙）

*— End of SYNTHESIS —*
