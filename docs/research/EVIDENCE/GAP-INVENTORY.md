# Hive Engineering Quality 全量缺陷清单（Gap Inventory）

> **目的**：把基于 4 家对比发现的 **所有** Hive 缺陷列全 — 不分优先级，先 full coverage
> **方法**：每条标 `[Hive 当前状态]` / `[顶级标杆]` / `[来源]`
> **后续**：列完后再分优先级 + 分阶段（不在本文件做）
> **日期**：2026-04-25

---

## §维度 1：工具体系广度（Tools — 数量 / 类别覆盖）

### G1.1 工具总数
- Hive：~15（含 9 LSP）
- Claude Code：**42**
- 缺 ~27 个工具

### G1.2 缺失的工具类别（按 Claude Code 分类）

| 类别 | Claude Code 工具 | Hive 当前 |
|---|---|---|
| **Task 系统** | TaskCreate / TaskGet / TaskList / TaskOutput / TaskStop / TaskUpdate（6 个）| ❌ 完全没有 |
| **Team 协作** | TeamCreate / TeamDelete（2 个）| ❌ 完全没有 |
| **Plan Mode** | EnterPlanMode / ExitPlanMode（2 个）| ❌ 完全没有 |
| **Worktree** | EnterWorktree / ExitWorktree（2 个）| ❌ 完全没有 |
| **Schedule / Cron** | ScheduleCronTool | ❌ 完全没有 |
| **Remote Trigger** | RemoteTriggerTool | ❌ 完全没有 |
| **Sleep** | SleepTool（agent self-pace）| ❌ 完全没有 |
| **Notebook** | NotebookEditTool | ❌ 完全没有 |
| **REPL** | REPLTool | ❌ 完全没有 |
| **Synthetic Output** | SyntheticOutputTool（Coordinator Mode 用）| ❌ 完全没有 |
| **Send Message** | SendMessageTool（multi-agent）| ❌ 完全没有 |
| **Skill 显式调用** | SkillTool | ❌ 完全没有 |
| **Tool Search** | ToolSearchTool（meta-tool 搜工具）| ❌ 完全没有 |
| **PowerShell** | PowerShellTool（Windows）| ❌ 完全没有（看是否需要）|
| **Ask User Question** | AskUserQuestionTool（结构化询问）| ❌ 完全没有 |
| **Brief** | BriefTool（任务简报）| ❌ 完全没有 |
| **Config** | ConfigTool（运行时改配置）| ❌ 完全没有 |
| **MCP 子工具集** | McpAuthTool / ListMcpResourcesTool / ReadMcpResourceTool（3 个 MCP 子工具）| ⚠️ 仅总入口 mcphost，无细分工具 |

**来源**：Claude Code `src/tools/*/`（主线程亲自 ls + verified）

---

## §维度 2：单工具防御深度（Tool Engineering Quality）

### G2.1 BashTool 防御深度
- Hive：1 层（19 条 regex + AST parser + LLM classifier，全堆 internal/security/）
- Claude Code：**6 层**（destructive warning / bashPermissions / bashSecurity / pathValidation / readOnlyValidation / sedValidation）
- 差距 6x

### G2.2 BashTool 攻击 vector 防御覆盖
- Hive：完全不考虑绕过攻击
- Claude Code：显式防 17+ 种攻击 vector：
  - Zsh `=cmd` EQUALS expansion（绕过 `Bash(curl:*)` deny）
  - heredoc in `$()` substitution
  - `<()` / `>()` / `=()` process substitution
  - `${} / $[ / ~[ / (e: / (+` 各种 expansion
  - shell quote single-quote bug
  - PowerShell `<#` 注释（defense-in-depth）
- **来源**：`bashSecurity.ts` 头 80 行

### G2.3 BashTool Zsh 危险命令防御
- Hive：0 个 Zsh 命令防御
- Claude Code：**25 个**显式 block（zmodload / emulate / sysopen / sysread / syswrite / sysseek / zpty / ztcp / zsocket / mapfile / zf_rm / zf_mv / zf_ln / zf_chmod / zf_chown / zf_mkdir / zf_rmdir / zf_chgrp 等）
- **来源**：`bashSecurity.ts:62-92`

