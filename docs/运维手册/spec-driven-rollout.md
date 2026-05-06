# Runbook: Spec-Driven Cognition Rollout

> Scope: `harden-spec-driven-phase2` dual → spec promotion。针对 `spec_driven.mode` 配置切换的灰度放量流程。
> Owner: Master Agent platform owner。
> Last updated: 2026-04-18（Phase 2 TG11 落地）。

## 0. 背景与前置

本 change 引入的 feature flag `spec_driven.mode` 有三档：

| mode | 语义 | 当前就绪度 |
|------|------|-----------|
| `legacy`（默认） | 完全走原 ReAct 路径，零 spec-driven 开销 | **已就绪**（#20 TG10.4 short-circuit 已落） |
| `dual` | spec 与 legacy 双跑，diff 写日志，用户响应仍走 legacy | **plumbing 就位、runner 未接**（TG10.5 stub 状态，`ErrSpecRunnerNotImplemented` 自动 downshift） |
| `spec` | spec 为 primary，legacy 仅作 fallback | **plumbing 就位、runner 未接**（TG10.6 同上） |

**前置条件**：在进入 dual 之前，以下门槛必须 100% 达标（对应 TG12 acceptance gate）：

- [ ] `make test-specdriven` 本地与 CI 双绿（CI = `test-specdriven` workflow，见 §0.1）
- [ ] `internal/specdriven/...` + `internal/store/...` 行覆盖率 ≥ 75%（Sprint 1.2 扩 -coverpkg 后覆盖分母含 store 包）
- [ ] 8 个 required eval fixture 100% pass（fm01..fm08）
- [ ] `go test -race ./internal/master/... ./internal/specdriven/...` 全绿
- [ ] spec runner（`internal/specdriven/ingress/runner.go`）已拿到 LLM client + SpecChangeStore，`ErrSpecRunnerNotImplemented` 已下线
- [ ] CEO review 报告（`~/.gstack/workspace/company/ceo-plans/2026-04-18-add-spec-driven-cognition-review.md`）列出的 5 处致命反例（FM-1..FM-5）均有对应 fixture 锁定

### 0.1 Branch protection required status check 配置（Sprint 1.2 物理闸门）

Round 4 三路评审（P10 CEO / Codex / P9 Tech Lead）裁定：dual rollout 准入不再接受 self-report。`main` 分支必须把 `test-specdriven` workflow 绑到 branch protection 的 required status check，合入前物理绿。

**一次性配置**（repo admin 执行，`GITHUB_TOKEN` 需要 `admin:repo` scope）：

```bash
# 检查当前 branch protection 状态
gh api "repos/$GITHUB_OWNER/$GITHUB_REPO/branches/main/protection" \
  --jq '.required_status_checks.contexts'

# 追加 test-specdriven 到 required contexts（保留已有 checks）
gh api "repos/$GITHUB_OWNER/$GITHUB_REPO/branches/main/protection/required_status_checks/contexts" \
  -X POST \
  -f 'contexts[]=specdriven gate (race + coverage + SKIP→RED)'
```

> ⚠️ **context 名的拼法**：GitHub 用的是 workflow 里 `jobs.<job>.name` 的值（即 `specdriven gate (race + coverage + SKIP→RED)`），不是 workflow 的 `name`。写错 → protection rule 永远匹配不到 → **假门**，PR 看似在等一个永不出现的 check。改 workflow 的 job name 要同步改这里。

**验收（Sprint 1.2 DONE 断言）**：

```bash
gh api "repos/$GITHUB_OWNER/$GITHUB_REPO/branches/main/protection" \
  --jq '.required_status_checks.contexts' \
  | grep -q 'specdriven gate' && echo "PROTECTION: bound" || echo "PROTECTION: MISSING"
```

**反例验证（Sprint 1.2 DONE，强制跑一次）**：
1. 开 feature 分支，故意把 workflow env `TEST_DATABASE_URL` 去掉 / 改错
2. 开 PR → 预期：`specdriven gate` 红（SKIP→RED 触发：`--- SKIP: TestSpecStore_*`）
3. 恢复 env → 预期：绿
4. 证据链截图 / run URL 归档到 `~/.gstack/projects/agents-e11a52bce7/ceo-plans/2026-04-18-sprint-1.2-skipred-drill.md`

**失败态诊断**：
- workflow 红 but coverage 达阈值 → 看 `coverage-specdriven.testlog` artifact 里是否有 `--- SKIP:`（SKIP→RED 触发）
- PG 连不上 → GHA services health check 超时，看 `verify postgres ready` step 输出
- `-coverpkg` 下 `internal/store/spec_store.go` 缺失 → `-coverpkg` 参数拼错或 Go version 下 covdata 不可用

## 1. 灰度阶梯

### Stage 0: legacy（基线）

- 所有环境默认 `spec_driven.mode = "legacy"`
- 观测 baseline：p99 first-token latency、ReAct 平均 step 数、工具调用错误率
- **持续时间**：≥ 2 周，形成稳定对照组

### Stage 1: dual（双跑观测）

**准入**：Stage 0 基线稳定 + 所有前置条件打勾 + spec runner 已 wire。

切换命令（单节点验证）：
```bash
# 修改 config.json
jq '.spec_driven.mode = "dual"' config.json | sponge config.json

# 或通过热重载（若已接）
curl -X POST http://localhost:8080/admin/reload-config
```

**观测窗口**：单节点先切 → 24h SLO 全绿 → 集群滚动切。

