# Agent 反思与可观测能力补强方案 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` or `executing-plans` to implement this plan task-by-task. Steps use GitHub task checkbox syntax for tracking.

**Goal:** 在不重写 ReAct 主循环、不增加用户审批摩擦的前提下，把 Hive Agent 的反思、trace、replay、测试反馈与 evaluator 能力补齐成一条可观测、可回放、可优化的生产闭环。

**Architecture:** 利用现有 `observability` 自研 PostgreSQL trace/metric/log、`agentquality` 质量事件、`journal` replay、`sessiontodo` Plan Runtime、`toolctx` trace context 继续演进。P0 先修正现有链路语义和反思注入，P1 再补 step snapshot 与 reasoning effort 自动调度，P2 才上 evaluator/test-driven shadow 闭环，避免一开始引入不可控的自动改写。

**Tech Stack:** Go backend, PostgreSQL, existing `internal/observability`, `internal/agentquality`, `internal/journal`, `internal/sessiontodo`, React/Vite frontend, existing admin/replay UI.

---

## 1. 最终结论

这份方案不再把系统描述为“trace 从 0 开始”。当前代码已经具备自研 PG-backed observability：

| 能力 | 当前代码证据 | 结论 |
|---|---|---|
| task span/metric | `internal/master/session_loop.go:317` `:347` | 已有 `hive.task.started/finished` 与 `session.process` |
| LLM span/metric | `internal/master/react_processor.go:548` `:613` | 已有 `llm.call` span、duration、tokens |
| tool span/metric | `internal/master/react_processor.go:1170` `:1342` `:1443` | 已有 `tool.execute` span |
| plan runtime obs | `internal/master/plan_runtime.go:240` `:485` | 已有 `todo_write.execute`、`sessiontodo.guard_decide` |
| quality event | `internal/master/quality_events.go:59` | 已有 metrics + logs + journal decision |
| replay/journal | `internal/journal/journal.go:14` `frontend/src/pages/SessionReplay.tsx:63` | 已有事件回放骨架 |
| delegation event | `internal/tools/delegation.go:11` | 字段存在，但 parent/child trace 多数调用点未填 |

真正缺口是：

| 缺口 | 优先级 | 实施策略 |
|---|---|---|
| 反思只停不改路 | P0 | loop/call failure/guard failure 注入结构化 reflection note |
| trace 语义不统一 | P0 | 统一 span 命名、parent-child 传播、reader API、UI |
| subagent/ACP trace 断链 | P0 | `TaskRequest` 与 delegation event 透传 trace context |
| replay 只能看事件，不能回到状态 | P1 | 先加单调 snapshot checkpoint，再做 fork-from-step |
| reasoning effort 只有手动配置 | P1 | provider capability map + 自动分类 |
| evaluator/test-driven 自动优化风险高 | P2 | 先 shadow 评估，人工 review 后再进入优化建议 |

本方案最终只保留一套最终路线，不保留 v1/v2，也不保留旧版错误前提。

---

## 2. 不再讨论的事项

| 事项 | 最终决定 |
|---|---|
| 是否引入 OpenTelemetry SDK | 本期不引入。继续使用现有 `internal/observability`。文档中统一称“自研 PG trace”，不再误称 OTel。 |
| 是否新增大量审批 | 不新增。只危险命令、删除、外部破坏性操作走已有 HITL/安全策略。计划、反思、trace、replay、shadow evaluator 都默认放行。 |
| 是否自动让 evaluator 改写用户可见输出 | 本期不自动改写。P2 只做 shadow verdict 与优化建议。 |
| 是否单独建设“反思工作台” | 不建设。reflection note 是运行时纠偏信号，只作为 `quality.reflection` 事件出现在运行回放与 Trace 诊断面板里。 |
| step 以什么为准 | P0 trace UI 以 journal event index 只读定位；P1 snapshot 用单调递增的 `snapshot_seq` 作为稳定 step，`message_count` 只作为快照属性。 |
| 是否单独建 `hive_reflection_events` | P0 不建。先扩展 `agentquality.EventName` 增加 `quality.reflection`，继续走 metrics/logs/journal。P1 如查询压力明显再做物化表。 |
| 是否为了 reasoning effort 增加复杂配置 | 不增加复杂配置。默认自动开启，可用单开关关闭。provider 不支持时 no-op。 |

---

## 3. 行业实践校准

| 来源 | 对本方案的约束 |
|---|---|
| OpenTelemetry trace model | trace 是 span DAG，跨边界要传播 SpanContext，采样和保留策略是生产必需项。我们不引 SDK，但遵守 trace/span/parent 语义。 |
| Prometheus metrics practices | metric label 禁止高基数值。当前 `observability.SanitizeMetricLabels` 已过滤 `session_id/user_id/trace_id/span_id`，后续新增指标必须继续遵守。 |
| Anthropic Building Effective Agents | evaluator-optimizer 只适合有明确评分标准的任务。本期先 shadow，不直接替换主循环产物。 |
| OpenAI Agents tracing | agent/generation/function_tool/guardrail/handoff 都应该可追踪。我们映射为 `agent.turn`、`llm.call`、`tool.execute`、`guard.decide`、`delegation.*`。 |
| LangGraph persistence/time travel | time travel 依赖 checkpoint，不是仅靠事件流。P1 必须保存 step snapshot 后才能声称 rewind/fork-from-step。 |
| MCP tools output schema | 工具结构化输出应使用 `outputSchema` 与 `structuredContent` 语义，不自造 incompatible 字段。 |

旧文档中的 “Anthropic 2026-03 Enterprise Agent Playbook”“Claude 4.7”“LangGraph 1.0 2026-Q1 GA”“HumanEval 80→91%” 不作为实施依据。

---

## 4. 目标与非目标

### 4.1 目标

1. 循环、连续失败、guard 拦截不再只是硬停或泛提示，而是产生可追踪的 reflection note。
2. 任一会话能通过 session_id 查到 task、LLM、tool、plan guard、delegation、quality event 的统一时间线。
3. subagent、parallel_dispatch、ACP 的 parent/child trace 可连成 agent tree。
4. replay 页面能从只看 journal 事件升级为 trace + quality + journal 的运行回放与 Trace 诊断面板；不建设独立反思工作台。
5. step snapshot 为后续 fork-from-step、自动优化、失败样本复现打基础。
6. reasoning effort 自动调度默认开启，但 provider 不支持时静默 no-op。
7. evaluator/test-driven 先以 shadow 方式产出质量信号，不自动改写用户可见结果。

### 4.2 非目标

1. 不重写 `runReActLoop`。
2. 不改变 `finish_plan` 作为计划完成的 source of truth。
3. 不把普通工具调用、计划生成、todo 写入改成审批动作。
4. 不引入 OpenTelemetry SDK、Jaeger、Tempo、Prometheus exporter。
5. 不训练 PRM，不做 multi-agent debate，不做 LATS。
6. 不把 replay 做成全量 deterministic replay；本期只做诊断级 checkpoint/fork。

---

## 5. 最终优先级

| 阶段 | 名称 | 目标 | 是否可并行 |
|---|---|---|---|
| P0-A | Trace 语义归一与查询 API | 让已有 PG trace 可查、可串、可展示 | 可与 P0-B 并行 |
| P0-B | Reflection Note 基础闭环 | loop/call failure/guard failure 产生结构化反思 | 可与 P0-A 并行 |
| P0-C | 运行回放与 Trace 诊断面板 | 前端复用 replay/AgentTreeView 展示 trace 与 reflection 事件 | 依赖 P0-A API |
| P1-A | Step Snapshot 与 Fork From Step | replay step 能还原上下文并 fork | 依赖 P0-A/P0-C |
| P1-B | Reasoning Effort Auto | 自动 low/medium/high 调度，默认开启 | 可与 P1-A 并行 |
| P1-C | MCP Output Schema 基础 | 工具可声明 output schema，并记录结构化校验结果 | 可与 P1-A 并行 |
| P2-A | Test-Driven Shadow Feedback | 代码改动后运行最小验证并记录质量事件 | 依赖 P0-B |
| P2-B | Evaluator-Optimizer Shadow | evaluator 给评分和建议，optimizer 只生成候选建议 | 依赖 P2-A |
| P2-C | 自动优化候选接入 | 失败轨迹转 candidate/optimization suggestion | 依赖 P2-B |

