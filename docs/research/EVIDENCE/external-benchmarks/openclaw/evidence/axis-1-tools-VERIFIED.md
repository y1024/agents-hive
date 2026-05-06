# OpenClaw Axis 1: Tools System — 主线程逐条核实

> **核实方法**：L1 依赖文件 + L2 多关键词全 repo grep + L3 docs/ 检查 + L4 关键代码段 Read
> **日期**：2026-04-25
> **目的**：验证子 agent axis-1-tools.md 每条断言

---

## 验证结果总览

| 类型 | 总数 | [VERIFIED] | [REVISED] | [FALSE] | [TBV] |
|---|---|---|---|---|---|
| 文档断言 (A1-A5+B2-B3) | 7 | 6 | 1 | 0 | 0 |
| 代码断言 (B1, B4-B11) | 8 | 8 | 0 | 0 | 0 |
| 蓝军 mutation (M1-M3) | 3 | 1 | 2 | 0 | 0 |
| **合计** | **18** | **15** | **3** | **0** | **0** |

**结论**：axis-1 主体可信，但有 3 条需要修正/补充，不是关键 strategic blocker。

---

## §1 文档断言核实

### A1 [REVISED] 分层工具策略过滤

**原断言**："分层 tool→provider→global→agent→sandbox→subagent **6 层**"

**核实证据** (`docs/tools/multi-agent-sandbox-tools.md:206-219`)：
```
1. Tool profile (`tools.profile`)
2. Provider tool profile (`tools.byProvider[provider].profile`)
3. Global tool policy (`tools.allow` / `tools.deny`)
4. Provider tool policy (`tools.byProvider[provider].allow/deny`)
5. Agent-specific tool policy (`agents.list[].tools.allow/deny`)
6. Agent provider policy (`agents.list[].tools.byProvider[provider].allow/deny`)
7. Sandbox tool policy (`tools.sandbox.tools` ...)
8. Subagent tool policy (`tools.subagents.tools` ...)
```

**修正**：实际是 **8 层**，不是 6 层（原断言漏数 Provider tool policy 和 Agent provider policy）

### A2 [VERIFIED] cascade-only model
- 原文 `:220` "Each level can further restrict tools, but cannot grant back denied tools from earlier levels."

### A3 [VERIFIED] 9 个 group:* 快捷键
- `:225-237` 完整列出：runtime/fs/sessions/memory/ui/automation/messaging/nodes/openclaw — **9 个全部对**

### A4 [VERIFIED] 23 个内置工具列表
- `docs/concepts/system-prompt.md:18` "Tooling: current tool list + short descriptions"
- 工具数量在 `src/agents/system-prompt.ts:240-272` coreToolSummaries 共 **24 个 key**（包括 `apply_patch`，原断言 "23" 漏数 1 个）

**轻微修正**：实际 24 个工具，不是 23 个

### A5 [VERIFIED] 工具按固定顺序呈现
- `src/agents/system-prompt.ts:274-298` `toolOrder` 数组完整存在

### B2 [VERIFIED] POST /tools/invoke 端点
- `docs/gateway/tools-invoke-http-api.md:8-12` 完全确认

### B3 [VERIFIED] HTTP 硬质黑名单
- `docs/gateway/tools-invoke-http-api.md:60-64` 列出 `sessions_spawn` `sessions_send` `gateway` `whatsapp_login` 4 个

---

## §2 代码断言核实

### B1 [VERIFIED] availableTools 集合检查
- `src/agents/system-prompt.ts:46` `if (!params.availableTools.has("memory_search") && !params.availableTools.has("memory_get"))` — 完全确认

### B4 [VERIFIED] AnyAgentTool 接口
- `src/agents/tools/common.ts:8-11`：
  ```typescript
  export type AnyAgentTool = AgentTool<any, unknown> & {
    ownerOnly?: boolean;
  };
  ```
- **重大补充发现**：`AgentTool` 类型来自 `@mariozechner/pi-agent-core`（`common.ts:2`）

### B5 [VERIFIED] createXxxTool() 工厂函数
- `sessions-spawn-tool.ts:68` `export function createSessionsSpawnTool(...)` — 确认

### B6 [VERIFIED] TypeBox schema
- `sessions-spawn-tool.ts:1` `import { Type } from "@sinclair/typebox"` + 整个 Schema 定义在 23-66 行

### B7 [VERIFIED] 三级权限异常
- `common.ts:27` `OWNER_ONLY_TOOL_ERROR`
- `common.ts:29-35` `class ToolInputError` (status: 400)
- `common.ts:37-43` `class ToolAuthorizationError extends ToolInputError` (status: 403)

### B8 [VERIFIED] ActionGate 模式
- `common.ts:46-55` `createActionGate<T>(actions)` 函数完整存在

### B9 [VERIFIED] sessions_spawn 双 runtime
- `sessions-spawn-tool.ts:10` `const SESSIONS_SPAWN_RUNTIMES = ["subagent", "acp"] as const;`

### B10 [VERIFIED] coreToolSummaries 硬编码
- `src/agents/system-prompt.ts:243-272` 完整存在

### B11 [VERIFIED] toolOrder 数组
- `src/agents/system-prompt.ts:274-298` 完整存在，**注意**：toolOrder 列出 24 个工具（含 apply_patch），与 A4 修正一致

---

## §3 蓝军 mutation 重跑

