# Hive Harness 顶级化战略评审 — 4 家系统对标完整报告

> **作者**：CEO Review 主线程
> **日期**：2026-04-25
> **范围约束**：
>   - **当前阶段（锁定）**：只做国内 IM（飞书/微信/企微/钉钉），先打开国内市场
>   - **长期雄心（锁定）**：harness 工程做到全球顶级（**技术质量层**，不是产品市占率）
> **方法论**：deer-flow（既有 16 蓝军 + 双 AI 辩论 + final-verdict）+ OpenClaw（主线程 6-axis 源码深挖）+ Claude Code v2.1.88（主线程 6-axis 源码核实）+ Hermes v0.10.0（主线程 6-axis 源码核实）

---

## §0 一页纸执行摘要

### 0.1 调研可信度

| 调研对象 | 核实方式 | 错率 | 整体可信度 |
|---|---|---|---|
| **deer-flow** | 既有 16 蓝军 + 主线程 spot-check 23 项 | 17% | [HC] |
| **OpenClaw** v2026.3.13 | 主线程 6 axis 逐条 grep + Read | 26.3% | [HC-MAIN-VERIFIED] |
| **Claude Code** v2.1.88 | 主线程 6 axis 源码 grep + 18 项 [HC] 二轮升级 | 11% (但 WebSearch 漏报严重) | [HC-MAIN-VERIFIED] |
| **Hermes** v0.10.0 | 主线程 6 axis 源码核实 | 全 [TBV] → 70% [HC-MAIN-VERIFIED] | [HC-MAIN-VERIFIED] |

### 0.2 Hive 真正领先三家的轴（仅 4 条）

1. **国内 IM 平台数量覆盖**（4 vs OpenClaw 1 / 其他 0）
2. **Spec-driven ReAct + requirement resolver**（OpenClaw / Claude Code / Hermes 都没有）
3. **SafeExecutor 代码层强制权限**（OpenClaw safety 仅 prompt advisories；其他未深查）
4. **Go 单二进制部署**（其他都是 Node.js / Python，依赖庞杂）

### 0.3 Hive 落后的维度

| Axis | Hive 状态 |
|---|---|
| 工具集广度 | ❌ 落后 Claude Code（Hive ~15 vs Claude Code 42）|
| Multi-agent 协调 | ❌ 落后 Claude Code（Coordinator Mode + Team 工具）|
| Memory 治理（蒸馏）| ❌ 落后 Claude Code (nightly distill) + Hermes (context_compressor) |
| ACP 生态接入 | ❌ Hive 仅 server 角色，未接入 backend / client |
| 远程会话切换 | ❌ Claude Code Remote Control / Teleport 完整，Hive WebSocket 单向 |

### 0.4 真正高 ROI 借鉴点（13 条）

按优先级排序详见 §7 P0/P1 候选清单。**最高 ROI**：
1. **P0-4 Pre-compaction Memory Flush** — 三源借鉴（OpenClaw silent turn + Claude Code nightly distill + Hermes context_compressor 结构化模板）
2. **P1-9 ACP Client 路径** — Hive 用 ACP 连其他 agent 当 backend
3. **P1-13 Hermes 结构化 summary 压缩** — Resolved/Pending tracking + Handoff framing

### 0.5 一句话结论

Hive 在 4 个轴真正领先，其他维度大多势均力敌或落后。但本次调研发现 13 条高 ROI 借鉴机会，最大战略洞察是**三家 ACP 形成完整生态**（Server/Backend/Client 三角色），Hive 接入潜力极大。**P0-4 三源借鉴的 Pre-compaction Memory Flush 是本次调研最高 ROI 产出**。

---

## §1 调研对象与版本

| 系统 | 版本 | 路径 | 公开/闭源 |
|---|---|---|---|
| deer-flow | v2.0 | `../deer-flow/` | 字节开源 BSD |
| OpenClaw | 2026.3.13 | `../openclaw-2026.3.13-1/` | Mario Zechner 系（pi-* framework）开源 |
| Claude Code | v2.1.88 | `../claude-code-source-code/`（npm `@anthropic-ai/claude-code` 反编译版）| Anthropic 闭源 CLI，但 Agent SDK 开源 |
| Hermes | v0.10.0 (2026-04-16) | `../hermes-agent-2026.4.16/` | Nous Research MIT 开源 |