### G2.4 BashTool 关注点分离
- Hive：1 层 19 条规则**同时承担** destructive warning + permission decision + attack defense（耦合）
- Claude Code：严格分层（informational warning ≠ permission decision ≠ attack defense ≠ specialized validation）
- 这是 engineering taste 级别的差距

### G2.5 工具结构化程度
- Hive：每工具 1 个 .go 文件
- Claude Code：每工具是结构化目录（含 prompt.ts + tool.tsx + UI.tsx + security.ts + permissions.ts + 等多文件）
- 例：BashTool/ 18 文件 12,411 行

### G2.6 Reasoning 文档化
- Hive：仅"禁止删除根目录"短描述
- Claude Code：每条规则都有 attack vector comment（zmodload 25 行注释解释 zsh/system / zsh/zpty / zsh/net/tcp 等）

### G2.7 Defense-in-depth
- Hive：无 forward-looking 防御
- Claude Code：显式防御 PowerShell `<#`（即使 Claude Code 自己不执行 PowerShell，是为 future changes 防御）

---

## §维度 3：Prompt 质量

### G3.1 工具级专门 prompt
- Hive：0 个工具有专门 prompt（统一在 `internal/i18n/prompts/`）
- Claude Code：**42 个工具每个有 prompt.ts**（BashTool prompt.ts 369 行）
- Hermes：每模块有 prompt_builder.py 集成

### G3.2 System prompt 模块化
- Hive：i18n MD 文件 + `internal/master/prompt_builder.go`
- Claude Code：3 prefix 动态选择 + system.ts 95 行 + prompts.ts 914 行 + 42 工具 prompt.ts 累加
- OpenClaw：3 promptMode（full/minimal/none）+ 9 段固定结构

### G3.3 Prompt 教导技巧（防 LLM 错误模式）
- Hive：未审视
- Claude Code BashTool prompt.ts 含：
  - Git Safety Protocol（NEVER 列表 + 详细 reasoning）
  - "Always create NEW commits rather than amending" + 解释为什么（hook fail 后 amend 会改前一次 commit）
  - "prefer adding specific files by name rather than git add -A"（防误提交 .env / credentials）
  - undercover 模式（Anthropic 内部用户的特殊路径）
  - 用户类型分流（USER_TYPE === 'ant' vs external）
  - 设置可调（shouldIncludeGitInstructions / CLAUDE_CODE_DISABLE_BACKGROUND_TASKS）

### G3.4 Prompt cache 优化
- Hive：未集成 Anthropic prompt cache
- Hermes：`agent/prompt_caching.py` 专门模块
- Claude Code：`--exclude-dynamic-system-prompt-sections` 把 per-machine 段挪到首条 user message 复用 cache

### G3.5 Prompt redact / 脱敏
- Hive：无显式 redact 机制
- Hermes：`agent/redact.py` 专门模块
- Claude Code：undercover instructions（防 model 在 commit message 暴露内部 codename）

---

## §维度 4：Memory 治理

### G4.1 Pre-compaction memory flush
- Hive：❌ 无
- OpenClaw：silent agentic turn（接近 compaction 时显式保存）— 三层证据 [HC-MAIN-VERIFIED]
- 已立项 P0-4

### G4.2 Date-named append-only daily log
- Hive：❌ 无
- OpenClaw：`memory/YYYY-MM-DD.md`
- Claude Code：`src/memdir/` date-named log + nightly distill

### G4.3 Nightly distill process
- Hive：❌ 无
- Claude Code：**唯一独有** — "separate nightly process distills these logs into MEMORY.md and topic files"

### G4.4 Structured summary 模板
- Hive：无
- Hermes context_compressor 10 项改进：
  - Resolved/Pending question tracking
  - Handoff framing "different assistant"（防 summary 被读成 active instructions）
  - "Remaining Work" 替换 "Next Steps"
  - 跨次迭代保留 info
  - Token-budget tail protection
  - Tool output pruning before LLM
  - Scaled summary budget
  - 用 auxiliary model（cheap/fast）
  - 保护 head + tail context

