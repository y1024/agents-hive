# P3 PART1 Cluster Status

> 主入口: [P3-QUALITY-WORKBENCH-CODE.md](./P3-QUALITY-WORKBENCH-CODE.md)

## 当前实现

失败聚类已经落在 `internal/qualityworkbench/cluster.go`:

- `GroupingRule`, `GroupingMatch`, `Cluster` 是当前权威类型。
- 默认规则来自 `DefaultGroupingRule()` / `DefaultGroupingRules()`。
- 聚类入口是 `AggregateClusters(rules, records)`。
- 聚类 key 使用当前代码字段:
  - failure type: `Event.FailureType` 或 `CandidateRecord.FailureType`
  - tool: `Event.ToolDecision.Actual`
  - skill: `Event.Attributes["skill"]`
  - prompt key: `Event.Prompt.Key`
  - case id: `Event.CaseID`
  - delegation depth: `Event.Delegation.SpawnDepth`
  - error digest: `Event.Attributes["error"]`, `Event.Attributes["error_message"]`, 或 `RetryReason`
- cluster id 由 key hash 生成,格式为 `cl_<hex>`。

## API

| Method | Path | 当前语义 |
|---|---|---|
| GET | `/api/v1/admin/quality-workbench/clusters` | 即时读取 candidates 并聚类,返回 `clusters/items/total/page/size` |
| POST | `/api/v1/admin/quality-workbench/clusters/recompute` | 重新计算当前候选窗口,返回 `cluster_count/took_ms` |
| POST | `/api/v1/admin/quality-workbench/grouping-rules/preview` | 使用当前有效规则 preview,不写表 |
| GET | `/api/v1/admin/quality-workbench/grouping-rules` | 列出持久化 grouping rules |
| PUT | `/api/v1/admin/quality-workbench/grouping-rules/{id}` | upsert grouping rule |
| DELETE | `/api/v1/admin/quality-workbench/grouping-rules/{id}` | 删除 grouping rule |

## 当前测试

```bash
go test ./internal/qualityworkbench -run 'Cluster|Grouping' -count=1
go test ./internal/api -run 'WorkbenchClusters|Preview' -count=1
```

已存在测试:

- `TestAggregateClusters_DefaultRuleGroupsSimilarFailures`
- `TestMatchGroupingRule_PriorityOrder`
- `TestPreviewGroupingRulesHonorsPriorityAndEnabledWithoutMutatingInput`

## 边界

- `agentquality_grouping_rules` PG 表和 CRUD 已落地,前端有最小保存/删除入口。
- 没有单独 cluster store。clusters 当前由 candidate records 即时聚合。
- preview 是只读预览,不会持久化规则或 cluster。
- 旧文档中 `PromptRef`, `ToolDecision.Tool`, `SkillName`, `Delegation.Depth`, `ErrorMessage` 不是当前实现字段名。

## 待办

- 如果要把 grouping rules 做成完整运营产品,继续补复杂编辑器、审计和租户权限。
- 如果要跨租户聚类,在 candidate/filter/store 层补 tenant 维度,不要只在前端过滤。