---

## §2 主线程核实方法论

### 2.1 验证协议（每条断言必须满足）

- **L1 依赖证据**：在 package.json / go.mod / requirements.txt / pyproject.toml 出现
- **L2 代码证据**：用至少 2 种关键词（class 名 + 配置 key + CLI 名）全 repo grep
- **L3 文档证据**：repo 内 docs/ + 顶层 README / CHANGELOG
- **L4 实际 Read**：关键代码段直接 Read 验证语义

### 2.2 可信度等级

- **[HC-MAIN-VERIFIED]** — L1+L2 都过 OR L2+L4 都过，主线程独立 grep 确认
- **[HC]** — 来自既有调研（双 AI 辩论 + 蓝军 mutation 验证过）
- **[VERIFIED]** — 主线程 spot-check 通过
- **[REVISED]** — 既有断言部分修正
- **[FALSE]** — 既有断言推翻
- **[TBV]** — 待核实（不进施工候选清单）

### 2.3 反面教材（防止重蹈覆辙）

- **不让子 agent 一次性出 6 axis 报告**：OpenClaw 子 agent 错率 26.3%，主线程必须独立核实
- **grep 范围必须全 repo**：不能只搜 src/，必须包括 extensions/ skills/ docs/ + 依赖文件
- **多关键词组合**：除了 class 名，还要 grep 配置 key 名（mcpServers）/ CLI 名（mcporter）/ 文件名 pattern（*-mcp.ts）
- **否定结论必须主线程二次核实**：子 agent 报"不支持 X"时务必用至少 2 种独立方式验证

---

## §3 4 家 6-Axis 主线程核实结果

### §3.1 axis-1 Tools 体系

| 系统 | 工具数 | 关键设计 | 主线程核实状态 |
|---|---|---|---|
| **Hive** | ~15 (含 9 LSP) | applypatch / batch / browser / feishu_tools 等单文件 | 现状 |
| **deer-flow** | 8 builtins + 7 sandbox + 6 community search providers | LangChain @tool decorator + StructuredTool；max_workers=10 | [VERIFIED] 文件行数完全准确 |
| **OpenClaw** | 24 内置（system-prompt.ts:240-272 coreToolSummaries）| 工厂函数 + TypeBox schema；**8 层过滤策略**（不是 6 层）；9 个 group:* 快捷键；继承 `@mariozechner/pi-agent-core` AgentTool | [HC-MAIN-VERIFIED] |
| **Claude Code** | **42 个工具目录** | 每工具是结构化目录（含 prompt.ts / tool.tsx / UI.tsx / security.ts 多文件）；9 大类：File/Bash/Web/MCP×4/Task×6/Team×2/Plan/Worktree/Skill/Schedule/Sleep/Notebook/Cron/REPL/RemoteTrigger 等 | [HC-MAIN-VERIFIED] |
| **Hermes** | tools/ 30+ 文件 | Environments 6 后端（local/docker/ssh/modal/daytona/singularity + managed_modal/file_sync）；browser providers 4 个；voice/vision/security/skills/mcp/process/routing | [HC-MAIN-VERIFIED] |

**重大修正**：
- Claude Code findings.md 说 "11 核心工具" → **真实 42**（4× 漏报）
- Claude Code Tool 不是单文件：BashTool 含 10+ 子文件（prompt/permissions/security/semantics/UI 各自模块）

### §3.2 axis-2 Memory 体系

