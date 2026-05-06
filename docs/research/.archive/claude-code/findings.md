# Claude Code Harness 调研

> **状态**：Agent 调研草稿（一次性 WebSearch + WebFetch 通道）
> **核实状态**：每条断言下方标 [HC] 高确定性 / [TBV] 待核实
> **日期**：2026-04-25
> **范围约束**：Hive 锁定国内 IM 阶段性边界 + harness 工程全球顶级雄心
> **目的**：作为 deer-flow 6-axis 框架的对照源之一（与 OpenClaw 并列）

---

## 0. 一页纸结论

Claude Code 是 **Anthropic 闭源 CLI + 开源 Agent SDK** 的双层产品。harness 设计的核心特征：
1. **"Dumb loop"**：harness 不做编排，所有决策权交给模型；与 LangGraph 的显式状态机/DAG 形成范式对立
2. **多层 memory**：CLAUDE.md（静态指令）+ MEMORY.md（auto-memory，25KB 上限懒加载）分离
3. **Progressive skill loading**：startup 只加载 frontmatter（~100 token/skill），命中时才读全文（~5K token）
4. **分层 permission**：Deny → Ask → Allow，配合 modal execution（plan / auto / dontAsk / bypassPermissions）替代每次 prompt
5. **Hooks 决定性自动化**：26+ 生命周期事件，CI/CD 风格，区别于 LLM 决策

**对 Hive 借鉴的高 ROI 候选**（待与 deer-flow 既有结论合并）：
- Progressive loading 模式（解决"工具/skill 越多越塞 context"）
- 分层 permission tier（Hive 已有 SafeExecutor `Allow/Ask/Deny`，可对照其 modal execution）
- Hooks 作为决定性自动化（Hive 当前主要靠 prompt 驱动）

---

## 1. Tools 体系

**公开来源**：
- https://code.claude.com/docs/en/cli-reference [TBV，URL 需核实是否官方]
- https://code.claude.com/docs/en/permissions [TBV]
- https://github.com/anthropics/claude-agent-sdk-python [HC，已知开源仓库]
- https://www.anthropic.com/engineering/claude-code-auto-mode [TBV]

**内置工具集**（始终在 system prompt，~14-17K token）：
Read / Write / Edit / Glob / Grep / Bash / Task / WebFetch / WebSearch / Skill / Agent — 11 个核心 [HC]

**Permission 模型分层**：
- 评估顺序：Deny（首匹配阻断）→ Ask（确认）→ Allow（自动）[HC]
- `permission_mode`：`default` / `plan`（read-only）/ `auto`（ML 分类器）/ `dontAsk` / `bypassPermissions` [HC]
- 每条命令颗粒度：`Bash(npm run test *)` 通配 [HC]
- 进程包装剥离：`timeout`、`xargs` 等会被识别 [TBV]
- 复合命令拆分 [TBV]
- Symlink：allow 规则需 link + target 都匹配；deny 规则任一匹配即阻 [TBV]

**Hive 可借鉴**：
- **Deny-first 评估顺序**比 Allow-first 更安全 — Hive 当前 `MatchPolicy` 顺序需对照
- **Per-command 颗粒度** + 通配符比 per-tool-class 更细
- **Modal execution** 减少每次 prompt 的摩擦 — Hive 现在 IM 通道的 PolicyAsk 自动放行就是同思路
- 14-17K token 永远塞 system prompt 是反面例子 — Hive 应保留 progressive loading 路径

**已知局限**：
- Bash URL 模式不可靠（无法稳健限制 `curl` 到特定 URL）
- Read/Edit 规则只覆盖 Claude 的工具，不覆盖 Bash 子进程（需要 OS 沙箱）
- `bypassPermissions` 仍提示关键路径（.git, .claude, .vscode）

---

## 2. Memory 体系

**公开来源**：
- https://code.claude.com/docs/en/memory [TBV]
- https://github.com/centminmod/my-claude-code-setup [TBV]
- https://milvus.io/blog/claude-code-memory-memsearch.md [TBV]

