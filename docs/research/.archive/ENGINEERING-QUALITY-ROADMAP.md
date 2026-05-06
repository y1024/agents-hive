# Hive Engineering Quality 升级路线图

> **目的**：把 Hive 打造成顶级 harness engineering 系统
> **约束**：当前阶段只做国内 IM
> **基础**：BashTool 1 个工具深度对比已确认 Hive engineering quality 严重落后 Claude Code（详见 `docs/research/_synthesis/MAIN-VERIFICATION-3-REPOS.md` + 上一轮对话产出）
> **日期**：2026-04-25

---

## §0 立场：诚实定位

**两条修正**（来自用户澄清 + BashTool 深度对比）：

### 修正 1：Spec-driven Cognition 是 OpenSpec 思想借鉴，不是产品哲学赌注

之前的实现把 OpenSpec 借鉴成"hidden spec layer / 用户完全无感" — **完全反了 OpenSpec 真意**：
- OpenSpec 精髓是 **artifact 显式可见**（markdown 文件 + git 追踪 + code review 可读）
- 目的是**保证 AI 质量**（防 vibe coding / 跑偏 / 漏 case / 失败不可追溯）
- 不是产品哲学，不是"无感智能"赌注

**新硬约束**：所有 Hive 规划的 todos / spec / tasks **必须让用户可见**：
- IM 渠道用**飞书 PatchCard 渐进展示**（边规划边展示）
- Web Console **完整列表 + 可改 + 可干预**

### 修正 2：Hive engineering quality 严重落后 Claude Code 标杆

FINAL-REPORT §6.1 列了"Hive 4 条领先"，深度审视后只有 1 条真领先（国内 IM 平台数量）。其他 3 条要么是 trade-off 取舍（Go 单二进制、Spec-driven），要么是 engineering quality 严重不足。

**真实状态**：
- Hive 工具数 ~15 vs Claude Code 42（**3x 落后**）
- Hive BashTool 安全防御 = 19 条 regex + AST parser 351 行 + LLM classifier 118 行 ≈ 670 行
- Claude Code BashTool 安全防御 = bashSecurity 2,592 + bashPermissions 2,621 + pathValidation 1,303 + readOnlyValidation 1,990 + sedValidation 684 + destructiveWarning 102 ≈ **9,292 行**
- **单 BashTool 防御深度差 14x**

**顶级 = Claude Code engineering quality 标杆**（不是 OpenClaw / Hermes / deer-flow，他们各自有结构性问题）。

---

## §1 顶级 harness 的可量化定义

| 维度 | 顶级标准（Claude Code 已达成）| Hive 当前 | 差距倍数 |
|---|---|---|---|
| 工具数 | 42 | ~15 | 3x |
| 单工具防御深度 | BashTool 12K 行 / 18 文件 | shell.go 223 + security/ 2,160 = ~2,400 行 | 5x |
| 防御层数 | 6 层（destructive warning / permissions / security / pathValidation / readOnlyValidation / sedValidation 各自独立）| 单层（19 条 regex 同时承担警告+决策+防御）| 6x |
| 绕过攻击意识 | 显式防御 Zsh `=cmd` / heredoc / shell quote bug / process substitution | 完全不防 | ∞ |
| Defense-in-depth | 显式防御 PowerShell 注释（即使不执行 PowerShell）| 无 forward-looking 防御 | ∞ |
| Reasoning 文档化 | 每条规则有 attack vector comment（zmodload 25 行注释解释 zsh/system, zsh/zpty 等）| 仅"禁止删除根目录"短描述 | 5-10x |
| Observability | 数字化 BASH_SECURITY_CHECK_IDS（避免 logging 高基数）| 无对应设计 | ∞ |
| 工具 prompt 质量 | 每工具有专门 prompt.ts（BashTool prompt.ts 369 行含 git safety / undercover / 用户类型分流）| Hive prompts 是 i18n MD 文件，无工具级专门 prompt | ∞ |
| 关注点分离 | 严格分层（informational warning ≠ permission decision ≠ attack defense）| 19 条规则同时做 3 件事，耦合 | 严重 |

**顶级 = 同时满足所有 9 个标准**。Hive 至少要做到其中 5-6 项才算"接近顶级"。

---

## §2 阶段路线（按你最初目的反推）

### 阶段 1（Q2 2026 / 接下来 3 个月）：补齐工程质量基础

