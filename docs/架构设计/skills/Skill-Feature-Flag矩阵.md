# Skills Feature Flags（D15 矩阵 cheat-sheet）

## 1. 四位开关

| 位 | 配置键 | 语义 |
|----|--------|------|
| A | `specdriven.mode` (`spec` / `legacy`) | 主开关：是否启用 Spec-Driven 认知链路 |
| B | `specdriven.subagent_mode` (`spec-only` / `legacy`) | SubAgent 是否仅接受 spec 任务 |
| C | `specdriven.skills_semantic_routing` | 是否用 requirement-based 路由（`FindBySpecRequirements`） |
| D | `agent.skills.on_demand_enabled` | 是否注册 `skill_install` / `skill_search` + 启用远程 Discovery |

`SnapshotFeatureFlags(cfg)` 汇总成 `FeatureFlagCombo{SpecDrivenEnabled, SubagentMode, SemanticRouting, OnDemandEnabled}`。

## 2. 组合约束（`ValidateFlagCombination`）

硬约束：`!specdriven && (subagent_mode || semantic_routing)` **fail-fast** bootstrap。

即 B 和 C 都依赖 A。D 独立，任意时候都可开。

结果：2^4=16 组合 → **10 valid + 6 invalid**。详见 `internal/config/flag_matrix_test.go`。

## 3. 完整矩阵

| # | A specdriven | B subagent | C semantic | D on_demand | Valid | skill_install/search 注册 | 远程 Discovery | SubAgent userID 强制继承 |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
|  1 | 0 | 0 | 0 | 0 | ✓ |   |   |   |
|  2 | 0 | 0 | 0 | 1 | ✓ | ✓ | ✓ |   |
|  3 | 0 | 0 | 1 | 0 | ✗ fail-fast | - | - | - |
|  4 | 0 | 0 | 1 | 1 | ✗ fail-fast | - | - | - |
|  5 | 0 | 1 | 0 | 0 | ✗ fail-fast | - | - | - |
|  6 | 0 | 1 | 0 | 1 | ✗ fail-fast | - | - | - |
|  7 | 0 | 1 | 1 | 0 | ✗ fail-fast | - | - | - |
|  8 | 0 | 1 | 1 | 1 | ✗ fail-fast | - | - | - |
|  9 | 1 | 0 | 0 | 0 | ✓ |   |   |   |
| 10 | 1 | 0 | 0 | 1 | ✓ | ✓ | ✓ |   |
| 11 | 1 | 0 | 1 | 0 | ✓ |   |   |   |
| 12 | 1 | 0 | 1 | 1 | ✓ | ✓ | ✓ |   |
| 13 | 1 | 1 | 0 | 0 | ✓ |   |   | ✓ |
| 14 | 1 | 1 | 0 | 1 | ✓ | ✓ | ✓ | ✓ |
| 15 | 1 | 1 | 1 | 0 | ✓ |   |   | ✓ |
| 16 | 1 | 1 | 1 | 1 | ✓ | ✓ | ✓ | ✓ |

## 4. 运维视角：常见组合

### 4.1 全关（#1）— 绝对兼容

```yaml
specdriven:   { mode: legacy }
agent.skills: { on_demand_enabled: false }
```

零改动兼容 pre-change 行为。Skills 全量静态加载。

### 4.2 仅开 on-demand（#2）— 灰度起点

```yaml
specdriven:   { mode: legacy }
agent.skills:
  on_demand_enabled: true
  marketplace_urls: [...]
```

保留 legacy 认知链路，但开放用户按需安装。最低风险试点。

### 4.3 Full stack（#16）— 目标形态

```yaml
specdriven:
  mode: spec
  subagent_mode: spec-only
  skills_semantic_routing: true
agent.skills:
  on_demand_enabled: true
  marketplace_urls: [...]
```

全部契约激活：Spec Planner + SubAgent 强租户继承 + Semantic Routing + On-demand 安装。

## 5. 迁移建议

推荐灰度路径：

```
#1 全关  →  #2 仅 on-demand  →  #11 加 semantic routing  →  #16 full stack
   │          │                    │                          │
   │          │                    │                          └─ 要求 subagent 都迁到 spec-only
   │          │                    └─ 要求 skill 库已有 provides_requirements 字段
   │          └─ 要求 marketplace 就绪
   └─ 起点
```

每步之间建议**跑 1-2 周生产观察**，看 `skills_feature_flags` 启动日志 + skill.install.progress 事件流是否符合预期。

## 6. Rollback 路径

任意组合回到任意组合，**hot**：

1. 修改 `config.yaml`
2. 滚动重启
3. 启动日志 grep `skills_feature_flags:` 确认新组合

**数据层不丢**：
- `$HIVE_DATA/skills/users/` 保留所有 personal skill
- DB `skills` 表保留所有记录
- 回到 #1（全关）只是不再注册 skill_install/skill_search 工具；旧 skill 工具继续工作

## 7. 观测锚点

启动日志：

```
skills_feature_flags: specdriven=true subagent_mode=true semantic_routing=true on_demand=true
```

Invalid 组合启动直接 panic，日志锚点：

```
FATAL ValidateFlagCombination failed: specdriven must be enabled for subagent_mode/semantic_routing
```

运维可以用这一行直接 alert。
