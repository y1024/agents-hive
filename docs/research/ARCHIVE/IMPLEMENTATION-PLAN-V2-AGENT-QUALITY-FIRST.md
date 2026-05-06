# Hive 实施计划 V2 — Agent 质量优先（头等大事）

> **取代**：`IMPLEMENTATION-PLAN.md` v1（v1 把工程基线当头等大事，错了）
> **核心修正**：**Agent 质量（prompt + 工具 + skill + context 4 大支柱）= 头等大事**；工程基线（observability / 安全 / 权限 / 渠道 / 扩展）= 支撑层
> **依据**：v1 的 W1-W16 中真正提升 agent 质量的只有 W9 部分 + W12 押注 + W16 探索（3/16 = 19%），其余 81% 是工程外围
> **日期**：2026-04-27

---

## §0 优先级反转的根本理由

**Agent 质量本质 = LLM 在 harness 里能不能把任务做对**，决定因素：

| 决定因素 | 比重 | 之前 v1 覆盖 |
|---|---|---|
| **Prompt 质量** | LLM 收到指令清晰度 + 防错设计 | ❌ 几乎 0 |
| **工具描述质量** | LLM 调对工具的概率 | ❌ 几乎 0 |
| **Skill 管理质量** | LLM router 选对 skill + 内容教得好 | ❌ 几乎 0 |
| **Context 管理** | 不污染 + 长会话稳定 | ⚠️ W9 部分覆盖 |
| 工程基线（observability / timeout / 安全 / 权限 / 渠道）| 必要支撑，不直接影响 LLM 行为 | ✅ v1 W1-W16 大部分 |

**残酷真相**：v1 把 81% 精力放在了"工程外围"。Hive agent 跑得好不好，**90% 取决于 prompt + 工具 + skill + context**，剩下 10% 才是 v1 优先做的工程基线。

---

## §1 V2 优先级（Agent 质量在前）

### Phase 0 ★ Agent 质量本质（头等大事，先做）

| W | 内容 | 工期 | 详细 spec |
|---|---|---|---|
| **W22** | **Prompt 工程化**（system + 每工具专门 prompt + eval suite + regression test + cache + redact）| 4-6 周 | `SPEC-AGENT-QUALITY-W22-W25.md` §1 |
| **W23** | **工具质量管理**（description quality eval + selection eval + 失败模式 catalog + A/B test framework + schema 版本管理）| 3-4 周 | §2 |
| **W24** | **Skill 管理本质**（quality eval + router eval + 失败追溯 + 内容质量评分 + 版本演化）| 3-4 周 | §3 |
| **W25** | **Context 治理**（size 监控 + 污染追溯 + compaction eval + 长会话稳定性）| 2-3 周 | §4 |

**Phase 0 工期**：12-17 周（**3-4 个月**）

**Phase 0 完成后用户感知提升**：
- 复杂任务一次完成率：~50% → **80%**
- LLM 选对工具率：~70% → **95%**
- Skill router 选对率：~50% → **85%**
- 长会话事实召回率：~60% → **90%**
- 失败可追溯率：~30% → **90%**

### Phase 1 — 工程基础设施支撑（Phase 0 同期或后置）

支撑 Phase 0 的能力，**很多与 Phase 0 并行做**：

| W | 内容 | 优先级 | 与 Phase 0 关系 |
|---|---|---|---|
| W1 Observability | 必须 | 高 | **Phase 0 prompt eval / tool eval / skill eval 都依赖 metric** — 与 W22 并行启动 |
| W2 Tool timeout | 必须 | 中 | 独立 |
| W3 Capacity governance | 部分必须 | 中 | 独立 |
| W5 BashTool 关键 attack vector | 必须 | 中 | 独立（不阻塞 Phase 0）|
| W6 Permission Deny-First + group:* + /approve 三态 | 部分必须 | 中 | 独立 |
| W9 Memory pre-compaction flush | 部分必须 | 高 | **Phase 0 W25 Context 治理依赖** — 与 W25 合并做 |

