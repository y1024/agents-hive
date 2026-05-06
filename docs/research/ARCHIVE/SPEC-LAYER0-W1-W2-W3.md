# Layer 0 施工 Spec：W1 + W2 + W3

> **范围**：Hive harness engineering quality 升级 Layer 0（基础设施）
> **依赖**：无（最底层）
> **总工期**：3 周（W1 + W2 并行 2 周，W3 跟进 1 周）
> **施工后**：启动 W4 L1 结构化基础
> **日期**：2026-04-26
> **修订**：v1.1 — 应用 ARCH-REVIEW.md 修复 R1.1/R1.2/R1.3/R1.4/R1.5

---

## §0 三个 W 总览

| W | 主题 | 工期 | 改文件主路径 | 验收门 |
|---|---|---|---|---|
| **W1** | Observability 基础 | 2 周 | `internal/observability/` 扩展 + 各 tool/security 调用点接入 | hive_metrics 表能 query 到每个 check / tool / agent step |
| **W2** | Tool 级 timeout 统一 | 1 周（与 W1 并行）| `internal/tools/timeout.go` 新建 + 各 tool 调用点替换 | timeout mutation test 全过 + 无 goroutine 泄漏 |
| **W3** | Capacity governance 配置化 | 1 周（W1 完成 30% 后） | `internal/config/` 加 capacity section + `react_processor.go` / `subagent/factory.go` / `streaming_executor.go` 读配置 | 超额 spawn 拒绝 + metric 归类正确 + 配置热更生效 |

---

## §1 W1 — Observability 基础

### 1.1 Why now

Hive 现有 `internal/observability/` 已具备 Tracer / MetricsWriter / LogWriter 三个接口（PG-backed，写 hive_traces / hive_metrics / hive_logs 表，nil 安全 + fire-and-forget），但**接入点稀疏**：
- 大多 tool 没有 metric 接入（只看 grep `internal/master/` 部分接入）
- security check 没有数字化 ID（日志用字符串描述，高基数）
- 缺统一的"每个工具调用 / 每次 security check / 每次 agent step"接入规范

W1 不是从零搭，是**完成现有 observability 框架的接入覆盖** + 数字化 check ID。

这是 Layer 0 第一项，**所有上层 W 的 measurable 验收都依赖**：
- W5 BashTool 100 attack vector mutation 验收依赖 metric
- W3 capacity 拒绝率验收依赖 metric
- W7 todos UI P99 推送 latency 验收依赖 metric

### 1.2 接口设计

#### 1.2.1 数字化 Check ID（新增）

`internal/observability/check_ids.go`（新文件）：

```go
package observability

// CheckID 用 uint32 标识每个 security check / decision point
// 避免 logging 字符串高基数（Prom label 爆炸 + DB index 失效）
// **R1.1 修复**：从 uint16 改为 uint32，每段 65536 个 ID 给未来扩展
type CheckID uint32

// 命名空间分配（每段 65536 个 ID）：
//   0x00010000-0x0001FFFF  bash security
//   0x00020000-0x0002FFFF  bash permission
//   0x00030000-0x0003FFFF  path validation
//   0x00040000-0x0004FFFF  sed validation
//   0x00050000-0x0005FFFF  readonly mode
//   0x00060000-0x0006FFFF  capacity (W3 用)
//   0x00070000-0x0007FFFF  spec-driven cognition
//   0x00080000-0x0008FFFF  channel adapter
//   ...
const (
    CheckBashIncomplete       CheckID = 0x00010001
    CheckBashJqSystem         CheckID = 0x00010002
    CheckBashJqFileArgs       CheckID = 0x00010003
    CheckBashZshEqualsExpand  CheckID = 0x00010004
    CheckBashHeredocInSubst   CheckID = 0x00010005
    // ... W5 时按需扩展
    
    CheckCapacitySpawnLimit   CheckID = 0x00060001  // W3 用
    CheckCapacityToolConcur   CheckID = 0x00060002  // W3 用
    CheckCapacityTimeout      CheckID = 0x00060003  // W3 用
)

// String 仅用于 log readability，不进 metric label
func (c CheckID) String() string { ... }
```