---

## 6. P0-A Trace 语义归一与查询 API

### Task P0-A0: observability queue 丢失可观测

**背景:** `internal/master/master.go:845` 的 `enqueueSpan/enqueueMetric/enqueueLog` 在 `obsCh` 队列满时走 `default` 静默丢弃,**没有任何指标**。这与 P0-A5 `MaxSpanPerSession` 主动限流不同:前者是"系统过载意外丢失",用户感知为"trace timeline 莫名缺 span";后者是"已知主动限流"。两类丢失必须分别可观测,否则 P0-A 想做的 "trace 可信完整查询" 在生产高并发下不成立。

统一指标名：

```text
hive.observability.dropped
```

低基数 labels：

| label | values | 含义 |
|---|---|---|
| `kind` | `span` / `metric` / `log` | 被丢弃的 observability entry 类型 |
| `reason` | `obs_queue_full` / `max_span_per_session` | 丢弃原因 |

不使用 `hive.span.dropped`、`hive.metric.dropped`、`hive.log.dropped` 三个独立指标，避免后续新增 entry 类型时扩散指标名。

**Files:**

- Modify: `internal/master/master.go`
- Test: `internal/master/observability_drop_test.go`

- [x] **Step 1: Master struct 增加 atomic counter**

```go
import "sync/atomic"

type Master struct {
    // ...既有字段...
    spansDropped   atomic.Int64
    metricsDropped atomic.Int64
    logsDropped    atomic.Int64
}
```

不用 `enqueueMetric` 自身做 dropped 计数,避免 `obsCh` 满时递归再丢。

- [x] **Step 2: enqueue* default 分支累加**

```go
func (m *Master) enqueueSpan(span observability.Span) {
    if m.obsCh == nil {
        return
    }
    select {
    case m.obsCh <- observabilityEntry{span: &span}:
    default:
        m.spansDropped.Add(1)
    }
}
```

`enqueueMetric` 与 `enqueueLog` 同样修改：

```go
func (m *Master) enqueueMetric(metric observability.Metric) {
    if m.obsCh == nil {
        return
    }
    select {
    case m.obsCh <- observabilityEntry{metric: &metric}:
    default:
        m.metricsDropped.Add(1)
    }
}

func (m *Master) enqueueLog(entry observability.LogEntry) {
    if m.obsCh == nil {
        return
    }
    select {
    case m.obsCh <- observabilityEntry{log: &entry}:
    default:
        m.logsDropped.Add(1)
    }
}
```

- [x] **Step 3: 增加直接 flush helper**

新增 helper，**绕开 obsCh** 直接通过底层 `observability.MetricsWriter` 写入。如果 `metricsWriter == nil`，不能 `Swap(0)`，避免把内存计数清空后仍无落盘。

```go
func (m *Master) flushObservabilityDropped(ctx context.Context) {
    if m.metricsWriter == nil {
        return
    }
    m.flushDroppedCounter(ctx, "span", "obs_queue_full", &m.spansDropped)
    m.flushDroppedCounter(ctx, "metric", "obs_queue_full", &m.metricsDropped)
    m.flushDroppedCounter(ctx, "log", "obs_queue_full", &m.logsDropped)
}

func (m *Master) flushDroppedCounter(ctx context.Context, kind, reason string, counter *atomic.Int64) {
    dropped := counter.Load()
    if dropped <= 0 {
        return
    }
    if !counter.CompareAndSwap(dropped, 0) {
        return
    }
    err := m.metricsWriter.Record(ctx, observability.Metric{
        Name:  "hive.observability.dropped",
        Value: float64(dropped),
        Labels: map[string]any{
            "kind":   kind,
            "reason": reason,
        },
    })
    if err != nil {
        counter.Add(dropped)
    }
}
```

P0-A5 的 `MaxSpanPerSession` 主动限流也用同一指标，写入：

```go
hive.observability.dropped{kind="span",reason="max_span_per_session"}
```

- [x] **Step 4: StartObsWorker 定时与 shutdown flush**

在已有的 `StartObsWorker` 内新增 ticker：

```go
ticker := time.NewTicker(30 * time.Second)
defer ticker.Stop()
```

worker select 增加：

```go
case <-ticker.C:
    m.flushObservabilityDropped(context.Background())
```

`ctx.Done()` drain 完 `obsCh` 后、`close(m.obsDone)` 前，必须再调用一次：

```go
m.flushObservabilityDropped(context.Background())
```

这样短任务结束时不会等不到 30 秒 ticker 而丢掉 dropped 计数。

- [x] **Step 5: 测试 obsCh 满路径**

```go
func TestEnqueueSpanCountsDroppedWhenChannelFull(t *testing.T) {
    m := newTestMaster(t)
    m.obsCh = make(chan observabilityEntry, 1)
    m.obsCh <- observabilityEntry{} // 占满
    m.enqueueSpan(observability.Span{Operation: "test"})
    if got := m.spansDropped.Load(); got != 1 {
        t.Fatalf("spansDropped = %d, want 1", got)
    }
}
```

补充测试：

```go
func TestEnqueueMetricAndLogCountDroppedWhenChannelFull(t *testing.T) {
    m := newTestMaster(t)
    m.obsCh = make(chan observabilityEntry, 1)
    m.obsCh <- observabilityEntry{}
    m.enqueueMetric(observability.Metric{Name: "x"})
    m.enqueueLog(observability.LogEntry{Message: "x"})
    if got := m.metricsDropped.Load(); got != 1 {
        t.Fatalf("metricsDropped = %d, want 1", got)
    }
    if got := m.logsDropped.Load(); got != 1 {
        t.Fatalf("logsDropped = %d, want 1", got)
    }
}
```

```go
func TestFlushObservabilityDroppedWritesDirectMetric(t *testing.T) {
    writer := &recordingMetricsWriter{}
    m := newTestMaster(t)
    m.SetMetricsWriter(writer)
    m.spansDropped.Store(2)
    m.flushObservabilityDropped(context.Background())
    got := writer.metrics[0]
    if got.Name != "hive.observability.dropped" || got.Value != 2 {
        t.Fatalf("metric = %#v", got)
    }
    if got.Labels["kind"] != "span" || got.Labels["reason"] != "obs_queue_full" {
        t.Fatalf("labels = %#v", got.Labels)
    }
}
```

```go
func TestFlushObservabilityDroppedKeepsCounterWhenWriterNil(t *testing.T) {
    m := newTestMaster(t)
    m.spansDropped.Store(3)
    m.flushObservabilityDropped(context.Background())
    if got := m.spansDropped.Load(); got != 3 {
        t.Fatalf("spansDropped = %d, want 3", got)
    }
}
```

```go
func TestStartObsWorkerFlushesDroppedOnShutdown(t *testing.T) {
    writer := &recordingMetricsWriter{}
    m := newTestMaster(t)
    m.SetMetricsWriter(writer)
    ctx, cancel := context.WithCancel(context.Background())
    m.StartObsWorker(ctx)
    m.spansDropped.Store(1)
    cancel()
    <-m.obsDone
    if len(writer.metrics) == 0 {
        t.Fatal("expected shutdown flush metric")
    }
}
```

- [x] **Step 6: Grafana 告警建议**

当前 metrics 存在 PostgreSQL `hive_metrics`，不是 Prometheus 原生 counter。Grafana SQL panel/alert 用：

