# Hive Engineering Quality 实施计划（唯一可执行版）

> **本文件取代之前所有 roadmap / synthesis / final-report**
> 之前的文件作为**支撑材料**保留（推理过程 / 调研证据），实际施工只看本文件
> **日期**：2026-04-25

---

## §0 一句话总览

**Hive 是通用 agent harness 系统**。IM 只是 channel adapter 之一（与 Web Console / 终端 TUI / IDE 插件等平等），**不是 product 边界**。

**目标**：12 个月把 Hive 从"engineering quality 严重落后"打造到"接近顶级 harness 系统"（满足 7/8 验收项），18 个月达到 8/8 真正顶级。

**做法**：5 层 DAG 分层施工，16 个工作流按依赖关系排，飞书 channel adapter（W8）等飞书施工完成后做但不阻塞核心 todos UI（W7 通用 adapter + Web Console 实现）。

**当前阶段**：立即启动 Layer 0 三个工作流（W1-W3），不依赖任何 channel。

**衡量标准（非 IM 数量）**：agent 稳定完成复杂任务能力 / 监督可见性 / 跨工具协调 / 自我改进 / LLM 高效利用。

---

## §1 完整工作流时间线（按月看）

```
月 1                月 2-3              月 4-6                月 7-12              月 13-18
═══════════════════════════════════════════════════════════════════════════════════════
[W1 Observability]
[W2 Timeout]
[W3 Capacity]                                                                           
              [W4 L1 + 扩展现有 EventRenderer]                                         
                          [W5 BashTool 工程化]                                          
                          [W6 Permission 升级]                                          
                          [W7 Web Console（实现 EventRenderer）]                        
                          [W8 飞书 todos case] ← 阻塞已解除（飞书改造完成）              
                                              [W9 Memory 治理]                          
                                              [W10 Skills 重构]                         
                                              [W11 MCP 生态]                            
                                              [W12 Spec-driven 大重构]                  
                                                              [W13 工具广度补齐 ──→]    
                                                              [W14 ACP 生态 ──→]        
                                                              [W15 Multi-agent ──→]     
                                                                          [W16 GEPA →]  
═══════════════════════════════════════════════════════════════════════════════════════
└── L0 ──┘└─── L1+L2 ───┘└──── L3 ────┘└────── L4 ──────┘└── L5 ──┘
```

---

## §2 工作流详细清单（W1-W16）

### Layer 0：基础设施（月 1）

#### W1 — Observability 基础
- **内容**：security check 数字化 ID（`BASH_SECURITY_CHECK_IDS = {1: INCOMPLETE, 2: JQ_SYS, ...}`）+ per-tool metric 完整暴露 + Prom 端点
- **改文件**：新增 `internal/observability/`，扩展 `internal/master/*` 的 metric 调用
- **不改**：任何 channel 实现
- **工期**：2 周
- **验收**：
  - 每个 security check / tool call / agent step 在 Prom 端点可见
  - metric 命名一致（前缀 + label 规范，避免高基数）
  - Prom scrape 可正常拉数据
  - **dashboard 配置不在本 W 范围**（部署期由用户选 grafana / Datadog / 自建栈决定）

#### W2 — Tool 级 timeout 统一
- **内容**：`getDefaultToolTimeoutMs() / getMaxToolTimeoutMs()` 函数化 + per-tool override + ctx cancel 全链路
- **改文件**：`internal/tools/context.go` 扩展 + 各 tool 加 timeout 调用
- **不改**：飞书相关
- **工期**：1 周（与 W1 并行）
- **验收**：timeout 用例测试全过 + 超时不泄漏 goroutine

#### W3 — Capacity governance 配置化
- **内容**：spawn limit + safe tool max concurrency + long-running admission control + 排队/拒绝/超时 metric 全部从同一 config 读
- **改文件**：`internal/master/react_processor.go` + `internal/subagent/factory.go` + `internal/master/streaming_executor.go` + `internal/tools/spawn_agent.go`
- **不改**：飞书相关
- **工期**：1 周（依赖 W1 完成 30%）
- **验收**：mutation test 验证：超额 spawn 被拒 / 超时被强制 kill / metric 正确归类

**Layer 0 总工期**：~3 周（W1-W2 并行 + W3 跟进）

---

### Layer 1+L2 部分：结构化基础 + 不阻塞项（月 2-3）