**关键约束**：
- CheckID 进 metric **作为数值 label**（不是字符串）
- 每个 ID 在 docs 里有 attack vector 注释（reasoning 文档化）
- 不允许字符串 ad-hoc 上报 metric

#### 1.2.2 扩展 MetricsWriter 接口

`internal/observability/observability.go` 加 helpers：

```go
// RecordCheck 写入一次 check 结果（pass/fail/blocked）
// **R1.2 修复**：session_id 不再进 metric labels（高基数会爆 PG index）
//   - metric label 仅保留有限基数（check_id / result / tool_name 等）
//   - 如需按 session 维度查询，走 hive_traces 表 attributes
func (m *metricsWriter) RecordCheck(ctx context.Context, id CheckID, sessionID string, result CheckResult, durationUs int) {
    // metric: 有限基数 label
    _ = m.Record(ctx, Metric{
        Name:       "hive_check_total",
        Labels:     map[string]string{"check_id": fmt.Sprintf("0x%08x", id), "result": string(result)},
        Value:      1,
        DurationUs: durationUs,
        Ts:         time.Now(),
    })
    // trace: 高基数 attributes（session_id 进这里）
    if span := SpanFromContext(ctx); span != nil {
        span.SetAttributes(map[string]any{
            "check_id":   fmt.Sprintf("0x%08x", id),
            "result":     string(result),
            "session_id": sessionID,
        })
    }
}

// RecordToolCall 写入一次 tool call 完整生命周期
// **R1.2 修复**：session_id 仅在 trace attributes，不进 metric labels
func (m *metricsWriter) RecordToolCall(ctx context.Context, toolName, sessionID string, durationMs int, status ToolCallStatus) { ... }

// RecordAgentStep 写入一次 agent ReAct step（thought / action / observation）
// **R1.2 修复**：同上
func (m *metricsWriter) RecordAgentStep(ctx context.Context, sessionID string, stepType StepType, ...) { ... }

type CheckResult string
const (
    CheckResultPass    CheckResult = "pass"
    CheckResultFail    CheckResult = "fail"
    CheckResultBlocked CheckResult = "blocked"
)
```

#### 1.2.3 Trace 接入扩展

利用现有 `Tracer.StartSpan` + `SpanContext.End`，在以下点位接入：
- 每个 ReAct step → span
- 每个 tool call → child span
- 每个 security check → child-child span（按需，避免过细）
- 每个 channel adapter render → span

### 1.3 改动文件清单

| 文件 | 操作 | 内容 |
|---|---|---|
| `internal/observability/check_ids.go` | 新建 | CheckID 类型 + 命名空间 + 初始 ~10 个 W1-W3 用到的 ID |
| `internal/observability/observability.go` | 扩展 | RecordCheck / RecordToolCall / RecordAgentStep helpers |
| `internal/observability/helpers.go` | 扩展 | 工具函数 |
| `internal/master/react_processor.go` | 改 | runReActLoop 入口加 trace span + 每个 step 接入 RecordAgentStep |
| `internal/master/streaming_executor.go` | 改 | 每个 tool call 加 RecordToolCall |
| `internal/security/exec.go` | 改 | MatchPolicy 调用 RecordCheck（数字化 CheckID） |
| `internal/security/builtin_rules.go` | 改 | 19 条规则各自分配 CheckID |
| `internal/master/session_loop.go` | 改 | session intake / dispatch / continuation resolve 各自加 trace span |
| `migrations/` | 新建 | hive_metrics / hive_traces 表的索引补全（如不够）|

**不改**：任何 channel 实现 / 前端

### 1.4 测试 plan（含蓝军 mutation）