```sql
SELECT COALESCE(SUM(value), 0) AS dropped
FROM hive_metrics
WHERE name = 'hive.observability.dropped'
  AND labels->>'reason' = 'obs_queue_full'
  AND ts >= NOW() - INTERVAL '5 minutes';
```

`dropped > 0` 先作为 warn；如果高并发场景噪声过大，再调成 `dropped > 10`。`reason="max_span_per_session"` 不告警，只用于说明 trace 被主动限流。

- [x] **Step 7: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run 'TestEnqueue.*Dropped|TestFlushObservabilityDropped|TestStartObsWorkerFlushesDroppedOnShutdown' -count=1`

Expected: `PASS`

### Task P0-A1: 定义统一 trace 视图类型

**Files:**

- Create: `internal/observability/trace_view.go`
- Test: `internal/observability/trace_view_test.go`

- [x] **Step 1: 创建 trace view 类型**

```go
package observability

import "time"

type TraceTimeline struct {
	SessionID string          `json:"session_id"`
	TraceID   string          `json:"trace_id,omitempty"`
	Items     []TraceTimelineItem `json:"items"`
	AgentTree []AgentTraceNode `json:"agent_tree,omitempty"`
}

type TraceTimelineItem struct {
	Kind         string         `json:"kind"` // span | quality_event
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id,omitempty"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Operation    string         `json:"operation,omitempty"`
	Service      string         `json:"service,omitempty"`
	Status       string         `json:"status,omitempty"`
	DurationMs   int            `json:"duration_ms,omitempty"`
	Timestamp    time.Time      `json:"timestamp"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type AgentTraceEdge struct {
	ParentTraceID string `json:"parent_trace_id,omitempty"`
	ChildTraceID  string `json:"child_trace_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	AgentType     string `json:"agent_type,omitempty"`
	GroupID       string `json:"group_id,omitempty"`
}

type AgentTraceNode struct {
	TraceID   string           `json:"trace_id"`
	AgentID   string           `json:"agent_id,omitempty"`
	AgentType string           `json:"agent_type,omitempty"`
	Children  []AgentTraceNode `json:"children,omitempty"`
}
```

P0 trace API 不返回 `journal` kind。Journal 已有 `GET /api/v1/sessions/{id}/journal`，由前端 Replay 页面合并展示，避免一个新接口重复承担 journal 职责。

- [x] **Step 2: 添加排序 helper**

```go
package observability

import "sort"

func SortTraceTimelineItems(items []TraceTimelineItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Timestamp.Before(items[j].Timestamp)
	})
}
```

- [x] **Step 3: 写测试**

```go
package observability

import (
	"testing"
	"time"
)

func TestSortTraceTimelineItems(t *testing.T) {
	t2 := time.Date(2026, 5, 6, 10, 0, 2, 0, time.UTC)
	t1 := time.Date(2026, 5, 6, 10, 0, 1, 0, time.UTC)
	items := []TraceTimelineItem{{Operation: "b", Timestamp: t2}, {Operation: "a", Timestamp: t1}}
	SortTraceTimelineItems(items)
	if items[0].Operation != "a" {
		t.Fatalf("first operation = %q, want a", items[0].Operation)
	}
}
```

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/observability -run TestSortTraceTimelineItems -count=1`

Expected: `PASS`

### Task P0-A2: 增加 PG trace reader

**Files:**

- Modify: `internal/observability/pg_writer.go`
- Create: `internal/observability/pg_reader_test.go`

- [x] **Step 1: 添加 reader interface 与实现**

```go
type TraceReader interface {
	GetSessionTimeline(ctx context.Context, sessionID string, limit int) (TraceTimeline, error)
}
```

在 `PgTracer` 上实现：

```go
func (t *PgTracer) GetSessionTimeline(ctx context.Context, sessionID string, limit int) (TraceTimeline, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := t.pool.Query(ctx, `
		WITH span_items AS (
			SELECT 'span' AS kind, trace_id, span_id, COALESCE(parent_span_id, '') AS parent_span_id,
			       operation, service, status, duration_ms, COALESCE(attributes, '{}'::jsonb) AS attributes, ts, id
			FROM hive_traces
			WHERE session_id = $1
		), quality_items AS (
			SELECT 'quality_event' AS kind, trace_id, COALESCE(span_id, '') AS span_id, '' AS parent_span_id,
			       message AS operation, 'agentquality' AS service, 'ok' AS status, 0 AS duration_ms,
			       COALESCE(attributes, '{}'::jsonb) AS attributes, ts, id
			FROM hive_logs
			WHERE session_id = $1 AND message LIKE 'quality.%'
		)
		SELECT kind, trace_id, span_id, parent_span_id, operation, service, status, duration_ms, attributes, ts
		FROM (
			SELECT * FROM span_items
			UNION ALL
			SELECT * FROM quality_items
		) AS items
		ORDER BY ts ASC, id ASC
		LIMIT $2`, sessionID, limit)
	if err != nil {
		return TraceTimeline{}, err
	}
	defer rows.Close()

	out := TraceTimeline{SessionID: sessionID}
	for rows.Next() {
		var item TraceTimelineItem
		var attrs []byte
		if err := rows.Scan(&item.Kind, &item.TraceID, &item.SpanID, &item.ParentSpanID, &item.Operation, &item.Service, &item.Status, &item.DurationMs, &attrs, &item.Timestamp); err != nil {
			return TraceTimeline{}, err
		}
		_ = json.Unmarshal(attrs, &item.Attributes)
		if out.TraceID == "" {
			out.TraceID = item.TraceID
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
```

- [x] **Step 2: 测试 empty session 与 quality event 聚合**

```go
func TestPgTracerGetSessionTimelineEmpty(t *testing.T) {
	tracer := &PgTracer{querier: fakeTimelineQuerier{rows: newFakeTimelineRows()}, logger: zap.NewNop()}
	got, err := tracer.GetSessionTimeline(context.Background(), "session-1", 0)
	require.NoError(t, err)
	require.Empty(t, got.Items)
}
```

实际测试落在 `internal/observability/pg_writer_test.go`，覆盖 empty session、默认 limit、`hive_logs.message LIKE 'quality.%'` 被映射为 `Kind="quality_event"`，以及 delegation quality event 构建 agent trace tree。

- [x] **Step 3: 验证编译**

Run: `env GOCACHE=/tmp/go-build go test ./internal/observability -count=1`

Expected: `PASS`

### Task P0-A3: API 暴露 session trace

**Files:**

- Modify: `internal/api/server.go`
- Modify: `internal/api/routes.go`
- Create: `internal/api/session_trace_handlers.go`
- Create: `internal/api/session_trace_handlers_test.go`

- [x] **Step 1: Server 增加 traceReader 字段**

在 `Server` struct 增加：

```go
traceReader observability.TraceReader
```

新增 setter：

```go
func (s *Server) SetTraceReader(reader observability.TraceReader) {
	s.traceReader = reader
}
```

`NewServer` 中如果 `db` 是 `*store.PostgresStore`，设置：

```go
s.traceReader = observability.NewPgTracer(pgStore.Pool(), logger)
```

- [x] **Step 2: 增加路由**

在 `internal/api/routes.go` session routes 附近添加：

```go
mux.HandleFunc("GET /api/v1/sessions/{id}/trace", s.handleGetSessionTrace)
```

- [x] **Step 3: handler**

**权限与数据源边界:**

`session trace` 是诊断、审计和回放入口,不是运行时内存调试入口。handler 必须先通过 `s.master.GetSessionByID(r.Context(), sessionID)` 读取可授权的 `store.SessionRecord`,再调用 `s.checkSessionOwnership`。不要在 API 层依赖 `Master.GetSession` 或 `SessionManager.GetSession` 这类裸内存 getter。

原因:

- `user_id` 的权威来源是持久化 session record。内存里的 `SessionState.UserID` 可能为空、滞后,或者只代表当前进程的 live 状态。
- `traceReader.GetSessionTimeline` 本身按 `session_id` 从 `hive_traces` / `hive_logs` 查询持久化诊断数据,与 store-backed session 读取保持一致。
- 越权场景必须在调用 trace reader 之前结束,避免诊断接口泄露 session 是否存在或触发额外 DB 查询。
- `GetSession` 只允许用于 session loop、终止态检查、运行时内部协同等 live state 场景。trace、ownership、audit、replay API 都走 `GetSessionByID` 或等价的持久化授权读口。

```go
func (s *Server) handleGetSessionTrace(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要会话 ID", Code: errs.CodeBadRequest})
		return
	}
	sess, err := s.master.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "会话不存在或无权访问", Code: errs.CodeNotFound})
		return
	}
	if !s.checkSessionOwnership(w, r, sess) {
		return
	}
	if s.traceReader == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "trace reader 未初始化", Code: errs.CodeUnavailable})
		return
	}
	limit := parseSessionTraceLimit(r.URL.Query().Get("limit"))
	timeline, err := s.traceReader.GetSessionTimeline(r.Context(), sessionID, limit)
	if err != nil {
		s.logger.Error("读取 session trace 失败", zap.String("session_id", sessionID), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "读取 session trace 失败", Code: errs.CodeStoreReadFailed})
		return
	}
	writeJSON(w, http.StatusOK, timeline)
}

func parseSessionTraceLimit(raw string) int {
	limit := 2000
	if raw == "" {
		return limit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return limit
	}
	if n > 2000 {
		return 2000
	}
	return n
}
```

- [x] **Step 4: handler test**

测试创建 session 时必须走 `m.CreateSession(...)` 或直接写入 `store.SessionRecord`,不要只调用 `m.GetOrCreateSession(...)` 后断言内存里存在。这样测试才能覆盖真实的 ownership 来源,避免把 API 绑到裸内存 getter。

用 fake reader 断言：

```go
type fakeTraceReader struct {
	gotSession string
	gotLimit int
	timeline observability.TraceTimeline
	err error
}

func (f *fakeTraceReader) GetSessionTimeline(_ context.Context, sessionID string, limit int) (observability.TraceTimeline, error) {
	f.gotSession = sessionID
	f.gotLimit = limit
	return f.timeline, f.err
}
```

测试必须覆盖：

- 成功路径返回 `session_id`、`items`。
- session 不存在时返回 404。
- 当前用户无权访问时返回 403，且不调用 `traceReader`。
- `limit=5000` 被截断为 2000。
- trace reader 未初始化时返回 503。

- [x] **Step 5: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/api -run TestHandleGetSessionTrace -count=1`

Expected: `PASS`

### Task P0-A4: subagent/ACP trace context 透传

**Files:**

- Modify: `internal/subagent/base.go`
- Modify: `internal/master/master.go`
- Modify: `internal/tools/spawn_agent.go`
- Modify: `internal/tools/parallel_dispatch.go`
- Modify: `internal/acpclient/remote_agent.go`
- Modify: `internal/acpserver/agent.go`
- Test: `internal/master/delegation_quality_test.go`
- Test: `internal/acpclient/quality_test.go`
- Test: `internal/tools/delegation_observer_test.go`

- [x] **Step 1: TaskRequest 增加 trace 字段**

```go
type TaskRequest struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SessionID     string          `json:"session_id,omitempty"`
	UserID        string          `json:"user_id,omitempty"`
	TraceID       string          `json:"trace_id,omitempty"`        // 当前 child agent trace
	ParentSpanID  string          `json:"parent_span_id,omitempty"`  // 发起 delegation 的 tool span
	ParentTraceID string         `json:"parent_trace_id,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}
```

- [x] **Step 2: ExecuteTask 填充 child trace**

在 `internal/master/master.go` 构造 `TaskRequest` 处读取：

```go
tc := toolctx.GetToolContext(ctx)
taskReq.ParentTraceID = tc.TraceID
taskReq.ParentSpanID = tc.SpanID
taskReq.TraceID = deriveChildTraceID(tc.TraceID, agentID)
```

新增 helper，避免各调用点拼接规则漂移：

```go
func deriveChildTraceID(parentTraceID, agentID string) string {
	if parentTraceID == "" {
		return agentID
	}
	if agentID == "" {
		return parentTraceID + ":unknown-agent"
	}
	return parentTraceID + ":" + agentID
}
```

- [x] **Step 3: DelegationEvent 填充 parent/child**

在 `spawn_agent` 和 `parallel_dispatch` 内所有 `observer.RecordDelegation` 处读取 `toolctx.GetToolContext(ctx)`：

```go
tc := toolctx.GetToolContext(ctx)
ParentTraceID: tc.TraceID,
ChildTraceID:  deriveChildTraceID(tc.TraceID, agentID),
```

这样不复用父 trace，也不新增随机 trace，能稳定连接 parent/child。

- [x] **Step 4: ACP 记录 trace**

`RemoteACPAgent.recordDelegation` 从 `TaskRequest` 填：

```go
ParentTraceID: req.ParentTraceID,
ChildTraceID:  req.TraceID,
```

ACP server 新会话没有 parent 时允许为空，但 status=`started` 必须保留 session_id；如果 `req.TraceID` 为空，使用 `deriveChildTraceID(req.ParentTraceID, a.ID())` 补齐。

- [x] **Step 5: 测试 delegation trace coverage**

新增断言：

```go
require.Equal(t, "trace-parent", got.Delegation.ParentTraceID)
require.Contains(t, got.Delegation.ChildTraceID, "agent")
```

- [x] **Step 6: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master ./internal/tools ./internal/acpclient ./internal/acpserver -run 'Test.*Delegation|Test.*Quality' -count=1`

Expected: `PASS`

### Task P0-A5: trace retention 与采样边界

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/master/master.go`
- Test: `internal/master/session_loop_test.go`

- [x] **Step 1: 增加配置结构**

```go
type ObservabilityConfig struct {
	Tracing TracingConfig `json:"tracing,omitempty"`
}

type TracingConfig struct {
	Enabled           bool    `json:"enabled,omitempty"`
	SampleRate        float64 `json:"sample_rate,omitempty"`
	MaxSpanPerSession int     `json:"max_span_per_session,omitempty"`
}
```

在 `AgentConfig` 增加：

```go
Observability ObservabilityConfig `json:"observability,omitempty"`
```

默认：

```go
Observability: ObservabilityConfig{
	Tracing: TracingConfig{Enabled: true, SampleRate: 1.0, MaxSpanPerSession: 2000},
},
```

- [x] **Step 2: enqueueSpan 加本地上限**

在 `Master` 增加 session span counter map，按 `SessionID` 计数。`MaxSpanPerSession <= 0` 时使用 2000。

行为：

```go
if !m.tracingEnabled() {
	return
}
if !m.tryCountSessionSpan(span.SessionID) {
	return
}
```

- [x] **Step 3: 测试超过上限丢弃**

构造 `MaxSpanPerSession=1`，连续 `enqueueSpan` 两次，断言 obsCh 只有 1 条。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestObservabilityTraceLimit -count=1`

Expected: `PASS`

---

## 7. P0-B Reflection Note 基础闭环

### Task P0-B0: Reflection 配置默认开启

**Files:**

- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [x] **Step 1: 增加配置结构**

```go
type ReflectionConfig struct {
	Enabled          bool                 `json:"enabled,omitempty"`
	TestDrivenShadow ReflectionShadowConfig `json:"test_driven_shadow,omitempty"`
	EvaluatorShadow  ReflectionShadowConfig `json:"evaluator_shadow,omitempty"`
}

type ReflectionShadowConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}
```

在 `AgentConfig` 增加：

```go
Reflection ReflectionConfig `json:"reflection,omitempty"`
```

- [x] **Step 2: 默认值**

服务器默认值与 CLI 默认值都设置：

```go
Reflection: ReflectionConfig{
	Enabled: true,
	TestDrivenShadow: ReflectionShadowConfig{Enabled: false},
	EvaluatorShadow: ReflectionShadowConfig{Enabled: false},
},
```

P0 只默认开启 reflection note。`test_driven_shadow` 和 `evaluator_shadow` 属于 P2，涉及命令执行和额外 LLM 成本，默认关闭；P2 实施完成并验收后再显式开启。

- [x] **Step 3: 测试**

断言 `DefaultConfig().Agent.Reflection.Enabled == true`，`CLIDefaults()` 后也为 true；同时断言 `TestDrivenShadow.Enabled == false`、`EvaluatorShadow.Enabled == false`。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/config -run TestReflectionConfigDefaults -count=1`

Expected: `PASS`

### Task P0-B1: 增加 quality.reflection 事件类型

**Files:**

- Modify: `internal/agentquality/types.go`
- Test: `internal/agentquality/types_test.go`

- [x] **Step 1: 增加事件名**

```go
EventReflection EventName = "quality.reflection"
```

- [x] **Step 2: 增加结构**

```go
type Reflection struct {
	Trigger        string   `json:"trigger,omitempty"` // batch_loop | call_failure | guard_failure | validation_failure
	Severity       string   `json:"severity,omitempty"` // info | warn | hard_stop
	ToolName       string   `json:"tool_name,omitempty"`
	Consecutive    int      `json:"consecutive,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Recommended    []string `json:"recommended,omitempty"`
	Injected       bool     `json:"injected,omitempty"`
}
```

在 `Event` 加：

```go
Reflection Reflection `json:"reflection,omitempty"`
```

- [x] **Step 3: MetricLabels 支持 trigger/severity**

仅允许低基数字段：

```go
if ev.Name == EventReflection {
	if ev.Reflection.Trigger != "" { labels["reflection_trigger"] = ev.Reflection.Trigger }
	if ev.Reflection.Severity != "" { labels["severity"] = ev.Reflection.Severity }
}
```

- [x] **Step 4: 测试 label 低基数**

断言 `MetricLabels(Event{Name: EventReflection, FinalStatus: StatusNeedsUser, Reflection: Reflection{Trigger:"batch_loop", Severity:"warn"}})` 包含 `reflection_trigger=batch_loop`、`severity=warn`，并保留原有 `status=needs_user`。

- [x] **Step 5: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/agentquality -run TestReflection -count=1`

Expected: `PASS`

### Task P0-B2: Reflection note builder

**Files:**

- Create: `internal/master/reflection_note.go`
- Create: `internal/master/reflection_note_test.go`

- [x] **Step 1: 实现 note 类型**

```go
package master

import (
	"fmt"
	"strings"
)

type reflectionNoteInput struct {
	Trigger     string
	Severity    string
	ToolName    string
	Consecutive int
	Detail      string
}

func buildReflectionSystemNote(in reflectionNoteInput) string {
	trigger := strings.TrimSpace(in.Trigger)
	if trigger == "" {
		trigger = "unknown"
	}
	switch trigger {
	case "batch_loop":
		return fmt.Sprintf("[系统反思] 检测到相同工具组合连续出现 %d 次。请先说明重复原因，然后换一种策略；如果没有可行路径，直接向用户说明阻塞点，不要继续重复相同工具参数。", in.Consecutive)
	case "call_failure":
		return fmt.Sprintf("[系统反思] 工具 %s 使用相同参数连续失败 %d 次。下一步必须先验证前置条件、调整参数或换工具；不要再次调用同一 tool+args。", in.ToolName, in.Consecutive)
	case "guard_failure":
		return "[系统反思] 当前回答被质量护栏拦截。下一步必须补证据、调用必要工具或明确无法完成，不能直接复述被拦截内容。"
	case "validation_failure":
		return "[系统反思] 当前产物未通过后置验证。下一步必须根据验证错误修正证据链或输出结构。"
	default:
		return "[系统反思] 当前执行路径没有取得进展。请总结阻塞原因并改变策略。"
	}
}
```

- [x] **Step 2: 测试不为空且不泄漏大段 detail**

```go
func TestBuildReflectionSystemNoteBatchLoop(t *testing.T) {
	got := buildReflectionSystemNote(reflectionNoteInput{Trigger: "batch_loop", Consecutive: 3})
	if !strings.Contains(got, "连续出现 3 次") {
		t.Fatalf("note = %q", got)
	}
}
```

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestBuildReflectionSystemNote -count=1`

Expected: `PASS`

### Task P0-B3: loopDetector 使用 reflection note

**Files:**

- Modify: `internal/master/react_processor.go`
- Test: `internal/master/react_processor_test.go`
- Test: `internal/master/reflection_note_test.go`

- [x] **Step 1: warn 分支注入结构化 note**

替换当前泛警告：

```go
note := buildReflectionSystemNote(reflectionNoteInput{
	Trigger: "batch_loop",
	Severity: "warn",
	Consecutive: detector.consecutiveSame,
})
m.appendSessionMessage(session, llm.MessageWithTools{
	Role:      "system",
	Content:   llm.NewTextContent(note),
	CreatedAt: time.Now().Format(time.RFC3339),
	Metadata:  map[string]string{"agent_id": "master", "reflection_trigger": "batch_loop"},
})
m.emitQualityEvent(sessionTraceID, sessionSpanID, session.ID, agentquality.Event{
	Name: agentquality.EventReflection,
	Route: routeFromSession(session),
	FailureType: agentquality.FailureRuntime,
	FinalStatus: agentquality.StatusNeedsUser,
	Reflection: agentquality.Reflection{
		Trigger: "batch_loop",
		Severity: "warn",
		Consecutive: detector.consecutiveSame,
		Summary: "same tool batch repeated",
		Recommended: []string{"change_strategy", "ask_user_if_blocked"},
		Injected: true,
	},
})
```

- [x] **Step 2: hard_stop 分支也记录 reflection event**

保持 5 次硬停安全网，但返回前记录 `Severity: "hard_stop"`，并把用户可见错误文案改成具体阻塞原因。

- [x] **Step 3: 调用级连续失败注入 note**

在 `recordCallResult` 达阈值时，除 terminal cache 外追加 reflection note：

```go
note := buildReflectionSystemNote(reflectionNoteInput{
	Trigger: "call_failure",
	Severity: "warn",
	ToolName: toolCall.Name,
	Consecutive: 2,
})
```

注意：必须在 tool result 写入后注入，保持 OpenAI tool-call protocol 顺序。

- [x] **Step 4: 测试消息顺序**

复用现有 ordering 测试，断言 sequence：

```text
assistant(tool_calls) -> tool(result) -> system(reflection)
```

- [x] **Step 5: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run 'TestLoopDetector|TestReflection|Test.*Ordering' -count=1`

Expected: `PASS`

### Task P0-B4: 异常 guard failure 产生 reflection event

**Files:**

- Modify: `internal/master/react_processor.go`
- Modify: `internal/master/plan_runtime.go`
- Test: `internal/master/tool_choice_detector_test.go`
- Test: `internal/master/plan_runtime_test.go`

- [x] **Step 1: required-zero-tool retry/fail 记录 reflection**

在 `requiredGuardRetry` 与 `requiredGuardFail` 分支调用：