#### W4 — L1 结构化基础（**RE-REVIEW-POST-FEISHU 修订**）
- **内容**：
  - G2.5 工具结构化目录改造（先选 5 核心工具：bash/read/write/edit/web_fetch，每个改成子目录含 `tool.go` + `prompt.go` + `permissions.go` + `security.go`）
  - G2.4 关注点分离层定义（destructive_warning / permission / attack_defense 拆 module）
  - **扩展现有 `master.BroadcastMessage` 加 4 个 todos BroadcastType**（不新建 ChannelAdapter — 飞书改造完成揭示 Hive 已有完整 `channel.EventRenderer` interface）
- **改文件**：`internal/tools/{bash,read,write,edit,web_fetch}/` 重构 + `internal/security/` 拆分 + `internal/master/event_bus.go` 加 todo BroadcastType
- **不改**：`internal/channel/plugin.go`（已有 EventRenderer interface 不动）/ 任何 channel 实现 / `internal/channels/`（撤销新建包）
- **工期**：**1 周**（修订后 — 撤销 ChannelAdapter 新发明 + 复用现有 EventRenderer 抽象）
- **验收**：
  - 5 个核心工具按结构化目录组织
  - todos BroadcastMessage 能从后端 emit 到现有 EventBus
  - 现有 EventRenderer 实现（飞书）能正确接收 todo type（不实现 todo handle 也不报错）

#### W5 — L2A BashTool 工程化（用户硬约束 + destructive 风险）
- **内容**：BashTool 6 层防御：
  - destructive_warning module（参考 Claude Code destructiveCommandWarning.ts）
  - bypass_defense module（Zsh `=cmd` / heredoc / process subst / shell quote bug 等 17+ 攻击 vector）
  - path_validation module（专门 path 校验 + .git / .env / credentials 防误删）
  - sed_validation module（专门 sed 校验，防 `sed -i '...' /etc/passwd`）
  - readonly_mode module
  - permission decision module（与 destructive_warning 严格分离）
  - 每条规则有 attack vector comment（reasoning 文档化）
  - BashTool 专门 prompt（含 git safety / undercover / 用户类型分流）
- **改文件**：`internal/tools/bash/` + `internal/security/{destructive_warning,bypass_defense,path_validation,sed_validation,readonly_mode}.go`（新增 ~5 个模块）+ `internal/security/builtin_rules.go` 扩到 50+ 条
- **不改**：飞书相关
- **工期**：3 周
- **验收**：
  - Red team mutation test 100 个 attack vector 全过（含 50 destructive mutation + 30 attack vector + 20 readonly mutation）
  - 防御层级 ≥ 4 层
  - per-check metric 暴露
  - P99 pre-exec validation < 5ms

#### W6 — L2C Permission 模型升级
- **内容**：
  - G10.1 Deny-First 评估顺序核实 + 调整
  - G10.2 8 层过滤策略级联（参考 OpenClaw `multi-agent-sandbox-tools.md:206-219`）
  - G10.3 `/approve <id> allow-once|allow-always|deny` 三态 + IM callback button
  - G10.4 Modal execution（5 modes：default/plan/auto/dontAsk/bypassPermissions）
  - G10.5 group:* 9 个工具组快捷键
  - G10.6 path-scoped rules `.claude/rules/*.md` glob
- **改文件**：`internal/security/SafeExecutor.go` + 新增 `internal/security/{policy_chain,approve_modes,modal_exec,tool_groups,path_rules}.go`
- **不改**：飞书相关
- **工期**：3 周（与 W5 并行）
- **验收**：8 层级联过滤 mutation test 全过 + `/approve` 三态在 IM 飞书 callback 测试通过（**飞书 callback 测试可能要等飞书完成**，先做后端逻辑 + Web 测试）

#### W7 — L2B.2 Web Console（实现现有 channel.EventRenderer interface）（**RE-REVIEW 修订**）
- **内容**：
  - Web Console 实现现有 `channel.ChannelPlugin + channel.EventRenderer` interface（与飞书平等）
  - 完整 todos 列表组件（参照 Claude Code TodoWriteTool）
  - 用户可点改某项 / 标记完成 / 取消
  - 实时订阅 BroadcastMessage 流（参考飞书 feishuRenderer.run 模式）
- **改文件**：`frontend/src/components/todos/` 新建 + `frontend/src/store/todos.ts` 新建 + `internal/channel/web/` 新建（与现有 feishu/wechat/wecom/dingtalk 同级，**注意：channel 单数包，不是 channels 复数**）
- **不改**：任何 IM channel 的现有实现
- **工期**：**1.5 周**（修订后 — 复用现有 EventRenderer 抽象省 0.5 周）
- **验收**：
  - Web 用户能看到 todos + 改 + 干预
  - WebSocket 推送 P99 < 500ms
  - **核心**：证明 ChannelAdapter interface 抽象正确（任何后续 adapter 都能复用同一接口）

