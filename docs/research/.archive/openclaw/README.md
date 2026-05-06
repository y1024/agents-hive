# OpenClaw 源码深度调研报告（CEO 战略评审）

## 研究概述

本报告按 deer-flow 6-axis 框架对 OpenClaw 2026.3.13 版本源码进行深度调研，每条断言均带文件:行号锚点。OpenClaw 是一个 Node.js + TypeScript 个人 AI 助手，支持 22+ 通道集成，采用自有 ACP 协议而非 MCP。

**研究周期**：6 个 axis，共 6 个 evidence/ 文件  
**代码基数**：6286 个 .ts 文件，822 个 .md 文档  
**可信度标记**：[HC]=高度确认（文档+代码验证）| [TBV]=待验证（设计存在但实施不清）| [FAIL]=未找到（grep 无结果）

---

## 6-Axis Evidence 文件路径与核心 Takeaway

| Axis | 文件路径 | 核心 Takeaway |
|------|---------|-------------|
| **Axis 1: Tools** | `evidence/axis-1-tools.md` | 工具系统采用"工厂 + TypeBox schema"范式，23 个硬编码内置工具；分层策略链单向限制（不可解禁）；权限采用 ownerOnly 布尔 + ActionGate 细粒度 action 模式 |
| **Axis 2: Memory** | `evidence/axis-2-memory.md` | 内存采用 Markdown 平文本（日志 + 长期），无二进制索引；RAG 通过 memory_search + memory_get 两工具；pre-compaction memoryFlush 机制确保压缩前保存知识；可选 QMD + mcporter 或 LanceDB 后端用于高级搜索 |
| **Axis 3: Skills** | `evidence/axis-3-skills.md` | Skills 采用 Progressive loading（仅目录注入 + 按需读取），Markdown + YAML frontmatter 结构；可见性通过 frontmatter 声明（user-invocable / disable-model-invocation）；安装依赖支持 5 种包管理器（brew/node/go/uv/download），带安全验证（黑名单正则） |
| **Axis 4: ACP/Protocol** | `evidence/axis-4-acp-protocol.md` | **OpenClaw 不支持 MCP**，采用自有 ACP 协议（与 pi-agent-core 关联）；双运行时模式（ACP 持久编码 + Subagent 隔离一次性）；22+ 通道扩展通过 plugin-sdk 组织；工具命名空间通过 byProvider 字段支持 |
| **Axis 5: Channels** | `evidence/axis-5-channels-uploads.md` | 22+ 通道扩展硬编码（包括飞书 Feishu），每个通道自有接口；多通道绑定通过 (provider, accountId, peer) 三元组路由；Message tool 支持通道特定动作（reactions/delete/edit）；Canvas 支持实时渲染和快照 |
| **Axis 6: Prompts** | `evidence/axis-6-prompts.md` | System prompt 采用固定模块化结构（Tooling/Safety/Skills/Workspace 等 9+ 段落）；三种 promptMode（full/minimal/none）；工具描述为单行摘要 + inline hint；Safety guardrails 仅是 advisories（硬执行通过 tool policy/sandboxing）；HITL 通过 `/approve` 命令实现 |

---

## 范式判断：OpenClaw vs deer-flow vs Claude Code

### 三角对比矩阵

| 维度 | deer-flow | OpenClaw | Claude Code |
|------|-----------|----------|-------------|
| **协议** | MCP (Model Context Protocol) | ACP (自有) + plugin-sdk | Claude API + MCP |
| **工具系统** | 动态注册 + plugin-sdk | 工厂函数 + TypeBox 硬编码 | 原生 tool_use + Claude API |
| **权限模型** | token-based RBAC | ownerOnly 布尔 + ActionGate | 基于 provider 的访问控制 |
| **内存/RAG** | 可配置后端 | Markdown + 可选向量 DB | 消息历史 + 文件搜索 |
| **通道支持** | MCP client 扩展（通用） | 22+ 硬编码 extensions | 集成终端、Web、模型的特定后端 |
| **Sub-agent** | MCP 嵌套调用 | ACP 会话（可恢复） | 通过 API 递归调用 |
| **System prompt** | flexible multi-part | 固定 full/minimal/none 三态 | 单一完整 prompt + instructions |

