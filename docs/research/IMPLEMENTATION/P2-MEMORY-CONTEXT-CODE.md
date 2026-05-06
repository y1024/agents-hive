# P2 Memory And Context Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> P2-CONTEXT 不再作为待施工计划维护。本文件记录当前 memory/context 注入治理能力和边界。

## 最终能力

当前 memory/context 能力包括:

- `Governance` metadata 编解码, 包含 source/evidence/confidence/expires/source user/tenant 等治理信息。
- `InjectionResult` 结构化返回, 包含注入 memory、跳过 ID、过期/低置信/跨用户/token budget 统计。
- `Injector.InjectContextDetailed` 过滤过期、低置信、跨用户和预算超限 memory。
- Master turn 内暂存并消费 memory injection result, 将 context build 质量信号写入 `agentquality.Event.ContextBuild`。
- memory eval fixtures 和 harness, 覆盖相关性、过期和跨用户污染。

## 当前实现入口

- Governance metadata: `internal/memory/governance.go`
- Injection result: `internal/memory/injection_result.go`
- Injector: `internal/memory/injector.go`
- Master quality context bridge: `internal/master/session.go`, `internal/master/session_loop.go`, `internal/master/react_processor.go`
- Memory eval: `internal/memory/eval`

## 当前测试入口

```bash
go test ./internal/memory -run 'Governance|Injection|Injector' -count=1
go test ./internal/memory/eval -count=1
go test ./internal/master -run QualityMemory -count=1
```

## 代码现实

P2-CONTEXT 已被 P2-PROD 扩展。当前 memory governance 不只是 metadata:

- Admin `/api/v1/admin/memory/governance` 可读治理统计。
- Admin `/api/v1/admin/memory/prune` 支持 dry-run 和显式删除。
- `memory_governance_policies` 持久化 default policy。
- 无 policy store 或无 default policy 时回退 `min_confidence=0.5`。

## 边界

- P2-CONTEXT 负责注入治理和质量事件,不负责生产级 backlog、import/export、vector-space plan。这些见 `P2-MEMORY-PRODUCTION-CODE.md`。
- 当前权威测试文件名是 `internal/memory/eval/loader_test.go`, `harness_test.go`, `runner_test.go`。
