# Layer 1 施工 Spec：W4 结构化基础 + 扩展现有 EventRenderer

> **依赖**：Layer 0（W1+W2+W3）完成
> **工期**：**1 周**（修订后，撤销 ChannelAdapter 新发明）
> **施工后**：启动 Layer 2（W5+W6+W7 三条并行）
> **日期**：2026-04-27（v1.2 — RE-REVIEW-POST-FEISHU 修订）

---

## §1 W4 总览

W4 是承上启下的"骨架"层 — 把 Layer 2 之后所有工作流的**容器**和**抽象边界**定义出来。三块内容：

1. **G2.5 工具结构化目录改造**：5 核心工具（bash/read/write/edit/web_fetch）从单文件改成结构化目录
2. **G2.4 关注点分离层定义**：把 destructive_warning / permission / attack_defense 三种检查从耦合层拆出
3. **L2B.1 通用 ChannelAdapter interface**：todos 事件流的 channel 解耦层

---

## §2 三块内容详细 spec

### 2.1 工具结构化目录改造（G2.5）

#### Why
Hive 当前每工具是单文件（`internal/tools/{shell,read_file,write_file,edit,webfetch}.go`），包含工具实现 + permission + security + prompt + UI 渲染所有逻辑。这导致：
- W5 BashTool 6 层防御的代码无处可放（堆 internal/security/ 不可复用）
- W6 Permission 改造无清晰 module 边界
- W7 todos UI 与 tool result 渲染耦合

Claude Code 标杆：每工具是子目录（BashTool/ 18 文件）。

#### 接口设计

**5 核心工具的目标结构**（以 bash 为例）：
```
internal/tools/bash/
├── bash.go              # 主入口（实现 Tool interface）
├── prompt.go            # 工具自带 system prompt 段
├── permissions.go       # permission 决策（deny → ask → allow）
├── security.go          # attack defense（heredoc/zsh/process subst 等绕过防御）
├── destructive_warning.go # informational warning（不影响 permission）
├── path_validation.go   # 专门 path 校验（W5 扩展）
├── readonly_mode.go     # readonly 模式校验（W5 扩展）
├── sed_validation.go    # sed 命令校验（W5 扩展）
├── render.go            # 结果渲染（前端 channel 用）
└── bash_test.go         # 单测
```

**Tool interface 不变**（避免 break 现有 caller）：
```go
package tools

type Tool interface {
    Name() string
    Description() string
    JSONSchema() json.RawMessage
    Execute(ctx context.Context, args map[string]any) (Result, error)
}
```

**Module 拆出**为内部子包，不影响外部：
```go
// internal/tools/bash/bash.go
package bash

func New(deps Dependencies) tools.Tool { ... }

type tool struct {
    permEngine    *permissions.Engine
    securityChain *security.Chain
    destructWarn  *destructive_warning.Detector
    pathValidator *path_validation.Validator
    // ...
}
```

#### 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/tools/shell.go` | 重构为 `internal/tools/bash/` 目录 |
| `internal/tools/read_file.go` | 重构为 `internal/tools/read/` 目录 |
| `internal/tools/write_file.go` | 重构为 `internal/tools/write/` 目录 |
| `internal/tools/edit.go` | 重构为 `internal/tools/edit/` 目录 |
| `internal/tools/webfetch.go` | 重构为 `internal/tools/webfetch/` 目录 |
| `internal/tools/registry.go`（如有）| 改 import 路径 |
| 现有所有调用 `tools.NewShellTool()` 等的地方 | 改导入路径 |

**保留**：其他 ~10 个工具（applypatch / batch / browser / feishu_tools 等）保持单文件，**等 W5 验证模式后再扩散**。

#### 测试 plan

- T4.1.1 重构后单测全过（不允许 regression）
- T4.1.2 5 个工具的现有 happy path 行为不变
- T4.1.3 import 路径全部更新（CI grep `tools.NewShellTool` 应返回 0）

#### 工期（R2.2 修复：分批做）

| 子阶段 | 工作 | 工期 |
|---|---|---|
| W4.1 | bash 单工具改造（reference 模式）+ Tool interface 接入测试 | 1 周 |
| W4.2 | read / write / edit / web_fetch 4 工具并行（已有 W4.1 reference） | 1 周 |