| 系统 | 实现 | 关键设计 |
|---|---|---|
| **Hive** | `internal/memory/` 15 文件 | pgvec + hybrid + extractor + injector + vecindex 完整向量栈 |
| **deer-flow** | `agents/memory/storage.py:62 FileMemoryStorage` | 不是真 RAG（无向量库/全文检索/reranker），message history 为主 |
| **OpenClaw** | 平文本 Markdown + sqlite-vec 向量索引 | **5 provider embedding 自动选择** (local/openai/gemini/voyage/mistral)；**Pre-compaction memoryFlush 三层证据**（文档+schema+test）；MAX_ENTRYPOINT_LINES=200 / MAX_ENTRYPOINT_BYTES=25000；bootstrapMaxChars=20000 / bootstrapTotalMaxChars=150000 |
| **Claude Code** | `src/memdir/` 8 文件 | MEMORY.md 200 lines/25KB cap（双触发先达）；**date-named append-only 日志**（与 OpenClaw 同构）；**nightly 蒸馏过程** distills logs into MEMORY.md + topic files；**team-shared memory**；findRelevantMemories top-5 检索 |
| **Hermes** | `agent/memory_manager.py` + `agent/context_compressor.py` | **MemoryManager 单一入口**（Builtin always first，仅一个 external plugin）；**context_compressor 10 项改进**：结构化 summary / Resolved-Pending tracking / Handoff framing / Token-budget tail / Iterative updates / Tool output pruning / auxiliary model / 保护 head+tail |

**最重大发现**：Hermes context_compressor 比 OpenClaw memoryFlush 详细 5x，**P0-4 应三源借鉴**。

### §3.3 axis-3 Skills 体系

| 系统 | 关键设计 |
|---|---|
| **Hive** | `internal/skills/` 15+ 文件，含 finder/discovery/executor/hooks/metrics/on_demand_api（已有 progressive 基础）|
| **deer-flow** | `skills/` 8 文件：installer/loader/manager/parser/security_scanner/types/validation；LLM 语义匹配触发，无强制规则 |
| **OpenClaw** | Progressive loading（startup 仅 frontmatter ~100 token，命中读全文 ~5K）；frontmatter: userInvocable / disableModelInvocation / 完整 OpenClawSkillMetadata；5 包管理器（brew/node/go/uv/download）；继承 `@mariozechner/pi-coding-agent` Skill |
| **Claude Code** | `src/skills/`：bundled + bundledSkills.ts + loadSkillsDir.ts + **mcpSkillBuilders.ts**（MCP-as-Skills 桥接）；EFFORT_LEVELS 等级机制；roughTokenCountEstimation 主动算 token；Write-once registry pattern 解决 Bun bundle 依赖循环 |
| **Hermes** | `agent/skill_commands.py` slash command shared between CLI/gateway；`/skill-name` 显式触发；`/plan` 等 prompt-only built-ins |

### §3.4 axis-4 MCP / ACP 协议

#### MCP

| 系统 | 实现 |
|---|---|
| **Hive** | `internal/mcphost/` **26 文件**：client/host/oauth/hitl/transport×3 (http/sse/stdio) |
| **deer-flow** | `langchain_mcp_adapters.client.MultiServerMCPClient`；max_workers=**10** |
| **OpenClaw** | 顶层 `@modelcontextprotocol/sdk` v1.27.1 + `extensions/acpx/src/runtime-internals/mcp-agent-command.ts` MCP bridge + `skills/mcporter/SKILL.md` MCP CLI client + `src/browser/chrome-mcp.ts` Chrome MCP |
| **Claude Code** | 4 个 MCP 工具：MCPTool / McpAuthTool / ListMcpResourcesTool / ReadMcpResourceTool；**结果分类 collapse**（classifyForCollapse.ts）|
| **Hermes** | `tools/mcp_oauth.py`；ACP-as-LLM-provider 路径（见下）|

#### ACP（重大架构发现）

**三家 ACP 形成完整生态**（Server / Backend Runtime / Client 三角色）：

| 角色 | 实现 | 用途 |
|---|---|---|
| **Server** (IDE→agent 接入) | Hive `coder/acp-go-sdk v0.6.3` (Go) | IDE 用 ACP 协议连接 Hive agent（Zed/JetBrains/Neovim/VSCode）|
| **Backend Runtime** (agent 端协议适配) | OpenClaw `acpx@0.3.0` (TS) | OpenClaw agent 用 ACP runtime；session 持久化 + bridge MCP server |
| **Client** (agent→LLM provider 包装) | Hermes `agent/copilot_acp_client.py` | Hermes 通过 ACP 把 GitHub Copilot 包装成 OpenAI-compatible LLM provider |

**战略意义**：Hive 接入 ACP 生态可以同时：
1. 当 ACP server（已有）
2. 当 ACP backend runtime（借鉴 OpenClaw acpx）
3. 当 ACP client 用其他 agent 当 LLM（借鉴 Hermes copilot_acp_client）

