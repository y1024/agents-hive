# P3 PART3 Replay And Batch Eval Status

> 主入口: [P3-QUALITY-WORKBENCH-CODE.md](./P3-QUALITY-WORKBENCH-CODE.md)

## Replay 当前实现

代码入口:

- `internal/qualityworkbench/replay.go`
- `internal/qualityworkbench/replay_runner.go`
- `internal/qualityworkbench/pg_store.go`
- API handlers: `internal/api/admin_workbench_handlers.go`

当前状态机:

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancelled`

当前 API:

| Method | Path | 当前语义 |
|---|---|---|
| POST | `/api/v1/admin/quality-workbench/replays` | 创建一个 replay job,支持 `candidate` 或 `cluster` |
| GET | `/api/v1/admin/quality-workbench/replays` | 按 batch/kind/status/page/size 列表 |
| GET | `/api/v1/admin/quality-workbench/replays/{id}` | 获取 job |
| POST | `/api/v1/admin/quality-workbench/replays/{id}/run` | 标记 running,加载 case,跑 eval runner,写 result/error |
| POST | `/api/v1/admin/quality-workbench/replays/{id}/cancel` | 取消非终态 job |
| POST | `/api/v1/admin/quality-workbench/replays/fanout` | 只计算 fanout 批次计划,不创建 job |

`ReplayRunner` 当前行为:

- candidate job: 通过 `CandidateStore.GetCandidate` 加载候选,转成 `agentquality.LoadedCase`。
- cluster job: 通过 `CandidateStore.ListCandidates` 找同 cluster id 的候选。
- eval runner 必须显式配置；未配置时返回失败/unknown,不会使用 `StaticEvalRunner` 假成功。
- gate 默认是 `agentquality.DefaultGateThresholds()`。
- 结果写入 `ReplayJob.Result` 和 `ReplayJob.Error`。

## Batch Eval 当前实现

代码入口:

- `internal/qualityworkbench/batch_eval.go`
- `cmd/quality-batch-eval`

当前 API:

| Method | Path | 当前语义 |
|---|---|---|
| POST | `/api/v1/admin/quality-workbench/batch-evals` | 创建 batch eval,支持 `manual/replay/full/incremental/shadow`,可传 `cases_dir` |
| GET | `/api/v1/admin/quality-workbench/batch-evals` | 列表 |
| GET | `/api/v1/admin/quality-workbench/batch-evals/{id}` | 获取 run |

当前 diff:

- `changed_candidate_ids`
- `new_failures`
- `recovered`

当前 summary:

- `total`
- `passed`
- `failed`
- `unknown`
- `reasons`

当前 case results:

- `case_results`: version diff 和前端比较使用的真实 case 级 passed/reason 列表。

## 当前测试

```bash
go test ./internal/qualityworkbench -run 'Replay|BatchEval|Fanout' -count=1
go test ./internal/api -run Workbench -count=1
go test ./cmd/quality-batch-eval -count=1
```

## 明确边界

- 没有独立后台 replay worker loop。
- `POST /replays/{id}/run` 是显式人工触发的同步入口。
- 当前没有 approved -> promoted 自动 enqueue hook。
- 当前 batch eval 接受 full/incremental/shadow kind,但三者尚未有差异化后台状态机。
- 当前 batch eval 不自动刷新 candidate `verify_result`。
- replay 不是线上 SessionReplay 动作重放,而是 case/eval 验证。

## 待办

- 如果要自动联动 promoted candidate,在 `handleAdminQualityUpdateCandidate` 明确加 hook,并补测试。
- 如果要 shadow 模式,先定义“不写 candidate”的持久化语义,再扩展 API。
- 如果要真实 worker,补并发、重试、取消和可观测性,不要只加 goroutine。