**为什么分批**：5 工具一次性改造，任何一个出 regression 都阻塞整体。先验证 bash 模式 OK 后扩散，风险小。

---

### 2.2 关注点分离层定义（G2.4）

#### Why
Hive 当前 19 条 builtin_rules **同时承担**：
- destructive warning（信息提示）
- permission decision（block/ask/allow）
- attack defense（防绕过）

这是 W5 BashTool 6 层防御的最大障碍 — 没有清晰的 module 边界，新增的 attack vector 防御代码不知道放哪。

Claude Code 严格分层：destructive_warning（informational only）≠ bashPermissions（decision）≠ bashSecurity（attack defense）。

#### 接口设计

新增三个内部包（在 `internal/tools/bash/` 内，**不全局**）：

```go
// internal/tools/bash/destructive_warning/detector.go
package destructive_warning

type Warning struct {
    CheckID     observability.CheckID
    Pattern     string
    Description string  // "may discard uncommitted changes"
}

// Detector 仅返回 informational warning，不阻止执行
type Detector interface {
    Detect(command string) []Warning
}

// internal/tools/bash/permissions/engine.go
package permissions

type Decision int
const (
    DecisionAllow Decision = iota
    DecisionAsk
    DecisionDeny
)

type Engine interface {
    Evaluate(ctx context.Context, command string, sessionContext SessionContext) Decision
}

// internal/tools/bash/security/chain.go
package security

type Chain interface {
    // Validate 检查命令是否触发 attack vector，触发即阻止
    Validate(ctx context.Context, command string) error
}
```

**关键约束**：三层独立调用顺序由 bash.go 决定：
```go
func (t *tool) Execute(ctx context.Context, args map[string]any) (Result, error) {
    cmd := args["command"].(string)
    
    // Layer 1: attack defense（最严，最先）
    if err := t.security.Validate(ctx, cmd); err != nil {
        return Result{Blocked: true, Reason: err.Error()}, nil
    }
    
    // Layer 2: permission decision
    decision := t.permissions.Evaluate(ctx, cmd, t.sessionContext)
    if decision == DecisionDeny {
        return Result{Blocked: true, Reason: "permission denied"}, nil
    }
    if decision == DecisionAsk {
        // HITL 路径
    }
    
    // Layer 3: destructive warning（仅 informational，不影响决策）
    warnings := t.destructWarn.Detect(cmd)
    
    // 真正执行
    output, err := t.executor.Run(ctx, cmd)
    return Result{Output: output, Warnings: warnings}, err
}
```

#### 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/tools/bash/destructive_warning/` | 新建（W4 阶段先建空骨架 + interface + 1-2 个示例规则）|
| `internal/tools/bash/permissions/` | 新建（W4 阶段从 `internal/security/builtin_rules.go` 摘出 bash 相关 19 条移入这里）|
| `internal/tools/bash/security/` | 新建（W4 阶段先建空骨架 + interface，**实际 attack defense 在 W5 填充**）|
| `internal/security/builtin_rules.go` | 改：bash 相关规则移到 `bash/permissions/`，保留非 bash 通用规则 |
| `internal/tools/bash/bash.go` | 改：调用三层（按上面顺序）|

**关键**：W4 不实现 W5 的 attack defense 内容，只**搭骨架**。W5 在这个骨架上填实际 attack vector 防御代码。

**R2.3 修复**：W4 必须建好 W5 用到的 4 个 module **空 package 骨架**（含 interface 定义），W5 直接填实现，**不允许新建包**：
- `internal/tools/bash/security/` — chain.go + interface 空骨架
- `internal/tools/bash/path_validation/` — validator.go interface
- `internal/tools/bash/sed_validation/` — validator.go interface
- `internal/tools/bash/readonly_mode/` — validator.go interface

这样 W5 完全聚焦在"填 detector 实现"，不分心搭包结构。

#### 测试 plan

- T4.2.1 三层调用顺序固定（mutation：颠倒顺序应导致测试 fail）
- T4.2.2 destructive warning 只填 Result.Warnings，不影响 Decision（mutation：让 destructive warning 返回 deny 应导致测试 fail）
- T4.2.3 现有 19 条 bash 规则在 `permissions` 包里行为不变（regression）