#### Happy path 测试
- T1.1 启动 server → 跑 1 个 ReAct turn → 查 hive_traces 表至少 1 个 root span + ≥3 个 child span（agent step / tool call / security check）
- T1.2 跑 1 个 BashTool 调用（带 destructive pattern）→ 查 hive_metrics 表 hive_check_total 有 CheckBashXXX 计数
- **T1.3（F4 修复）** metric label set 不存在 `session_id`：跑 1000 个不同 session_id 后 query `SELECT DISTINCT key FROM hive_metric_labels`，**结果集不能含 `session_id`**（session 维度只能在 hive_traces.attributes 查）

#### 蓝军 mutation
- M1.1 故意删除某个 RecordCheck 调用 → 该 check 在 metric 不可见 → 测试 fail（验证接入不能漏）
- M1.2 用字符串 ad-hoc 上报 metric（不走 CheckID）→ lint 工具应拒绝（CI gate）
- M1.3 fire-and-forget 写入失败（PG 不可达）→ 主路径不阻塞（已有 nil 安全保证，验证仍成立）
- M1.4 伪造一个新 check 用未分配 CheckID → 编译 fail（CheckID 是 typed const，不允许 magic number）
- M1.5 高负载下（1000 QPS）metric 写入丢失率：< 1%（写入异步 + buffer）

#### CI gate
- 新增 `scripts/check_metric_coverage.sh`：grep `RecordCheck` 调用点数量 vs 预期清单（保证不漏接入）
- 新增 `scripts/lint_no_string_metric.sh`：禁止 ad-hoc 字符串 metric

### 1.5 工期 + 里程碑

| 周 | 里程碑 |
|---|---|
| 第 1 周 | check_ids.go + observability helpers 扩展 + 5 个核心接入点（react_processor / streaming_executor / security 主路径）+ Happy path 测试 T1.1-T1.3 通过 |
| 第 2 周 | 完整接入点覆盖（19 条 builtin_rules / session_loop 全 trace span / channel adapter span 占位）+ 蓝军 mutation M1.1-M1.5 通过 + CI gate 上线 |

### 1.6 验收（测量基准）

- ✅ hive_metrics 表 query 能看到 `hive_check_total{check_id, result, session_id}` 全部 19 条规则
- ✅ hive_traces 表 query 能看到完整 ReAct turn → tool call → security check 三级 span 链
- ✅ metric 命名规范：所有 metric 前缀 `hive_*`，label 集合有限（无字符串爆炸）
- ✅ 写入异步 fire-and-forget：主路径 P99 不变化（< 1ms 影响）
- ✅ 蓝军 mutation 5 条全过
- ✅ CI gate 上线：grep coverage + 字符串 metric lint
- ❌ **不要求**：任何 dashboard 配置（部署期决定）/ Prom 兼容（如要可单独加 exporter）

---

## §2 W2 — Tool 级 timeout 统一

### 2.1 Why now

现状（grep 证据）：
- 每个 tool 自己 hardcode timeout：
  - `spawn_agent.go:121` 30 min
  - `task.go:135` 30 min
  - `websearch.go:137` 30 sec
  - `webfetch.go:112` fetchTimeout（变量）
  - `browser.go:317` timeoutSec（参数）
  - `create_tool.go:234` 5 min（HITL approval）
  - `parallel_dispatch.go:226` timeout（变量）
- **没有统一的默认值 / 最大值机制 + 没有 per-tool override**

风险：
- 30 min 对某些 tool 太长（用户等不了），对某些 tool 太短（长任务被截断）
- 无法在配置层统一调整
- ctx cancel 路径不一致（部分 tool ctx 取消后清理不全）

W2 目标：把 timeout 提升为**工具运行的一等概念**，不再各自硬编码。

### 2.2 接口设计

#### 2.2.1 timeout policy（新建）

`internal/tools/timeout.go`（新文件）：

