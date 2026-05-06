# Runbook: Spec-Driven Cognition Rollback

> Scope: `harden-spec-driven-phase2` 各 guard 的降档 / 回退流程。
> Companion: `spec-driven-rollout.md`（promotion 流程）。
> Owner: Master Agent platform oncall。
> SLO 回退: < 5 min（Stage 1/2 → legacy）, < 30 min（schema/DB-level 回退）。

## 0. 总原则

**单飞行员回退开关**：`config.json > spec_driven.mode = "legacy"`。

- 所有 spec path 在 `applySpecDrivenIntake` 的 `mode == legacy` 分支立即 short-circuit
- `StoreSpecCtx(nil)` 清零任何残留 `*specdriven.Context`
- `specdriven.intake_decision_total{decision="legacy"}` 立刻回到 100%
- **不需要**重启进程、不需要 DB 迁移、不需要删表；legacy 路径从未改过

**关键不变量**：legacy 模式下的所有 ReAct 行为与本 change 落地前**完全等价**。任何 regression 都是 bug，必须 RCA 到根因。

## 1. 分级回退

| 触发信号 | 动作 | 决策人 | 耗时目标 |
|---------|------|--------|---------|
| P1 事故 / 用户投诉 / 数据异常 | 立即 `mode=legacy` | Oncall（无需审批） | ≤ 5 min |
| SLO 越线（fallback > 5%, CAS > 0.1%） | 降一档（spec→dual 或 dual→legacy） | Oncall + platform owner | ≤ 15 min |
| 单 guard 异常（某个 fixture 开始飘红） | 保持当前 mode，但触发该 guard 的专项回退 | platform owner | ≤ 30 min |
| 灰度窗口期数据不理想 | 按 rollout runbook 的 Stage 0 基线评估，必要时降档 | platform owner | 观测窗内 |

## 2. Mode 降档步骤

### spec → dual

```bash
jq '.spec_driven.mode = "dual"' config.json | sponge config.json
# 热重载（若已接）
curl -X POST http://localhost:8080/admin/reload-config
# 验证：
# specdriven.intake_decision_total{decision="dual*"} 应立即出现
```

### dual → legacy（最常用）

```bash
jq '.spec_driven.mode = "legacy"' config.json | sponge config.json
curl -X POST http://localhost:8080/admin/reload-config
# 验证：
# specdriven.intake_decision_total{decision="legacy"} 占比 → 100%
# specdriven.plan_fallback_total / *_downshift_* 归零（无新增）
# 残留 specCtx 被 StoreSpecCtx(nil) 清零；无 race test 失败
```

**热重载未接** → 滚动重启：每批 ≤ 20% 流量，保持 session stickiness，观察 p99 latency 不升。

## 3. 按 guard 的专项回退

### Guard 1 — Continuation 默认 OFF（FM-1）

**症状**：`specdriven.continuation_resume_total{trigger="weak_signal_mru"}` 突增（应该始终为 0，因为默认 off）。

**回退**：
1. 确认 `spec_driven.continuation.default` 仍为 `"off"`（`TestDefaultSpecDrivenConfig_SystemLevelInvariant` 锁定此不变量）
2. 检查 `internal/specdriven/continuation/resolver.go` 的 `Result{Decision}` 是否被上游忽略 → 走 ASK 路径
3. 应急：把 config `spec_driven.continuation.default = "off"` 改为 `"ask"`（强制所有 ambiguous 情况都 ASK），同步 push 补丁修 resolver 调用点

### Guard 2 — SpecChangeStore CAS（Guard 2/3）

**症状**：`ErrSpecChangeConflict` 爆炸 / `hive_spec_change_events` 事件序列跳号。

**回退**：
1. **不要**回滚 DB 表（events 表是 append-only 审计证据）
2. `mode=legacy` 阻断新写入路径
3. 人工 audit `hive_spec_changes` 表：按 `updated_at DESC` 找到最后一个 healthy revision，对比受影响 session 的 `SessionSpecState.Changes`
4. 若确实需要手动修复：单事务 `UPDATE ... WHERE id = $1 AND revision = $expected` + `AppendEvent(event_type='operator_rollback')`，禁止绕过 CAS

### Guard 3 — SessionSpecState 持久化

**症状**：session 重启后 `SpecState` 丢失 / FocusMRU 长度异常（> 16）。

