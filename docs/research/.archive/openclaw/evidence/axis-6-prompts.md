# OpenClaw Axis 6: System Prompts & Safety

## 1. 设计文档断言（来自 docs/）

### System Prompt 结构
- [HC] OpenClaw system prompt 采用分模块设计，包含固定段落：Tooling/Safety/Skills/OpenClaw Self-Update/Workspace/Documentation/Workspace Files/Sandbox/Current Date & Time/Reply Tags/Heartbeats/Runtime/Reasoning — `docs/concepts/system-prompt.md:15-31`
- [HC] Prompt 采用"OpenClaw-owned"方式，不使用 pi-coding-agent 默认 prompt — `docs/concepts/system-prompt.md:11`
- [HC] 三种 promptMode：full (默认，主agent) / minimal (sub-agents，简化) / none (仅身份行) — `docs/concepts/system-prompt.md:35-49`

### 工具描述 (Tool Description) 风格
- [HC] 工具描述采用单行简述格式，放在 Tooling section — `src/agents/system-prompt.ts:240-272` 示例
- [HC] 工具摘要包括用途 hint，如 cron 工具："use for reminders; when scheduling a reminder, write the systemEvent text as something that will read like a reminder..." — `src/agents/system-prompt.ts:256`
- [HC] 工具可见性通过 availableTools 集合动态过滤，仅显示允许的工具 — `src/agents/system-prompt.ts:40-64`

### HITL / Permission Prompt
- [HC] 系统 prompt 的 Safety 段落提醒模型避免 power-seeking behavior 和绕过监督 — `docs/concepts/system-prompt.md:20`
- [HC] Safety guardrails 是"建议性"的，不强制；硬执行通过 tool policy / exec approvals / sandboxing / channel allowlists — `docs/concepts/system-prompt.md:33`
- [HC] 执行批准流程：通过 `/approve <id> allow-once|allow-always|deny` 命令响应 exec approval prompts — `docs/tools/slash-commands.md` + 推断

### 上下文注入与截断
- [HC] Bootstrap 文件（AGENTS.md/SOUL.md/TOOLS.md/IDENTITY.md/USER.md/HEARTBEAT.md/BOOTSTRAP.md/MEMORY.md）注入到 context，受 bootstrapMaxChars (20000) 和 bootstrapTotalMaxChars (150000) 限制 — `docs/concepts/system-prompt.md:52-79`
- [HC] 截断时可配置警告（off/once/always），通过 `agents.defaults.bootstrapPromptTruncationWarning` — `docs/concepts/system-prompt.md:77-79`
- [HC] 内部 hook `agent:bootstrap` 可拦截并变异注入的 bootstrap 文件（如交换 SOUL.md） — `docs/concepts/system-prompt.md:85`

### 运行时信息与动态部分
- [HC] System prompt 包含 Runtime 段落（host/OS/node/model/repo root/thinking level），单行呈现 — `docs/concepts/system-prompt.md:30`
- [HC] Current Date & Time 段落仅包含 **timezone**（无动态时钟），保持 prompt cache 稳定性 — `docs/concepts/system-prompt.md:89-103`
- [HC] `session_status` 工具用于获取当前时间戳（包含在 status card 中） — `docs/concepts/system-prompt.md:95-96`

## 2. 代码实现验证（grep + Read）

### Prompt 构建函数与模式
- [HC] `buildSystemPrompt()` 函数在 system-prompt.ts 中集中生成完整 prompt，参数接收 { toolDescriptions, skillsPrompt, heartbeatPrompt, ... promptMode, ... } — `src/agents/system-prompt.ts:170-236`
- [HC] 各部分通过对应的 `build*Section()` 函数构建：buildSkillsSection, buildMemorySection, buildUserIdentitySection, buildTimeSection 等 — `src/agents/system-prompt.ts:20-105`
- [HC] 段落返回字符串数组，最后 join 成完整 prompt — 代码推断

### 工具可见性与 Prompt 过滤
- [HC] buildMemorySection 根据 `availableTools.has("memory_search")` 决定是否注入 Memory Recall 段落 — `src/agents/system-prompt.ts:46`
- [HC] Tool descriptions 从 coreToolSummaries 对象按 toolOrder 遍历排列 — `src/agents/system-prompt.ts:274-299`
- [HC] 缺失工具的 summary 使用默认文本或跳过 — 代码推断

