# Layer 4-5 施工 Spec：W13 工具广度 + W14 ACP + W15 Multi-agent + W16 GEPA

> **依赖**：Layer 3（W9-W12）完成
> **工期**：6-12 个月（Layer 4 多 quarter 分批 + Layer 5 探索性 1-2 quarter）
> **施工后**：8/8 验收 = 真正顶级 harness
> **日期**：2026-04-25

---

## §1 W13 — 工具广度补齐

### 1.1 Why
Hive ~15 工具 vs Claude Code 42 工具。差距 27 个。**不是单纯堆数量**，是补缺类别：
- **Task 系统**（6 个）— Hive 完全没有
- **Plan Mode**（2 个）— Hive 没有
- **Worktree**（2 个）— Hive 没有
- **Schedule / Cron**（1 个）
- **RemoteTrigger**（1 个）— 远程触发
- **Sleep**（1 个）— agent self-pace
- **Notebook**（1 个）
- **REPL**（1 个）
- **AskUserQuestion**（1 个）— 结构化询问
- **ToolSearch**（1 个）— meta-tool 搜工具
- **Brief / Config**（2 个）
- **SkillTool**（1 个）— 显式 skill 调用
- **Team×2**（2 个）— 依赖 W15 Multi-agent
- **SyntheticOutput / SendMessage**（2 个）— 同上
- **PowerShell**（1 个）— 视需要

### 1.2 分批策略

| 批次 | 工具 | 工期 | 启动条件 |
|---|---|---|---|
| **批 1（优先）**| TaskCreate / TaskGet / TaskList / TaskOutput / TaskStop / TaskUpdate（6）+ EnterPlanMode / ExitPlanMode（2）+ EnterWorktree / ExitWorktree（2）+ AskUserQuestion + ToolSearch（共 13 个）| 2-3 个月 | Layer 3 完成 |
| **批 2（次优）**| ScheduleCron / RemoteTrigger / Sleep / REPL / NotebookEdit / Brief / Config / SkillTool（8 个）| 2-3 个月 | 批 1 完成 |
| **批 3（依赖 W15）**| TeamCreate / TeamDelete / SyntheticOutput / SendMessage（4 个）| 1 个月 | W15 完成 |
| **可选** | PowerShell | 视需要 | — |

### 1.3 每个工具的 spec template

每工具 spec 长度控制在 ~100 行，覆盖：

```markdown
## ToolName

### Why
（解决什么用户场景）

### Interface
- 工具名 / 参数 schema
- 返回值结构

### 文件结构（按 W4 G2.5 结构化目录）
internal/tools/<tool_name>/
├── tool.go         # 主实现
├── prompt.go       # 工具自带 prompt（参照 BashTool prompt.go）
├── permissions.go  # permission 决策
├── security.go     # attack defense（如需要）
└── tool_test.go    # 单测

### 测试 plan
- happy path
- 边界
- attack vector（如适用）

### 工期：1 周
```

### 1.4 关键工具详细 spec

#### 1.4.1 TaskCreate / TaskGet / TaskList / TaskOutput / TaskStop / TaskUpdate（核心）

参照 Claude Code Task 工具集。这是 Multi-agent 协调的基础（W15 依赖）。

**Task 状态机**：
```
pending → claimed → in_progress → (completed | failed | cancelled)
                ↓
              blocked → (released → in_progress)
```

**接口**：

**F14 修复**：加 optimistic locking（version）+ TTL lease + append-only output revision（防 stale writer 污染 + 完成态被覆盖）。