```go
m.emitQualityEvent(sessionTraceID, sessionSpanID, session.ID, agentquality.Event{
	Name: agentquality.EventReflection,
	Route: routeFromSession(session),
	FailureType: agentquality.FailureTool,
	RetryReason: "required_zero_tool",
	FinalStatus: agentquality.StatusNeedsUser,
	Reflection: agentquality.Reflection{
		Trigger: "guard_failure",
		Severity: "warn",
		Summary: "tool_choice required returned no tool calls",
		Recommended: []string{"call_required_tool", "ask_user_if_tool_unavailable"},
		Injected: action == requiredGuardRetry,
	},
})
```

- [x] **Step 2: PostValidation failure 记录 reflection**

PlanRuntimeGuard 的 `paused`、open todos remain、`PlanStatusPlanning` 都是正常计划状态，不写 `quality.reflection`。

只在异常护栏失败时写 reflection：

```go
m.emitQualityEvent(sessionTraceID, sessionSpanID, session.ID, agentquality.Event{
	Name: agentquality.EventReflection,
	Route: routeFromSession(session),
	FailureType: agentquality.FailureModel,
	RetryReason: "grounding_validation",
	FinalStatus: agentquality.StatusFail,
	Reflection: agentquality.Reflection{
		Trigger: "validation_failure",
		Severity: "warn",
		Summary: "post validation blocked assistant output",
		Recommended: []string{"add_evidence", "call_required_tool", "state_limitation"},
		Injected: false,
	},
})
```

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run 'TestRequiredGuard|TestPostValidation|TestPlanRuntimeGuard' -count=1`

Expected: `PASS`

---

## 8. P0-C 运行回放与 Trace 诊断面板

### Task P0-C1: 前端类型和 API

**Files:**

- Modify: `frontend/src/types/journal.ts`
- Modify: `frontend/src/api/node-client.ts`
- Test: `frontend/src/api/__tests__/node-client.test.ts`

- [x] **Step 1: 增加 Trace 类型**

```ts
export interface TraceTimelineItem {
  kind: 'span' | 'quality_event';
  trace_id?: string;
  span_id?: string;
  parent_span_id?: string;
  operation?: string;
  service?: string;
  status?: string;
  duration_ms?: number;
  timestamp: string;
  attributes?: Record<string, unknown>;
}

export interface AgentTraceNode {
  trace_id: string;
  agent_id?: string;
  agent_type?: string;
  children?: AgentTraceNode[];
}

export interface TraceTimeline {
  session_id: string;
  trace_id?: string;
  items: TraceTimelineItem[];
  agent_tree?: AgentTraceNode[];
}
```

- [x] **Step 2: NodeClient 增加方法**

```ts
getSessionTrace(sessionId: string, limit?: number): Promise<TraceTimeline>;
```

实现：

```ts
async getSessionTrace(sessionId: string, limit?: number): Promise<TraceTimeline> {
  const query = limit ? `?limit=${limit}` : '';
  return this.client.get(`/api/v1/sessions/${encodeURIComponent(sessionId)}/trace${query}`);
}
```

- [x] **Step 3: 测试 URL**

断言 `getSessionTrace('s1', 100)` 调用 `/api/v1/sessions/s1/trace?limit=100`。

- [x] **Step 4: 验证**

Run: `cd frontend && npm test -- --run src/api/__tests__/node-client.test.ts`

Expected: `PASS`

### Task P0-C2: Replay 页面合并 trace

**Files:**

- Modify: `frontend/src/pages/SessionReplay.tsx`
- Modify: `frontend/src/components/replay/AgentTreeView.tsx`
- Create: `frontend/src/components/replay/TraceTimelinePanel.tsx`

- [x] **Step 1: 加载 trace**

在 `SessionReplay` 原有并行加载中加入：

```ts
client.getSessionTrace(id).catch(() => ({ session_id: id, items: [] })),
```

保持 trace API 失败不影响 journal replay。Journal 仍使用现有 `client.getSessionJournal(id)`；页面负责把 journal 事件、trace span、quality event 在视觉上并列展示，不把 journal 塞进 trace API。

- [x] **Step 2: 增加 TraceTimelinePanel**

```tsx
import type { TraceTimelineItem } from '../../types/journal';

export function TraceTimelinePanel({ items }: { items: TraceTimelineItem[] }) {
  if (items.length === 0) {
    return <div className="p-3 text-sm text-[var(--text-secondary)]">暂无 trace 事件</div>;
  }
  return (
    <div className="flex flex-col gap-2 p-3 overflow-auto">
      {items.map((item, idx) => (
        <div key={`${item.span_id || idx}`} className="rounded-xl border border-[var(--border-color)] bg-[var(--bg-card)] p-3">
          <div className="flex items-center gap-2 text-sm">
            <span className="font-mono text-[var(--accent-600)]">{item.operation || item.kind}</span>
            <span className="ml-auto text-xs text-[var(--text-secondary)]">{item.duration_ms ?? 0}ms</span>
          </div>
          <div className="mt-1 text-xs text-[var(--text-secondary)]">
            {item.service || 'unknown'} · {item.status || 'unknown'}
          </div>
        </div>
      ))}
    </div>
  );
}
```

- [x] **Step 3: AgentTreeView 支持 node 输入**

保留 edges builder，同时接受 `nodes?: AgentTraceNode[]`，后端有 `agent_tree` 时优先渲染 node。

- [x] **Step 4: 验证**

Run: `cd frontend && npm run build`

Expected: build passes.

---

## 9. P1-A Step Snapshot 与 Fork From Step

### Task P1-A1: 数据模型

**Files:**

- Modify: `internal/store/postgres_migrate.go`
- Create: `internal/trajectory/snapshot.go`
- Create: `internal/trajectory/pg_store.go`
- Create: `internal/trajectory/noop_store.go`

- [x] **Step 1: 建表**

```sql
CREATE TABLE IF NOT EXISTS hive_step_snapshots (
	id BIGSERIAL PRIMARY KEY,
	session_id TEXT NOT NULL,
	snapshot_seq INTEGER NOT NULL,
	trace_id TEXT,
	span_id TEXT,
	iteration INTEGER NOT NULL DEFAULT 0,
	message_count INTEGER NOT NULL DEFAULT 0,
	messages JSONB NOT NULL DEFAULT '[]'::jsonb,
	sessiontodo JSONB,
	memory_refs JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(session_id, snapshot_seq)
);
CREATE INDEX IF NOT EXISTS idx_step_snapshots_session_seq ON hive_step_snapshots(session_id, snapshot_seq);
CREATE INDEX IF NOT EXISTS idx_step_snapshots_trace ON hive_step_snapshots(trace_id);
```

- [x] **Step 2: Store interface**

```go
package trajectory

import "context"

type Snapshot struct {
	SessionID string `json:"session_id"`
	SnapshotSeq int `json:"snapshot_seq"`
	TraceID string `json:"trace_id,omitempty"`
	SpanID string `json:"span_id,omitempty"`
	Iteration int `json:"iteration"`
	MessageCount int `json:"message_count"`
	Messages []byte `json:"messages"`
	SessionTodo []byte `json:"sessiontodo,omitempty"`
	MemoryRefs []byte `json:"memory_refs,omitempty"`
}

type Store interface {
	Save(ctx context.Context, snapshot Snapshot) error
	Get(ctx context.Context, sessionID string, snapshotSeq int) (Snapshot, error)
}
```

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/trajectory -count=1`

Expected: `PASS`

### Task P1-A2: ReAct 每轮保存 snapshot

**Files:**

- Modify: `internal/master/master.go`
- Modify: `internal/master/react_processor.go`
- Test: `internal/master/session_loop_test.go`

- [x] **Step 1: Master 注入 trajectory store**

```go
trajectoryStore trajectory.Store
```

setter：

```go
func (m *Master) SetTrajectoryStore(store trajectory.Store) {
	m.trajectoryStore = store
}
```

- [x] **Step 2: 保存时机**

在每轮 LLM 完成并处理完工具结果后保存 messages snapshot。`snapshot_seq` 必须是该 session 内单调递增序号，不使用 `len(session.Messages)` 作为 step 主键；`message_count` 只记录当前消息数量，供诊断显示。

