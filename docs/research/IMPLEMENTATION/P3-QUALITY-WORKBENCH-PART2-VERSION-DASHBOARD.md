# P3 PART2 Version Diff And Dashboard Status

> 主入口: [P3-QUALITY-WORKBENCH-CODE.md](./P3-QUALITY-WORKBENCH-CODE.md)

## 当前实现

### Version Diff

当前实现是离线 case run diff,不是持久化 version matrix:

- 代码: `internal/qualityworkbench/version_diff.go`
- API: `POST /api/v1/admin/quality-workbench/version-diff`
- 输入: `VersionMatrixInput{baseline_run_id,treatment_run_id,baseline,treatment}`
- 输出: `VersionMatrixDiff`,包含 regressed/recovered/new_failure case ids。

测试:

- `internal/qualityworkbench/version_diff_test.go`
- `TestCompareVersionMatrixMarksRegressedCases`
- `TestCompareVersionMatrixMarksRecoveredCases`

### Dashboard

当前 dashboard 是 candidate/cluster 聚合:

- 代码: `internal/qualityworkbench/dashboard.go`
- API:
  - `GET /api/v1/admin/quality-workbench/dashboard/snapshot`
  - `GET /api/v1/admin/quality-workbench/dashboard/series`
- snapshot 字段:
  - `open_clusters`
  - `candidate_status_counts`
  - `failure_type_counts`
  - `verify_result_counts`
- series 按 bucket 汇总上述计数。

测试:

- `internal/qualityworkbench/report_dashboard_test.go`
- `TestBuildDashboardSnapshotAggregatesWindowedCounts`
- `TestBuildDashboardSeriesBucketsByDay`

## 明确边界

- 没有 `agentquality_version_metrics` 表。
- 没有 t-digest latency digest 存储。
- 没有 `GET /version-matrix` 或 query 参数式 `GET /version-diff`。当前是 `POST /version-diff`。
- dashboard 当前不承诺 success/cost/latency 三联趋势,不展示 P50/P95/P99。

## 待办

如果后续要做真正 version matrix:

- 新增 artifact metrics schema,明确 prompt/tool/skill version 来源。
- 使用可合并的 latency 数据结构,不要平均 p95。
- 明确 journal consumer 写入路径。
- 保持当前 `POST /version-diff` 作为离线比较工具,不要混淆成实时矩阵 API。