```go
type Task struct {
    ID          string
    Subject     string
    Description string
    Status      TaskStatus
    Owner       string         // agent ID
    BlockedBy   []string
    Blocks      []string
    Metadata    map[string]any
    CreatedAt   time.Time
    UpdatedAt   time.Time
    
    // F14 新增字段
    Version       int64        // 单调递增，每次 update +1
    LeaseExpiresAt *time.Time  // claim 时设置 TTL（默认 5 分钟）
    OutputHistory []TaskOutputRevision  // append-only 输出历史
}

type TaskOutputRevision struct {
    Revision  int64     // 单调 1, 2, 3, ...
    Content   string
    Author    string    // agent ID
    CreatedAt time.Time
}

// 6 个工具（F14 修复后）:
//   - task_create({subject, description}) → task_id  (无 version 概念，新 task 总是 v1)
//   - task_get(id) → task (含 Version + 最新 OutputRevision)
//   - task_list({status?, owner?}) → []task
//   - task_claim(id, ttl_seconds) → 申请 lease（设置 LeaseExpiresAt + 提升 Version）
//                                    若已被其他 agent claim 且 lease 未过期 → 拒绝
//                                    若 lease 已过期 → 强制接管（记 audit log）
//   - task_output(id, content, expected_version) → append 新 revision（不 supersede！）
//                                    若 expected_version != Task.Version → CAS 失败
//   - task_stop(id, expected_version) → 同样 CAS
//   - task_update(id, {...}, expected_version) → 同样 CAS
```

**改动**：
- `internal/tools/task/` 6 个文件（每工具一个）
- `internal/store/task_store.go` 持久化
- `migrations/` 加 hive_tasks 表

**工期**：2 周

#### 1.4.2 EnterPlanMode / ExitPlanMode

**Why**：让 agent 进入"只读探索"模式，规划好再执行。
**对应**：W6 Permission 模型的 `ModePlan`

**接口**：
```go
// EnterPlanMode 进入 plan mode（agent 只能调 read-only 工具）
//   - read / glob / grep / 其他 read-only tool 允许
//   - bash / write / edit / 任何 mutation 拒绝
//   - 直到 ExitPlanMode 调用退出
```

**工期**：3 天（依赖 W6）

#### 1.4.3 AskUserQuestion

**Why**：结构化询问用户，比自由 prompt 更可控。

**接口**：
```go
// 输入：
//   {
//     "questions": [
//       { "question": "...", "options": [{"label": "A", "description": "..."}, ...] },
//       ...
//     ]
//   }
// 输出：用户选择的 option label
```

**工期**：1 周

#### 1.4.4 其他工具

略（按 spec template 各自展开，工期合计 ~2-3 个月）

### 1.5 Layer 4 W13 总验收

- ✅ 工具数 ≥ 30（含 Task 系统 + Plan Mode + Worktree + AskUserQuestion + ToolSearch 13 个）
- ✅ 每工具按 W4 G2.5 结构化目录组织
- ✅ 每工具有专门 prompt（接 W4 G3.1）
- ✅ Layer 0 metric 完整接入

---

## §2 W14 — ACP 生态接入

### 2.1 Why
三家 ACP 完整生态发现（FINAL-REPORT §5）：
- **Server 角色**（Hive 已有，coder/acp-go-sdk）
- **Backend Runtime 角色**（OpenClaw acpx，Hive 缺）
- **Client 角色**（Hermes copilot_acp_client，Hive 缺）

W14 让 Hive 接入 backend 和 client 两个新角色，**不重新发明协议**，复用 Agent Client Protocol 标准。

### 2.2 接口设计

#### 2.2.1 ACP Backend Runtime（参考 OpenClaw acpx）

`internal/acpbackend/runtime.go`（新建）：

```go
package acpbackend

// Runtime 把 Hive Master Agent 暴露为 ACP backend
//   - 接收 ACP server 发来的 session 请求
//   - 执行 agent loop（与现有 Master 共用 internal/master/）
//   - 通过 ACP 流式回复
type Runtime struct {
    master *master.Master
    config *Config
}

func (r *Runtime) Start(ctx context.Context) error
func (r *Runtime) HandleSession(ctx context.Context, session ACPSession) error

// 与 OpenClaw acpx 的 session 持久化语义对齐：
//   - runtime="acp" 持久 session（可 resume）
//   - resumeSessionId 加载历史
```

#### 2.2.2 ACP Client（参考 Hermes copilot_acp_client）

`internal/acpclient/llm_provider.go`（扩展现有 acpclient）：