**Phase 1 工期**：~6-8 周（与 Phase 0 部分并行）

### Phase 2 — 工程深度（Phase 0 验收通过后）

只在 Phase 0 数据证明确实有用时才做：

| W | 内容 | 触发条件 |
|---|---|---|
| W4 工具结构化目录 | 工程美学，**不阻塞 agent 质量** | Phase 0 完成 + 团队工程师感觉痛 |
| W10 Progressive skills | skill 量到 100+ 才需要 | skill marketplace 起来后 |
| W11 MCP 生态扩展（collapse / mcporter / chrome-mcp）| 集成扩展 | 用户需求驱动 |
| W12 Spec-driven 大重构 | 押注验证 | Phase 0 W22-W24 完成后再 dual-flag |
| W13 工具广度补齐 | **必须先 W22+W23 把现有工具质量做好**，否则数量×质量低=灾难 | W22+W23 ship 后 |

### Phase 3 — 扩展能力（按需）

| W | 触发条件 |
|---|---|
| W7+W8 todos UI | 产品决定要做用户可见 plan |
| W14 ACP backend / client | 有 IDE 集成 / 跨 agent 协作场景 |
| W15.1 Multi-agent 本地 | 有 multi-agent 场景 |
| W15.2 Multi-agent 跨进程 | W14 ACP 完成 + 跨进程需求 |
| W16 GEPA | Phase 0 完成 + 长期研究兴趣 |

---

## §2 V2 时间轴

```
月 1-4              月 5-6              月 7-9             月 10-12             月 13-18
═══════════════════════════════════════════════════════════════════════════════════════
[★W22 Prompt 工程化]
  [★W23 工具质量]
  [★W24 Skill 管理]                                                                     
  [★W25 Context 治理]                                                                   
[W1 Observability（与 W22 并行，支撑 eval）]                                           
[W2 Timeout]                                                                            
[W3 Capacity]                                                                           
[W5 BashTool 关键防御]                                                                  
[W6 Permission 核心]                                                                    
[W9 Memory flush]                                                                       
                    [Phase 0 验收 + 数据]                                              
                                       [W12 Spec-driven dual-flag]                     
                                       [W13 工具广度（W22/W23 完成后）]                
                                       [W4 工具结构化目录]                              
                                                              [W7+W8 todos UI]         
                                                              [W14 ACP]                
                                                              [W15 Multi-agent]        
                                                                          [W16 GEPA]   
═══════════════════════════════════════════════════════════════════════════════════════
└── ★ Agent 质量（Phase 0）+ 基础设施 ──┘└── 工程深度 ──┘└── 扩展能力 ──┘└探索┘
```

**v1 vs v2 对比**：

| 阶段 | v1（错的）| v2（对的）|
|---|---|---|
| 月 1-3 | W1-W3 + W4 + W5 + W6（工程基线先做）| **★ W22-W25 + 必要 W1-W9 并行**（agent 质量先做）|
| 月 4-6 | W9 + W10 + W11 + W12（系统能力扩展）| Phase 0 验收数据 + W12 dual-flag + W13 工具广度 |
| 月 7-12 | W13-W15（高级能力）| 工程深度 + 扩展能力（按需）|
| 月 13-18 | W16 GEPA | Phase 3 扩展（按需）|

---

## §3 8/8 验收清单（修订）

| # | 验收项 | 当前状态 | v2 目标 |
|---|---|---|---|
| **1 ★** | **Prompt 行为符合教导率**（LLM 实际行为 vs prompt 期望） | 未测 | ≥ 95% |
| **2 ★** | **LLM 选对工具率**（多工具场景）| 未测 | ≥ 95% |
| **3 ★** | **Skill router 选对率**（多 skill 场景）| 未测 | ≥ 85% |
| **4 ★** | **复杂任务一次完成率** | ~50% | ≥ 80% |
| **5 ★** | **失败可追溯率**（能定位 LLM 哪步跑偏）| ~30% | ≥ 90% |
| **6** | **长会话事实召回率**（100 轮后）| ~60% | ≥ 90% |
| **7** | BashTool 100 attack vector mutation 全过 | 0 | 100% |
| **8** | Observability 完整 metric / trace 接入 | 部分 | 完整 |

