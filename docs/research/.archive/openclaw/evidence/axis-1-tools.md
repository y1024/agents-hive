# OpenClaw Axis 1: Tools System

## 1. 设计文档断言（来自 docs/）

### 工具可见性与过滤机制
- [HC] OpenClaw 采用分层工具策略过滤：tool profile → provider profile → global allow/deny → agent-specific allow/deny → sandbox policy → subagent policy — `docs/tools/multi-agent-sandbox-tools.md:205-220`
- [HC] 工具可见性采用"只能进一步限制，不能解禁"的级联模型（each level can only further restrict, but cannot grant back denied tools） — `docs/tools/multi-agent-sandbox-tools.md:220-221`
- [HC] 支持工具组（group:*）快捷键：`group:runtime`, `group:fs`, `group:sessions`, `group:memory`, `group:ui`, `group:automation`, `group:messaging`, `group:nodes`, `group:openclaw` — `docs/tools/multi-agent-sandbox-tools.md:225-237`

### 系统 Prompt 中的工具呈现
- [HC] 内置工具清单在 system prompt 的 Tooling 部分，包含 23 个核心工具 (read/write/edit/grep/find/ls/exec/process/web_search/web_fetch/browser/canvas/nodes/cron/message/gateway/agents_list/sessions_*/subagents/session_status/image) — `docs/concepts/system-prompt.md:15-19`
- [HC] 工具按固定顺序呈现，每个工具配有单行简述 — `src/agents/system-prompt.ts:240-298`
- [HC] 工具可见性跟踪：availableTools 集合在 system prompt 的多个 section 中被检查（如 memory section 检查 has("memory_search") 和 has("memory_get")） — `src/agents/system-prompt.ts:40-64`

### HTTP /tools/invoke 端点
- [HC] Gateway 暴露 POST /tools/invoke HTTP 端点，用于直接工具调用，绕过完整 agent turn — `docs/gateway/tools-invoke-http-api.md:9-14`
- [HC] 该端点应用完整的工具策略链（tool profile → provider → agent → group → subagent）但有额外的硬质黑名单（sessions_spawn/sessions_send/gateway/whatsapp_login） — `docs/gateway/tools-invoke-http-api.md:50-81`

## 2. 代码实现验证（grep + Read）

### 工具定义与注册
- [HC] 工具通过 `AnyAgentTool` 接口定义：{ label, name, description, parameters: TypeBox Schema, execute } — `src/agents/tools/common.ts:1-15`
- [HC] 每个工具是独立的 .ts 文件，导出 `createXxxTool()` 工厂函数，接收 SpawnedToolContext — `src/agents/tools/sessions-spawn-tool.ts:68-79`
- [HC] 工具参数通过 TypeBox schema 定义，支持 Optional/String/Object/Array 等复合类型 — `src/agents/tools/sessions-spawn-tool.ts:23-66`

### 权限与错误处理
- [HC] 三级权限异常：ToolInputError (400)、ToolAuthorizationError (403，继承 ToolInputError)、OWNER_ONLY_TOOL_ERROR 常量 — `src/agents/tools/common.ts:25-42`
- [HC] ActionGate 模式用于细粒度工具 action 控制：`createActionGate<T>(actions?)` 返回 `(key, defaultValue?) => boolean` — `src/agents/tools/common.ts:45-55`

### 工具搭配 Subagent/ACP 运行时
- [HC] sessions_spawn 支持两种 runtime：subagent（隔离子会话）和 acp（ACP 编码会话）— `src/agents/tools/sessions-spawn-tool.ts:10, 26, 86-99`
- [HC] 支持 sandbox="inherit"|"require" 参数，控制子会话是否继承或强制沙箱 — `src/agents/tools/sessions-spawn-tool.ts:11, 43`

### 内置工具列表与动态可见性
- [HC] coreToolSummaries 硬编码在 buildSystemPrompt 函数，包含所有 23 个工具的单行描述 — `src/agents/system-prompt.ts:240-272`
- [HC] 系统 prompt 中工具列表顺序由 toolOrder 数组确定（固定顺序：read → write → edit → ... → image） — `src/agents/system-prompt.ts:274-299`

## 3. 蓝军 Mutation

### Mutation 1: "OpenClaw 真的支持工具黑名单吗？"
- 命令：`grep -r "tools.*deny\|deny.*tool" /src --include="*.ts"`
- 结果：PASS — 找到 `tools.deny` 在 multi-agent-sandbox-tools.md:65-66, 306 出现多次；网关 HTTP deny list 在 docs/gateway/tools-invoke-http-api.md:62-68
- 断言确认：OpenClaw 支持全局、agent、group、provider 级别的 deny list，且 HTTP endpoint 有默认硬质 deny list

### Mutation 2: "工具是否支持并发限制？"
- 命令：`grep -r "concurr\|parallel\|limit.*exec\|rateLimit.*tool" /src --include="*.ts"`
- 结果：FAIL（找到 rateLimit 但仅在网关认证层，非工具级） — `src/gateway/server.ts` 搜索无工具级并发限制的显式实现
- 断言：OpenClaw 在工具级别不原生支持并发限制；依赖网关、沙箱或外部编排

### Mutation 3: "tool_call 是否有显式的中止/超时机制？"
- 命令：`grep -r "timeout\|abort\|cancel.*tool\|kill.*tool" /src/agents/tools --include="*.ts"`
- 结果：FAIL（toolCall 本身无内置超时；但 sessions_spawn 支持 runTimeoutSeconds） — `src/agents/tools/sessions-spawn-tool.ts:37-39`
- 断言：OpenClaw 工具本身无显式超时；超时由上层 session/ACP runtime 管理

## 4. 与 Hive 现状对照

### 借鉴
- Hive `/tools/invoke` HTTP 端点设计可参考 OpenClaw 的实现：分层策略链 + 硬质黑名单 — `Hive: internal/gateway/tools.go`
- 工具权限分类（ownerOnly/restricted）可参考 `AnyAgentTool.ownerOnly` 属性 — `src/agents/tools/common.ts:10`
- 工具分组快捷键（group:runtime, group:fs 等）可简化 Hive 配置 — `docs/tools/multi-agent-sandbox-tools.md:225-237`

### 反面教材
- Hive 如果在工具级实现并发限制，OpenClaw 不支持，需慎重（可能导致 agent 行为差异）

### 别抄
- ToolAuthorizationError extends ToolInputError（HTTP 400->403 的继承设计）在 Hive 可能过度设计；建议平坦化

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **工具注册** | 动态注册机制（plugin-sdk）| 工厂函数模式，编译时确定工具集 |
| **可见性控制** | tool_visibility 字段 | 分层 allow/deny 策略链 + group: 快捷键 |
| **tool_call 执行** | 带中止/超时的 tool runner | 工具本身无超时；由 session/ACP runtime 管理 |
| **权限模型** | token-based RBAC | ownerOnly 布尔值 + ActionGate 细粒度 action 控制 |
| **provider 插件** | 通过 provider SDK | 通过 /extensions/provider 扩展目录 |

---

## 核心断言总结

1. **工具系统采用"工厂 + TypeBox schema"范式**，23 个硬编码内置工具
2. **分层过滤策略严格单向限制**（cascade-only model），不支持解禁
3. **权限采用 ownerOnly 布尔 + ActionGate 细粒度 action** 而非 token-based RBAC
4. **工具级无原生并发限制和超时机制**，依赖上层 runtime
5. **支持工具组快捷键和 HTTP /tools/invoke 直接调用**，绕过完整 agent turn
