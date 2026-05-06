# P0 Agent Quality Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> P0 不再作为待施工计划维护。本文件记录当前代码能力、入口、验证和边界。

## 最终能力

P0 已提供质量闭环的基础:

- golden task case schema、loader、static eval runner 和 gate runner。
- quality event model, 覆盖 agent turn、tool decision、context build、permission decision、delegation。
- regression candidate 生成、去重、状态机、PG store、Admin CRUD 和 golden case 导出。
- candidate 内置 optimization suggestions, 供 P5 继续生成 reviewable suggestion。
- prompt meta 归因, 区分 DB/file/`embedded`/hardcoded/default。

## 当前实现入口

- 领域包: `internal/agentquality`
- 质量事件 emit: `internal/master/quality_events.go`
- Candidate API: `/api/v1/admin/quality/candidates`
- Quality cases / prompt smoke: `/api/v1/admin/quality/cases`, `/api/v1/admin/quality/prompt-smoke`
- 前端候选池: `frontend/src/pages/admin/QualityCandidates.tsx`
- CLI: `go run ./cmd/agentquality`

## 权威字段名

当前代码字段名如下,旧文档中的伪字段不再使用:

- Prompt: `agentquality.Event.Prompt`
- Tool: `agentquality.Event.ToolDecision.Actual`
- Delegation depth: `agentquality.Event.Delegation.SpawnDepth`
- Skill: 通过 `Event.Attributes["skill"]` 表达,不是 `Event.SkillName`
- Error message: 优先 `Event.Attributes["error"]` / `Event.Attributes["error_message"]`,其次 `RetryReason`
- Store list: `CandidateStore.ListCandidates`

## Prompt Source 命名

内置 prompt 来源统一写作 `embedded`。`PromptLoader` 的解析顺序是:

1. DB
2. file
3. go:embed, source = `embedded`
4. 非空 fallback, source = `hardcoded`
5. 空 fallback, source = `default`

对应测试文件是 `internal/i18n/prompt_loader_test.go`。

## 运行验证

```bash
go test ./internal/agentquality -count=1
go test ./internal/master -run Quality -count=1
go test ./internal/api -run 'Quality|WorkbenchClusters' -count=1
go test ./cmd/agentquality -count=1
cd frontend && npm test -- --run src/pages/admin/QualityCandidates.test.ts
```

## 边界

- P0 只保证质量候选和最小质量门禁,不负责 P3 的完整工作台产品面。
- Candidate 状态机已经包含 `promoted_verified` 和 `promoted_regressed`,但状态推进依赖 P3 replay/batch eval 的运行入口。
- Admin endpoints 只在 `authEngine != nil` 时注册。