### Safety & Guardrails
- [HC] Safety 段落硬编码文本（"avoid power-seeking behavior or bypassing oversight"），保留在 minimal mode 下 — `docs/concepts/system-prompt.md:20, 33, 43-45`
- [HC] Session prompt 中可包含 messageToolHints 和 reactionGuidance，指导模型在通道中的行为 — `src/agents/system-prompt.ts:228, 230-234`
- [HC] Permission 相关的 prompt 通过 system prompt + `/approve` 命令交互实现 HITL 流程 — 设计推断

### Sub-agent 上下文与 Minimal Mode
- [HC] Minimal mode 下（仅限 sub-agents），移除的段落：Skills/Memory Recall/OpenClaw Self-Update/Model Aliases/User Identity/Reply Tags/Messaging/Silent Replies/Heartbeats — `docs/concepts/system-prompt.md:41-45`
- [HC] 保留的段落：Tooling/Safety/Workspace/Sandbox/Current Date & Time/Runtime/Reasoning — `docs/concepts/system-prompt.md:43-45`
- [HC] 注入的 context 被重新标记为 "Subagent Context" 而非 "Group Chat Context" — `docs/concepts/system-prompt.md:49`

## 3. 蓝军 Mutation

### Mutation 1: "System prompt 是否真的由 OpenClaw 自有而非 pi-coding-agent 默认？"
- 命令：`grep -r "pi-coding-agent\|default.*prompt\|system.*prompt.*override" /src/agents --include="*.ts" | head`
- 结果：PASS — `docs/concepts/system-prompt.md:11` 明确说"does not use the pi-coding-agent default prompt"；system-prompt.ts 是完全自主实现
- 断言确认：OpenClaw 完全自定义 system prompt

### Mutation 2: "Safety guardrails 是否被强制执行？"
- 命令：`grep -r "safety\|guardrail\|power.*seeking\|bypass.*oversight" /src --include="*.ts"`
- 结果：FAIL — Safety text 在 prompt 中但无代码层强制；强制通过 tool policy / sandboxing / approvals
- 断言确认：Safety 是 advisories only，硬执行通过其他机制

### Mutation 3: "Prompt truncation 警告是否真的可配置？"
- 命令：`grep -r "bootstrapPromptTruncationWarning\|truncation" /src --include="*.ts"`
- 结果：TBV — 文档提及配置（off/once/always），但代码实现不可达
- 断言：配置存在但实施不确定；可能仅文档说明

## 4. 与 Hive 现状对照

### 借鉴
- Prompt 的模块化构建（buildSkillsSection/buildMemorySection 等）便于维护和扩展 — `src/agents/system-prompt.ts:20-105`
- Tool descriptions 采用单行 + hint 格式易于模型理解，避免过长描述 — `src/agents/system-prompt.ts:256`
- Prompt caching 的稳定性优化（仅 timezone，无动态时钟）可参考 — `docs/concepts/system-prompt.md:89-103`

### 反面教材
- Bootstrap 文件注入受 token 限制可能导致截断；Hive 需谨慎 MEMORY.md 大小
- Safety guardrails 仅基于 prompt text，易被对抗性 prompt 绕过；需配合代码层强制

### 别抄
- promptMode="none" 选项用处有限；Hive 可能不需要该选项

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **Prompt 模式** | flexible multi-part | 固定 full/minimal/none 三态 |
| **工具描述** | schema-based 长描述 | 单行摘要 + inline hint |
| **Safety guardrails** | 代码强制 + prompt advisories | 仅 prompt advisories |
| **HITL/permission** | 显式 permission_prompt section | `/approve` 命令交互 |
| **Bootstrap injection** | 可配置大小限制 | 固定 20K/150K 限制 |
| **上下文注入** | 动态决策 | 固定模板 + 可选 hook 变异 |
| **Prompt caching** | 显式支持标记 | 通过 timezone-only 实现稳定性 |

---

## 核心断言总结

1. **System prompt 采用固定模块化结构**（Tooling/Safety/Skills/Workspace/... 等 9+ 段落）
2. **三种 promptMode**：full (主agent) / minimal (sub-agents) / none (仅身份行)
3. **工具描述**：单行摘要 + 通道特定 hint（如 cron 工具的 reminder 指导）
4. **Safety guardrails 仅是 prompt advisories**，硬执行通过 tool policy / sandboxing / approvals
5. **HITL 通过 `/approve` 命令**交互实现，非 prompt 段落
6. **Bootstrap 文件注入受 token 限制**，支持警告配置（off/once/always）
7. **Prompt caching 优化**：Current Date & Time 仅含 timezone（无动态时钟）