**CLAUDE.md（静态，用户写）**：
- 多层级：managed policy（组织级，不可豁免）> project（./CLAUDE.md 或 ./.claude/CLAUDE.md）> user（~/.claude/CLAUDE.md）> local（./CLAUDE.local.md，gitignore）[HC]
- 通过 `@path/to/file` 导入，最多 5 跳，相对引用文件解析 [HC]
- Path-scoped 规则：`.claude/rules/*.md` 配 `paths:` frontmatter（glob 匹配）[TBV]
- **启动时全量装入 context**（不懒加载，吃 token）[HC，本会话亲见]

**Auto-memory（Claude 自己写的学习）**：
- 路径：`~/.claude/projects/<project>/memory/`，按 git repo 分隔 [HC，本会话亲见]
- MEMORY.md 是索引（首 200 行 / 25KB / 会话）+ 主题文件（懒加载）[HC]
- Claude 决定写什么（构建命令、调试模式、偏好）[HC]
- 不跨机器；同 repo 各 worktree 共享一个 auto-memory 目录 [HC]

**Context window 管理**：
- `/compact` 压缩后重新读 CLAUDE.md，但子目录嵌套 CLAUDE.md 不自动 reload [TBV]
- 压缩保留每个 invoked skill 最多 5,000 token，合计 25K [TBV]
- system prompt（~2.5K）+ tool definitions（~14-17K）已占大头 [HC]

**Hive 可借鉴**：
- **静态指令（CLAUDE.md）vs 学习笔记（MEMORY.md）的分离** — Hive 现有 memory 体系（向量搜索）偏后者，可参考其分层
- **Path-scoped 规则**：monorepo 不爆 CLAUDE.md
- **Imports（@path）跨项目共享**

**已知局限**：
- CLAUDE.md 全量装入，长文件降低指令遵守度（建议 <200 行/文件）
- Auto-memory 主题文件需显式 Bash 读，无相关性自动加载
- 嵌套 CLAUDE.md 压缩后不自动 reload

---

## 3. Skills 体系

**公开来源**：
- https://code.claude.com/docs/en/skills [TBV]
- https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview [TBV]
- https://github.com/anthropics/skills [TBV，需核实是否真存在该 org]

**Frontmatter 驱动的路由（Progressive Disclosure）**：
- Startup：只加载 frontmatter（~100 token/skill）[HC，本会话亲见 SKILL 列表]
  - 字段：`name`、`description`、`when_to_use`、`disable-model-invocation`、`user-invocable`、`allowed-tools`、`context`、`agent`、`paths`
- 命中：完整 SKILL.md 装入（~5K token）[HC]
- 主题文件：Claude 按需 Bash 读 [HC]

**关键 frontmatter**：
- `disable-model-invocation: true` → 仅用户触发 [HC]
- `user-invocable: false` → 仅 Claude（背景知识）[TBV]
- `paths: ["src/**/*.ts"]` → 仅匹配文件时加载 [TBV]
- `allowed-tools: "Bash(git *) Bash(npm run *)"` → 执行期预批准 [TBV]
- `context: fork` → 隔离子 agent 跑 [TBV]
- `$ARGUMENTS`, `$N`, `$name` → 位置/命名参数替换 [HC]

**进阶**：
- `` !`command` `` 块：Claude 看到内容前 shell 预处理 [HC]
- 压缩：每 skill 保留前 5,000 token；多 skill 共享 25K 预算 [TBV]

**Hive 可借鉴**：
- **Progressive loading**：Hive Skills 系统已声明式 Markdown，但是否做 frontmatter-only startup 需对比
- **Glob path-scoping**：减少 monorepo skill 噪音
- **Shell 预处理 `` !`...` ``**：动态注入比静态指令灵活
- 与 Hive `FindBySpecRequirements`（`registry.go:360-400`）的"宿主 requirement resolver"思路对比

**已知局限**：
- description + when_to_use 列表合计截断 1,536 字符
- 子 agent 预加载 skill 是全量（非懒）
- shell 预处理需显式 `` !`...` `` 语法