#### 工期：3 天（与 2.1 工具结构化并行）

---

### 2.3 ~~通用 ChannelAdapter interface（L2B.1）~~ → **扩展现有 EventRenderer**（RE-REVIEW-POST-FEISHU 修订）

#### Why（修订后）

**飞书改造完成揭示 Hive 已有完整 channel interface 体系**（`internal/channel/plugin.go`）：
- `ChannelPlugin` interface（Platform / Send / WebhookHandler / Verify）
- `EventRenderer` interface（继承 ChannelPlugin + RenderEventStream）
- `InboundController` interface
- `RendererError + LastContent` 兜底机制

W4 **不再新建 ChannelAdapter interface**（撤销 NIH 重复发明），改为：
1. **扩展现有 EventRenderer 不动**（飞书已实现完整 763 行）
2. **加 TodoEvent 到 master.BroadcastMessage**（让现有 EventRenderer 渲染 todos）
3. W7 Web Console 实现现有 `EventRenderer` interface（与飞书平等）
4. W8 仅在飞书 `feishuRenderer.dispatchEvent()` 加 `handleTodoEvent()` case

#### 接口设计（修订后）

**复用现有 `internal/channel/plugin.go`**：

```go
// 已存在（不动）
type ChannelPlugin interface {
    Platform() Platform
    Send(ctx, msg OutboundMessage) error
    WebhookHandler() http.HandlerFunc
    Verify(r *http.Request) bool
}

type EventRenderer interface {
    ChannelPlugin
    RenderEventStream(ctx, scope SessionScope, eventCh <-chan master.BroadcastMessage) error
}
```

**W4 不新建任何 channel interface**。Web Console（W7）和未来 channel（CLI / TUI / IDE）都实现现有 `EventRenderer`。

**~~F3 拆 Patcher / Acker capability interface~~ 撤销**：现有 EventRenderer 单方法 RenderEventStream + RendererError 兜底已足够。

**Registry 也不需要新建**：现有 router.go 已管理 plugins。

type HealthStatus struct {
    Healthy   bool
    LastError string
    Latency   time.Duration
}

// Registry 中央注册表，所有 adapter 启动时 register
type Registry interface {
    Register(adapter ChannelAdapter) error
    Get(name string) (ChannelAdapter, bool)
    All() []ChannelAdapter
}
```

#### todos 事件 schema（接现有 master.BroadcastMessage）

**修订**：不新建 `internal/streaming/events.go` events 体系。现有 `internal/master/master.go:77 BroadcastMessage` 已是事件总线类型。新增 todos 类型即可：

```go
// 在 internal/master/event_bus.go 或同级文件加 BroadcastMessage 类型扩展
// （与现有 input_received / message / tool_call / input_request / error / agent_progress 等同级）
const (
    BroadcastTypeTodoCreated   = "todo_created"
    BroadcastTypeTodoUpdated   = "todo_updated"
    BroadcastTypeTodoCompleted = "todo_completed"
    BroadcastTypeTodoBatch     = "todo_batch"  // 一次性 emit 整个 todo list
)

type TodoEvent struct {
    EventID     string
    SessionID   string
    TodoID      string  // 单个 todo 的 ID
    PlanID      string  // 所属 plan
    Status      TodoStatus  // pending / in_progress / completed / cancelled
    Description string
    ParentID    string  // 嵌套 todo 用
    Order       int
    Timestamp   time.Time
    
    // **F8 修复**：单调递增 version + CAS 防 multi-tab/断线重连/乱序投递写坏
    PlanVersion int64  // plan 整体单调版本（每次 plan mutation +1）
    TodoVersion int64  // 单 todo 单调版本（每次 todo update +1）
    
    // 重连 / 增量协议：
    //   - client 启动：先 GET /api/sessions/<sid>/plan/snapshot → 拿到完整 plan + 当前 PlanVersion=N
    //   - 然后订阅 events with ?since_plan_version=N → 只收 PlanVersion>N 的增量
    //   - client 收到 event 时按 TodoVersion CAS update 本地状态（version <= local 时丢弃）
    //   - server 端 update 走 CAS：UPDATE ... WHERE todo_version = expected
}

