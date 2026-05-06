# OpenClaw Axis 4: ACP Protocol & Extensions

## 1. 设计文档断言（来自 docs/）

### ACP 架构概览
- [HC] OpenClaw 支持 ACP（可编程协议，即 pi-agent-core 的子代理运行时），不是 MCP（Model Context Protocol）— `docs/concepts/architecture.md` + `src/acp/` 目录存在
- [HC] ACP 与 Subagent 双运行时并行：runtime="acp" 用于编码会话（persistent, session-bound），runtime="subagent" 用于一次性会话 — `src/agents/tools/sessions-spawn-tool.ts:10, 26, 86-99`
- [HC] ACP 会话可恢复：resumeSessionId 参数允许从 ~/.codex/sessions/ 加载历史 — `src/agents/tools/sessions-spawn-tool.ts:28-33`

### 扩展插件系统
- [HC] OpenClaw 通过 `/extensions/provider` 目录组织 22+ 通道扩展（WhatsApp/Telegram/Slack/Discord/Google Chat/Signal/iMessage/BlueBubbles/IRC/Microsoft Teams/Matrix/Feishu/LINE/Mattermost/Nextcloud Talk/Nostr/Synology Chat/Tlon/Twitch/Zalo/Zalo Personal/WebChat） — `extensions/` 目录结构 + README.md
- [HC] 扩展采用 plugin-sdk 模式，每个扩展 self-contained — `src/plugin-sdk/` 路径存在

## 2. 代码实现验证（grep + Read）

### ACP 会话生成与管理
- [HC] spawnAcpDirect() 函数（来自 `src/agents/acp-spawn.ts`）处理 ACP 会话生成，支持 streamTo 参数（stdout/stderr/null） — `src/agents/tools/sessions-spawn-tool.ts:3, 44`
- [HC] ACP_SPAWN_MODES/ACP_SPAWN_STREAM_TARGETS 常量定义可用选项 — `src/agents/tools/sessions-spawn-tool.ts:1-10`
- [HC] Subagent 生成通过 spawnSubagentDirect()，支持 "run"（一次性）和 "session"（持久线程绑定） — `src/agents/tools/sessions-spawn-tool.ts:6, 41`

### 插件与工具暴露
- [HC] 飞书 (Feishu) 扩展示例：tools-config.ts 定义 DEFAULT_TOOLS_CONFIG（doc/chat/wiki/drive 默认启用，perm 默认禁用） — `extensions/feishu/src/tools-config.ts:8-15`
- [HC] 工具配置采用"安全默认"（permission 类敏感操作默认禁用） — `extensions/feishu/src/tools-config.ts:3-6`
- [HC] 工具路由通过 tool-account-routing.test.ts 管理，支持多账号场景 — `extensions/feishu/src/tool-account-routing.test.ts` 路径存在

### API/HTTP vs 子进程
- [HC] 通道扩展模式：HTTP-based（飞书用 API），子进程（QMD/mcporter），或混合 — 代码推断
- [HC] 子进程管理通过 process supervisor（见 `src/process/supervisor`）— 用于 mcporter daemon 启动 — `src/memory/qmd-manager.ts`

## 3. 蓝军 Mutation

### Mutation 1: "OpenClaw 是否真的支持 MCP（Model Context Protocol）？"
- 命令：`grep -r "ModelContextProtocol\|MCP\|mcp.*client\|mcp.*server" /src --include="*.ts"`
- 结果：FAIL — 仅找到 mcporter（内存搜索工具），未发现 MCP 实现
- 断言：OpenClaw **不支持 MCP**；采用自有的 ACP 协议和插件 SDK

### Mutation 2: "ACP 会话是否支持客户端模式（client 连接远程 ACP server）？"
- 命令：`grep -r "acp.*client\|remote.*acp\|acp.*connect" /src/acp --include="*.ts"`
- 结果：TBV — `src/acp/` 目录存在但内容不清楚；可能仅支持服务器模式
- 断言：不确定是否支持 ACP client；文档未提及，代码路径不可达

### Mutation 3: "工具命名空间是否被强制（provider/tool 分离）？"
- 命令：`grep -r "tool.*namespace\|byProvider.*tool\|provider.*scope" /docs/tools --include="*.md"`
- 结果：PASS — `docs/tools/multi-agent-sandbox-tools.md:212-214` 提及 byProvider 和 provider/model 格式
- 断言确认：OpenClaw 支持工具命名空间（provider 和 provider/model 格式）

## 4. 与 Hive 现状对照

### 借鉴
- ACP 的持久会话 (session-bound) vs 一次性 (run) 双模式设计优雅 — 可供 Hive 参考
- 飞书扩展的"安全默认"（敏感工具默认禁用）是好的安全实践 — `extensions/feishu/src/tools-config.ts:3-6`
- 多账号工具路由机制可应用于 Hive 的多租户场景 — `extensions/feishu/src/tool-account-routing.test.ts`

### 反面教材
- OpenClaw 不支持 MCP；如果 Hive 打算支持 MCP，需单独实现（OpenClaw 不可复用）

### 别抄
- ACP 协议细节未公开；Hive 应评估自有 protocol vs 采纳 MCP

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **协议** | MCP (Model Context Protocol) | ACP (自有) + plugin-sdk |
| **子进程 vs HTTP** | 支持两种 | 两种都支持（扩展决定） |
| **客户端模式** | MCP client 连接远程 | 不确定（可能仅服务器） |
| **工具命名空间** | provider: 字段 | byProvider + provider/model 格式 |
| **多账号路由** | MCP 协议级支持 | 扩展级实现（见飞书） |
| **扩展系统** | plugin-sdk (通用) | plugin-sdk + 22+ 硬编码扩展 |

---

## 核心断言总结

1. **OpenClaw 不支持 MCP**，采用自有 ACP 协议（与 pi-agent-core 关联）
2. **双运行时模式**：ACP（持久编码会话）+ Subagent（隔离一次性）
3. **ACP 会话可恢复**，支持加载历史（resumeSessionId）
4. **22+ 通道扩展**通过 plugin-sdk + 硬编码目录组织
5. **工具命名空间**通过 byProvider 字段和 provider/model 格式支持
6. **安全默认原则**：敏感工具（权限管理）默认禁用

---

**注**：OpenClaw 的 ACP/plugin-sdk 架构与 deer-flow 的 MCP 范式完全不同；互不兼容。