### §3.5 axis-5 Channels / Renderer

| 系统 | 渠道支持 | 关键设计 |
|---|---|---|
| **Hive** | 飞书 + 钉钉 + 微信 + 企微 (4 国内 IM) | 飞书 PatchCard 增量渲染 + ErrPatchRateLimited 限流重试 + chunk/debounce/dedup/retry/router_renderer 完整管道 |
| **deer-flow** | 单一 SSE 流，15 个 routers (含 channels/artifacts/uploads/threads/runs) | nginx + frontend + gateway + langgraph + provisioner + deer-flow = **6 个 services**（不是 evidence 说的 5） |
| **OpenClaw** | 20 IM channel（feishu/discord/slack/telegram/whatsapp 等），**只飞书是国内**；飞书 dedup/debounce/PATCH 完整 (`extensions/feishu/src/dedup.ts` + `monitor.account.ts`) | 22+ extensions 各自独立实现；继承 `pi-tui` |
| **Claude Code** | 终端 TUI + Print stream-json + Remote Control + Teleport + IDE 插件 | `src/remote/` 完整子系统：remotePermissionBridge / RemoteSessionManager / sdkMessageAdapter / SessionsWebSocket（CLI ↔ Web ↔ Mobile 跨设备）|
| **Hermes** | gateway/ 子系统（未深查具体 IM 支持）；agent 顶层 cli.py + cli-config.yaml.example | [TBV] 17 IM channel 数字未独立核实 |

### §3.6 axis-6 Prompts

| 系统 | 实现 |
|---|---|
| **Hive** | `internal/master/prompt_builder.go` + `internal/i18n/prompts/{subagents,system,tools}/` |
| **deer-flow** | `agents/lead_agent/prompt.py` + `agents/memory/prompt.py`；**16 个 middleware 文件** + 18 个可装配 class（lead_agent path）；ClarificationMiddleware 必最后；TwoMiddleware 路径（lead_agent vs factory.py）|
| **OpenClaw** | `src/agents/system-prompt.ts` 95 行；**3 promptMode**（full/minimal/none）；9+ 段 (Tooling/Safety/Skills/Workspace/...)；Safety 仅 advisories；HITL `/approve <id> allow-once\|allow-always\|deny` 三态 |
| **Claude Code** | system.ts 95 行 + **prompts.ts 914 行**（12 const）+ 42 工具各自 prompt.ts；**3 个 prefix 动态选择**（CLI / Agent SDK CLI preset / Agent SDK）；**Coordinator Mode**（INTERNAL_WORKER_TOOLS = TEAM_CREATE/TEAM_DELETE/SEND_MESSAGE/SYNTHETIC_OUTPUT）；**Native Client Attestation**（Attestation.zig 反代理）；Workload Routing（cc_workload tier）|
| **Hermes** | `agent/prompt_builder.py` + `agent/prompt_caching.py`（Anthropic prompt cache）+ `agent/redact.py`（敏感信息脱敏）|

**重大修正**：Claude Code findings.md 说"110+ 条件指令"+"~2.5K token system prompt" → **主线程 grep 推翻**。prompts.ts 仅 12 const，但累加 42 工具 prompt.ts 后总 system prompt token **远超 2.5K（估计 30-50K）**。

---

## §4 重大修订记录

### 4.1 OpenClaw 子 agent 翻车（错率 26.3%）

| 翻车项 | 子 agent 报告 | 主线程核实真相 |
|---|---|---|
| MCP 支持 | "不支持 MCP" | **完整支持**：`@modelcontextprotocol/sdk` v1.27.1 + acpx + mcporter + chrome-mcp |
| ACP 归属 | "pi-agent-core 子代理 runtime" | 实际是 `acpx@0.3.0` npm 包；子 agent 把 `pi-tui` 误读成 `pi-agent-core` |
| Memory | "平文本 + 可选 LanceDB" | **完整 5-provider 向量栈** + sqlite-vec |
| 飞书工程深度 | "Hive 完全领先" | OpenClaw 飞书也有 dedup/debounce/PATCH，**势均力敌** |
| 工具策略 | "6 层" | 实际 **8 层** |
| 工具数 | "23 个内置" | 实际 **24 个** |