type TodoStatus string
const (
    TodoStatusPending     TodoStatus = "pending"
    TodoStatusInProgress  TodoStatus = "in_progress"
    TodoStatusCompleted   TodoStatus = "completed"
    TodoStatusCancelled   TodoStatus = "cancelled"
)
```

后端 emit 路径（待 W12 Spec-driven 大重构时**真正使用**，W4 阶段先 emit 占位事件做端到端验证）：
- Master Agent 在生成 plan / 分解 tasks 时 emit `EventTypeTodoBatch`
- 每个 todo 状态变化 emit `EventTypeTodoUpdated`
- W12 阶段把 spec-driven plan 接到这条 emit 路径

#### 改动文件清单（修订后）

| 文件 | 操作 |
|---|---|
| ~~`internal/channels/adapter.go`~~ | ❌ **撤销**（NIH，复用现有 `internal/channel/plugin.go`）|
| ~~`internal/channels/registry.go`~~ | ❌ **撤销**（复用现有 router.go）|
| `internal/master/event_bus.go`（或同级）| 加 4 个 BroadcastType + TodoData 字段到 BroadcastMessage |
| `internal/master/session_loop.go` | 加占位 emit 调用（W4 阶段 stub，W12 真正接入）|
| `internal/channel/feishu/renderer.go` | **不动**（W8 时仅加 dispatchEvent 中 todos case）|
| `internal/channel/router.go` | **不动** |

**关键修订**：W4 不破坏 + 不新增任何 channel 抽象。仅扩展事件类型 + 占位 emit。

**R2.4 channels/channel 两包并存问题 → 不存在了**（不新建 channels 包）。

#### 测试 plan（修订后）

- T4.3.1 BroadcastMessage 加 4 个 todo BroadcastType 编译通过
- ~~T4.3.2 Registry 注册~~（撤销，复用现有 router.go）
- T4.3.3 emit todo BroadcastMessage → 现有 EventBus 转发 → 在 hive_traces 表可见
- T4.3.4 mock EventRenderer 实现接收 → RenderEventStream 内 dispatch todo case 被调用

#### 工期：2 天（撤销新 interface 设计后大幅缩减）

---

## §3 W4 联合验收

W4 完成（2 周后）必须满足：

| 验收项 | 检查方式 |
|---|---|
| 5 核心工具改成结构化目录 | `ls internal/tools/{bash,read,write,edit,webfetch}/` 全为目录 + 各含 ≥3 个 .go 文件 |
| 关注点分离三层骨架到位 | `internal/tools/bash/{destructive_warning,permissions,security}/` 包都建好 + interface 定义 + bash.go 按顺序调用 |
| ChannelAdapter interface 定义 + Registry 工作 | mock adapter 注册 + 收到 emit 的 TodoEvent |
| 现有所有单测全过 | `go test ./...` 不出现 regression |
| Layer 0 metric 接入这些新模块 | hive_metrics 表能 query 到 destructive_warning / permission / security 三层各自的 metric |

---

## §4 风险与缓解

| 风险 | 缓解 |
|---|---|
| 5 工具重构引入 regression | 严格 happy path 单测覆盖 + PR 分批 merge（一个工具一个 PR）|
| ChannelAdapter interface 设计不当导致 W7/W8 返工 | W4 阶段做 mock adapter 端到端验证 + W7 第一个真实实现暴露 interface 不足时及时迭代 |
| 与现有 `internal/channel/` 包冲突 | 命名为 `internal/channels/`（复数）做新抽象层，逐步迁移 |
| 工具结构化扩散到所有 ~15 个工具会拖延 | W4 阶段只做 5 核心，其余等 W5 验证模式后扩散 |

---

## §5 完成后下一步

W4 ship → **Layer 2 三条并行启动**：
- W5 BashTool 工程化（往 `internal/tools/bash/security/` `path_validation/` `sed_validation/` `readonly_mode/` 填实际防御代码）
- W6 Permission 模型升级（往 `internal/tools/bash/permissions/` 填 8 层级联 + modal exec）
- W7 Web Console adapter（实现 ChannelAdapter interface 的第一个具体 channel）

详细 spec 见 `SPEC-LAYER2-W5-W6-W7.md`。

---

*— End of Layer 1 Spec —*