### G4.5 Team-shared memory
- Hive：单 session memory，无 team
- Claude Code：`teamMemPaths.ts` + `teamMemPrompts.ts`

### G4.6 findRelevantMemories top-N 检索
- Hive：检索机制存在，但策略未见 top-N 显式
- Claude Code：`findRelevantMemories.ts` top-5 + Excludes MEMORY.md（已 in system prompt）

### G4.7 5-provider embedding fallback
- Hive：单 provider（pgvec）
- OpenClaw：**5 provider 自动选择**（local node-llama-cpp → openai → gemini → voyage → mistral）
- 已立项 P1-8

### G4.8 Bootstrap 文件 caps
- Hive：未见显式 caps
- OpenClaw：bootstrapMaxChars=20000 / bootstrapTotalMaxChars=150000
- Claude Code：MEMORY.md MAX_ENTRYPOINT_LINES=200 / MAX_ENTRYPOINT_BYTES=25000（双触发先达）

### G4.9 MemoryManager 单一入口
- Hive：分散调用
- Hermes：MemoryManager 单一入口（Builtin always first，仅一个 external plugin）

### G4.10 Skill memory（procedural）
- Hive：无 skill memory 概念
- Hermes：3 层 memory 含 skill memory（agent 自动从 trace 创建）

---

## §维度 5：Skills 体系

### G5.1 Progressive loading（frontmatter-only startup）
- Hive：`internal/skills/on_demand_api.go` 已有基础，需核实是否真做到
- OpenClaw：startup 仅 frontmatter ~100 token
- Claude Code：`mcpSkillBuilders.ts` write-once registry pattern
- 已立项 P1-1

### G5.2 MCP-as-Skills 桥接
- Hive：❌ 无
- Claude Code：`src/skills/mcpSkillBuilders.ts`（MCP server 包装成 skill 暴露给模型）
- 已立项 P1-11

### G5.3 EFFORT_LEVELS 等级机制
- Hive：无
- Claude Code：`loadSkillsDir.ts` EFFORT_LEVELS / parseEffortValue

### G5.4 Token budget 主动估算
- Hive：未审视
- Claude Code：`roughTokenCountEstimation` 主动算 skill content 装入 token

### G5.5 Slash command 显式触发
- Hive：未审视
- Hermes：`/skill-name` 显式触发 + `/plan` 等 prompt-only built-ins

### G5.6 Skill marketplace / hub
- Hive：`docs/架构设计/Skill-市场协议.md` 设计中
- OpenClaw：ClawHub
- Hermes：skills_hub.py

### G5.7 Skill autonomous creation
- Hive：无
- Hermes：agent 从成功 trace 自动创建 skill

---

## §维度 6：MCP 体系

### G6.1 MCP 工具结果 collapse 分类
- Hive：无
- Claude Code：`MCPTool/classifyForCollapse.ts`（主动分类决定折叠展示）

### G6.2 mcporter CLI 集成作为 skill
- Hive：❌ 无
- OpenClaw：`skills/mcporter/SKILL.md`（mcporter 作为外部 skill 接入海量 MCP）

### G6.3 chrome-mcp 浏览器 MCP server
- Hive：browser tool 直接 chrome control
- OpenClaw：`src/browser/chrome-mcp.ts`（浏览器作为 MCP server 暴露）

### G6.4 MCP 工具 UI 渲染
- Hive：无专门 UI
- Claude Code：每个 MCP 工具有 UI.tsx

---

## §维度 7：ACP 生态接入

### G7.1 ACP Server 角色
- Hive：✅ 已有（`coder/acp-go-sdk v0.6.3`）

### G7.2 ACP Backend Runtime 角色
- Hive：❌ 无
- OpenClaw：`extensions/acpx`（agent 端 ACP runtime + bridge MCP server）
- 已立项 P1-10

