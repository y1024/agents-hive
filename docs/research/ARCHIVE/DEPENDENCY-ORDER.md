# Hive 缺陷依赖关系排序（DAG + 施工层级）

> **输入**：`GAP-INVENTORY.md` ~104 项 / 17 维度
> **方法**：D — 按依赖关系排，先建底层（其他依赖），后建上层
> **目的**：避免返工 + 同层可并行 + 找 critical path
> **日期**：2026-04-25

---

## §1 整体 DAG（5 层 + 1 探索层）

```
┌──────────────────────────────────────────────────────────────────────┐
│ Layer 0 — 基础设施（最底层，其他全依赖）                             │
│  ├── G11.1/2  Observability 基础                                     │
│  │            数字化 check ID + per-tool metric + 完整 trace          │
│  ├── G12.4    Tool 级 timeout 统一机制（getDefault/MaxTimeout 函数化）│
│  └── G12.3    Capacity governance（spawn limit + tool concurrency cap）│
└────────────────┬─────────────────────────────────────────────────────┘
                 │
┌────────────────▼─────────────────────────────────────────────────────┐
│ Layer 1 — 结构化基础（工具级和 prompt 级容器）                        │
│  ├── G2.5     工具结构化目录（每工具子目录:tool.go+prompt+sec+UI）    │
│  ├── G2.4     关注点分离（warning ≠ permission ≠ attack defense）     │
│  └── 扩展现有 master.BroadcastMessage 加 todos 类型                   │
│     （**RE-REVIEW**：复用 internal/channel/plugin.go EventRenderer， │
│       不新建 ChannelAdapter interface — 飞书已是完整实现 17K 行）     │
└────────┬───────────────┬───────────────┬─────────────────────────────┘
         │               │               │
┌────────▼─────────┐  ┌──▼────────────┐ ┌▼──────────────────────────┐
│ Layer 2A         │  │ Layer 2B      │ │ Layer 2C                  │
│ BashTool 工程化  │  │ Todos UI      │ │ Permission 模型升级        │
│  (用户硬约束)    │  │ (用户硬约束)   │ │                           │
│                  │  │               │ │                           │
│ G2.1 防御 6 层   │  │ G13.2 飞书    │ │ G10.1 Deny-First 评估顺序 │
│ G2.2 攻击 vector │  │      PatchCard│ │ G10.2 8 层过滤策略级联    │
│ G2.3 Zsh 防御    │  │ G13.3 Web     │ │ G10.3 /approve 三态       │
│ G2.6 reasoning   │  │      Console  │ │ G10.4 Modal execution     │
│ G2.7 def-in-depth│  │ G13.1 markdown│ │ G10.5 group:* 快捷键      │
│ G3.1 工具级 prompt │  artifact 显式 │ │ G10.6 path-scoped rules   │
│                  │  │               │ │                           │
│ → G12.1 red team │  │ → IM + Web    │ │                           │
│   mutation test  │  │   端到端可见  │ │                           │
└──────────────────┘  └───────────────┘ └───────────────────────────┘
         │               │               │
         └───────┬───────┴───────────────┘
                 │
┌────────────────▼─────────────────────────────────────────────────────┐
│ Layer 3 — 系统能力扩展（依赖 Layer 0-2）                              │
│  ├── G4    Memory 治理（pre-compaction flush + daily log + nightly distill）│
│  ├── G5    Skills 重构（progressive loading + mcpSkillBuilders）       │
│  ├── G6    MCP 生态（collapse 分类 / mcporter skill / chrome-mcp）     │
│  └── G13   Spec-driven 大重构（接 Layer 2B todos UI + propose→apply→archive）│
│            （需要 OpenSpec 真意：artifact 显式 + AI 质量 measurable）  │
└────────────────┬─────────────────────────────────────────────────────┘
                 │
┌────────────────▼─────────────────────────────────────────────────────┐
│ Layer 4 — 高级能力（依赖 Layer 3）                                    │
│  ├── G1    工具广度补齐（Task×6 / Team×2 / Plan×2 / Worktree×2 / etc）│
│  │         （需要 Layer 1 G2.5 结构化目录 + Layer 2A 防御深度模式）   │
│  ├── G7    ACP 生态（backend runtime + client + ACP↔MCP bridge）      │
│  ├── G8    Multi-agent 协调（Coordinator Mode + 工人工具集）           │
│  │         **F16 修复**：拆 W15.1（本地 in-process，仅依赖 G1）       │
│  │         + W15.2（跨进程 / 远程，依赖 G7 ACP）                       │
│  ├── G15   Smart Model Routing（按任务复杂度路由）                     │
│  │         （需要 Layer 0 observability 算 task complexity）           │
│  └── G16   Distillation 优化（auxiliary model + tool output pruning）  │
│            （需要 Layer 3 G4.4 structured summary）                    │
└────────────────┬─────────────────────────────────────────────────────┘
                 │
┌────────────────▼─────────────────────────────────────────────────────┐
│ Layer 5 — 创新探索（最远期）                                          │
│  └── G14   Self-improvement（GEPA reflection / trajectory / insights）│
│            （需要 Layer 0 trajectory 数据基础 + Layer 4 model routing）│
└──────────────────────────────────────────────────────────────────────┘
```

