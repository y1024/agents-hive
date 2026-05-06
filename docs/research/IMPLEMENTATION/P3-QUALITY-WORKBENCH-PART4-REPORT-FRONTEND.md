# P3 PART4 Report And Frontend Status

> 主入口: [P3-QUALITY-WORKBENCH-CODE.md](./P3-QUALITY-WORKBENCH-CODE.md)

## Weekly Report 当前实现

代码入口:

- `internal/qualityworkbench/report.go`
- `internal/qualityworkbench/pg_store.go`
- `cmd/quality-weekly-report`

当前 API:

| Method | Path | 当前语义 |
|---|---|---|
| GET | `/api/v1/admin/quality-workbench/reports` | 列出 report,包含 markdown |
| GET | `/api/v1/admin/quality-workbench/reports/{id}` | 获取 report |
| GET | `/api/v1/admin/quality-workbench/reports/{id}/download` | 下载 markdown attachment |
| POST | `/api/v1/admin/quality-workbench/reports/generate` | 按 `week_start` 生成并保存 report |

当前 CLI:

```bash
go run ./cmd/quality-weekly-report --week=2026-04-20
```

CLI 支持 `-input` JSON 文件读取 clusters/candidates/eval_runs；未传 input 时生成空数据 markdown,用于格式验证。

## Frontend 当前实现

当前不是 6 个拆分页面,而是一个集中式 Admin 页面:

- 文件: `frontend/src/pages/admin/qualityworkbench/QualityWorkbench.tsx`
- 路由: `/admin/quality-workbench`
- sidebar: `frontend/src/layouts/AdminSidebar.tsx`
- API client: `frontend/src/api/node-client.ts`
- 类型: `frontend/src/types/api.ts`

页面当前展示:

- KPI: open clusters/open candidates/replay jobs/batch evals。
- 失败聚类列表和详情。
- cluster replay 创建。
- replay queue 列表和手动 run。
- batch eval 列表和手动 run。
- weekly report 列表和手动生成。
- grouping rule 最小保存/删除入口。
- version diff 使用最近两次带 `case_results` 的 batch eval run,不再使用 UI 演示数据。
- failure type 分布。

## 当前测试

```bash
go test ./internal/qualityworkbench -run Report -count=1
go test ./cmd/quality-weekly-report -count=1
cd frontend && npm test -- --run src/App.lazyRoutes.test.tsx
cd frontend && npm run build
```

## 命名修正

- 前端测试命令使用 npm scripts: `cd frontend && npm test -- --run ...`。
- 当前候选池测试文件是 `frontend/src/pages/admin/QualityCandidates.test.ts`。
- 当前 Workbench 页面文件是 `QualityWorkbench.tsx`,不是旧文档中的 `Overview.tsx`, `Clusters.tsx`, `VersionMatrix.tsx`, `ReplayQueue.tsx`, `BatchEval.tsx`, `WeeklyReport.tsx` 六页拆分。

## 明确边界

- 当前 report 不自动推送飞书/邮件。
- 当前 weekly report 不是每周一自动调度,需要 API/CLI 显式触发。
- 当前前端没有完整 grouping rule 编辑器和 version matrix 独立页面。