---

## 4. MCP / 外部协议

**公开来源**：
- https://code.claude.com/docs/en/mcp [TBV]
- https://github.com/anthropics/claude-agent-sdk-python [HC]
- https://www.anthropic.com/engineering/building-agents-with-the-claude-agent-sdk [TBV]
- https://mcpservers.org [TBV]

**MCP 集成模型**：
- 子进程模型（默认）：Claude Code spawn MCP server 子进程，stdin/stdout JSON-RPC 2.0 [HC]
- HTTP 模式：部分 server 支持，URL 连接 [HC]
- 配置：`.claude/mcp.json`（项目）或用户级；`claude mcp` CLI 管理 [HC]
- 认证：MCP 处理 OAuth；Claude Code 不直接管凭证 [TBV]

**Agent SDK 中的 MCP**：
- `mcp_servers` dict：`{"name": ServerInstance}` [HC]
- 工具命名：`mcp__servername__toolname` [HC]
- Permission 规则：`mcp__puppeteer__*`（通配）/ `mcp__puppeteer__navigate`（具体）[HC]

**Hive 可借鉴**：
- **Subprocess MCP** 比 HTTP 简单（无端口管理）— Hive 已用 MCP Go SDK，路径一致
- **工具命名空间** `mcp__servername__toolname` 防冲突 — 对照 Hive `mcphost.Host` 的去重策略

**已知局限**：
- MCP server 子进程无跨会话状态（每次 spawn 新进程）
- HTTP MCP 需手动配置 URL，无自动发现
- MCP 不替代内置工具（Bash/Read/Edit），只附加
- 大型 MCP 工具（浏览器）吃大量工具定义 token

---

## 5. Channels / Renderer

**公开来源**：
- https://code.claude.com/docs/en/cli-reference [TBV]
- https://code.claude.com/docs/en/remote-control [TBV]
- https://code.claude.com/docs/en/interactive-mode [TBV]

**Interactive 模式（终端 TUI）**：
- 流式文本 + 实时工具调用渲染 [HC]
- Shift+Tab 循环 permission mode（default → auto → plan → bypassPermissions）[HC]
- `/commands` 交互工作流（/add-dir, /memory, /permissions, /model, /compact, /resume, /rename）[HC]
- 会话持久化：`~/.claude/sessions/`，`--resume` / `--continue` [HC]

**Print 模式（非交互）**：
- `claude -p "query" --output-format text|json|stream-json` [HC]
- `--output-format stream-json`：流式事件（messages, tool calls, hook events）[HC]
- `--input-format stream-json`：消费流式输入（管道串接）[HC]

**IDE 集成**：
- VS Code 插件 + JetBrains 侧边栏 [HC]
- IDE 不加工具，同 harness 不同 UI 层 [HC]

**Remote Control（Web + CLI）**：
- `--remote-control`：本地起 session，claude.ai 上访问 [TBV]
- `--remote`：仅 web session [TBV]
- `--teleport`：把 web session 拉回本地终端 [TBV]
- Agent Team 模式：多子 agent 并排渲染（tmux 或进程内）[TBV]

**Hive 可借鉴**：
- **Stream JSON 结构化事件** vs Hive WebSocket 事件类型（`input_request`/`tool_call`/`agent_status`）— 几乎对齐，Hive 优势已确认
- **Permission mode 循环（Shift+Tab）** 减少 UI 摩擦
- **Hive 反向输出确认**：deer-flow final-verdict §6 已说明 Hive `EventRenderer` + 飞书 `PatchCard` 比 deer-flow 单一 SSE 流领先 — Claude Code 这条也是单一 stream 模式，Hive 仍领先

**已知局限**：
- IDE 插件仍 shell 绑定（无 IDE 沙箱）
- Web session 时长受限
- Stream JSON 需手动解析，Agent SDK 无内置 client lib

---

## 6. Prompts / System