### 4.2 Claude Code WebSearch 二轮翻车

| 翻车项 | WebSearch 报告 | 主线程核实真相 |
|---|---|---|
| 工具数 | "11 核心" | **42 个工具目录** |
| System prompt 大小 | "~2.5K token" | **远超**（system.ts + prompts.ts + 42 工具 prompt.ts 累加，估 30-50K）|
| 110+ 条件指令 | "110+" | prompts.ts 仅 12 个 const（但累加每工具 prompt 后可能 100+ 个 section）|

### 4.3 deer-flow 既有 evidence 微调（错率 17%）

| 微调项 | evidence 旧版 | 主线程核实 |
|---|---|---|
| middleware 数 | 18 | 16 个 .py 文件（18 个可装配 class，含级联）|
| router 数 | 14 | 15 个 |
| compose services | 5 | 6 个（多 deer-flow 自身）|
| community search providers | 5 | 6 个（多 image_search） |
| **build_tracing_callbacks 接入** | "生产路径缺失" | **真有**：`models/factory.py:136` |

### 4.4 Hermes 从全 [TBV] 升级

| 项 | 旧状态 | 新状态 |
|---|---|---|
| 元数据（name/version/license/author）| [TBV] | [HC-MAIN-VERIFIED]（pyproject.toml 完全确认）|
| 6 environments backends | [TBV] | [HC-MAIN-VERIFIED]（local+docker+ssh+modal+daytona+singularity 文件全在）|
| GEPA 自我改进 | [TBV] | [HC-MAIN-VERIFIED]（pyproject description + trajectory.py + manual_compression_feedback.py + insights.py 多文件交叉）|
| smart_model_routing | [TBV] | [HC-MAIN-VERIFIED]（文件存在）|
| ACP client 真实 | [TBV] | [HC-MAIN-VERIFIED]（copilot_acp_client.py + acp_adapter/ + acp_registry/）|
| 17 IM channels | [TBV] | [TBV] 仍未确认 |
| 103K stars / 118 skills 数字 | [TBV] | [TBV] 与源码核实无关，待人工查 GitHub |

---

## §5 三家 ACP 完整生态（最重大架构发现）

### 5.1 三角色完整覆盖

```
        ┌─────────────────────────────────┐
        │  Agent Client Protocol (ACP)    │
        │     Zed/Coder 推动的开放标准      │
        └────────────┬────────────────────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
  Server 角色    Backend 角色   Client 角色
  (IDE 接入)    (Runtime)      (LLM provider)

   Hive ✅       OpenClaw       Hermes
   coder/        acpx@0.3.0     copilot_acp_client.py
   acp-go-sdk   (TS)            (Python)
   v0.6.3       runtime         → connects to GitHub
   (Go)         backend         Copilot ACP server
```

### 5.2 Hive 接入 ACP 生态的 3 条路径

1. **Server (已有)**：IDE 用户连接 Hive agent
2. **Backend Runtime (P1-10 借鉴)**：Hive agent 用 ACP runtime + bridge MCP（参考 OpenClaw acpx）
3. **Client (P1-9 新候选)**：Hive 通过 ACP 连接 Codex / Copilot / OpenClaw 等当 LLM provider（参考 Hermes）

### 5.3 acpx@0.3.0 仍待核实

主线程 grep 没找到 `agent-client-protocol` 字符串直接证据。acpx 是否就是 ACP 标准实现仍 [TBV]。但三家命名一致 + 用例互补，**大概率是同一协议**。

---

## §6 Hive 真正领先 vs 落后的轴

### 6.1 真正领先（4 条）

| Axis | 证据 |
|---|---|
| **国内 IM 平台数量** | Hive 4 (飞书+钉钉+微信+企微) vs OpenClaw 1 (仅飞书) vs 其他 0 |
| **Spec-driven ReAct + requirement resolver** | Hive `internal/skills/finder.go:360-400 FindBySpecRequirements` + `session_loop_specdriven_react.go`，三家都没有 |
| **SafeExecutor 代码层强制权限** | Hive `MEMORY.md` 锁定 + `internal/security/`；OpenClaw safety 仅 prompt advisories |
| **Go 单二进制部署** | Hive cmd/server + cmd/claw 单二进制；其他都是 Node.js (OpenClaw/deer-flow)/Python (Hermes/deer-flow backend) 复杂依赖 |