```go
package tools

import (
    "context"
    "time"
)

// TimeoutPolicy 描述工具 timeout 配置
type TimeoutPolicy struct {
    Default time.Duration // 默认值
    Max     time.Duration // 上限（用户传值不能超过）
    Min     time.Duration // 下限（避免 0 或过短）
}

// DefaultPolicy 全局默认（可被 config / per-tool override）
var DefaultPolicy = TimeoutPolicy{
    Default: 30 * time.Second,
    Max:     30 * time.Minute,
    Min:     1 * time.Second,
}

// PerToolPolicies 按工具名 override
var PerToolPolicies = map[string]TimeoutPolicy{
    "spawn_agent":   {Default: 5 * time.Minute, Max: 30 * time.Minute, Min: 10 * time.Second},
    "task":          {Default: 5 * time.Minute, Max: 30 * time.Minute, Min: 10 * time.Second},
    "websearch":     {Default: 30 * time.Second, Max: 2 * time.Minute, Min: 1 * time.Second},
    "webfetch":      {Default: 30 * time.Second, Max: 2 * time.Minute, Min: 1 * time.Second},
    "browser":       {Default: 60 * time.Second, Max: 10 * time.Minute, Min: 1 * time.Second},
    "shell":         {Default: 5 * time.Minute, Max: 30 * time.Minute, Min: 1 * time.Second},
}

// ResolveTimeout 解析 tool 实际 timeout（参数 → per-tool default → global default）
func ResolveTimeout(toolName string, userValue time.Duration) time.Duration {
    policy := DefaultPolicy
    if p, ok := PerToolPolicies[toolName]; ok {
        policy = p
    }
    if userValue == 0 {
        return policy.Default
    }
    if userValue < policy.Min {
        return policy.Min
    }
    if userValue > policy.Max {
        return policy.Max
    }
    return userValue
}

// **F1 修复（替代旧 R1.3 方案）**：不依赖 cancel 包装上报 metric（依赖 defer 易漏）
//   - 不在 cancel 包装里上报，改在 tool 执行边界统一 observe
//   - 调用方用 RunWithToolTimeout 而非裸 WithToolTimeout

// ResolveContext 仅返回 ctx + cancel（标准 stdlib 行为，无 metric 副作用）
func ResolveContext(parentCtx context.Context, toolName string, userValue time.Duration) (context.Context, context.CancelFunc, time.Duration) {
    timeout := ResolveTimeout(toolName, userValue)
    ctx, cancel := context.WithTimeout(parentCtx, timeout)
    return ctx, cancel, timeout
}

// RunWithToolTimeout 标准执行包装 — 在 tool 边界统一 observe
//   - fn 执行 → 返回 result
//   - 执行后通过 ctx.Err() + 实际耗时判断 cancel cause
//   - 不依赖调用方记得 defer cancel（cancel 在 RunWithToolTimeout 内 defer 调用）
func RunWithToolTimeout[T any](
    parentCtx context.Context,
    toolName string,
    userValue time.Duration,
    sessionID string,
    fn func(ctx context.Context) (T, error),
) (result T, err error) {
    ctx, cancel, timeout := ResolveContext(parentCtx, toolName, userValue)
    defer cancel()
    
    start := time.Now()
    result, err = fn(ctx)
    elapsed := time.Since(start)
    
    // 统一在边界 observe（不依赖任何外部 defer）
    observeToolExecution(ctx, toolName, sessionID, elapsed, timeout, ctx.Err(), parentCtx.Err())
    
    return result, err
}

// observeToolExecution 精确区分 cancel cause，避免 false positive
//   - DeadlineExceeded + parent 仍活 → 真 tool timeout，上报 CheckCapacityTimeout
//   - parent 已 Canceled → user/parent cancel，不报 timeout
//   - elapsed >= timeout 但 err 是别的 → 边界情况，记 audit log
//   - 正常完成（err==nil）→ 上报 CheckResultPass
func observeToolExecution(
    ctx context.Context,
    toolName, sessionID string,
    elapsed, timeout time.Duration,
    ctxErr, parentErr error,
) {
    switch {
    case ctxErr == context.DeadlineExceeded && parentErr == nil:
        // 真 tool timeout
        metricsWriter.RecordCheck(ctx, observability.CheckCapacityTimeout, sessionID,
            observability.CheckResultBlocked, int(elapsed/time.Microsecond))
    case parentErr == context.Canceled:
        // 上游 cancel，不报 timeout
    case ctxErr == nil:
        // 正常完成
        metricsWriter.RecordCheck(ctx, observability.CheckCapacityTimeout, sessionID,
            observability.CheckResultPass, int(elapsed/time.Microsecond))
    }
}

// **关键约束**：
//   - 旧 WithToolTimeout 标 deprecated，所有调用必须改用 RunWithToolTimeout
//   - CI lint 加规则：禁止 internal/tools/ 下直接 context.WithTimeout 也禁止裸 WithToolTimeout
```