**目标**：BashTool + 3-5 个核心工具达到 Claude Code engineering quality 60-70%（不追 12K 行规模，追防御层级 + reasoning + 关注点分离）

**measurable 验收**：
- BashTool red team mutation 测试 100 个攻击 vector 全过（含 Zsh `=cmd` / heredoc in subst / shell quote bug / 参数顺序换位 / find -delete 等绕过）
- 防御层级从 1 层 → 4 层（informational warning / permission / attack defense / specialized validation）
- 每条 attack defense 有 comment 解释 attack vector
- Observability：security check 数字化 ID + per-check metric

### 阶段 2（Q3 2026）：Spec-driven 大重构 + 借鉴落地

**Spec-driven Cognition 大重构**（按 OpenSpec 真意重新设计）：
- **撤销 hidden 设计** — 抛弃"用户完全无感"思路
- **artifact 显式化** — todos / spec / tasks 上前端
  - IM 渠道：飞书 PatchCard 渐进展示（边规划边渲染）
  - Web Console：完整 todo 列表 + 可改 + 可干预
- **保留 OpenSpec 流程**：propose → apply → archive 三阶段
- **保留 Phase 1 SafeExecutor 权限极简**（已上线，与 OpenSpec 思想无关，独立保留）
- 重构现有 `internal/specdriven/` 14 文件：
  - 砍掉 hidden DB 持久化路径（或降级为 audit log）
  - 新增 markdown artifact export 层（参考 OpenSpec `openspec/changes/<id>/` 结构）
  - 新增 Web Console UI + 飞书 PatchCard 渐进展示

**P0-4 三源借鉴落地**：
- OpenClaw silent turn 触发机制
- Claude Code nightly distill 离线批处理
- Hermes context_compressor 结构化模板（Resolved/Pending tracking）

### 阶段 3（Q4 2026 ~ Q1 2027 / 6-12 月长线）：扩展深化

- **ACP 生态接入**（P1-9 + P1-10）：Hive 同时当 server + backend runtime + client 三个角色
- **Multi-agent 协调**（P1-12）：参考 Claude Code Coordinator Mode + Team 工具
- **Memory 治理**（nightly distill + structured compression）

---

## §3 接下来 1 个 Sprint（2-4 周）：**1 件事**

### Sprint 主题：**BashTool Engineering Quality 大升级**

#### Why now
- BashTool 是 destructive 风险最高的工具
- Hive 当前防御 19 条 regex 几秒内就能想到 5+ 种绕过
- 上线后任何 IM 用户走 PolicyAsk → IM 自动放行路径都可能被利用
- 这是单点最高 ROI 的工程质量升级

#### Why this（不是别的）
- 工具质量 / prompt 质量 / 工程化 3 个维度里，工具质量是最具体可执行的
- BashTool 是所有工具的"皇冠"，做好了立 engineering quality 标杆
- Sprint 1 不能选 Spec-driven Phase 2（数据未到 / 风险大），不能选 ACP 生态（架构改动大）

#### 具体范围（按 Claude Code 标杆裁剪到 Hive 现实）

```
internal/security/  — 现状：~2,160 行单层
internal/tools/shell.go — 223 行 PersistentShell

升级目标:
├── internal/security/destructive_warning.go    # 新增 ~120 行（informational layer，与 permission 分离）
├── internal/security/bypass_defense.go         # 新增 ~400 行（Zsh =cmd / heredoc / shell quote / process subst 等绕过防御）
├── internal/security/path_validation.go        # 新增 ~300 行（专门 path 校验，含 .git / .env / credentials 防误删）
├── internal/security/sed_validation.go         # 新增 ~200 行（专门 sed 命令校验，防 sed -i 修系统文件）
├── internal/security/readonly_mode.go          # 新增 ~250 行（readonly 模式专门校验）
├── internal/security/builtin_rules.go          # 重构：从 19 条扩到 50+ 条，每条有 attack vector comment
├── internal/security/check_ids.go              # 新增 ~80 行（数字化 check ID + per-check metric）
└── internal/tools/shell.go                     # 不动 PersistentShell 主体，加 pre-exec validation 调用层

规模目标: 现状 ~2,400 行 → 升级后 ~4,000 行（70% 增长，不追 Claude Code 12K）
```

#### Measurable 验收