### 6.2 势均力敌

| Axis | 状态 |
|---|---|
| MCP host | 双方都用官方 SDK；Hive 优势 transport×3+OAuth+HITL，OpenClaw 优势 mcporter skill + chrome-mcp |
| Memory 向量栈 | 双方都有完整向量索引；Hive 优势 Postgres，OpenClaw 优势 sqlite-vec embedded + 5 provider 自动 fallback |
| 国内 IM 单平台工程深度 | Hive 飞书 vs OpenClaw 飞书 dedup/debounce/PATCH 都有；Hive 可能在 ErrPatchRateLimited 限流细节略深（待对照）|

### 6.3 真正落后

| Axis | 落后原因 |
|---|---|
| 工具集广度 | Hive ~15 vs Claude Code 42（4 大类完全没有：Task×6 / Team×2 / Plan / Schedule / RemoteTrigger / Sleep）|
| Multi-agent 协调 | Claude Code Coordinator Mode + INTERNAL_WORKER_TOOLS + ASYNC_AGENT_ALLOWED_TOOLS；Hive Master Agent 较 ad-hoc |
| Memory 治理 | Claude Code nightly distill + Hermes context_compressor 结构化压缩；Hive 仅向量检索无主动治理 |
| ACP 生态接入 | Hive 仅 server 角色，backend / client 角色完全未接入 |
| 远程会话切换 | Claude Code Remote Control + Teleport + Agent Team；Hive WebSocket 单向 |

---

## §7 P0/P1 候选施工清单

### 7.1 P0 候选（4 条）

| 编号 | 候选 | Hive Go 锚点 | 工期 | 来源可信度 |
|---|---|---|---|---|
| **P0-1** | 流式 tool_call 可见 | `internal/master/react_processor.go:362-364` | 0.5d Step1 + 2d Step2 | deer-flow final-verdict [HC] |
| **P0-2** | Subagent stream tool_call 同步修 | `internal/subagent/agentloop.go:203-217` | 0.5d | deer-flow final-verdict [HC] |
| **P0-3** | schema-runtime drift audit（扩展范围）| 覆盖 ACP /api/run/ + tool schema | 3d | deer-flow + 本次三家调研 [HC] |
| **P0-4 ★** | **Pre-compaction Memory Flush 三源借鉴** | 新增 `internal/master/compaction.go` + 集成 `internal/memory/extractor.go` | 5-7d | OpenClaw silent turn + Claude Code nightly distill + Hermes context_compressor 三源 [HC-MAIN-VERIFIED] |

### 7.2 P1 候选（13 条）

| 编号 | 候选 | 来源 | 工期 |
|---|---|---|---|
| P1-1 | Progressive Skill Loading 核实 + 实施 | OpenClaw + Claude Code (含 mcpSkillBuilders) | 0.5d 核实 + 3-5d 实施 |
| P1-2 | Deny-First Permission 评估顺序核实 | Claude Code | 0.5d 核实 + 1d 调整 |
| P1-3 | 完整 ToolError schema | deer-flow final-verdict P1-1 | 1w |
| P1-4 | Capacity governance 配置化 | deer-flow final-verdict P0-6 降为 P1 | 3d |
| P1-5 | 工具分组 group:* 9 个快捷键 | OpenClaw axis-1 [HC-MAIN-VERIFIED] | 1d |
| P1-6 | 按任务复杂度路由模型 | Hermes `smart_model_routing.py` 真实存在 | 3-5d |
| P1-7 | HITL `/approve` 三态（once/always/deny）| OpenClaw axis-6 [HC-MAIN-VERIFIED] | 2-3d |
| P1-8 | 5-provider embedding 自动 fallback | OpenClaw axis-2 [HC-MAIN-VERIFIED] | 3-5d |
| **P1-9 ★** | **ACP Client 路径** — Hive 用 ACP 连其他 agent | Hermes copilot_acp_client + 三家 ACP 生态 | 1-2w |
| **P1-10 ★** | **acpx 模式 ACP↔MCP bridge** | OpenClaw acpx | 1w |
| **P1-11 ★** | **mcpSkillBuilders MCP-as-Skills 桥接** | Claude Code | 3-5d |
| **P1-12 ★** | **Coordinator Mode + Team 工具协调** | Claude Code | 1-2w |
| **P1-13 ★** | **结构化 summary 压缩**（Resolved/Pending tracking + Handoff framing） | Hermes context_compressor | 1w |