只保存压缩后的必要字段：

```go
messagesJSON, _ := json.Marshal(session.Messages)
_ = m.trajectoryStore.Save(ctx, trajectory.Snapshot{
	SessionID: session.ID,
	SnapshotSeq: nextSnapshotSeq(session),
	TraceID: sessionTraceID,
	SpanID: sessionSpanID,
	Iteration: i + 1,
	MessageCount: len(session.Messages),
	Messages: messagesJSON,
})
```

- [x] **Step 3: 测试保存被调用**

fake store 记录 `SnapshotSeq`、`Iteration` 和 `MessageCount`，断言 `SnapshotSeq` 单调递增且 `MessageCount` 大于 0。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestTrajectorySnapshot -count=1`

Expected: `PASS`

### Task P1-A3: Snapshot API 与 fork-from-step

**Files:**

- Modify: `internal/api/routes.go`
- Create: `internal/api/session_trajectory_handlers.go`
- Modify: `internal/api/session_handlers.go`
- Test: `internal/api/session_trajectory_handlers_test.go`

- [x] **Step 1: API**

```text
GET  /api/v1/sessions/{id}/trajectory/{step}
POST /api/v1/sessions/{id}/fork-from-step
```

`fork-from-step` body：

```json
{
  "snapshot_seq": 12,
  "fork_name": "debug-branch",
  "prompt": "从这里重新尝试，避免重复调用 grep"
}
```

- [x] **Step 2: GET 返回 snapshot**

路径里的 `{step}` 表示 `snapshot_seq`。校验 session ownership 后从 `trajectory.Store.Get(ctx, sessionID, snapshotSeq)` 返回。

- [x] **Step 3: fork-from-step**

读取 snapshot messages，创建 fork session，messages 截断到 snapshot 的 messages，不继承运行中状态，追加用户 prompt 后异步触发新 turn。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/api -run TestSessionTrajectory -count=1`

Expected: `PASS`

---

## 10. P1-B Reasoning Effort Auto

### Task P1-B1: 分类器

**Files:**

- Create: `internal/master/reasoning_effort.go`
- Create: `internal/master/reasoning_effort_test.go`

- [x] **Step 1: 实现分类**

```go
func classifyReasoningEffort(input string, todoCount int, toolRequired bool) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 20 && !toolRequired {
		return "low"
	}
	if todoCount >= 5 {
		return "high"
	}
	if strings.Contains(trimmed, "计划") || strings.Contains(strings.ToLower(trimmed), "refactor") || toolRequired {
		return "medium"
	}
	return "low"
}
```

- [x] **Step 2: 测试**

```go
func TestClassifyReasoningEffort(t *testing.T) {
	cases := []struct{ input string; todos int; required bool; want string }{
		{"1+1等于几", 0, false, "low"},
		{"重构这个模块并补测试", 6, true, "high"},
		{"分析 README 并给出风险", 2, true, "medium"},
	}
	for _, tc := range cases {
		if got := classifyReasoningEffort(tc.input, tc.todos, tc.required); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
```

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestClassifyReasoningEffort -count=1`

Expected: `PASS`

### Task P1-B2: 接入 ReAct 请求

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/master/react_processor.go`
- Test: `internal/master/react_processor_test.go`

- [x] **Step 1: 配置**

```go
type ReasoningEffortAutoConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	DefaultLevel string `json:"default_level,omitempty"`
}
```

在 `AgentConfig` 增加：

```go
ReasoningEffortAuto ReasoningEffortAutoConfig `json:"reasoning_effort_auto,omitempty"`
```

默认 `Enabled=true`，`DefaultLevel="low"`。

- [x] **Step 2: 接入**

如果用户显式传了 `pendingReasoningEffort`，尊重用户。否则调用 classifier。

```go
effectiveEffort := reasoningEffort
if effectiveEffort == "" && m.config.Agent.ReasoningEffortAuto.Enabled {
	effectiveEffort = classifyReasoningEffort(latestQuery, currentTodoCount, toolChoice == ToolChoiceRequired)
}
llmReq.ReasoningEffort = effectiveEffort
```

- [x] **Step 3: provider no-op**

不改 `llm` transformer 现有 no-op 行为；不支持 provider 只打 debug。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master ./internal/llm -run 'TestClassifyReasoningEffort|TestReasoning' -count=1`

Expected: `PASS`

---

## 11. P1-C MCP Output Schema 基础

### Task P1-C1: ToolDefinition 增加 OutputSchema

**Files:**

- Modify: `internal/mcphost/host.go`
- Modify: `internal/mcphost/client.go`
- Modify: `internal/mcphost/convert.go`
- Test: `internal/mcphost/convert_test.go`

- [x] **Step 1: 字段**

```go
OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
```

- [x] **Step 2: remote tool 读取**

`remoteTool` 增加 `OutputSchema` 并传给 `ToolDefinition`。

- [x] **Step 3: convert 保留字段**

OpenAI tool schema 仍只传 input schema；MCP internal list 保留 output schema。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/mcphost -run 'Test.*Schema|Test.*Convert' -count=1`

Expected: `PASS`

### Task P1-C2: 输出校验只记录不阻断

**Files:**

- Create: `internal/mcphost/output_schema.go`
- Modify: `internal/master/react_processor.go`
- Test: `internal/mcphost/output_schema_test.go`

- [x] **Step 1: 校验函数**

初版不引第三方 JSON schema 依赖，只校验 structured JSON 是否可解析以及 required top-level keys。

```go
func ValidateToolOutput(schema json.RawMessage, content json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(content, &out); err != nil {
		return fmt.Errorf("tool output is not json: %w", err)
	}
	return nil
}
```

- [x] **Step 2: Master 记录 quality event**

工具成功后如果 schema 校验失败，记录 `quality.tool_decision` 或 `quality.reflection`，但不把结果改为 error。

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/mcphost ./internal/master -run 'TestValidateToolOutput|TestToolOutputSchema' -count=1`

Expected: `PASS`

---

## 12. P2-A Test-Driven Shadow Feedback

### Task P2-A1: changed package detector

**Files:**

- Create: `internal/agentquality/test_feedback.go`
- Create: `internal/agentquality/test_feedback_test.go`

- [x] **Step 1: 输入输出**

```go
type ChangedFileSet struct {
	Files []string
}

type ValidationCommand struct {
	Name string `json:"name"`
	Command string `json:"command"`
	TimeoutSec int `json:"timeout_sec"`
}
```

- [x] **Step 2: 生成命令**

规则：

| 文件 | 命令 |
|---|---|
| `internal/x/*.go` | `go test ./internal/x -count=1` |
| `cmd/server/*.go` | `go test ./cmd/server -count=1` |
| `frontend/src/**/*.ts(x)` | `cd frontend && npm run build` |

只生成命令，不执行。

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/agentquality -run TestBuildValidationCommands -count=1`

Expected: `PASS`

### Task P2-A2: 安全执行器 shadow 运行

**Files:**

- Create: `internal/master/test_feedback_runner.go`
- Test: `internal/master/test_feedback_runner_test.go`

- [x] **Step 1: runner interface**

```go
type ValidationExecutor interface {
	Execute(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error)
}
```

- [x] **Step 2: 执行策略**

只执行 `agent.reflection.test_driven_shadow.enabled=true` 且命令通过已有 SafeExecutor 的情况。危险命令仍沿用现有审批/deny，不新增审批路径。

- [x] **Step 3: 记录事件**

每个命令完成后记录 `quality.reflection`：