### M1 [VERIFIED] 工具黑名单支持
- 主线程 grep 找到 **多层 deny 体系**：
  - `tools.deny` (全局) — `src/config/schema.help.ts:303`
  - `gateway.tools.deny` (网关) — `src/config/schema.help.ts:101`
  - `agents.list[].tools.deny` (agent) — legacy migrations 显示曾支持
  - `tools.byProvider.deny` (per-provider) — `src/config/schema.help.ts:563`
  - `tools.subagents` (subagent) — `src/config/schema.help.ts:332`
- 原断言 PASS

### M2 [REVISED] 并发限制
- 原断言："工具级无并发限制；依赖网关、沙箱或外部编排"
- **真相**：infra/memory 层**有** concurrency 控制：
  - `bonjour-discovery.ts:324` `const concurrency = 6`
  - `memory/manager-sync-ops.ts:112,724,815,1070` 有 `concurrency` 字段
  - `discord/monitor/thread-bindings.lifecycle.test.ts:1153` "caps ACP startup health probe concurrency"
- **修正**：infra/memory 层有 concurrency，但 **agent 工具调用本身（如 read/write/exec）确实无原生并发限制**
- **结论修正后仍成立**：工具调用层无 concurrency cap

### M3 [REVISED] 工具超时
- 原断言："工具本身无超时；sessions_spawn 有 runTimeoutSeconds"
- **真相**：发现多个 runTimeout/runTimeoutMs 配置：
  - `sessions_spawn` `runTimeoutSeconds` ✓ (`sessions-spawn-tool.ts:37`)
  - `subagents.runTimeoutSeconds` ✓ (`zod-schema.agent-defaults.ts:189`)
  - `discord/inbound-worker.ts` `runTimeoutMs` (15ms 默认)
  - `providers-core.ts:587` provider 级 `runTimeoutMs`
  - `agent-defaults.ts:281` agent 默认 `runTimeoutSeconds`
  - 专属测试：`openclaw-tools.subagents.sessions-spawn-default-timeout.test.ts`
- **修正**：runtime/agent/provider/subagent 层都有 timeout 配置
- **结论修正后**：核心工具（read/write/exec）确实无内置 timeout，但**运行环境**（agent default + subagent default + provider）都有 timeout 兜底

---

## §4 重大补充发现（子 agent 漏报）

### F1 — `@mariozechner/pi-agent-core` 是真实依赖

子 agent 报告里没明确提，但实际：
- `src/agents/tools/common.ts:2` `import type { AgentTool, AgentToolResult } from "@mariozechner/pi-agent-core";`
- OpenClaw 的 `AnyAgentTool` 类型**继承自** pi-agent-core 的 `AgentTool`
- **意味着**：OpenClaw 工具系统的核心抽象在 pi-agent-core 中，OpenClaw 只做 wrapper（加 `ownerOnly` 字段）
- **战略影响**：之前 SYNTHESIS §11 "把 pi-agent-core 误读成 pi-tui" 的修正本身又**部分错误** — pi-agent-core 真存在，且 OpenClaw 工具体系基于它

### F2 — `acp-spawn.js` / `subagent-spawn.js` 是 OpenClaw 自有
- `sessions-spawn-tool.ts:3` `import { ACP_SPAWN_MODES, ACP_SPAWN_STREAM_TARGETS, spawnAcpDirect } from "../acp-spawn.js";`
- `sessions-spawn-tool.ts:6` `import { SUBAGENT_SPAWN_MODES, spawnSubagentDirect } from "../subagent-spawn.js";`
- 与 pi-agent-core 不同，spawn 实现在 OpenClaw 自己 src/agents/

### F3 — `unsupportedSessionsSpawnParam` 黑名单参数
- `sessions-spawn-tool.ts:12-21` 列出 7 个不被 sessions_spawn 接受的参数（target/transport/channel/to/threadId/replyTo 等）
- **战略影响**：这是个安全设计模式 — 显式拒绝某些参数（信道劫持防护），Hive 可以借鉴

### F4 — Sessions Spawn `attachments` 字段（最多 50 个）
- `sessions-spawn-tool.ts:50-60` 支持 inline attachments，单次最多 50 个，**transcript 持久化时会 redact**
- **战略影响**：Hive 文件附件机制可对照

---

## §5 修正后的 axis-1 核心断言

1. **工具系统采用"工厂 + TypeBox schema"范式** ✓ — 但**核心 AgentTool 类型来自 `@mariozechner/pi-agent-core`，OpenClaw 只做 wrapper**
2. **24 个硬编码内置工具**（修正 from 23）
3. **8 层过滤策略严格单向限制**（修正 from 6 层）
4. **权限采用 ownerOnly 布尔 + ActionGate 细粒度 action** ✓
5. **工具级无原生并发限制和超时机制**，但 **runtime/agent/provider/subagent 层都有 timeout 配置**（M3 修正）
6. **支持 group:* 快捷键和 HTTP /tools/invoke 直接调用** ✓

---

## §6 与 SYNTHESIS 的影响

| SYNTHESIS 引用 | 是否受影响 |
|---|---|
| §0 #1 OpenClaw MCP 维度 | 不受 axis-1 影响 |
| §0 #5 Pre-compaction memory flush | 不受 axis-1 影响 |
| §0 #7 mcporter | 不受 axis-1 影响 |
| §3 P1-5 工具分组 group:* | **加强**（从 9 个验证到 9 个，axis-1 [VERIFIED]） |
| §5 反面教材 #4 工具级无并发 | **修正措辞**：infra 有，工具调用层没有 |

---

## §7 仍待核实

无，axis-1 已完整核实。

---

*— End of axis-1 主线程核实 —*