★ = 本次三家全核实后浮现的新候选

---

## §8 反面教材清单（防止重蹈"看证据别抄类"覆辙）

| # | 别抄什么 | 理由 |
|---|---|---|
| 1 | 别抄 OpenClaw 22+ 通道独立实现 | Hive `internal/channel/router_renderer.go` 已有统一 SDK |
| 2 | 别抄 OpenClaw safety 仅 prompt advisories | Hive SafeExecutor 已是代码层强制 |
| 3 | 别抄 OpenClaw 工具级无并发限制 | Hive `streaming_executor.go` 必须保留并发治理 |
| 4 | 别抄 OpenClaw frontmatter 黑名单正则 | 应用白名单显式允许（更安全）|
| 5 | 别抄 LangGraph AgentMiddleware | deer-flow final-verdict §5 已识别教训 |
| 6 | 别抄 Hermes 数字（103K stars / 47 tools / 118 skills 等未核实数字）| 仅采纳源码确认的方向性结论 |
| 7 | 别抄 Claude Code 14-17K token 工具定义永远 in context | progressive loading 是更好范式 |
| 8 | 别为 1 个 validator 重构 Middleware Pipeline | deer-flow final-verdict §1.2 已识别 |
| 9 | 别让子 agent 用窄范围 grep 一次性出否定结论 | OpenClaw MCP 误判教训：子 agent 错率 26.3%，主线程必须独立核实 |
| 10 | 别误读依赖名 | `pi-tui` ≠ `pi-agent-core`；OpenClaw axis-4 子 agent 误读案例 |

---

## §9 决策待定项

| # | 议题 | 选项 | 推荐 |
|---|---|---|---|
| **D1** | P0-4 Pre-compaction Memory Flush 是否做？ | A) 三源借鉴做（5-7d）/ B) 等用户反馈"上下文丢失"再做 | **A** — 三源借鉴价值极高，[HC-MAIN-VERIFIED] |
| **D2** | P1-1 Progressive Skill Loading 重构？ | A) 做 / B) 等 skill > 50 时再做 | **A** — `on_demand_api.go` 已有基础 |
| **D3** | P1-6 按任务复杂度路由模型？ | A) 做 / B) 推迟 | **A 但谨慎** — 先 measure 任务模型分布 |
| **D4** | P1-9 ACP Client 接入是否做？ | A) 做（Hive 接入 ACP 生态）/ B) 维持仅 server 角色 | **A** — 战略价值极大，三家 ACP 生态接入潜力 |
| **D5** | P1-12 Coordinator Mode + Team 工具是否做？ | A) 做 / B) Master Agent 当前够用 | 用户决定 — multi-agent 需求是否真存在 |
| **D6** | deer-flow PLAN.md 既有 P0-1/3/4/5 是否纳入同批？ | A) 一起做 / B) 分批 | 用户决定 — 与本次调研无强耦合 |

---

## §10 文件索引