1. **Red team mutation test 100% pass**（必须）：
   - 50 个 destructive 命令 mutation（含 `rm -fr /` / `cd / && rm -rf .` / `eval` 包装 / `bash -c` 包装 / Zsh `=rm -rf /`）
   - 30 个 attack vector mutation（heredoc in `$()` / `<()` process subst / shell quote bug）
   - 20 个 readonly mode mutation（在 readonly 模式下的写操作必须 deny）
   - **全过才能 merge**

2. **Reasoning 文档化**：每条新增/修改规则必须有 comment 解释 attack vector（参照 Claude Code zmodload 注释风格）

3. **关注点分离**：destructive warning（informational）≠ permission decision ≠ attack defense（3 个独立模块）

4. **Observability**：每个 security check 有数字化 ID，per-check metric 暴露 prom 端点

5. **性能门槛**：pre-exec validation 全链路 P99 < 5ms（不能拖慢 happy path）

#### 工期估算

- 设计 + spec 写作：2 天
- destructive_warning + bypass_defense + path_validation：5 天
- sed_validation + readonly_mode + check_ids：4 天
- 重构 builtin_rules + 集成：3 天
- Red team mutation test 编写 + 跑通：3 天
- 蓝军 review + iteration：3 天

**总计：~3 周（含 review + iter buffer）**

#### 反面教材（防止本 sprint 翻车）

- 不要试图复制 Claude Code 12K 行（追求"接近顶级"，不是"等同顶级"）
- 不要为了 Hive 没有的 sed 工具加 sedValidation（先看 Hive 工具集是否真有 sed 调用）
- 不要在 Hive 不支持 PowerShell 的情况下加 PowerShell defense（Claude Code 加是因为他们 forward-looking，Hive 当前阶段不必）
- 不要把这次升级和 Spec-driven Phase 2 / ACP 生态混做（混做必失败）

---

## §4 接下来 1 个 Quarter（3 个月）：**3 件事**

### Q2.1：BashTool 升级（即 §3 sprint）

接 §3，3 周完成。

### Q2.2：Hive Prompt 质量审计 + 升级

**触发**：BashTool 升级中你会发现 Hive 当前 i18n prompts 没有"工具级专门 prompt"概念。Claude Code BashTool prompt.ts 369 行含完整 git safety protocol / undercover / 用户类型分流，是 prompt engineering taste 的标杆。

**目标**：
- 审计 Hive 现有 `internal/i18n/prompts/{system,subagents,tools}` 的 prompt 质量
- 给 5 个核心工具（shell / read_file / write_file / edit / web_fetch）写专门 prompt（参照 Claude Code prompt.ts 风格）
- 引入"工具级 prompt 段"机制，让每工具贡献自己的 system prompt 段

**measurable 验收**：
- Prompt eval suite：5 个工具的"危险/边界场景"用例，LLM 行为符合 prompt 教导
- Prompt regression test：现有 happy path 不退化
- 引入 prompt cache（参照 Hermes prompt_caching.py）

**工期**：3 周

### Q2.3：Pre-compaction Memory Flush 三源借鉴落地（P0-4）

**触发**：deer-flow final-verdict 已批准 + 主线程 verified 三源借鉴可信度 [HC-MAIN-VERIFIED]

**目标**：
- 实现 OpenClaw silent agentic turn 触发机制（接近 compaction threshold 时）
- 集成 Claude Code nightly distill 思路（async 离线 distill memory log）
- 用 Hermes context_compressor 结构化 summary 模板（Resolved/Pending tracking）

**measurable 验收**：
- 长会话压缩前后的事实召回准确率 mutation test：> 90%
- silent turn 不污染 conversation（前端无感知）
- compaction P99 latency < 5s

**工期**：1.5 周（含三源融合 + 测试）

### Q2.4（新增）：Todos 可见性 UI + 后端接入（OpenSpec 真意落地第一步）

**触发**：用户硬约束"todos 必须让用户可见" + 修正 Hive 之前 hidden 实现走偏 OpenSpec 真意

**目标**（Q3 大重构的前置基建）：
- 后端：Hive Master Agent 在生成 plan / 分解 tasks 时**显式发出 todos 事件**（与现有 `tool_call` `agent_status` 等事件类型同级）
- 前端：
  - **Web Console 完整 todos 列表组件**（参照 Claude Code TodoWriteTool UI）
  - **飞书 PatchCard 渐进 todos 展示**：复用现有 `internal/channel/feishu/renderer.go` PatchCard 增量渲染能力，把 todos 作为 card section 渐进 PATCH 上去