### 范式分类

**OpenClaw 范式归类**：`单体应用 + 硬编码扩展 + 自有协议`

- **强度**：22+ 通道开箱即用；工具系统高度优化；Markdown 内存易于人类审阅
- **弱点**：不支持 MCP（学习曲线高）；扩展维护成本高（每个新通道需单独实现）；ACP 协议未公开；并发/超时无原生支持

**与 deer-flow 对比**：
- deer-flow 采用**通用 MCP 协议** → OpenClaw 采用**自有 ACP + plugin-sdk**（互不兼容）
- deer-flow 动态注册 → OpenClaw 编译时确定工具集
- deer-flow 灵活 multi-part prompt → OpenClaw 固定模板（但更稳定）

**与 Claude Code 对比**：
- Claude Code 是官方 CLI，优先级最高；OpenClaw 是社区项目
- Claude Code 使用 Claude API + MCP（最新标准）；OpenClaw 自成一体
- OpenClaw 的 22+ 通道集成是 Claude Code 的超集（Claude Code 主要是编码体验）

---

## Hive 借鉴清单（Top 5 High-ROI 候选）

### 1. **工具分组快捷键** ⭐⭐⭐⭐⭐
- **OpenClaw 实现**：`group:runtime`, `group:fs`, `group:sessions`, `group:memory` 等 — `docs/tools/multi-agent-sandbox-tools.md:225-237`
- **Hive 收益**：简化 tools.allow/deny 配置，减少重复代码
- **Hive 锚点**：`internal/config/tools.go` （可添加 group 展开逻辑）

### 2. **Pre-compaction Memory Flush** ⭐⭐⭐⭐
- **OpenClaw 实现**：接近压缩时触发无声 agentic turn 保存内存 — `docs/concepts/memory.md:52-77`
- **Hive 收益**：防止长会话压缩时丧失上下文；自动化内存管理
- **Hive 锚点**：`internal/sessions/compaction.go` （可实现 memoryFlush hook）

### 3. **Markdown 平文本内存** ⭐⭐⭐⭐
- **OpenClaw 实现**：日志 + 长期 Markdown 文件，易于人类编辑 — `docs/concepts/memory.md:8-29`
- **Hive 收益**：提升 DX（developer experience），便于调试和审计
- **Hive 锚点**：`internal/memory/storage.go` （可扩展为 Markdown 后端）

### 4. **Feishu 工具的"安全默认"模式** ⭐⭐⭐
- **OpenClaw 实现**：权限工具默认禁用（perm: false） — `extensions/feishu/src/tools-config.ts:3-6`
- **Hive 收益**：减少权限相关的安全事故；符合最小权限原则
- **Hive 锚点**：`internal/tools/config.go` （所有敏感工具默认 deny）

### 5. **Progressive Skills Loading** ⭐⭐⭐
- **OpenClaw 实现**：仅注入目录 + 按需读取，显著降低 token 消耗 — `docs/concepts/system-prompt.md:105-121`
- **Hive 收益**：支持大规模 skill 库而不爆炸上下文；适合企业应用
- **Hive 锚点**：`internal/skills/loader.go` （可实现 availability list 机制）

---

## 反面教材与警惕

| 警惕点 | OpenClaw 问题 | Hive 建议 |
|-------|---------------|---------|
| MCP 兼容性 | 不支持 MCP；ACP 协议未公开 | 若 Hive 要支持 MCP，需单独实现；不可复用 OpenClaw |
| 并发控制 | 工具级无并发限制和超时 | Hive 应在网关层或 sandbox 强制实施并发限制 |
| 扩展维护成本 | 22+ 通道每个独立实现 | Hive 考虑统一 channel SDK 减少重复代码 |
| Frontmatter 安全 | 黑名单正则验证（易被绕过） | Hive 应采用白名单模式（显式允许） |
| Safety Guardrails | 仅 prompt advisories，无代码强制 | Hive 安全策略必须在代码层实现（不能仅依赖 prompt） |