#### 2.2.2 配置层（W3 完成后启用）

**R1.5 修复**：W2 timeout 与 W3 capacity 合并到统一 `capacity` 配置 section（避免双源真相）：

```json
{
  "capacity": {
    "spawn": {...},
    "tool_concurrency": {...},
    "tool_timeout": {
      "default_ms": 30000,
      "max_ms": 1800000,
      "min_ms": 1000,
      "per_tool": {
        "spawn_agent": { "default_ms": 300000, "max_ms": 1800000 },
        "task":        { "default_ms": 300000, "max_ms": 1800000 }
      }
    },
    "admission": {...}
  }
}
```

W2 阶段先不接配置（用 const 默认值），**W3 完成后切到读 `capacity.tool_timeout`**（统一 capacity 配置入口）。

### 2.3 改动文件清单

| 文件 | 操作 | 内容 |
|---|---|---|
| `internal/tools/timeout.go` | 新建 | TimeoutPolicy 类型 + DefaultPolicy + PerToolPolicies + ResolveTimeout + WithToolTimeout |
| `internal/tools/timeout_test.go` | 新建 | 单测 + mutation test |
| `internal/tools/spawn_agent.go:121` | 改 | `context.WithTimeout(ctx, 30*time.Minute)` → `WithToolTimeout(ctx, "spawn_agent", userVal, sessionID)` |
| `internal/tools/task.go:135` | 改 | 同上 |
| `internal/tools/websearch.go:137` | 改 | 同上 |
| `internal/tools/webfetch.go:112` | 改 | 同上 |
| `internal/tools/browser.go:317` | 改 | 同上 |
| `internal/tools/create_tool.go:234` | 改 | 同上 |
| `internal/tools/parallel_dispatch.go:226` | 改 | 同上 |
| `internal/tools/tools.go:978` | 改 | 同上 |
| `scripts/lint_no_raw_timeout.sh` | 新建 CI | 禁止 `internal/tools/` 下 ad-hoc `context.WithTimeout`（必须走 WithToolTimeout）|

### 2.4 测试 plan

#### Happy path
- T2.1 各 tool 用默认 timeout 跑 → resolve 出预期值
- T2.2 用户传 timeout 在 [Min, Max] 内 → 用用户值
- T2.3 用户传 timeout < Min → clamp 到 Min
- T2.4 用户传 timeout > Max → clamp 到 Max

#### 蓝军 mutation
- M2.1 timeout 触发后，goroutine 是否泄漏：用 goroutine 数对比（before/after）
- M2.2 ctx cancel 后，子 goroutine 内的 io 操作是否真停止（不只 ctx.Err() 标记）
- M2.3 嵌套 timeout（parent ctx 已 timeout 的情况下子 timeout 是否被覆盖）
- M2.4 用 0 / 负数 / 极大值（math.MaxInt64）作为 userValue → 都被正确 clamp
- M2.5 timeout 触发时 RecordCheck 是否真上报（接 W1 metric）

#### CI gate
- `scripts/lint_no_raw_timeout.sh`：grep `internal/tools/` 下不允许 `context.WithTimeout(` 直接调用（必须用 WithToolTimeout）

### 2.5 工期 + 里程碑

| 周 | 里程碑 |
|---|---|
| 第 1 周 | timeout.go + 8 个 tool 改造 + 单测 + 蓝军 mutation 通过 + CI gate 上线 |