**回退**：
1. `mode=legacy` 阻断新 mutation
2. 检查 `SpecSessionStateStore.Save` 是否在持锁内调用（违反 tasks.md 3.4 纪律）
3. 必要时 `DELETE FROM hive_spec_session_state WHERE session_id = $1`——session 下次创建时会重新 init，spec 上下文丢失但不会污染 legacy path

### Guard 4 — specCtx atomic.Pointer

**症状**：`-race` CI 红 / `react_processor.go` 出现 panic。

**回退**：
1. `mode=legacy` 阻断新写入
2. `StoreSpecCtx` 的调用点必须 100% 在 `session.mu` **之外**（OffLock discipline）——grep `StoreSpecCtx` 搜所有调用点，确认无 `s.mu.Lock()` 包围
3. `StoreSpecCtxGuarded` 的 unauthorized write metric 上涨 → 找出违规调用点并删除

### Guard 5 — Planner schema gate（FM-4）

**症状**：`specdriven.plan_fallback_total{reason="schema_invalid"}` 激增 / `specdriven.plan_overbudget_total` 激增。

**回退**：
1. 立刻 `mode=legacy`（planner 完全不被调用）
2. 跑 `go test ./internal/specdriven/planner/ -run TestDecode` 回归 17 个 schema fixture（FM-4）
3. 检查 LLM 模型是否切换（planner 走 `airouter.TaskPlanning` → cheapest json/tools；**禁止** fallback 到 `TaskChat`）
4. 临时提高 `spec_driven.planner.token_budget`（≤ 1500，绝不回到 2000 — FM-4 反例），Hot-patch 发版时同步写 prompt 加强 schema 约束

### Eval Harness（Guard 5 metarule）

**症状**：`make test-specdriven` CI 红 / required fixture fail。

**硬规则**：**`make test-specdriven` 红 = dual/spec 模式禁止 promote**（tasks.md 1.7 CI workflow 门控）。

**回退**：
1. 已在 dual/spec？立即 `mode=legacy`
2. fail fixture = required 的 → 必须修代码而非改 fixture（`fm01_wrong_continuation.json` 等是反坑不变量）
3. fail fixture = optional 的 → `t.Logf("WARN optional fixture failed: ...")` 已显式忽略，不阻塞；但需写 follow-up 补齐

## 4. 不可回退项（前向一次性）

以下一旦部署即不可回退（因为 `mode=legacy` 下它们是休眠的、零成本）：

- `hive_spec_changes` / `hive_spec_change_events` / `hive_spec_session_state` 表（仅增加列不删列）
- `internal/specdriven/` 包代码（legacy 模式下 0 调用）
- `SpecDrivenConfig` struct 字段（默认值 = 零开销）
- `session_loop.go:757` 的 `applySpecDrivenIntake(session, request)` 调用（mode=legacy 下 2μs 内返回 PathLegacy）

**如果真的需要"移除 spec 能力"**：删掉 `session_loop.go` 的 hook 调用 + 删 `session_loop_specdriven.go` 即可。但这不是回退动作，是退出 Phase 2 项目本身。

## 5. 事故通告模板

```
[P1/P2] Spec-Driven Mode Regression
Mode before: <spec|dual>
Mode after:  legacy
Trigger:     <SLO 越线 / P1 事故 / 用户投诉>
Metric evidence:
  - specdriven.plan_fallback_total rate: X%
  - CAS conflict rate: Y%
Symptoms:    <30 words>
Root cause:  <TBD / 已定位 / 修复中>
ETA to fix:  <hours>
Follow-up fixture: <fixture name to add>
```

所有 P1 事故必须产出一个新的 regression fixture（`internal/specdriven/eval/fixtures/regression_<id>.json`）锁定反例——见 tasks.md 1.8。

## 6. 回退后的 RCA 纪律

回退完成后 48h 内产出 RCA（放 `~/.gstack/workspace/company/ceo-plans/YYYY-MM-DD-<incident>-rca.md`）：

- 事故时间线（5-min 精度）
- 根因（5-Why，拒绝"环境问题"/"偶发"结论）
- 修复方案 + 验证 fixture
- 规避类似问题的纪律更新（runbook / spec 反例）
- Keeper Test：这段代码值得保留吗？不值得就删