### G7.3 ACP Client 角色
- Hive：❌ 无
- Hermes：`copilot_acp_client.py`（连 GitHub Copilot ACP server 当 LLM provider）
- 已立项 P1-9

### G7.4 ACP↔MCP bridge
- Hive：无
- OpenClaw：acpx mcp-agent-command 完整 bridge

---

## §维度 8：Multi-agent 协调

### G8.1 Coordinator Mode + 工人工具集
- Hive：Master Agent 较 ad-hoc，无显式 coordinator
- Claude Code：`coordinator/coordinatorMode.ts` + INTERNAL_WORKER_TOOLS = {TEAM_CREATE, TEAM_DELETE, SEND_MESSAGE, SYNTHETIC_OUTPUT}
- 已立项 P1-12

### G8.2 ASYNC_AGENT_ALLOWED_TOOLS 子集
- Hive：未见显式工具子集控制
- Claude Code：异步 agent 允许的工具子集

### G8.3 Team 协作工具
- Hive：无 TeamCreate / TeamDelete
- Claude Code：2 个 Team 工具

### G8.4 Subagent 工具委托
- Hive：subagent 已有，但 task 委托工具未审视
- OpenClaw：subagent.task + ACP 双 runtime（一次性 vs 持久）
- Hermes：`delegate_task` spawning + 配置 depth + 文件状态 coordination 防 edit 冲突

### G8.5 Mid-run steering
- Hive：无
- Hermes：`/steer <prompt>` 在 next tool call 后注入 guidance

---

## §维度 9：Channels / Renderer

### G9.1 国内 IM 数量覆盖
- Hive：✅ 4 平台（飞书 + 钉钉 + 微信 + 企微，**真领先**）
- 其他 3 家：OpenClaw 仅飞书；deer-flow 0；Hermes 0 国内 IM 确认

### G9.2 飞书 PatchCard 增量渲染
- Hive：✅ 已有（renderer.go + ErrPatchRateLimited）
- OpenClaw：飞书 send.ts 也用 message.patch / message.update（势均力敌）

### G9.3 Remote Control / Teleport 跨设备
- Hive：❌ WebSocket 单向，不支持 CLI ↔ Web ↔ Mobile 切换
- Claude Code：`src/remote/` 完整子系统（remotePermissionBridge / RemoteSessionManager / sdkMessageAdapter / SessionsWebSocket）

### G9.4 Agent Team 多 agent 并排渲染
- Hive：无
- Claude Code：split-and-merge pattern + up to 10 sub-agents

### G9.5 心跳触发 PATCH 重试
- Hive：✅ 已有
- 其他：未对比深度

---

## §维度 10：Permission 模型

### G10.1 Deny-First 评估顺序
- Hive：`SafeExecutor.MatchPolicy` 已有 Allow/Ask/Deny 三态，评估顺序需核实
- Claude Code：明确 Deny → Ask → Allow（首匹配阻断）
- 已立项 P1-2

### G10.2 多层过滤策略级联
- Hive：单层（builtin_rules 19 条）
- OpenClaw：**8 层**（tool profile → provider profile → global → provider → agent → agent provider → sandbox → subagent）
- 差 8x

### G10.3 HITL `/approve` 三态
- Hive：Allow/Ask/Deny（Ask 没区分 once/always）
- OpenClaw：`/approve <id> allow-once|allow-always|deny`（IM callback button 一次点击）
- 已立项 P1-7

### G10.4 Modal execution
- Hive：单一模式（permission 配置 + match）
- Claude Code：5 modes（default / plan / auto / dontAsk / bypassPermissions）

### G10.5 group:* 工具组快捷键
- Hive：无
- OpenClaw：9 个 group（runtime/fs/sessions/memory/ui/automation/messaging/nodes/openclaw）
- 已立项 P1-5

### G10.6 Path-scoped 规则 glob
- Hive：未见
- Claude Code：`.claude/rules/*.md` + `paths:` frontmatter glob 匹配

---

## §维度 11：Observability