### 2.6 验收

- ✅ 8 个 tool 全部改造为 WithToolTimeout
- ✅ 蓝军 mutation 5 条全过（含 goroutine 泄漏检查）
- ✅ CI gate 上线：禁止 ad-hoc context.WithTimeout
- ✅ timeout 触发时 metric 自动上报（接 W1 CheckCapacityTimeout）
- ✅ P99 验证：高负载下 timeout 误触发率 < 0.1%

---

## §3 W3 — Capacity governance 配置化

### 3.1 Why now

现状（grep 证据）：
- `internal/master/react_processor.go:786` `const maxConcurrentSpawn = 3`（hardcoded）
- `internal/subagent/factory.go:54-55` `maxPerSession=3 / maxGlobal=30`（hardcoded 默认）
- `internal/master/streaming_executor.go:87` `IsConcurrencySafe`（safe tool 概念存在但**无 cap**）
- `internal/tools/spawn_agent.go:121` 30 min 硬编码 timeout
- 缺**统一配置入口** + **超额拒绝 metric** + **admission control**

deer-flow final-verdict P0-6 已识别此项（"capacity governance 配置化"），影响 W5 BashTool 是否敢开 early execution。

### 3.2 接口设计

#### 3.2.1 配置 schema

`config.example.json` 加：
```json
{
  "capacity": {
    "spawn": {
      "max_per_turn": 3,
      "max_per_session": 3,
      "max_global": 30
    },
    "tool_concurrency": {
      "default_max": 5,
      "per_tool": {
        "shell":     { "max": 1 },
        "websearch": { "max": 3 },
        "webfetch":  { "max": 5 }
      }
    },
    "admission": {
      "queue_size": 10,
      "queue_wait_max_ms": 5000,
      "reject_on_full": true
    }
  }
}
```

#### 3.2.2 capacity 模块

`internal/capacity/` 新建包：

```go
package capacity

// Governance 统一管理 capacity 决策
// **R1.4 修复**：用 Permission release closure 模式替代 OnComplete leaky abstraction
//   - 旧：CheckSpawn 后必须 defer OnComplete（容易忘）
//   - 新：RequestSpawn 返回 Permission 含 Release closure（强制可见）
type Governance interface {
    // RequestSpawn 申请一个 spawn_agent 名额，返回 Permission
    //   - 调用方 defer permission.Release()，counter 不会错乱
    //   - panic / early return 也不会泄漏（但 release 不被调时 counter 也不释放，需要监控）
    RequestSpawn(ctx context.Context, sessionID string) Permission
    
    // RequestToolConcurrency 申请一个工具并发名额
    RequestToolConcurrency(ctx context.Context, toolName, sessionID string) Permission
}

// Permission 资源许可（含 release closure）
// **F2 修复**：sync.Once 幂等 release + ctx.Done 撤销排队
//   - Release 多次调用安全（sync.Once 保护）
//   - 排队中 ctx 取消触发自动 release（不要等无限期）
//   - panic 后 recover 再 Release counter 正确归零
type Permission struct {
    Allowed bool
    Reason  string         // 拒绝时填写
    Queued  bool           // 是否进了队列
    WaitMs  int            // 排队等待时间
    
    // release 必须 idempotent + safe
    releaseOnce sync.Once
    releaseFn   func()
}

// Release 幂等释放（多次调用安全）
func (p *Permission) Release() {
    if p == nil || p.releaseFn == nil {
        return
    }
    p.releaseOnce.Do(p.releaseFn)
}

// **F2 续 — 排队请求支持 ctx 撤销**：
//   RequestSpawn 内部实现：
//     1. 立即 try 获取 → Allowed=true，返回 Permission
//     2. 失败但 admission queue 有空 → 进队列，select { case granted: ...; case <-ctx.Done(): return Allowed=false 退出队列 }
//     3. 失败且队列满 → Allowed=false 立即返回（admission policy 决定）
//   排队中 ctx 取消时必须从 queue 中 dequeue（否则永久占位）
//
// **测试规范**（W3 测试 plan 强化）：
//   - mutation: Permission.Release() 调 2 次 → counter 不减 2 次
//   - mutation: 排队中 ctx cancel → counter 立即归还
//   - mutation: panic + recover + Release → counter 归 0
//   - mutation: 1000 并发 RequestSpawn + 部分 panic → 总 counter 守恒

// 实现：基于 config + atomic counter + 队列
type governanceImpl struct {
    config       *Config
    spawnCounters sync.Map  // sessionID → atomic.Int32（per-session）
    globalCounter atomic.Int32
    toolCounters  sync.Map  // toolName → atomic.Int32
    metricsWriter observability.MetricsWriter
}
```