**Layer 1+L2 部分总工期**：~5 周（W4 → W5+W6+W7 三条并行）

---

### Layer 2 飞书 todos case（**RE-REVIEW**：飞书改造完成 + 阻塞解除 + 大幅缩减）

#### W8 — 飞书 dispatchEvent 加 todos case（与 W7 同期做）
- **触发条件**：~~飞书施工完成~~ — **已完成**
- **内容**（RE-REVIEW 大幅缩减）：
  - **飞书已是完整 EventRenderer 实现（763 行 + 17,904 行总规模）** — 已含 dedup / gap_fetch / reconnect_watchdog / reliability_leader_gate / governance / ratelimit / retry_queue / acl / audit
  - 仅在 `feishuRenderer.dispatchEvent()` switch 加 4 个 todos BroadcastType case
  - 新增 `handleTodoEvent()` 函数 + `card_builder.go` 加 `buildTodosSection()` 函数
  - 复用所有现有飞书可靠性机制（patchWithRetry / ErrPatchRateLimited / cardBuilder / state 管理）
- **改文件**：`internal/channel/feishu/renderer.go` + `internal/channel/feishu/card_builder.go`
- **不新建任何包装层 / interface / 包**
- **工期**：**0.2 周**（RE-REVIEW 修订，原 0.5 周）
- **验收**：飞书用户能看到 todos + 实时状态更新 + 完整复用现有飞书可靠性
- **后续 channel（不在本 Plan，按需做）**：企微 / 钉钉 / 微信 / 终端 TUI / IDE 插件 — 都实现现有 `channel.EventRenderer` interface，按需新增

---

### Layer 3：系统能力扩展（月 4-6）

#### W9 — L3.1 Memory 治理
- **内容**：
  - G4.8 bootstrap caps（最简，先做）
  - G4.6 findRelevantMemories top-N 检索
  - G4.1+G4.2+G4.3 三源借鉴（OpenClaw silent turn + Claude Code daily log + Claude Code nightly distill）
  - G4.4 structured summary（Hermes 风格 Resolved/Pending tracking + Handoff framing）
  - G4.7 5-provider embedding fallback
  - G4.9 MemoryManager 单一入口
- **改文件**：`internal/memory/` 重构 + 新增 `internal/master/compaction.go`
- **不改**：飞书相关
- **工期**：3 周
- **验收**：长会话压缩前后事实召回准确率 > 90% + compaction P99 < 5s + silent turn 不污染前端

#### W10 — L3.2 Skills 重构
- **内容**：
  - G5.4 token budget 主动估算
  - G5.1 Progressive loading（frontmatter-only startup）
  - G5.2 mcpSkillBuilders MCP-as-Skills 桥接
- **改文件**：`internal/skills/finder.go` 重构 + 新增 `internal/skills/mcp_skill_builders.go`
- **工期**：2 周
- **验收**：startup 时 100 个 skills context 占用 < 10K token（vs 全量 500K+）

#### W11 — L3.3 MCP 生态
- **内容**：
  - G6.1 MCP 工具结果 collapse 分类
  - G6.2 mcporter CLI 集成作为 skill（叶子项，可任意时段）
  - G6.3 chrome-mcp 浏览器 MCP server
- **改文件**：`internal/mcphost/` 扩展 + 新增 `skills/mcporter/` 配置
- **工期**：1 周
- **验收**：MCP 工具 UI 可折叠 + mcporter skill 可用

#### W12 — L3.4 Spec-driven 大重构（接 W7 + W8 todos UI）
- **内容**：
  - 砍 hidden DB 路径（或降级为 audit log）
  - 加 markdown artifact export 层（参考 OpenSpec `openspec/changes/<id>/` 结构）
  - 接 W7 Web Console todos UI 通道
  - 接 W8 飞书 PatchCard todos 通道
  - 保留 propose → apply → archive 流程
  - AI 质量 measurable metric（长任务完成率 / 跑偏率 / 失败可追溯性）
- **改文件**：`internal/specdriven/` 大重构 + 新增 `internal/specdriven/markdown_export.go`
- **依赖**：W7 + W8 完成
- **工期**：3 周
- **验收**：todos 全前端可见可干预（W7+W8 通道）+ 长任务完成率 measurable 提升

**Layer 3 总工期**：~9 周（W9-W11 部分并行 + W12 串行）