---

## §2 Critical Path（最长依赖链）

```
Layer 0 → Layer 1 → Layer 2A BashTool → Layer 4 G1 工具广度 → Layer 4 G8 Multi-agent → Layer 5 G14 GEPA
```

**critical path 估算工期**：
- L0：observability 基础 + timeout + capacity = 3 周
- L1：工具结构化目录 + 关注点分离 + todos 事件 schema = 2 周
- L2A：BashTool 工程化（6 层防御 + red team mutation）= 4 周
- L4 G1：工具广度补齐（17 个新工具，每个 1 周）= 4-6 个月分摊
- L4 G8：Coordinator Mode = 1 个 quarter
- L5 G14：GEPA = 1 个 quarter

**critical path 总长 ≈ 12-15 个月**（这是最长的串行链，并行做能压缩）

---

## §3 用户硬约束在哪一层

### 硬约束 1：todos 用户必须可见
**Layer 2B**（与 BashTool 升级同层并行）

依赖：Layer 0 observability + Layer 1 todos 事件 schema
被依赖：Layer 3 G13 Spec-driven 大重构（这是 Spec-driven 重构的前置）

### 硬约束 2：Phase 1 SafeExecutor 权限极简（已上线）
**已完成，独立保留**（不在 DAG 内，不需要重做）

---

## §4 可并行做（叶子项 / 独立项）

这些项可以**任何阶段插入**，不卡 critical path：

| 项 | 所属维度 | 工期 | 独立性 |
|---|---|---|---|
| G6.2 mcporter skill 集成 | MCP | 2-3d | 完全独立（外部 CLI 包装为 skill）|
| G3.5 Prompt redact / 脱敏 | Prompt | 1w | 独立（参考 Hermes redact.py）|
| G4.7 5-provider embedding fallback | Memory | 1w | 独立（不影响其他 memory 路径）|
| G15.2 多 LLM provider | Smart Routing | 已有 | 已具备 |
| G17.1 飞书 ErrPatchRateLimited | Channel 工程 | 已有 | 已具备 |
| G9.5 心跳触发 PATCH 重试 | Channel | 已有 | 已具备 |
| G10.5 group:* 工具组快捷键 | Permission | 1d | 几乎独立（仅依赖配置展开层）|

**这 7 项可作为"边角料"**：在 Layer 0/1/2 主线工程间隙穿插做。

---

## §5 推荐施工层级（按依赖建议的顺序）

### 阶段 1（接下来 2-4 周）：Layer 0 基础设施

**为什么先做 Layer 0**：所有上层都依赖 observability + timeout + capacity 这三块底座。如果先做 BashTool 升级或 todos UI，没有 observability 验收就靠不住，必返工。

| 项 | 工期 | 依赖 |
|---|---|---|
| G11.1 数字化 security check ID | 1 周 | 无 |
| G11.2 per-tool metric 完整暴露 | 1 周 | G11.1 |
| G12.4 Tool 级 timeout 统一（默认 + 最大可配）| 1 周 | 无（与 G11 并行）|
| G12.3 Capacity governance 配置化 | 1 周 | G11.2（metric 验证） |

**Layer 0 验收**：每个新 metric 在 Prom 端点可见 + timeout 用例测试全过 + 并发治理 mutation 测试

### 阶段 2（接下来 5-8 周）：Layer 1 + Layer 2 并行 3 条

#### 阶段 2.1：Layer 1 结构化基础（2 周）