**6/8 是 agent 质量验收 + 1 个 context 召回 + 1 个工程基线**。重点已完全转移到 agent 质量。

**v1 旧验收 8/8 中**：
- 工具数 ≥ 30 / 每工具有专门 prompt / ACP ≥ 2 角色 / Spec-driven +20% 完成率 / Todos 全前端可见 等
- **大部分降为 Phase 2/3 选项，不是必须**

---

## §4 Phase 0 详细 spec

见 `SPEC-AGENT-QUALITY-W22-W25.md`：
- §1 W22 Prompt 工程化
- §2 W23 工具质量管理
- §3 W24 Skill 管理本质
- §4 W25 Context 治理

---

## §5 v1 → v2 关键变化总结

| 变化 | v1 | v2 |
|---|---|---|
| **核心叙事** | "对标 Claude Code 工程深度" | "提升 agent 质量本质" |
| **Phase 0** | W1-W3 工程基础设施 | **W22-W25 agent 质量本质** + 必要 W1-W9 并行 |
| **第一个 ship 后用户感知** | "debug 时间从 30min → 30s" | **"复杂任务一次完成率 50% → 80%"** |
| **8/8 验收** | 5 项工程深度 + 3 项 agent 能力 | **5 项 agent 质量 + 3 项工程基线** |
| **W4 工具结构化目录** | Phase 1 必做 | Phase 2 按需 |
| **W7+W8 todos UI** | Phase 1 必做 | Phase 3 按需 |
| **W13 工具广度补齐** | Phase 4 必做 | **必须先 W22+W23 把现有工具质量做好再加广度** |
| **总工期** | 12-18 个月（全做）| **6-9 个月达 80% 用户价值**（Phase 0 + Phase 1 必要项），剩余按需 |

---

## §6 立即启动

Phase 0（W22-W25）+ Phase 1 必要项（W1 / W9 / 部分 W2/W3/W5/W6）**并行启动**：

| 工作流 | 启动时机 | 工期 |
|---|---|---|
| W1 Observability | **立即**（支撑 Phase 0 所有 eval）| 2 周 |
| W22 Prompt 工程化 | **立即**（W1 完成 30% 后正式开始 eval）| 4-6 周 |
| W2 Tool timeout | 立即 | 1 周 |
| W23 工具质量管理 | W22 启动 1 周后 | 3-4 周 |
| W24 Skill 管理本质 | W23 启动 1 周后 | 3-4 周 |
| W25 Context 治理 | W22 启动 2 周后（依赖 W22 prompt 部分）+ 与 W9 合并 | 2-3 周 |
| W3 / W5 / W6 / W9 | 与上面并行 | 3-4 周 |

**3-4 个月后 Phase 0 完成 + 用户感知提升 measurable**。

---

## §7 文件索引（v2）

```
docs/research/
├── IMPLEMENTATION-PLAN-V2-AGENT-QUALITY-FIRST.md  # ⭐ 本文件（v2 总实施计划）
├── SPEC-AGENT-QUALITY-W22-W25.md                  # ⭐ Phase 0 详细 spec
├── IMPLEMENTATION-PLAN.md                          # v1（保留作历史参考）
├── SPEC-LAYER0~5（W1-W16 各 spec）                 # 保留作 Phase 1-3 参考
├── ARCH-REVIEW.md / CROSS-REVIEW-SYNTHESIS.md     # 保留
├── HIVE-EXISTING-CAPABILITIES.md                   # 保留
└── 其他历史文件                                     # 保留
```

---

*— End of V2 Implementation Plan —*