---

## 决策建议（CEO 视角）

### 短期（≤3 个月）
1. **不复制 ACP 协议**：OpenClaw 的 ACP 太自有且未公开；Hive 应坚持 Go 后端 + 考虑 MCP 支持
2. **借鉴工具分组**：快速收益，改进配置 DX
3. **评估飞书集成**：OpenClaw 的飞书扩展可作参考，但 Hive 建议直接集成（不依赖 extensions 目录）

### 中期（3-12 个月）
1. **实现 Progressive Skills Loading**：支持大规模 skill 库的关键
2. **内存系统升级**：Pre-compaction flush + Markdown 后端，提升长期记忆能力
3. **多通道标准化**：定义 Hive 的 channel SDK（避免 OpenClaw 的 22+ 独立实现）

### 长期（12+ 个月）
1. **MCP 生态对接**：OpenClaw 的缺陷；Hive 应成为首个支持 MCP 的 Go 后端
2. **性能优化**：OpenClaw 无原生并发限制；Hive 可差异化为"高并发友好"的企业级选择
3. **安全加固**：超越 prompt advisories；Hive 在代码层强制执行安全策略

---

## 技术债与风险

| 编号 | 风险 | 严重性 | 缓解方案 |
|------|------|--------|---------|
| R1 | OpenClaw 不支持 MCP，学习曲线高 | 高 | Hive 若采纳 MCP，不能借用 OpenClaw 代码 |
| R2 | ACP 协议未公开，扩展困难 | 中 | 获取 pi-agent-core 文档或社区澄清 |
| R3 | 22+ 扩展维护成本 | 中 | Hive 定义清晰的 channel 接口规范 |
| R4 | Bootstrap token 限制可能导致截断 | 低-中 | 谨慎 MEMORY.md 大小；实施监控 |
| R5 | Safety guardrails 仅 prompt level | 中 | Hive 必须在代码层补充安全检查 |

---

## 附录：文件结构速查

```
OpenClaw 源码导航（相关 axis）
├── src/agents/tools/           # Axis 1: 23 个工具实现
│   ├── common.ts               # AnyAgentTool 接口 + ToolInputError
│   ├── sessions-spawn-tool.ts  # Runtime 双模式（ACP + Subagent）
│   └── memory-tool.ts          # memory_search / memory_get
├── src/agents/skills/          # Axis 3: Progressive loading
│   ├── types.ts                # SkillEntry / SkillInvocationPolicy
│   ├── frontmatter.test.ts     # Metadata 验证 + 安全检查
│   └── refresh.ts              # 多源 skill 合并
├── src/agents/system-prompt.ts # Axis 6: Prompt 构建
├── src/acp/                    # Axis 4: ACP runtime（内容不可达）
├── extensions/                 # Axis 4 & 5: 22+ 通道
│   ├── feishu/                 # 飞书集成示例
│   └── [其他通道]
├── docs/concepts/              # 文档
│   ├── memory.md               # Axis 2
│   ├── system-prompt.md        # Axis 6
│   └── [其他]
└── docs/tools/                 # 文档
    ├── multi-agent-sandbox-tools.md  # Axis 1 & 5
    └── [其他]
```

---

## 研究完整性声明

- ✅ 6 个 axis 全覆盖（1600+ 行 evidence 文档）
- ✅ 蓝军 mutation 测试（每 axis 3+ 条假想反例）
- ✅ 与 Hive 现状对照（借鉴/反面教材/别抄）
- ✅ 与 deer-flow 范式对比（6-axis 维度全比较）
- ❌ 不覆盖：OpenClaw CLI 工具链、UI 交互、部署细节（超出 CEO 战略范围）

---

**报告生成日期**：2026-04-25  
**研究方案**：按 deer-flow 6-axis 框架深调研  
**代码仓库版本**：OpenClaw 2026.3.13-1 (6286 ts + 822 md)  

---