### G11.1 Security check 数字化 ID
- Hive：日志用字符串
- Claude Code：`BASH_SECURITY_CHECK_IDS = {INCOMPLETE_COMMANDS:1, JQ_SYSTEM_FUNCTION:2, ...}`（避免 logging 高基数）

### G11.2 Per-check / per-tool metric
- Hive：部分（spec-driven 有 metric）
- Claude Code：完整 per-tool metric

### G11.3 Workload routing tier
- Hive：无
- Claude Code：`cc_workload` 字段 turn-scoped hint，路由 cron 请求到 lower QoS pool

### G11.4 Token usage tracking
- Hive：部分（已有 LLM 调用计数）
- Hermes：`prompt_caching.py` + `usage_pricing.py` 完整集成

### G11.5 GEPA trajectory 记录
- Hive：无
- Hermes：`agent/trajectory.py` + `manual_compression_feedback.py` + `insights.py`（self-improvement 数据基础）

### G11.6 Tracing callbacks 接入
- Hive：未审视
- deer-flow：`build_tracing_callbacks` `models/factory.py:136` 真生产接入（核实推翻 evidence 错断）

---

## §维度 12：Engineering 工程化基础

### G12.1 Red team mutation test
- Hive：单测有，无 attack vector mutation
- 顶级：100 个 attack vector 全过

### G12.2 错误恢复路径
- Hive：未系统审视
- Claude Code：每工具有 retry / fallback / abort

### G12.3 Tool 级并发治理
- Hive：`streaming_executor.go:81-106` safe tool 无上限 goroutine
- 已立项 P1-4 capacity governance（deer-flow final-verdict）

### G12.4 Tool 级 timeout
- Hive：sessions_spawn 类有 timeout，工具调用本身无统一 timeout
- Claude Code：`getDefaultBashTimeoutMs` / `getMaxBashTimeoutMs` 函数化可配
- OpenClaw：subagents.runTimeoutSeconds + provider.runTimeoutMs + agent default + discord/inbound-worker

### G12.5 后台执行模式
- Hive：无
- Claude Code：`run_in_background` 参数（Bash 工具不需要 `&`）

### G12.6 Sandbox 隔离
- Hive：依赖 SafeExecutor，无独立 sandbox
- deer-flow：LocalSandboxProvider / AioSandboxProvider 完整 sandbox
- OpenClaw：sandbox="inherit"|"require" 控制

### G12.7 Native Client Attestation 风险
- Hive：用 Anthropic API 走第三方 proxy 风险
- Claude Code：`Attestation.zig` 反爬虫机制（Hive 未来风险标记）

### G12.8 完整 ToolError schema
- Hive：terminal 判定走字符串 contains（`react_processor.go:935-963`）
- 已立项 P1-3（deer-flow final-verdict P1-1）

---

## §维度 13：Spec-driven / OpenSpec 真意（用户硬约束）

### G13.1 Artifact 显式可见
- Hive：❌ 当前 hidden，违背 OpenSpec 真意
- 顶级：markdown 文件 + git 追踪 + code review 可读
- 已立项 Q3 大重构

### G13.2 IM 飞书 PatchCard 渐进 todos
- Hive：❌ 无
- 顶级：复用 `internal/channel/feishu/renderer.go` PatchCard，把 todos 作为 card section 渐进 PATCH

### G13.3 Web Console 完整 todos 列表 + 干预
- Hive：❌ 无
- Claude Code：TodoWriteTool 完整 UI + 用户改 + 干预

### G13.4 propose → apply → archive 流程
- Hive：内部已有 phase 1（SafeExecutor 已上线，Phase 2 hidden 实现走偏）
- 顶级：保留流程思想，artifact 显式化

### G13.5 长任务跨 session continuation
- Hive：内部 continuation Resolver 有，但默认 OFF
- 顶级：默认 ON + 用户可见 + measurable 命中率

### G13.6 AI 质量 measurable 指标
- Hive：无 measurable
- 顶级：长任务完成率 / 跑偏率 / 失败可追溯性 三个 metric measurable

---

## §维度 14：Self-improvement