---

### Layer 4：高级能力（月 7-12）

#### W13 — 工具广度补齐
- **内容**（按子优先级）：
  - 优先批：Task×6 + Plan×2 + Worktree×2 + AskUserQuestion + ToolSearch（13 个）
  - 次优批：Schedule + RemoteTrigger + Sleep + REPL + Notebook + Brief + Config + SkillTool（8 个）
  - 后置批：Team×2 + SyntheticOutput + SendMessage（依赖 W15 Multi-agent，4 个）
- **改文件**：`internal/tools/{task,plan,worktree,schedule,...}/` 各自新建
- **工期**：2-3 个 quarter（分批）
- **验收**：工具数 ≥ 30 + 每工具结构化目录 + 专门 prompt

#### W14 — ACP 生态接入
- **内容**：
  - G7.4 ACP↔MCP bridge（参考 OpenClaw acpx）
  - G7.2 Backend Runtime（agent 端 ACP runtime）
  - G7.3 Client（连其他 agent 当 LLM provider，参考 Hermes copilot_acp_client）
- **改文件**：`internal/acpserver/` 扩展 + 新增 `internal/acpbackend/` + `internal/acpclient/` 扩展
- **工期**：1-2 个 quarter
- **验收**：Hive 同时具备 server + backend + client 三个 ACP 角色

#### W15 — Multi-agent 协调（**F16 拆两阶段**）

> **F16 DAG 解耦**：W15 拆成 W15.1（本地 in-process，**不依赖 W14 ACP**）+ W15.2（跨进程，依赖 W14）。详见 `SPEC-LAYER4-5-W13-W16.md §3.1`。
>
| W15 子阶段 | 依赖 | 工期 |
|---|---|---|
| **W15.1 本地 in-process** | W13 批 1+3（不依赖 W14）| 1 quarter |
| **W15.2 跨进程 / 远程** | W15.1 + W14 ACP | 0.5 quarter |


- **内容**：
  - G8.1 Coordinator Mode + INTERNAL_WORKER_TOOLS（TEAM_CREATE/DELETE/SEND_MESSAGE/SYNTHETIC_OUTPUT）
  - G8.2 ASYNC_AGENT_ALLOWED_TOOLS 子集
  - G8.4 Subagent 工具委托深化（参考 OpenClaw + Hermes）
  - G8.5 Mid-run steering（参考 Hermes `/steer`）
- **改文件**：`internal/master/coordinator_mode.go` 新建 + `internal/subagent/` 扩展
- **依赖**：W13 Team×2 工具 + W14 ACP 生态
- **工期**：1 个 quarter
- **验收**：Master Agent 能 spawn + 协调多个 sub agents + mid-run steering 可用

**Layer 4 总工期**：6-9 个月（多条并行）

---

### Layer 5：创新探索（月 13-18）

#### W16 — Self-improvement (GEPA)
- **内容**：
  - G14.1 GEPA reflection on traces（论文：arXiv 2507.19457）
  - G14.2 Skill autonomous creation（agent 自动从成功 trace 写 skill）
  - G14.3 Insights 学习
  - G14.4 Manual compression feedback
- **改文件**：新增 `internal/selfimprove/`
- **依赖**：W1 trajectory observability + W14 model routing
- **工期**：1-2 个 quarter
- **验收**：agent 在重复任务上跑快 ≥ 30%（measurable）

---

## §3 飞书施工约束的处理

| 工作流 | 飞书阻塞？ | 怎么办 |
|---|---|---|
| W1-W3 | ❌ 无 | 立即启动 |
| W4 | ❌ 无 | RE-REVIEW 修订：复用现有 channel.EventRenderer，不新建抽象 |
| W5 BashTool | ❌ 无 | 立即并行 |
| W6 Permission | ❌ 无 | 后端逻辑 + Web 测试即可 |
| W7 Web Console adapter | ❌ 无 | W4 完成后立即做（实现现有 EventRenderer interface）|
| W8 飞书 todos | ❌ **阻塞已解除**（飞书改造完成）| 仅加 dispatchEvent case + handleTodoEvent 函数 |
| W9-W11 Memory/Skills/MCP | ❌ 无 | 与 channel 无关，纯 harness 核心 |
| W12 Spec-driven 大重构 | ❌ 无 | 依赖 W7 完成（Web adapter 已能验证 todos 通道），不依赖 W8 |
| W13-W16 | ❌ 无 | 独立做 |