- 数据流：每个 todo 状态变化（pending → in_progress → completed）实时推送到前端

**measurable 验收**：
- IM 飞书用户能看到 agent 的 todos + 实时状态更新
- Web Console 用户能看到完整 todos + 可点击改某项 + 可标记完成 + 可取消
- todo 状态变化 P99 < 500ms 推送到前端
- 与现有 WebSocket 事件流（`internal/streaming/`）兼容

**工期**：2.5 周（后端 1w + 前端 Web 1w + 飞书 PatchCard 0.5w）

**为什么 Q2.4 现在做**：
- 是 Q3 Spec-driven 大重构的**前置基建**（先打通显式 todos 通道，再重构 Spec-driven 写入这条通道）
- 独立可上线（不依赖 Spec-driven Phase 2）
- 立即提升 AI 质量（用户能看到 agent 在干嘛，跑偏立即可见）

### Q2 总工期与 buffer

4 件事约 10 周，剩 2 周作为：
- Sprint 间 review + iteration buffer
- 国内 IM 渠道 P2/P3 维护（TODOS.md 已列）
- Q3 Spec-driven 大重构准备（前置 Q2.4 todos UI 必须 ship）

---

## §5 6-12 月长线（Q3-Q4 2026 ~ Q1 2027）

### 战略级别项（按 ROI 排序）

| # | 项 | 触发条件 | 工期 |
|---|---|---|---|
| L1 | **Spec-driven 大重构 ship + 数据收集**（artifact 显式可见 + IM PatchCard 渐进 + Web Console 完整可干预）| Q2 BashTool 升级 + Q2.4 todos UI 完成 | 1 个 quarter |
| L2 | **ACP 生态接入**（P1-9 client + P1-10 backend runtime）| L1 启动后 | 1-2 个 quarter |
| L3 | **Multi-agent Coordinator Mode**（P1-12）| L2 完成后（依赖 ACP 接入）| 1 个 quarter |
| L4 | **工具集广度补齐**：Task×6 / Plan / Schedule / RemoteTrigger 等 | L1-L3 任意 sprint 间隔做 | 持续 |

### Spec-driven 的真实位置（修正后）

不是"Hive 产品哲学赌注"（"hidden 是否成立"），是 **OpenSpec 思想借鉴用于 AI 质量保证**：
- 目标 = 保证 AI 输出质量（长任务完成率 / 跑偏率 / 失败可追溯性 measurable 提升）
- 手段 = OpenSpec 的 propose → apply → archive 流程 + **artifact 显式可见**（IM PatchCard + Web Console）
- 不是手段 = 当前的"hidden spec layer / 用户完全无感"
- L1 验收 = AI 质量 metric measurable 提升 + todos 全前端可见可干预（不是验证"hidden 是否成立"）

---

## §6 不该做的事（防研究瘫痪 + 防方向走偏）

- ❌ **不再做任何 hidden artifact / 不上前端的 plan / spec / tasks**（OpenSpec 真意是显式可见，违背就是反 OpenSpec）
- ❌ **不再把 Spec-driven 当作产品哲学赌注**（它是 AI 质量保证机制）
- ❌ **不再做新调研对照表**：4 家 6-axis 已经做透，再做边际收益负
- ❌ **不再追工具数**（42 vs 15 不是核心，深度才是）
- ❌ **不再争论"先进性"哲学**：直接做 + 收集 measurable 数据
- ❌ **不要试图同时做 BashTool 升级 + Spec-driven 大重构 + ACP 生态**：混做必失败
- ❌ **不再读 Claude Code source 找新借鉴**（已找够，先 ship 当前的）

---

## §7 顶级 harness 的最终验收（12 个月后回看）

如果 12 个月后 Hive 满足以下 7 项，可以说"Hive 已是顶级 harness"：

| # | 验收项 | 当前状态 | 12 个月目标 |
|---|---|---|---|
| 1 | BashTool 防御深度 ≥ 4 层（warning / permission / attack defense / specialized）| 1 层 | 4 层 |
| 2 | Red team mutation test 100 个 vector 全过 | 0 | 100% pass |
| 3 | 工具数 ≥ 30（含 Task / Plan / Schedule / RemoteTrigger 类）| ~15 | 30+ |
| 4 | 每工具有专门 prompt 段（参照 Claude Code prompt.ts）| 0 | 全工具覆盖 |
| 5 | Spec-driven 大重构后**长任务完成率提升 ≥ 20%**（measurable，不是"验证 hidden 赌注"）| 未做 | 已 ship + 数据 |
| 6 | ACP 生态接入 ≥ 2 个角色（server + backend OR server + client）| 仅 server | 2 个角色 |
| 7 | observability：security check / agent step / tool call 全 metric 暴露 | 部分 | 完整 |
| **8** | **Todos 全前端可见可干预**（IM 飞书 PatchCard 渐进 + Web Console 完整列表）| ❌ hidden | ✅ 全前端可见 |