```go
package acpclient

// LLMProvider 把 ACP server（如 GitHub Copilot / Codex / OpenClaw）包装为 OpenAI-compatible LLM
//   - Hive 用这个把其他 agent 当作底层 LLM
type LLMProvider struct {
    serverURL string
    transport ACPTransport
}

func (p *LLMProvider) Chat(ctx context.Context, messages []Message, tools []Tool) (Response, error) {
    // 1. 启动 ACP session
    // 2. 把 messages 格式化成单 prompt
    // 3. 收 ACP text chunks
    // 4. 转换回 OpenAI Response 格式
}
```

#### 2.2.3 ACP↔MCP Bridge

`internal/acpbackend/mcp_bridge.go`（参考 OpenClaw acpx mcp-agent-command）：

```go
// MCPBridge 让 ACP backend 可以暴露 MCP tools 给 ACP client
//   - ACP client 看到 Hive 的 MCP tool
//   - Bridge mode: per-session mcpServers 不支持（与 OpenClaw 对齐）
//   - Gateway mode: gateway 级 MCP servers 支持
```

### 2.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/acpbackend/runtime.go` | 新建 |
| `internal/acpbackend/mcp_bridge.go` | 新建 |
| `internal/acpclient/llm_provider.go` | 扩展 |
| `internal/llm/factory.go` | 改：注册 ACPProvider 作为 LLM provider |
| `internal/config/acp.go` | 改：加 backend / client 配置 |

### 2.4 工期：1-2 个 quarter

### 2.5 验收

- ✅ Hive 同时具备 server + backend + client 三个 ACP 角色
- ✅ Hive 通过 ACP 连 GitHub Copilot 当 LLM provider 工作
- ✅ Hive backend runtime 能被 ACP client（如 Zed IDE）连接
- ✅ ACP↔MCP bridge 在 gateway mode 工作

---

## §3 W15 — Multi-agent 协调

### 3.1 Why
- W13 批 1 完成 Task 系统后基础就位
- ~~W14 完成后 ACP 生态可用于 agent 间通信~~

**F16 修复（DAG 解耦）**：W15 拆两阶段，**part 1 不依赖 W14 ACP**：

| 子阶段 | 依赖 | 内容 |
|---|---|---|
| **W15.1 本地 in-process multi-agent**（无 ACP 依赖）| W13 批 1（Task 系统）+ W13 批 3（Team×2 + SendMessage + SyntheticOutput） | Coordinator Mode + INTERNAL_WORKER_TOOLS + ASYNC_AGENT_ALLOWED_TOOLS + 本地 SendMessage 协调（in-process channel）+ Mid-run steering |
| **W15.2 跨进程 / 远程 agent 协作**（依赖 W14 ACP）| W14 ACP 生态 | 把 SendMessage 扩展到跨进程；把 Coordinator 扩展到 spawn 远程 ACP backend agent |

**为什么拆**：codex F16 指出 — 本地 SendMessage 是 in-process channel，**不需要 ACP**。强依赖 W14 是把 quarter 级依赖链人为拉长。80% multi-agent 协调场景在 in-process 即可完成，仅 20% 跨进程场景需要 ACP。

- Hive Master Agent 当前较 ad-hoc，缺 Coordinator Mode + Team 工具

### 3.2 接口设计

#### 3.2.1 Coordinator Mode

`internal/master/coordinator/mode.go`（新建）：

```go
package coordinator

// CoordinatorMode 让 Master Agent 转换为 coordinator 角色
//   - 不直接做事，只 dispatch + monitor
//   - 用 Team 工具创建 sub-agents
//   - 用 SendMessage 工具协调
type CoordinatorMode struct {
    enabled bool
    
    // INTERNAL_WORKER_TOOLS（参照 Claude Code）
    internalWorkerTools []string  // = [TEAM_CREATE, TEAM_DELETE, SEND_MESSAGE, SYNTHETIC_OUTPUT]
    
    // ASYNC_AGENT_ALLOWED_TOOLS 异步 agent 允许的工具子集
    asyncAllowedTools []string
}

// IsCoordinatorMode 检查 feature flag + env var
func IsCoordinatorMode() bool {
    if feature("COORDINATOR_MODE") {
        return os.Getenv("HIVE_COORDINATOR_MODE") == "true"
    }
    return false
}
```

#### 3.2.2 Team 工具（W13 批 3）