```go
Reflection.Trigger = "test_driven"
Reflection.Severity = "info" or "warn"
Attributes.command = command name
```

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestValidationFeedbackRunner -count=1`

Expected: `PASS`

---

## 13. P2-B Evaluator-Optimizer Shadow

### Task P2-B1: evaluator prompt 与 schema

**Files:**

- Create: `internal/i18n/prompts/system/reflection_evaluator.md`
- Create: `internal/agentquality/evaluator.go`
- Create: `internal/agentquality/evaluator_test.go`

- [x] **Step 1: evaluator 输出结构**

```go
type EvaluationInput struct {
	SessionID string `json:"session_id,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
	Trigger string `json:"trigger"`
	UserInput string `json:"user_input,omitempty"`
	AssistantOutput string `json:"assistant_output,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	ToolError string `json:"tool_error,omitempty"`
	ValidationOutput string `json:"validation_output,omitempty"`
}

type EvaluationVerdict struct {
	Score int `json:"score"`
	Verdict string `json:"verdict"`
	FailureType FailureType `json:"failure_type,omitempty"`
	Feedback []string `json:"feedback"`
	ShouldOptimize bool `json:"should_optimize"`
}
```

- [x] **Step 2: 校验**

`Score` 必须在 0..10，`Verdict` 必须非空，`Feedback` 最多 5 条。

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/agentquality -run TestEvaluationVerdict -count=1`

Expected: `PASS`

### Task P2-B2: shadow evaluator 接入

**Files:**

- Modify: `internal/master/react_processor.go`
- Modify: `internal/master/master.go`
- Test: `internal/master/reflection_evaluator_test.go`

- [x] **Step 1: 接口**

```go
type ReflectionEvaluator interface {
	Evaluate(ctx context.Context, input agentquality.EvaluationInput) (agentquality.EvaluationVerdict, error)
}
```

- [x] **Step 2: 触发条件**

只在以下场景 shadow：

| Trigger | 条件 |
|---|---|
| `test_failed` | P2-A validation failed |
| `loop_warn` | P0-B batch_loop warn |
| `long_artifact` | assistant content > 1200 chars 且包含代码块 |

- [x] **Step 3: 不改写用户输出**

evaluator 结果只写 `quality.reflection` 和 optimization candidate，不插入 assistant 内容。

- [x] **Step 4: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/master -run TestReflectionEvaluatorShadow -count=1`

Expected: `PASS`

---

## 14. P2-C 自动优化候选接入

### Task P2-C1: reflection failure 转 candidate

**Files:**

- Modify: `internal/agentquality/candidate.go`
- Modify: `internal/api/admin_quality_candidates_handlers.go`
- Test: `internal/api/admin_quality_candidates_handlers_test.go`

- [x] **Step 1: CandidateFromReflection**

```go
func CandidateFromReflection(sessionID string, input string, replayRef string, ev Event) CandidateRecord {
	rec := CandidateFromFailure(sessionID, input, replayRef, ev)
	rec.FailureType = ev.FailureType
	rec.SourceEvent = ev
	return rec
}
```

- [x] **Step 2: API 接受 reflection event**

现有 create candidate 接口允许 `quality_event.name == quality.reflection`。

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/api -run TestAdminQualityCandidates_CreateCandidateFromReflection -count=1`

Expected: `PASS`

### Task P2-C2: optimization suggestion 仅人工 review

**Files:**

- Modify: `internal/agentquality/optimization.go`
- Test: `internal/agentquality/optimization_test.go`

- [x] **Step 1: reflection suggestion**

当 `FailureType=runtime/tool/model` 且 source event 是 `quality.reflection`，生成 prompt/tool/test suggestion，但 `ReviewRequired=true`。

- [x] **Step 2: 禁止自动 apply**

不改现有 apply 入口权限，但 suggestion 默认 pending，必须 admin/lead review。

- [x] **Step 3: 验证**

Run: `env GOCACHE=/tmp/go-build go test ./internal/agentquality -run TestBuildOptimizationSuggestionsFromReflection -count=1`

Expected: `PASS`

---

## 15. 验收标准

| 阶段 | 验收 |
|---|---|
| P0-A | `GET /api/v1/sessions/{id}/trace` 返回 `session.process/llm.call/tool.execute/sessiontodo.*` 时间线，span parent-child 不断链 |
| P0-B | loop 3 次重复后注入 reflection note，5 次仍硬停但有 `quality.reflection` 事件 |
| P0-C | Replay 页面能看到 trace timeline、quality.reflection 与 agent tree；trace API 失败不影响 journal replay |
| P1-A | 任一保存过 snapshot 的 `snapshot_seq` 可 `GET trajectory/{step}`；fork-from-step 后新 session messages 截断正确 |
| P1-B | 简单问答 `low`，复杂计划/多 todo `high`，provider 不支持时请求不失败 |
| P1-C | 工具可声明 `outputSchema`；校验失败记录 quality event 但不阻塞原工具结果 |
| P2-A | changed Go/TS 文件产生最小验证命令，危险命令不执行 |
| P2-B | evaluator verdict 进入 quality event/candidate，不改变用户可见输出 |
| P2-C | reflection 失败能进入 optimization suggestions，默认必须人工 review |

全量验收命令：

```bash
env GOCACHE=/tmp/go-build go test \
  ./internal/observability \
  ./internal/agentquality \
  ./internal/mcphost \
  ./internal/master \
  ./internal/api \
  -count=1
```

前端验收命令：

```bash
cd frontend && npm test -- --run src/api/__tests__/node-client.test.ts
cd frontend && npm run build
```

---

## 16. 风险与回滚

| 风险 | 缓解 | 回滚 |
|---|---|---|
| trace 写入量过大 | `max_span_per_session` + queue 满丢弃 + PG index | `agent.observability.tracing.enabled=false` |
| reflection note 污染上下文 | note 短文本、只在失败后注入、保持 tool-call 顺序 | `agent.reflection.enabled=false` |
| evaluator 误判 | P2 shadow，不改写用户输出 | 关闭 `agent.reflection.evaluator_shadow.enabled` |
| test feedback 执行慢 | changed package only + timeout | 关闭 `agent.reflection.test_driven_shadow.enabled` |
| fork-from-step 状态不完整 | 明确 P1 snapshot 是诊断级，不承诺 deterministic replay | 禁用 trajectory API |

---

## 17. 实施顺序

1. P0-A1 到 P0-A3：先让 trace 可查。
2. P0-B1 到 P0-B4：补 reflection event/note。
3. P0-A4：补 subagent/ACP trace 断链。
4. P0-C1 到 P0-C2：运行回放与 Trace 诊断面板接入。
5. P1-B：reasoning effort auto，低风险独立上线。
6. P1-A：snapshot 与 fork-from-step。
7. P1-C：output schema。
8. P2-A：test-driven shadow。
9. P2-B：evaluator shadow。
10. P2-C：optimization candidate 接入。

P0-A 与 P0-B 可并行；P0-C 依赖 P0-A；P1-A 依赖 P0-C；P2-B 依赖 P2-A。

---

## 18. 本次不需要继续讨论的细节

没有阻塞实施的待讨论项。以下是默认决策，除非实现中发现代码事实冲突，否则不再开会讨论：

| 细节 | 默认决策 |
|---|---|
| trace 名称 | `session.process`、`llm.call`、`tool.execute`、`sessiontodo.guard_decide`、`delegation.spawn`、`reflection.inject` |
| trace_id | 一个 master turn 一个 trace_id；subagent child trace 用 `parentTraceID:agentID` 稳定派生 |
| step 定位 | P0 用 journal event index 只读定位；P1 snapshot 用 `snapshot_seq` 作为稳定 step，`message_count` 只做属性 |
| reflection 注入位置 | tool results 后、下一轮 LLM 前 |
| evaluator | shadow only |
| optimizer | 只生成建议，不自动 apply |
| 审批 | 不新增；复用已有危险命令/删除审批 |
| 配置 | P0 reflection note 默认开启；P2 test-driven/evaluator shadow 默认关闭，验收后显式开启 |