> **Round 5 G3 决策**：原"5% session → 50% → 100%" per-session 灰度阶梯**取消**。原因：`internal/specdriven/intake/decide.go` 当前**没有 per-session 采样代码**，SamplePercent 字段不存在。运行真去 5% 没有任何机制承接，承诺与实现脱钩。改为：
>
> 1. **单节点 canary**：选 1 个非高峰节点把 `spec_driven.mode = "dual"` 切上去，其它节点保持 `legacy`。Session stickiness 保证用户体验隔离
> 2. **24h 观测**：canary 节点 SLO 表全绿（见下表）
> 3. **集群滚动**：滚动重启把所有节点切 dual，每批 ≤ 20%；中途任意 SLO 越线立刻回滚
> 4. **全集群 7 天稳定**后再走 Stage 2 spec promotion
>
> Per-session 采样若未来真需要（如同租户灰度），单独开 change 接 SamplePercent 进 `intake.ResolveInput`，不在 Phase 2 范围。

每档前检查 SLO：

| 指标 | 阈值 | 来源 metric |
|------|------|-------------|
| Spec runner fallback 率 | ≤ 5% | `specdriven.plan_fallback_total / specdriven.plan_total` |
| CAS 冲突率 | ≤ 0.1% | `specdriven.cas_conflict_total / specdriven.spec_change_upsert_total` |
| Intake decision 非 legacy 占比 | ≥ 10%（证明 spec path 在跑） | Stage 1 看 `specdriven.intake_decision_total{decision="dual"}`；Stage 2 看 `decision="spec_ok"` |
| P1 事故数 | 7 天滚动窗口 = 0 | oncall 值班单 |

> **指标命名一致性约束**：上表 metric 名必须与 `internal/specdriven/metrics.go` 中常量字面量对齐。当前 emit 为 `specdriven.<name>` 命名空间（**不是** `store.<name>`）。修改 metric 名时同步更新本文件 + `metrics.go` 常量 + `metrics_test.go` enum 锁——三处一改全改。

**any 指标越线 → 立刻降档到 legacy**（见 `spec-driven-rollback.md`）。

### Stage 2: spec（primary）

**准入**：Stage 1 在 100% 流量下稳定 ≥ 7 天 + 全部 SLO 达标 + 无 P1。

切换命令：
```bash
jq '.spec_driven.mode = "spec"' config.json | sponge config.json
```

**注意**：`spec` 模式下 legacy 仅作 fallback，spec path 任何 downshift（`DowngradeOnError`）都会自动走 legacy，**用户无感**。但 fallback 率 > 5% 仍视为 promotion 失败信号。

## 2. Promotion 决策矩阵

| Dual 观测结果 | Action |
|--------------|--------|
| 所有 SLO 达标 + fallback 率 ≤ 2% | ✅ 进 `spec` 模式 |
| SLO 达标但 fallback 率 2-5% | ⚠️ 停留 dual 再观测 1 周；定位 fallback reason |
| fallback 率 > 5% 或 CAS 冲突率 > 0.1% | 🔴 立刻降档到 legacy，开 P1 排查 |
| 用户侧 regression（完成率下降 / 延迟上升 > 20%）| 🔴 立刻降档到 legacy |

## 3. 关键诊断入口

切档异常时，按顺序排查：

1. **Intake decision 分布**：`specdriven.intake_decision_total{decision}`
   - 已知 label 取值：`legacy` / `legacy_downshift_<reason>` / `dual` / `spec_ok`（见 `intake/decide.go:129-189`）
   - `legacy_downshift_*` 比例飙升 → 检查对应 reason label 对应的 guard
   - `legacy_empty_request` 飙升 → intake 上游改了 request normalization？
2. **CAS 冲突事件**：`hive_spec_change_events` 表里的 `revision_conflict` + counter `specdriven.cas_conflict_total{scenario}`
   - 冲突 change_id 聚集 → 单个 hot change 被多 session 并发写，检查 `SessionSpecState` 隔离
   - scenario 取值锁定在 `duplicate_create` / `ghost_id` / `stale_revision`（`specdriven.AllowedCASConflictScenarios` 白名单）
3. **Planner schema 失败**：`specdriven.plan_fallback_total{reason="schema_invalid"}`
   - 突增 → LLM 模型切换 / prompt 漂移，跑 fixture 回归
4. **specCtx race**：若 `-race` CI 突然红 → 检查是否有新路径在持 `session.mu` 时调 `StoreSpecCtx`（应该仅允许 ingress 持锁外调用）

## 4. 不可回退的变更（一次性前向动作）

以下变更落地后无需回滚（即使 `mode=legacy` 也不受影响）：

- `hive_spec_changes` / `hive_spec_change_events` / `hive_spec_session_state` PG 表
- `internal/specdriven/` 包的所有代码（legacy 模式下零调用）
- `SpecDrivenConfig` struct 字段（默认值 = legacy = 零开销）
- CEO review report 文件引用

## 5. 升级回顾与沉淀

每次 promotion 完成后，把以下项写入 `~/.gstack/workspace/company/ceo-plans/`：

- promotion 时间 / stage 转换 / 实际 SLO 值
- 新发现的 fallback reason（扩充 fixture 集合）
- 任何 incident 的根因 + fix + 新增的回归 fixture（对应 tasks.md:1.8）

**复盘四步法**（定目标 → 追过程 → 拿结果 → 沉淀 SOP）落成 runbook 增量更新 PR，review 合入本文件。