```go
// TeamCreate 创建一个新 team（含 N 个 sub-agents）
//   args: { team_name, agents: [{role, tools, ...}] }

// TeamDelete 解散 team

// SendMessage agent 之间通信（不通过 ACP，是 in-process）
//   args: { to_agent_id, content }

// SyntheticOutput coordinator 把 sub-agent 输出合成最终结果
//   args: { agent_outputs, merger_prompt }
```

#### 3.2.3 Mid-run steering（参考 Hermes /steer）

**F15 修复**：pending 不能用裸 map（data race + 顺序未定义 + 多次 /steer 丢/覆盖）。改 per-session queue + mutex + 明确插入点。

`internal/master/steering.go`：

```go
// SteeringInjector 让用户在 agent 跑的过程中注入 guidance
//   - 插入点：当前 tool 完成后的下一轮 planning 前（不打断 tool call）
//   - 多次 /steer 按 FIFO 顺序消费
//   - 并发安全
type SteeringInjector struct {
    mu       sync.Mutex
    sessions map[string]*sessionQueue  // sessionID → 该 session 的 pending queue
}

type sessionQueue struct {
    pending []SteerEntry  // FIFO
}

type SteerEntry struct {
    ID        string
    Prompt    string
    Author    string  // user_id
    QueuedAt  time.Time
}

// Push 用户调 /steer 时
func (s *SteeringInjector) Push(sessionID string, entry SteerEntry) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if _, ok := s.sessions[sessionID]; !ok {
        s.sessions[sessionID] = &sessionQueue{}
    }
    s.sessions[sessionID].pending = append(s.sessions[sessionID].pending, entry)
}

// DrainAll agent 在"当前 tool 完成 + 下一轮 planning 前"调
//   - 一次拿出全部 pending（避免每轮 1 个慢吞吞）
//   - 返回的 entries 按 QueuedAt 排序（FIFO）
//   - 调用方负责把这些 entries 注入下一轮 system message
func (s *SteeringInjector) DrainAll(sessionID string) []SteerEntry {
    s.mu.Lock()
    defer s.mu.Unlock()
    sq, ok := s.sessions[sessionID]
    if !ok || len(sq.pending) == 0 {
        return nil
    }
    drained := sq.pending
    sq.pending = nil  // 清空
    return drained
}
```

**F15 蓝军 mutation**：
- `go test -race` 并发 Push + DrainAll → 无 data race
- 长任务执行中连续 3 次 /steer + 插一无 tool-call 推理轮 → 3 entries 按 FIFO 顺序消费
- /steer 在 tool 执行中 push → 该 tool 不被打断 → tool 完成后下一轮 planning 前消费

### 3.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/master/coordinator/mode.go` | 新建 |
| `internal/master/coordinator/dispatcher.go` | 新建 |
| `internal/tools/team/` | 新建（Team 4 个工具，依赖 W13 批 3）|
| `internal/master/steering.go` | 新建 |

### 3.4 工期：1 个 quarter

### 3.5 验收

- ✅ Master Agent 可切到 Coordinator Mode（feature flag）
- ✅ 能 spawn 多个 sub-agents 并协调
- ✅ Mid-run steering 工作
- ✅ Team 4 工具可用

---

## §4 W16 — GEPA Self-improvement（探索性）

### 4.1 Why
Hermes GEPA（ICLR 2026 Oral）核心：基于 trajectory reflection 自动改进 prompt。
- Hive 当前无 self-improvement 闭环
- 需 Layer 0 trajectory 数据基础（W1 已建）+ Layer 4 model routing（W14）

### 4.2 范围（探索性，spec 简短）

#### 4.2.1 Trajectory 收集（已有 W1 基础）

W1 hive_traces 表已有 ReAct step span，扩展为完整 trajectory 数据。

#### 4.2.2 GEPA 核心算法

参照论文 arXiv 2507.19457：
- 读完整 execution trace（错误 + profiling + reasoning chain）
- LLM-based reflection：分析失败原因 → 提议 prompt 改进
- Pareto-optimization：多目标（质量 / 成本 / 延迟）
- 比 GRPO 高 6-20%，rollouts 少 35x