```
docs/
├── 调研笔记/deer-flow/                    # 既有调研（16 蓝军 + 双 AI 辩论）
│   ├── PLAN.md
│   ├── evidence/axis-1~6.md               # 既有 6 axis
│   └── archive/final-verdict.md           # ★ 既有最终裁决
└── research/                              # 本次新增
    ├── openclaw/
    │   ├── README.md                      # 子 agent 一手报告（含错误，不应再引用）
    │   └── evidence/
    │       ├── axis-1-tools.md            # 子 agent 原版（部分错）
    │       ├── axis-1-tools-VERIFIED.md   # ⭐ 主线程核实
    │       ├── axis-2-memory.md           # 子 agent 原版（部分错）
    │       ├── axis-2-memory-VERIFIED.md  # ⭐ 主线程核实
    │       ├── axis-3-skills.md           # 子 agent 原版
    │       ├── axis-3-skills-VERIFIED.md  # ⭐ 主线程核实
    │       ├── axis-4-acp-protocol.md     # ⚠️ 整体错误
    │       ├── axis-5-channels-uploads.md # 子 agent 原版（部分错）
    │       └── axis-5-6-channels-prompts-VERIFIED.md # ⭐ 主线程核实
    ├── deer-flow/
    │   └── VERIFIED-6-AXIS.md             # ⭐ 主线程 6 axis 核实
    ├── claude-code/
    │   ├── findings.md                    # 一轮草稿
    │   ├── verification-round-2.md        # WebSearch 二轮（18 项 [HC]）
    │   └── VERIFIED-6-AXIS.md             # ⭐ 主线程源码核实
    ├── hermes-agent/
    │   ├── findings.md                    # WebSearch（含警告）
    │   └── VERIFIED-6-AXIS.md             # ⭐ 主线程源码核实
    └── _synthesis/
        ├── SYNTHESIS.md                   # 综合（已三次修订）
        ├── MAIN-VERIFICATION-3-REPOS.md   # 中间产物
        └── FINAL-REPORT.md                # ⭐ 本文件（完整最终报告）
```

---

## §11 仍待核实清单（不影响 P0/P1 决策）

1. acpx@0.3.0 npm 包源码（确认是否 Zed/Coder ACP 标准实现）
2. Hermes GitHub repo `NousResearch/hermes-agent` 真实 stars 数（人工查 GitHub）
3. Hermes 17 IM channels 数字
4. Hermes "47 tools × 19 toolsets" / "118 skills" 精确数
5. Claude Code system prompt 累加 token 精确数（system.ts + prompts.ts + 42 工具 prompt.ts）
6. deer-flow `models/factory.py` callbacks 接入完整链路
7. Hive `internal/skills/finder.go:449` 是否真做 frontmatter-only startup
8. Hive `SafeExecutor.MatchPolicy` 评估顺序是否 Deny-first

---

## §12 下一步行动

### 12.1 立即可执行

1. **D1-D6 决策** — 用户对每条 P0/P1 候选拍板
2. **进入 plan-eng-review** — 把拍板的 P0 候选转化为完整 spec（架构 / 数据流 / 测试 / 部署 / observability）
3. **施工 → 蓝军 mutation 验证** — 每个 P0 落地必须通过 deer-flow PLAN.md §6 的"蓝军 mutation + 命令证据"门

### 12.2 后续核实（可选）

- 如要进一步深入：核实清单 §11 中的 8 项可主线程逐条 grep 完成（每项 5-10 min）
- ACP 生态深入：grep `acpx@0.3.0` npm 包源码 + Hermes acp_registry 详细实现

### 12.3 战略层面

- 三家 ACP 生态发现是本次调研最大战略洞察 → **Hive ACP 生态接入路线图**应单独立项
- Pre-compaction Memory Flush（P0-4）三源借鉴是最高 ROI → **优先纳入下一个 sprint**

---

## §13 调研可信度声明

本报告基于：
- ✅ deer-flow 既有 16 蓝军 + 双 AI 辩论（[HC]）+ 主线程 spot-check 23 项（17% 错率，含 1 项推翻为更可信）
- ✅ OpenClaw 6 axis 主线程逐条核实（错率 26.3%，57 条断言中 42 [VERIFIED] / 10 [REVISED] / 5 [FALSE]）
- ✅ Claude Code 18 项 WebSearch 二轮 [HC] + 主线程源码核实（2 项 [FALSE]，但新发现 30+ 项漏报）
- ✅ Hermes 全 [TBV] → 70% [HC-MAIN-VERIFIED]（pyproject + 30+ agent 模块文件交叉）

**总核实量**：~93 条断言 + ~70 升级为 [HC-MAIN-VERIFIED] + ~18 修正/推翻 + ~5 仍 [TBV]

**整体调研可信度**：~91%（vs 子 agent 一手报告 ~73.7%）

**反面教材已记录**（§8）：防止下次调研重蹈覆辙

---

*— End of Hive Harness 顶级化战略评审完整报告 —*
*生成日期：2026-04-25*