### G14.1 GEPA reflection on traces
- Hive：无
- Hermes：GEPA（ICLR 2026 paper）+ `hermes-agent-self-evolution` 仓库
- 集成深度未深查

### G14.2 Skill autonomous creation
- Hive：无
- Hermes：agent 自动从成功 trace 写 skill

### G14.3 Insights 学习
- Hive：无
- Hermes：`agent/insights.py` 学习见解

### G14.4 Manual compression feedback
- Hive：无
- Hermes：`agent/manual_compression_feedback.py` 用户反馈被记录

---

## §维度 15：Smart Model Routing

### G15.1 按任务复杂度路由模型
- Hive：无
- Hermes：`agent/smart_model_routing.py` + cheap model fallback（DeepSeek-V3 vs Sonnet 省 90%）
- 已立项 P1-6

### G15.2 多 LLM provider
- Hive：✅ 已有 9+ provider
- 其他三家：都有多 provider

### G15.3 Per-task model override
- Hive：未审视
- OpenClaw：sessions_spawn 支持 model 参数 override

---

## §维度 16：Distillation / Token 优化

### G16.1 Tool output pruning before LLM
- Hive：无
- Hermes：context_compressor cheap pre-pass

### G16.2 Auxiliary model 用 cheap 跑 summarization
- Hive：无
- Hermes：`auxiliary_client.py` 专门 cheap/fast model 给压缩用

### G16.3 Iterative summary updates
- Hive：无
- Hermes：跨次压缩保留 info

---

## §维度 17：Channel 工程深度（Hive 已有但要持续优化）

### G17.1 飞书 ErrPatchRateLimited 限流重试
- Hive：✅ 已有
- 其他：未对比深度

### G17.2 chunk/debounce/dedup/retry 完整管道
- Hive：✅ 完整
- OpenClaw：飞书也有（势均力敌）

### G17.3 多账号路由
- Hive：未审视
- OpenClaw：`tool-account-routing.test.ts` 多账号场景

---

## §统计总览

按维度的缺陷数量（粗略）：

| 维度 | 缺陷项数 | Hive 已有 | 完全缺 |
|---|---|---|---|
| 1. 工具体系广度 | 18 | 0 | 18 |
| 2. BashTool 防御深度 | 7 | 1 | 6 |
| 3. Prompt 质量 | 5 | 0 | 5 |
| 4. Memory 治理 | 10 | 1 | 9 |
| 5. Skills 体系 | 7 | 1 | 6 |
| 6. MCP 体系 | 4 | 0 | 4 |
| 7. ACP 生态 | 4 | 1 | 3 |
| 8. Multi-agent 协调 | 5 | 1 | 4 |
| 9. Channels / Renderer | 5 | 3 | 2 |
| 10. Permission 模型 | 6 | 1 | 5 |
| 11. Observability | 6 | 1 | 5 |
| 12. Engineering 工程化 | 8 | 1 | 7 |
| 13. Spec-driven OpenSpec 真意 | 6 | 1 | 5 |
| 14. Self-improvement | 4 | 0 | 4 |
| 15. Smart Model Routing | 3 | 1 | 2 |
| 16. Distillation / Token 优化 | 3 | 0 | 3 |
| 17. Channel 工程深度 | 3 | 2 | 1 |
| **合计** | **~104 项** | **14** | **~89** |

**Hive 真实状态**：~104 项 engineering quality 维度里，Hive 已有 14（13.5%），完全缺 ~89（85.6%），其余少量是 partial。

---

## §下一步

**不要在本文件里分优先级**。本文件目的是 full coverage。

下一步选项（你选）：

1. **A) 按维度分 P0/P1/P2**（每维度内部排优先级）
2. **B) 跨维度选最高 ROI 项**（综合排，pick top 10-15 进 next quarter）
3. **C) 按"风险驱动"排**（destructive 风险 / 用户硬约束 / 数据丢失风险 优先）
4. **D) 按"依赖关系"排**（如 todos UI 是 Spec-driven 重构前置 / observability 是其他验收前置）

我等你定后立即排。

---

*— End of Gap Inventory —*