#### 3.2.3 接入点

`internal/master/react_processor.go`：
```go
// 旧：const maxConcurrentSpawn = 3
// 新：RequestSpawn → Permission（含 Release closure）
perm := m.governance.RequestSpawn(ctx, sessionID)
defer perm.Release()  // 即使下面 return / panic 也释放
if !perm.Allowed {
    m.metricsWriter.RecordCheck(ctx, observability.CheckCapacitySpawnLimit, sessionID, observability.CheckResultBlocked, 0)
    return perm.Reason
}
```

`internal/master/streaming_executor.go`：
```go
// 工具调用前
perm := m.governance.RequestToolConcurrency(ctx, toolName, sessionID)
defer perm.Release()  // 强制可见 + early return / panic 安全
if !perm.Allowed {
    m.metricsWriter.RecordCheck(ctx, observability.CheckCapacityToolConcur, sessionID, observability.CheckResultBlocked, perm.WaitMs)
    return errors.New(perm.Reason)
}
```

`internal/subagent/factory.go:54-79`：把 `maxPerSession / maxGlobal` 改成读配置（不再 hardcoded）。

### 3.3 改动文件清单

| 文件 | 操作 | 内容 |
|---|---|---|
| `internal/capacity/governance.go` | 新建 | Governance interface + 实现 + 配置读取 |
| `internal/capacity/governance_test.go` | 新建 | 单测 + mutation |
| `internal/capacity/admission.go` | 新建 | 队列 + 等待 + 拒绝逻辑 |
| `internal/config/config.go` | 改 | 加 Capacity config struct |
| `internal/config/defaults.go` | 改 | 加 capacity 默认值 |
| `config.example.json` | 改 | 加 `capacity` 节 |
| `internal/master/react_processor.go:786` | 改 | 用 governance.CheckSpawn |
| `internal/master/streaming_executor.go:87` | 改 | 加 governance.CheckToolConcurrency |
| `internal/subagent/factory.go:54,55,76,77` | 改 | hardcoded 默认 → 读配置 |
| `internal/observability/check_ids.go`（W1）| 扩展 | 加 CheckCapacitySpawnLimit / CheckCapacityToolConcur |

### 3.4 测试 plan

#### Happy path
- T3.1 单 session 跑 spawn × 3 → 第 4 个被拒 → metric 显示 CheckCapacitySpawnLimit blocked
- T3.2 同时跑 shell × 2 → 第 2 个进入队列（max_concurrent=1）→ 等前一个完成 → 进入执行
- T3.3 配置 reload：动态调整 max_per_session=5 → 第 4-5 个 spawn 立即被允许
- T3.4 全局 max=30 → 30 个 session 各 spawn 1 个 OK，第 31 个被拒

#### 蓝军 mutation
- M3.1 OnComplete 漏掉调用（goroutine panic 后没释放）→ counter 错乱：用 defer 强制 + 蓝军强制 panic 测试
- M3.2 race condition：1000 并发 CheckSpawn → 不能超过 max_global（atomic counter 验证）
- M3.3 配置改 max=0 → 所有调用都被拒（不是死循环）
- M3.4 队列 overflow（admission.queue_size=10 + queue_wait_max_ms=5000）→ 第 11 个立即拒（不等）
- M3.5 reject_on_full=false → overflow 时 fail-open（兜底走默认 max）