| 项 | 工期 | 依赖 |
|---|---|---|
| G2.5 工具结构化目录改造（先选 5 个核心工具：bash/read/write/edit/web_fetch）| 1 周 | Layer 0 |
| G2.4 关注点分离层定义（destructive warning module / permission module / attack defense module 拆出） | 1 周 | Layer 0 |
| 后端 todos 事件 schema 设计 + WebSocket 事件类型新增 | 0.5 周 | Layer 0 |

#### 阶段 2.2：Layer 2A/B/C 三条并行（4 周）

**2A — BashTool 工程化**（用户硬约束 + destructive 风险）
- G2.1-G2.7 BashTool 6 层防御（destructive_warning + bypass_defense + path_validation + sed_validation + readonly_mode + reasoning 文档化 + defense-in-depth）
- G3.1 BashTool 专门 prompt（参考 Claude Code prompt.ts 369 行风格）
- G12.1 red team mutation test 100 个 attack vector 全过
- 工期：3 周（前面 ROADMAP §3 已有详细 spec）

**2B — Todos UI**（用户硬约束 + Spec-driven 重构前置）
- G13.2 飞书 PatchCard todos section 渐进 PATCH（复用 renderer.go）
- G13.3 Web Console 完整 todos 列表 + 改 + 干预
- 工期：2.5 周（前面 ROADMAP §Q2.4 已有详细 spec）

**2C — Permission 模型升级**
- G10.1 Deny-First 评估顺序核实 + 必要调整
- G10.2 8 层过滤策略级联（参考 OpenClaw `multi-agent-sandbox-tools.md:206-219`）
- G10.3 `/approve` 三态（once/always/deny）
- G10.4 Modal execution（5 modes）
- G10.5 group:* 快捷键
- G10.6 path-scoped rules glob
- 工期：3 周（独立模块多，顺序可灵活）

**Layer 2 总工期**：4 周（3 条并行）

### 阶段 3（接下来 9-16 周）：Layer 3 系统能力扩展

#### 阶段 3.1：Memory 治理（G4，3 周）
- 先做 G4.8 bootstrap caps（最简单）
- 再做 G4.6 findRelevantMemories top-N
- 再做 G4.1 + G4.2 + G4.3 三源借鉴（pre-compaction flush + daily log + nightly distill）
- 最后 G4.4 structured summary（Hermes 风格）

#### 阶段 3.2：Skills 重构（G5，2 周）
- G5.4 token budget 估算
- G5.1 Progressive loading 完整实施
- G5.2 mcpSkillBuilders MCP-as-Skills

#### 阶段 3.3：MCP 生态（G6，1 周）
- G6.1 工具结果 collapse 分类
- G6.2 mcporter skill 集成（叶子项，可任意时段做）
- G6.3 chrome-mcp 浏览器 MCP server

#### 阶段 3.4：Spec-driven 大重构（G13，3 周）
- 撤 hidden DB 路径（或降为 audit log）
- 加 markdown artifact export 层
- 接 Layer 2B todos UI 通道
- 保留 propose → apply → archive 流程
- AI 质量 measurable metric（长任务完成率 / 跑偏率）

**Layer 3 总工期**：~9 周（部分可并行，串行约 6 周）

### 阶段 4（17-30 周）：Layer 4 高级能力

#### 阶段 4.1：工具广度补齐（G1，分批 2-3 个 quarter）
- 优先补：Task×6 + Plan×2 + Worktree×2 + AskUserQuestion + ToolSearch（13 个）
- 次优：Schedule + RemoteTrigger + Sleep + REPL + Notebook + Brief + Config + SkillTool（8 个）
- 后置：Team×2 + SyntheticOutput + SendMessage（依赖 G8 Multi-agent，4 个）
- PowerShell 视需要

#### 阶段 4.2：ACP 生态（G7，1-2 个 quarter）
- G7.4 ACP↔MCP bridge（参考 OpenClaw acpx）
- G7.2 Backend Runtime
- G7.3 Client（连其他 agent 当 LLM）

#### 阶段 4.3：Multi-agent（G8，1 个 quarter）
- 依赖 Layer 4.1 G1 Team 工具 + G7 ACP

#### 阶段 4.4：Smart Model Routing（G15，1-2 周）
- 依赖 Layer 0 observability（task complexity 算法依赖 metric）

#### 阶段 4.5：Distillation 优化（G16，2 周）
- 依赖 Layer 3 G4.4 structured summary

**Layer 4 总工期**：6-9 个月（多条并行）

### 阶段 5（30+ 周）：Layer 5 创新探索

- G14 Self-improvement（GEPA / trajectory / insights / skill autonomous creation）
- 依赖 Layer 0 trajectory observability 数据 + Layer 4 model routing
- 工期：1-2 个 quarter（探索性）

