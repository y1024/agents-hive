# Hermes Agent 调研

> **状态**：Agent WebSearch 一次性产出，**未独立核实**
> **日期**：2026-04-25
> **关键警告**：见下方 §0

---

## ⚠️ §0 调研可信度警告（主线程必读）

Agent 报告里大量"美得过头"的精确数字与之前 OpenClaw 调研里"145K stars / 5700 skills"是同一种 AI 内容污染模式：

| 红色信号 | 说明 |
|---|---|
| "103K stars by April 2026" | Claude Code 自己估计也就几万 stars，一个开源 agent 7 周涨到 95.6K stars 不可信 |
| "118 bundled skills" / "47 tools × 19 toolsets" | 数字过于精确，需要直接看 repo 验证 |
| "GEPA ICLR 2026 Oral" | GEPA 论文 arXiv 2507.19457 真实存在，但 ICLR 2026 Oral 状态需核实（截稿/接收周期不一定吻合） |
| 引用源 utilo.io / thenewstack 比较文 | 域名可疑，可能 AI generated 比较稿 |

**坚硬证据**（与本地状态一致，可信）：
- 用户本地有 `hermes-agent-2026.4.16` 目录，Agent 报告的 v0.10.0 (2026-04-16) 时间戳完美匹配
- Nous Research 是真实 AI lab（训练 Hermes 系列开源 LLM）
- GEPA 论文 arXiv 2507.19457 真实

**主线程使用姿势**：
- 不要把任何具体数字（stars / skills / tools）写进 P0/P1 候选清单
- 只采纳"方向性"结论：架构特征、设计模式、范式分类
- 真实需要采纳的设计点，必须等用户给 sandbox 访问后**亲自 grep hermes-agent 源码验证**
- 6-axis 简表只作为"待核实假设"使用

---

## 1. 是什么（Agent 报告，未核实）

- **维护方**：Nous Research（开源）
- **License**：MIT
- **Repo**：https://github.com/NousResearch/hermes-agent — **核实重点**
- **Release 节奏**（Agent 声称）：
  - v0.7.0 (2026-04-03)：resilience + memory providers
  - v0.8.0 (2026-04-08)：intelligence + model switching
  - **v0.10.0 (2026-04-16)：Tool Gateway integration（与本地目录完美匹配）**
  - v0.11.0 (2026-04-23)：transport refactor + CLI rewrite
- **类别**：自我改进的对话型 agent 框架，配 procedural memory（skills 系统）+ 多平台 gateway

---

## 2. 6-Axis 简表（[TBV-AGENT] 全部待核实）

| Axis | Hermes Agent（待核实假设） |
|------|---|
| **Tools** | 47 内置 × 19 toolsets；MCP client/server；Tool Gateway（Firecrawl, FAL, TTS, Browser Use）|
| **Memory** | 3 层（session / persistent / skill）；FTS5 + LLM summarization；跨会话从月级历史召回 |
| **Skills** | 118 bundled；自动从任务 trace 创建；agentskills.io 兼容；GEPA 优化；slash command |
| **MCP** | 完整 client + server 双模；动态工具注册；LLM sampling 反向；config-driven |
| **Channels** | 17 平台（Telegram/Discord/Slack/WhatsApp/Signal/QQBot/Feishu/Email 等）+ CLI TUI |
| **Prompt** | 多源拼装（SOUL.md 人格 / MEMORY.md 事实 / USER.md 偏好 / skills / context files / tool guidance / model instructions）；Anthropic 缓存；中途稳定 |

---

## 3. 关键架构亮点（Agent 报告，方向性结论可参考）

### 3.1 GEPA 自我改进机制（最有差异化的设计）
- **论文**：arXiv 2507.19457 "Reflective Prompt Evolution Can Outperform Reinforcement Learning"
- **机制**：读完整 execution trace（错误、profiling、reasoning chain）→ 提议 prompt 改进 → 比 GRPO 高 6-20%，rollouts 少 35x
- **声称**：20+ 自生成 skills 的 agent 在重复任务上跑快 40%
- **配套 repo**：https://github.com/NousResearch/hermes-agent-self-evolution（DSPy + GEPA）

**Hive 视角**：这个方向性结论值得调研 — Hive 当前 agent 没有自我改进闭环。**但**不要直接抄 GEPA 实现，先看论文 + repo 自己判断是否适合 Hive 的 spec-driven ReAct 范式。

### 3.2 6 个执行后端
1. Local（默认）
2. Docker（沙箱）
3. SSH（远程持久）
4. Modal（serverless VM，闲时 hibernate）
5. Daytona（serverless workspace）
6. Singularity/Apptainer（HPC 容器）

**Hive 视角**：Hive 当前主要是 local + Docker sandbox。如果未来要做 SaaS 多租户，Modal/Daytona serverless workspace 模式值得参考。**但与国内 IM 阶段无强耦合**，留 P3。