**关键点**：
- 飞书阻塞**只影响 W8 一个工作流**（0.5 周工期）
- W7 Web Console adapter 已经让 todos UI 完整可用，**W12 Spec-driven 大重构不依赖 W8**
- 飞书是第二个 channel adapter，按需做，**不是核心**
- 16 个工作流中 **15 个独立可推进**，仅 W8 等飞书

---

## §4 12 个月 / 18 个月达成状态

| 月份 | 完成 | 验收项进度（8/8）|
|---|---|---|
| 月 1 (L0 done) | observability + timeout + capacity | 0/8（基础设施，不计验收）|
| 月 3 (L1+L2 不含飞书 done) | BashTool 6 层防御 + Web todos + Permission 升级 | **2/8**（项 1+2 BashTool 防御 + red team mutation） |
| 月 6 (L3 done) | Memory + Skills + MCP + Spec-driven 大重构 + 飞书 todos | **5/8**（+ 项 5+8 Spec-driven measurable + todos 全前端可见） |
| 月 12 (L4 done) | 工具广度 + ACP 生态 + Multi-agent | **7/8**（+ 项 3+4+6 工具数 + 工具级 prompt + ACP 角色）|
| 月 18 (L5 done) | GEPA + observability 完整 | **8/8 真正顶级**（+ 项 7 observability 完整）|

**8 项验收回顾**（基于 harness engineering quality，非 IM）：
1. BashTool 防御 ≥ 4 层
2. Red team 100 vector 全过
3. 工具数 ≥ 30
4. 每工具有专门 prompt
5. Spec-driven 长任务完成率 ≥ +20% measurable
6. ACP ≥ 2 个角色
7. Observability 完整 metric
8. **Todos 通过通用事件流可见 + ≥ 1 个 ChannelAdapter 实现可干预**（硬约束 — channel 形态可任意，核心是事件流抽象正确 + 至少有一个完整可用的 adapter）

---

## §5 立即启动的 3 个工作流（W1-W3）

| W | 内容 | 工期 | 启动条件 |
|---|---|---|---|
| W1 | Observability 基础（数字化 ID + per-tool metric + Prom 端点） | 2 周 | **立即** |
| W2 | Tool 级 timeout 统一 | 1 周 | **立即**（与 W1 并行）|
| W3 | Capacity governance 配置化 | 1 周 | W1 完成 30% 后启动 |

**3 周后 Layer 0 完成 → 启动 W4 (L1 结构化 + L2B.1 事件 schema)**

---

## §6 文件索引（之前调研产物的角色）

| 文件 | 角色 |
|---|---|
| **`IMPLEMENTATION-PLAN.md`** ⭐ | **本文件 — 唯一可执行实施计划** |
| `DEPENDENCY-ORDER.md` | DAG 依赖分析（本计划的理论基础）|
| `GAP-INVENTORY.md` | 全量缺陷清单（17 维度 / 104 项）|
| `ENGINEERING-QUALITY-ROADMAP.md` | 早期 roadmap 草稿（已被本文件取代部分）|
| `FINAL-REPORT.md` | 4 家系统对标完整报告（推理过程）|
| `_synthesis/SYNTHESIS.md` | 综合（已三次修订）|
| `claude-code/VERIFIED-6-AXIS.md` | Claude Code 6 axis 主线程核实 |
| `deer-flow/VERIFIED-6-AXIS.md` | deer-flow 主线程核实 |
| `hermes-agent/VERIFIED-6-AXIS.md` | Hermes 主线程核实 |
| `openclaw/evidence/axis-{1-5,6}-VERIFIED.md` | OpenClaw 6 axis 主线程核实 |

**实际施工只看 IMPLEMENTATION-PLAN.md**。其他文件作为：
- 推理可追溯（为什么这么做）
- 调研证据（每个借鉴点来自哪里）
- 不施工时不需要重读

---

## §7 没有阻塞性未定项

之前的"飞书施工时间"已不是阻塞 — 因为 W7 Web Console adapter 已能让 todos UI 完整可用，W12 不再依赖 W8。飞书 adapter 何时上线只是 channel 覆盖广度问题。

**计划 100% 锁定，立即可启动 W1-W3**。

---

## §8 启动方式

确认 IMPLEMENTATION-PLAN.md 后：
1. 我写 **W1+W2+W3 的完整施工 spec**（observability schema 详细 / timeout 接口设计 / capacity governance 配置 schema）
2. 转入 `plan-eng-review` 让架构层 review
3. ship → 3 周后 Layer 0 完成 → 启动 W4

---

*— End of Implementation Plan —*