**公开来源**：
- https://github.com/Piebald-AI/claude-code-system-prompts [TBV，疑似第三方提取]
- https://www.dbreunig.com/2026/04/04/how-claude-code-builds-a-system-prompt.html [TBV]
- https://code.claude.com/docs/en/cli-reference [TBV]

**System Prompt 架构（110+ 条件指令）**：
- Identity（~100 token）：身份定义 [HC]
- Task execution（~600 token）：read before modify / no over-engineer / no unnecessary files [HC]
- Safe action execution（~540 token）：可逆性 + 爆炸半径 per tool class [HC，本会话亲见]
- Output & tone（~320 token）：concise / lead with answer / minimal emoji [HC]
- **总计 ~2.5K token**，不含工具定义 [TBV，数字精确性需核实]

**Tool 定义（最大组件）**：
- ~14-17K token 工具 schema（name / description / input schema）[TBV]
- 描述用自然语言 + 约束提示（"Glob(pattern) — avoid using for large repos"）[HC]

**System prompt 定制**：
- `--system-prompt`：替换全部 [HC]
- `--append-system-prompt`：附加（保留内置能力）[HC]
- `--system-prompt-file`、`--append-system-prompt-file`：从文件加载 [HC]
- `--exclude-dynamic-system-prompt-sections`：把 per-machine 段（cwd / git status / memory paths）挪到首条 user message，复用 prompt cache [TBV]

**Agent SDK 默认 prompts**：
- `modifying_system_prompts` docs 展示 SDK options 定制 [TBV]
- 子 agent prompts（Explore / Plan / general-purpose）有专门 system prompt [HC]

**Hive 可借鉴**：
- **条件 system prompt 拼装（110+）模块化** — Hive 当前 i18n prompts 单文件，可对照拆分粒度
- **Tool 描述自然语言 + schema** 比纯 JSON schema 更可审计 — 已和 Hive 现状一致
- **Prompt cache 优化**（dynamic sections 挪首条 user msg）— 高 ROI 省 token

**已知局限**：
- 工具定义始终在 context（14-17K token），不可懒加载工具（区别于 skills）
- system prompt 不按模型版本（Haiku/Sonnet/Opus 同 prompt）
- 自定义 system prompt 替换会丢内置能力（除非 append）

---

## 7. 与 DeerFlow（LangGraph 范式）的 3 大范式差异

1. **Loop 架构**：LangGraph 维护显式 DAG state + node-based routing；Claude Code 是单 `query() → streaming async iterator` loop，模型自己决定动作。**无显式状态机** — Claude 拥有控制流。

2. **Memory 模型**：LangGraph 一般在 context 里压缩；Claude Code 把"持久指令（CLAUDE.md，全量读）"和"自学习笔记（MEMORY.md，25KB cap，懒加载主题）"分开。**Frontmatter 驱动的 skill discovery** vs 显式 tool registry。

3. **Tool 执行**：LangGraph 工具是显式 graph node；Claude Code 工具是"环境 context"（14-17K token tool 定义始终在）+ permission gating。**无 tool invocation graph** — permission rules + modal execution 替代路由逻辑。

---

## 8. 仍需核实清单（落到 PR/进一步调研之前必须做）

1. URL 是否官方（`code.claude.com` 这个域名是否真为 Anthropic 官方？已知 Anthropic 官方文档常在 `docs.anthropic.com`，`code.claude.com` 待核实）
2. 14-17K token 工具定义、110+ 指令、25KB MEMORY 上限、5,000 token/skill 压缩等具体数字
3. `Piebald-AI/claude-code-system-prompts` 是否可信第三方逆向（vs 官方泄漏）
4. `--remote-control` / `--teleport` / Agent Team 等高级特性是否真实存在（vs Agent 幻觉）

---

## 9. 文件索引

- 本文件：`docs/research/claude-code/findings.md`
- 对照：`docs/调研笔记/deer-flow/PLAN.md`、`docs/调研笔记/deer-flow/archive/final-verdict.md`
- 同期：`docs/research/openclaw/findings.md`

*— End of Claude Code 调研草稿 —*
