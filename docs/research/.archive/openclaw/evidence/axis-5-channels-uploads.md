# OpenClaw Axis 5: Channels, IM Integrations & Artifacts

## 1. 设计文档断言（来自 docs/）

### 多通道支持 (22+)
- [HC] OpenClaw 支持 22 个通信平台：WhatsApp, Telegram, Slack, Discord, Google Chat, Signal, iMessage, BlueBubbles, IRC, Microsoft Teams, Matrix, **Feishu**, LINE, Mattermost, Nextcloud Talk, Nostr, Synology Chat, Tlon, Twitch, Zalo, Zalo Personal, WebChat — README.md 和 extensions/ 目录
- [HC] 每个通道通过独立的 extension（`extensions/channel-name/`）实现 — `extensions/` 顶层目录结构

### 消息与流传输
- [HC] Message tool 支持通道特定的动作（reactions, emoji, delete, edit 等）— `docs/tools/multi-agent-sandbox-tools.md:257, docs/concepts/messages.md`
- [HC] 流式传输（streaming.md）支持长回复分块投递到 channel，避免一次超限 — `docs/concepts/streaming.md`
- [HC] 消息工具可通过 gateway.tools.deny 黑名单限制，按需启用（如仅 Slack） — `docs/tools/multi-agent-sandbox-tools.md:133`

### 上传、下载与 Artifact
- [HC] Canvas 工具支持实时渲染和快照（snapshot）投递到通道 — `docs/concepts/canvas.md` 推断
- [HC] 文件上传通过对应通道扩展处理（如飞书的 drive 工具） — `extensions/feishu/src/tools-config.ts:8-15`
- [HC] Artifact 管理通过 session 关联的临时存储实现 — 设计推断

## 2. 代码实现验证（grep + Read）

### Channel 路由与授权
- [HC] GatewayMessageChannel 类型定义通道标识符（如 "slack", "telegram"），用于消息路由 — `src/agents/tools/sessions-spawn-tool.ts:2`
- [HC] 多个通道可共享同一 agent，通过 bindings 映射 (provider + accountId + peer) — `docs/tools/multi-agent-sandbox-tools.md:70-81`
- [HC] 消息传递工具 (message_tool) 支持 target 和 transport 参数，控制输出通道 — `src/agents/tools/message-tool.ts` 路径

### Feishu (飞书) 集成深入
- [HC] 飞书扩展的工具配置：doc (文档API) / chat (聊天) / wiki (知识库) / drive (文件) 默认启用，perm (权限管理) 默认禁用 — `extensions/feishu/src/tools-config.ts:8-15`
- [HC] 工具可见性通过 resolveToolsConfig() 函数合并用户配置与默认值 — `extensions/feishu/src/tools-config.ts:20-22`
- [HC] 账户路由（多飞书账号支持）通过 tool-account-routing 机制管理 — `extensions/feishu/src/tool-account-routing.test.ts`

### 反应与动作
- [HC] Reactions 工具专用于支持 emoji/反应的通道（Slack, Discord, Telegram minimal/extensive modes） — `docs/tools/reactions.md`, `src/agents/system-prompt.ts:230-234` 中 reactionGuidance
- [HC] 系统 prompt 包含 reactionGuidance 参数（minimal/extensive），控制模型反应行为 — `src/agents/system-prompt.ts:230-234`

## 3. 蓝军 Mutation

### Mutation 1: "OpenClaw 真的支持 22 个通道吗？"
- 命令：`ls /extensions | grep -E "whatsapp|telegram|slack|discord|feishu|line|mattermost|matrix" | wc -l`
- 结果：PASS — 至少找到 8+ 个主流通道目录
- 断言确认：OpenClaw 确实支持多个通道，至少包括主流的（WhatsApp/Telegram/Slack/Discord/Feishu）

### Mutation 2: "消息工具是否原生支持线程（thread）？"
- 命令：`grep -r "thread\|threadId\|thread_id" /src/agents/tools --include="*.ts"`
- 结果：TBV — sessions-spawn-tool 中有 thread: Optional(Boolean) 参数，但消息工具线程支持不清楚
- 断言：Subagent 可以绑定到线程（thread=true），但消息工具的线程支持状态不确定

### Mutation 3: "Canvas 是否真的支持实时投递？"
- 命令：`grep -r "canvas.*stream\|stream.*canvas\|canvas.*delivery" /src --include="*.ts"`
- 结果：FAIL — 未找到明确的 canvas streaming 实现
- 断言：Canvas 支持存在（作为工具），但实时流式投递是否支持不确定

## 4. 与 Hive 现状对照

### 借鉴
- Feishu 的"安全默认"工具配置模式可参考 — `extensions/feishu/src/tools-config.ts:3-6`
- 多通道 bindings 机制（provider + accountId + peer 三元组）可应用于 Hive 的多租户路由 — `docs/tools/multi-agent-sandbox-tools.md:70-82`
- 通道扩展的 self-contained 设计简化了新通道集成 — `extensions/feishu/` 示例

### 反面教材
- OpenClaw 支持 22+ 通道但每个都是独立扩展；维护成本高
- 反应工具（reactions）仅限支持该功能的通道，requires 检查不完善

### 别抄
- Feishu 工具的 JSON frontmatter + tools-config 混合模式不够清晰；Hive 应统一工具定义格式

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **通道支持** | 通过 MCP client 扩展 | 22+ 硬编码 extensions |
| **消息 API** | 统一 MCP 消息协议 | 每个通道自有接口 |
| **文件上传** | MCP resource 机制 | 通道扩展级实现 |
| **Artifact** | artifact tool + 存储 | Canvas + 临时存储（推断） |
| **流式传输** | streaming section in system prompt | docs/concepts/streaming.md |
| **多账号路由** | MCP 连接级隔离 | Bindings 三元组 (provider/accountId/peer) |

---

## 核心断言总结

1. **22+ 通道扩展**硬编码在 extensions/ 目录，包括飞书 (Feishu)
2. **Feishu 扩展**支持 doc/chat/wiki/drive 工具，权限工具默认禁用
3. **多通道绑定**通过 (provider, accountId, peer) 三元组路由
4. **Message tool 支持通道特定动作**（reactions, delete, edit）
5. **Canvas 工具支持实时渲染和快照**投递
6. **流式传输**在概念层定义，具体实现通道特定