#### 4.2.3 Hive 集成（需要 spec 进一步细化，W16 启动时再做）

`internal/selfimprove/gepa.go`（新建占位）：

```go
package selfimprove

// GEPA 离线 batch job：每周跑一次
//   - 输入：上周的 trajectory 数据
//   - 输出：prompt 改进建议（人类 review 后接入）
type GEPA struct {
    auxLLM       LLMProvider
    trajectoryDB TrajectoryStore
}

// Reflect 分析 trajectory → 输出改进建议
func (g *GEPA) Reflect(ctx context.Context, since time.Time) ([]Suggestion, error)
```

### 4.3 工期：1-2 个 quarter（探索性）

### 4.4 验收

- ✅ GEPA pipeline 上线
- ✅ 重复任务 agent 跑快 ≥ 30%（measurable，参照 Hermes 数据）
- ⚠️ 这是探索性目标，可能需要多次迭代

---

## §5 完整 16 W spec 索引

| W | Spec 文件 |
|---|---|
| W1 + W2 + W3 | `SPEC-LAYER0-W1-W2-W3.md` |
| W4 | `SPEC-LAYER1-W4.md` |
| W5 + W6 + W7 + W8 | `SPEC-LAYER2-W5-W6-W7-W8.md` |
| W9 + W10 + W11 + W12 | `SPEC-LAYER3-W9-W12.md` |
| W13 + W14 + W15 + W16 | `SPEC-LAYER4-5-W13-W16.md`（本文件） |

---

## §6 总览：施工里程碑回顾

| 月份 | 完成 | 8/8 验收 |
|---|---|---|
| 月 1 | W1 + W2 + W3（Layer 0）| 0/8（基础设施）|
| 月 3 | W4 + W5 + W6 + W7（Layer 1+2 不含飞书）| **2/8** |
| 月 6 | W8 + W9 + W10 + W11 + W12（Layer 3 完成）| **5/8** |
| 月 12 | W13（批 1+2）+ W14 + W15（Layer 4）| **7/8** |
| 月 18 | W13（批 3）+ W16（Layer 5）| **8/8 真正顶级** |

---

## §7 Layer 4-5 联合验收

W13+W14+W15+W16 完成后必须满足：

| 验收 | 检查 |
|---|---|
| 工具数 ≥ 30（W13 批 1+2） | `ls internal/tools/ \| wc -l` |
| 每工具有专门 prompt | grep `prompt.go` in 每工具目录 |
| ACP 3 角色（server + backend + client） | E2E 三角色互通测试 |
| Multi-agent Coordinator Mode 工作 | E2E 测试 |
| GEPA 重复任务跑快 ≥ 30% | benchmark |
| Layer 0 metric 完整接入 | hive_metrics 表 query 全部 |

---

## §8 18 个月后 Hive 状态

满足 8/8 真正顶级 harness 后，Hive 应该具备：

1. ✅ **工具广度**：≥ 30 工具，每个结构化目录 + 专门 prompt
2. ✅ **工具深度**：BashTool 6 层防御 + 100 attack vector mutation 全过
3. ✅ **Permission 完整**：8 层级联 + 5 modes + /approve 三态 + group:* + path-rules
4. ✅ **Memory 治理**：pre-compaction flush + nightly distill + structured summary + 5-provider fallback + bootstrap caps
5. ✅ **Skills**：progressive loading + MCP-as-Skills + token budget
6. ✅ **MCP 生态**：collapse 分类 + mcporter skill + chrome-mcp
7. ✅ **ACP 生态**：server + backend + client 三角色
8. ✅ **Multi-agent**：Coordinator Mode + Team 4 工具 + mid-run steering
9. ✅ **Self-improvement**：GEPA reflection 闭环
10. ✅ **Observability 完整**：每个 check / tool / agent step 都在 hive_metrics + hive_traces 可见
11. ✅ **Spec-driven OpenSpec 真意落地**：artifact 显式可见 + AI 质量 measurable
12. ✅ **ChannelAdapter 抽象**：todos 通过通用事件流可见 + ≥ 1 个 adapter 实现可干预

---

*— End of Layer 4-5 Spec —*