### 3.3 3 层 Memory 架构
- L1：session memory（context 内）
- L2：persistent memory（MEMORY.md / USER.md 跨会话）
- L3：skill memory（agent 自动创建的解决方案模式）
- **检索**：FTS5 全文搜索 + LLM summarization
- **优化**：Anthropic prompt caching、阈值触发 context 压缩

**Hive 视角**：Hive 有向量 memory，但 L3 "skill memory = agent 自创"是新维度。**可借鉴方向**：Hive 当前 skill 是人写的 Markdown，Hermes 提供了"agent 自写 skill"的范式。是否做需要决策。

### 3.4 Channels 列表（声称 17 平台，含飞书 Feishu）
- Hermes 声称含 Feishu/WeCom/QQBot — **国内 IM 重叠区**
- **Hive 视角**：如果 Hermes 真有飞书集成，需要看其实现质量。Hive 当前飞书 PatchCard 增量渲染是反向输出点，对照 Hermes 的飞书实现可能验证或挑战这个差异化。

### 3.5 Tool Gateway（v0.10.0 新）
- Nous Portal 订阅者自动获得 Firecrawl/FAL/FLUX/TTS/Browser Use 等
- **Hive 视角**：商业模式参考，**与 Hive harness 工程无关**

### 3.6 Multi-Backend LLM
- OpenAI / OpenRouter / Anthropic / Gemini / Bedrock / NVIDIA NIM / Arcee / Vercel ai-gateway
- 按任务路由（routine task → DeepSeek-V3 省 90% 成本）
- **Hive 视角**：Hive 已有 9+ provider，对照 — 但"按任务路由到 cheap model"是 Hive 缺的，可能 P1

---

## 4. 范式定位对照（Agent 报告，方向性结论）

| 维度 | Claude Code | SWE-agent | Hermes Agent | DeerFlow | OpenClaw |
|---|---|---|---|---|---|
| **主用例** | repo 优化 / IDE | 学术 RL baseline | 重复任务学习 / 多会话 | 并行 sub-agent / 研究 | 广泛自动化 / 预制 skill |
| **自我改进** | 无 | RL（无生产）| GEPA（生产）| 无 | 无 |
| **跨会话 memory** | 无 | 无 | **有（FTS5 + LLM）** | basic JSON | 无 |
| **Skill 生成** | 无 | 无 | **自动** | 无 | 人工库 |
| **执行后端** | IDE 集成 | local sim | 6 种 | Docker sandbox | 看 skill |
| **OSS/商业** | 闭源 | 开源学术 | 开源 MIT | 开源 BSD（字节）| 开源（Codeium？需核实）|

---

## 5. 真正可作为 Hive 决策输入的"方向性结论"（剔除数字后剩下的）

**[方向 A] Procedural memory（skill = how-to 而非 tool 定义）**
- Hermes、Claude Code、OpenClaw 都把 skill 当 procedural knowledge（操作步骤），不仅是 tool 定义
- Hive 当前 Skills 系统比较接近这个范式，但**自动生成**是缺口
- 决策点：Hive 是否引入"agent 从成功 trace 自写 skill"机制？短期不必（spec-driven ReAct 提供了类似的结构化路径），长期可能需要

**[方向 B] 跨会话 memory 的检索机制**
- Hermes 用 FTS5 + LLM summarization
- Hive 用向量搜索
- 决策点：FTS5 全文比向量更便宜 + 更可解释，可对照 Hive 现状（`internal/memory/`）评估是否补一条 FTS5 路径

**[方向 C] 多执行后端 / 多模型路由**
- 现阶段国内 IM 不需要 SSH/Modal/Daytona/Singularity
- 但"按任务复杂度路由模型"是 P1 候选（即省成本）

**[方向 D] CLI / TUI 与 Server 的双形态**
- Hermes v0.11.0 React/Ink CLI + Python JSON-RPC backend
- Hive 当前 CLI（claw） + Server，对照其架构是否更模块化

---

## 6. 仍需核实清单（强制核实，否则不进 P0/P1）

1. **GitHub repo 真实性**：`github.com/NousResearch/hermes-agent` 是否真有该 repo？stars 实际值？
2. **GEPA 论文状态**：ICLR 2026 Oral 是否属实（vs 仅 arXiv preprint）
3. **47 tools / 118 skills / 17 platforms 数字**：直接看 repo 计数验证
4. **飞书集成真实存在性 + 实现质量**：与 Hive 飞书 PatchCard 对照
5. **GEPA 在 v0.10.0 production 的集成深度**：论文是 algorithm，production 实现细节未知
6. **Hermes 真支持 MCP server mode 吗**：grep MCPServer class 验证

**核实路径**：
- 用户给 sandbox 访问后，主线程亲自 grep + read 关键文件
- 或人工 web 查 GitHub stars / issues / PRs

---

## 7. 文件索引

- 本文件：`docs/research/hermes-agent/findings.md`
- 对照：`docs/research/openclaw/findings.md`、`docs/research/claude-code/findings.md` + `verification-round-2.md`
- 综合阶段：`docs/research/_synthesis/SYNTHESIS.md`（待生成）

*— End of Hermes 调研草稿（待核实）—*