---

## §6 关键依赖警告（违反 = 返工）

| 违反场景 | 后果 |
|---|---|
| 不做 Layer 0 observability，直接做 BashTool 升级 | 没有 metric 验证防御深度，靠不住，必返工 |
| 不做 Layer 1 工具结构化目录，直接做 BashTool 6 层防御 | 防御代码堆在 internal/security/ 单包，不可复制到其他工具 |
| 不做 Layer 2B todos UI，直接做 Layer 3 Spec-driven 大重构 | 大重构无前端通道，artifact 无法显式可见 = 重蹈 hidden 老路 |
| 不做 Layer 3 G4.4 structured summary，直接做 Layer 4 G16 Distillation | Distillation 无 summary 模板基础，做不出 Hermes 级别质量 |
| 不做 Layer 4 G7 ACP，直接做 G8 Multi-agent | Multi-agent 无协议层，agent 间通信 ad-hoc |
| 不做 Layer 0 metric，直接做 Layer 5 GEPA | GEPA reflection 无 trajectory 数据，无从 reflect |

---

## §7 总工期估算

| 阶段 | 工期 | 累计 |
|---|---|---|
| Layer 0 基础设施 | 2-4 周 | 1 个月 |
| Layer 1+2 工程化 + todos UI + Permission | 5-8 周 | 3 个月 |
| Layer 3 系统能力扩展 | 9 周 | 5-6 个月 |
| Layer 4 高级能力 | 6-9 个月 | 12-15 个月 |
| Layer 5 创新探索 | 1-2 个 quarter | 15-18 个月 |

**12 个月可达**：Layer 0-3 + Layer 4 一半（工具广度 + ACP）+ 第 8 项硬约束达成

**18 个月可达**：所有 Layer 4 + Layer 5 探索性（GEPA），即 8/8 验收**真正顶级**

---

## §8 接下来该做（基于 DAG）

### 立即可执行：Layer 0 工程

3 个工作流并行启动：
- **W1**：G11 Observability 基础（数字化 ID + per-tool metric）— 工期 2 周
- **W2**：G12.4 Tool 级 timeout 统一 — 工期 1 周（与 W1 并行）
- **W3**：G12.3 Capacity governance — 工期 1 周（依赖 W1 完成 30%）

3 周后 Layer 0 完成 → 启动 Layer 1+2 三条并行（BashTool / Todos UI / Permission）

### 不该做的事

- ❌ 跳过 Layer 0 直接做 BashTool（前面已警告）
- ❌ 跳过 Layer 2B todos UI 直接做 Layer 3 Spec-driven 大重构（重蹈 hidden 老路）
- ❌ 同时启动 Layer 4 G1 工具广度（先把 Layer 0-2 做扎实）
- ❌ 优先做 Layer 5 GEPA（数据基础未到位）

---

## §9 决策点（你拍）

| # | 议题 | 选项 |
|---|---|---|
| **D1** | 接受 5 层 DAG 框架？| A 接受 / B 调整层级划分 |
| **D2** | Layer 0 三个 W 并行启动？| A 启动 / B 串行做 |
| **D3** | Layer 2 三条并行启动（BashTool / Todos UI / Permission）？| A 三条全并行 / B 仅 BashTool + Todos UI 并行（Permission 推后）|
| **D4** | 18 个月达成 8/8 真正顶级目标 commit？| A commit / B 看 6 个月数据再定 |
| **D5** | 叶子项（mcporter skill / 5-provider embedding / prompt redact / group:* 等）什么时候穿插？| A 在 Layer 0-2 期间穿插 / B 集中在 Layer 3 期间 |

拍板后我立即写 **Layer 0 三个 W 的完整 spec**（observability schema / timeout 接口设计 / capacity governance 配置 schema），转入 plan-eng-review 启动施工准备。

---

## §10 文件索引

```
docs/research/
├── GAP-INVENTORY.md           # 全量缺陷清单（17 维度 / 104 项）
├── DEPENDENCY-ORDER.md        # ★ 本文件（DAG + 5 层施工建议）
├── ENGINEERING-QUALITY-ROADMAP.md  # 较早版本 roadmap（已被本文件取代部分内容）
├── FINAL-REPORT.md            # 4 家系统对标完整报告
└── _synthesis/SYNTHESIS.md    # 综合（已三次修订）
```

*— End of Dependency Order —*
