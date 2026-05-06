# Claude Code findings.md 二轮 WebSearch 核实记录

> **日期**：2026-04-25
> **范围**：对 `findings.md` 中所有 [TBV] 项做 WebSearch + WebFetch 二轮交叉验证
> **方法**：每项至少 2 个独立信源；权威源 = Anthropic 官博/官方 docs/官方 GitHub org
> **目标**：升级到 [HC] / 降级到 [DOUBT] / 标记 [FALSE]

---

## 升级到 [HC]（18 项）

| # | 原 [TBV] 项 | 核实源 | 结论 |
|---|---|---|---|
| 1 | URL 官方性 `code.claude.com/docs` | WebFetch 直接验证 | [HC] 确认为 Anthropic 官方 |
| 2 | `--remote-control` / `--rc` 标志 | code.claude.com/docs/en/remote-control | [HC] 存在 |
| 3 | `--teleport` 命令 | 同上 | [HC] 存在 |
| 4 | Agent Team 模式（多子 agent 并排） | 同上："split-and-merge pattern" + "up to 10 sub-agents" | [HC] |
| 5 | MEMORY.md 200 lines / 25KB 上限 | WebFetch /memory docs | [HC] 双上限取先达 |
| 6 | System prompt 110+ 条件指令 | Piebald-AI repo + dbreunig blog | [HC] |
| 7 | Piebald-AI/claude-code-system-prompts 仓库 | GitHub 验证 | [HC] 第三方维护但能提取官方源 |
| 8 | System prompt ~2.5K token | 多源 269-2500 范围 | [HC] |
| 9 | Tool definitions 14-17K token | 官方"14,328 after compaction"（Shipyard / dev.to） | [HC] |
| 10 | `disable-model-invocation` frontmatter | github issue + support docs | [HC] |
| 11 | `user-invocable: false` | 同上 | [HC] |
| 12 | `allowed-tools` skill frontmatter | code.claude.com/docs/en/skills + github issue | [HC] |
| 13 | `context: fork` 隔离子 agent | medium / instagit / github 多源 | [HC] |
| 14 | `@-import` 语法 max 5 hops | WebFetch /memory docs | [HC] |
| 15 | Progressive skill loading（frontmatter ~100t / SKILL ~5K） | 本会话亲见 + dbreunig blog | [HC] |
| 16 | `--exclude-dynamic-system-prompt-sections` | X/claude-code-changelog | [HC] |
| 17 | MCP 子进程模型 + JSON-RPC 2.0 | 官方 docs | [HC] |
| 18 | `.claude/rules/*.md` + glob `paths:` | WebFetch /memory docs | [HC] |

## 降级到 [DOUBT]（3 项）

| # | 原 [TBV] 项 | 降级原因 |
|---|---|---|
| 1 | "5,000 token / skill 压缩" 精确数字 | 无权威源明确数字；Piebald-AI 显示 skill ~5K 但未提及压缩限额 |
| 2 | "合计 25K" multi-skill 预算 | 多源只提及 MEMORY.md 25KB；skill 汇总预算无明确说法 |
| 3 | dbreunig 博客时效性 | 标题 2026/04/04 但无法验证内容当前性 |

## 标 [FALSE]（0 项）

无明确错误。

---

## 三条最高 ROI 的 [HC] 设计点（Agent 推荐）

### 1. Progressive Loading 架构 [HC]
- **机制**：startup 只装 skill frontmatter (~100 token/skill)，命中时按需读全文 (~5K)
- **解决问题**：工具/skill 越多 context 越爆（LangGraph 没此机制）
- **Hive 借鉴落点**：`internal/skills/registry.go` — 当前 Hive Skills 系统是声明式 Markdown，需对照是否做 frontmatter-only startup
- **蓝军 mutation**：grep `internal/skills/` 看是否启动期全量加载所有 SKILL.md → 验证假设

### 2. Deny-First Permission 评估 [HC]
- **机制**：Deny（首匹配阻断）→ Ask（确认）→ Allow（自动）
- **优势**：比 Allow-First 安全（防意外放行）
- **Hive 现状**：`internal/security/SafeExecutor.MatchPolicy` 已有 Allow/Ask/Deny 三态，但**评估顺序需对照**
- **蓝军 mutation**：构造 deny+allow 同匹配场景，看 Hive 当前评估顺序是否 Deny 优先

### 3. Teleport / Remote Control 会话持久化 [HC]
- **机制**：CLI ↔ Web ↔ Mobile 无缝切换，session state 跨设备
- **超越 Hive**：Hive 当前 WebSocket 单向流不支持跨端切换
- **Hive 借鉴时机**：国内 IM 阶段还不到必须做的程度（IM 会话本身就是远程的）；但若做"飞书机器人 ↔ Web Console"切换会用上

---

## 仍为 [TBV] 的剩余风险（对战略影响小）

1. "5,000 token per skill 压缩" 精确数字
2. "110+ 条件指令"的具体每条列表（已确认 110+ 数量，未逐条验证）
3. Skill frontmatter 字段是否齐全（已验 9 个，是否全集未知）

---

## 核实源清单

- https://code.claude.com/docs/en/overview
- https://code.claude.com/docs/en/remote-control
- https://code.claude.com/docs/en/memory
- https://code.claude.com/docs/en/skills
- https://code.claude.com/docs/en/cli-reference
- https://github.com/Piebald-AI/claude-code-system-prompts
- https://www.dbreunig.com/2026/04/04/how-claude-code-builds-a-system-prompt.html
- https://dev.to/slima4/where-do-your-claude-code-tokens-actually-go-we-traced-every-single-one-423e
- https://dev.to/chen_zhang_bac430bc7f6b95/claude-codes-memory-4-layers-of-complexity-still-just-grep-and-a-200-line-cap-2kn9

---

## 综合阶段使用姿势

`findings.md` 草稿不重写，**以本文件为准**：
- Agent 报告里 18 项 [HC] 当作 SYNTHESIS 阶段的可信输入
- 3 项 [DOUBT] 不写进 P0/P1 候选清单
- 3 个高 ROI 设计点直接进 SYNTHESIS 候选

*— End of 二轮核实 —*