#### CI gate
- 无新 lint，依赖 W2 已上线的 `lint_no_raw_timeout.sh`
- 加 `scripts/check_capacity_metrics.sh`：CI 跑 spawn-overflow scenario，检查 metric 上报正确

### 3.5 工期 + 里程碑

| 天 | 里程碑 |
|---|---|
| 第 1-3 天 | governance.go + admission.go + 配置 schema + 单测 |
| 第 4-5 天 | 接入点改造（react_processor / streaming_executor / subagent/factory）+ 蓝军 mutation 通过 |
| 第 6-7 天 | 集成测试 + 配置热更测试 + 文档 |

### 3.6 验收

- ✅ 3 个 hardcoded 数字（maxConcurrentSpawn / maxPerSession / maxGlobal）全部改成读配置
- ✅ tool concurrency 限制可配置（per-tool override）
- ✅ admission control 队列 + 拒绝策略可配置
- ✅ 蓝军 mutation 5 条全过（含 race condition + queue overflow）
- ✅ metric 上报正确：CheckCapacitySpawnLimit / CheckCapacityToolConcur 在 hive_metrics 可见
- ✅ 配置热更：改 config 不重启 server 生效（视基础设施支持，否则 W3 完成 90%）

---

## §4 三 W 联合验收

启动 W1+W2+W3 完成后，以下 mutation **必须全过**才能进入 Layer 1（W4）：

### 联合 mutation 1：BashTool destructive 命令打底测试
跑 `rm -rf /` → 应该被 builtin_rules 拦截 + metric 上报 CheckBashIncomplete blocked + trace span 完整记录决策过程 + W2 timeout 不影响（因为命令立即被拒，不到 timeout）

### 联合 mutation 2：spawn_agent 超额并发
单 session 同时 spawn 4 个 → 第 4 个被 W3 governance 拒 + W1 metric 上报 + W2 timeout 不影响（被拒后不进入 timeout 路径）

### 联合 mutation 3：tool 长任务 timeout
跑一个 spawn_agent 任务（6 分钟模拟长跑）→ W2 timeout 触发（30min default 应足够，不触发） / 改 spawn_agent default 为 1 分钟 → 触发 timeout → W1 metric 上报 + W3 OnComplete 释放资源（counter 不错乱）

---

## §5 风险与缓解

| 风险 | 缓解 |
|---|---|
| W1 接入点漏（某个 tool 没接 RecordToolCall）| `scripts/check_metric_coverage.sh` CI gate + code review checklist |
| W2 timeout 改造引入 regression（某个 tool 行为变化）| 现有单测全跑通 + happy path 4 类用例覆盖 |
| W3 race condition（atomic counter 错乱）| 1000 并发 mutation 测试 |
| W3 配置热更未实现 → 改配置要重启 server | 文档说明 + 当前阶段可接受（重启代价小） |
| 三 W 同时改动多个核心文件，merge conflict | 严格按 PR 顺序：W1 第 1 周完成 + W2 跟进 + W3 最后 |

---

## §6 完成后的下一步

W1+W2+W3 ship → **启动 W4 L1 结构化基础**：
- 工具结构化目录（先 5 核心工具）
- 关注点分离层定义
- 通用 ChannelAdapter interface 设计 + 后端 todos 事件 schema

W4 完成 → W5 BashTool 工程化 + W6 Permission 升级 + W7 Web Console adapter 三条并行启动。

---

## §7 文件索引

```
docs/research/
├── IMPLEMENTATION-PLAN.md       # 总实施计划（本 spec 是 W1-W3 的展开）
├── SPEC-LAYER0-W1-W2-W3.md      # ★ 本文件
├── DEPENDENCY-ORDER.md          # 5 层 DAG（理论依据）
├── GAP-INVENTORY.md             # 全量缺陷清单
└── ...
```

---

*— End of Layer 0 Spec —*