**满足 5/8 = 接近顶级；满足 8/8 = 真正顶级**

**重点**：第 8 项是**硬约束**（用户指令），缺这一项其他 7 项做满也不算顶级 — 因为反 OpenSpec 真意 + 用户无监督权 = 不可信赖的 AI

---

## §8 决策点（你定）

| # | 议题 | 选项 | 我的推荐 |
|---|---|---|---|
| **D1** | 接受修正后的 roadmap 总框架？| A) 接受 / B) 调整 / C) 重写 | **A** — 框架基于 BashTool 实证 + 用户硬约束（todos 可见）+ OpenSpec 真意 |
| **D2** | Sprint 1 选 BashTool 升级？| A) 选 BashTool / B) 选 Q2.4 todos UI 优先 / C) 平行 | **B**（修正推荐）— todos UI 是用户硬约束 + Q3 Spec-driven 大重构前置基建，应优先；BashTool 升级排 Sprint 2 |
| **D3** | Sprint 1 范围：todos UI 后端事件 + Web 列表 + 飞书 PatchCard | A) 全做（2.5w）/ B) 仅后端事件 + Web（IM 推 Sprint 3）/ C) 仅 Web（IM 推 Q3）| **A** — 用户硬约束含 IM，分批做信息断层 |
| **D4** | Sprint 1 完成后 Sprint 2 选 BashTool 升级？| A) 选 / B) 选 Prompt 质量审计 | **A** — destructive 风险最高 |
| **D5** | Q3 Spec-driven 大重构的范围 | A) 全重构（hidden DB → markdown export + UI + 砍 hidden 路径）/ B) 仅加 UI 层（DB 保留）/ C) 仅 markdown export | **A** — 用户选了 Q2 选项 B 大重构 |
| **D6** | Phase 1 SafeExecutor 权限极简（已上线）保留？| A) 保留（与 OpenSpec 思想无关，独立价值）/ B) 重审 | **A** — Phase 1 是独立工程，不受 Spec-driven 重构影响 |

---

## §9 立即可执行的 next step

1. **你对 D1-D6 拍板**（5 分钟决策）
2. 我以拍板结果**写 Sprint 1 BashTool 升级的完整 spec**（架构 / 模块边界 / 测试 plan / 工期 / 蓝军 mutation 用例集），转入 plan-eng-review
3. Sprint 1 启动 → 3 周后 ship → 启动 Q2.2 Prompt 质量审计

---

## §10 一句话总结

**Hive 不是顶级 harness — 现在不是**：
1. **OpenSpec 真意走偏**：当前 Spec-driven 做成 hidden 反了 OpenSpec "artifact 显式可见 + AI 质量保证"的精髓
2. **Engineering taste 严重不足**：BashTool 一个工具一个维度看，防御深度差 Claude Code 14x
3. **Todos 用户无监督权**：违背 AI 质量保证的核心原则（用户必须能看到 + 干预）

打造顶级的路径：
- **Sprint 1**: Todos UI（后端事件 + Web 列表 + 飞书 PatchCard 渐进）— 落地用户硬约束 + Q3 重构前置基建
- **Sprint 2**: BashTool 升级 — 防御深度 4 层
- **Q2.3-2.4**: Prompt 质量 + Pre-compaction Memory Flush 三源借鉴
- **Q3**: Spec-driven 大重构（撤 hidden + 上 markdown artifact + 接 todos UI）
- **Q4-Q1**: ACP 生态 + Multi-agent + 工具广度

**12 个月后满足 8/8 = 真正顶级，含硬约束第 8 项 todos 全前端可见**。

**你的最终目的不是"调研做完"或"领先叙事建立"，是 Hive 6-12 个月后真的更强 + 用户能信任**。这份 roadmap 围绕这两个目的写。

---

*— End of Engineering Quality Roadmap —*
