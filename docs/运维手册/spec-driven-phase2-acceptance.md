# Runbook: Spec-Driven Phase 2 Physical Acceptance (task 12.8)

> Scope: `harden-spec-driven-phase2` change 的灰度准入物理验收。对应 tasks.md:163-171 的 (a)-(h) 八项检查。
> Owner: Platform owner / on-call SRE。
> Pre-req: 已读 `spec-driven-rollout.md`（Stage 0/1/2 灰度阶梯）+ `spec-driven-rollback.md`（出事降档）。
> 完成判据：(a)-(h) 八项全 ✅，把每条 evidence 截图/输出粘到 sign-off 表里，归档到 `~/.gstack/projects/agents-a38590d1d1/ceo-plans/`。

---

## 0. 准备工作（一次性）

### 0.1 环境变量

```bash
# Staging 节点上 export 一次（或写进 ~/.profile）
export DATABASE_URL='postgres://hive:<password>@staging-pg.internal:5432/hive_staging?sslmode=require'
export GITHUB_OWNER='<your-org>'
export GITHUB_REPO='agents-hive'
export STAGING_LOG=/var/log/hive/server.log    # 或 docker logs 输出路径

# psql 连通性自检
psql "$DATABASE_URL" -c '\dt hive_*' | head -5
# Expected: 至少看到 hive_metrics / hive_spec_changes / hive_spec_change_events 三张表
```

### 0.2 切到 staging 单节点 + mode=dual

```bash
# 在 canary 节点 config.json 里改
jq '.spec_driven.mode = "dual"' /etc/hive/config.json | sponge /etc/hive/config.json
systemctl restart hive   # 或 kubectl rollout restart deployment/hive-canary

# 等 readiness probe 绿
curl -sf http://localhost:8080/health | jq -r .status   # Expected: "ok"
```

> ⚠️ 物理验收必须在**真 PG + 真 LLM 流量**的 staging 节点跑，不是本机沙箱。本地内存模式 PG 缺席分支会触发 (f) 但 (a)-(e) 全部 N/A。

---

## 1. (a) PG 实接 — `spec_change_store` 不应 disabled

**目的**：证明 `pgPool != nil` 走的是 `internal/bootstrap/server.go:243` 的 `SetSpecChangeStore` 真接通分支，不是 248 行的 disabled fallback。

```bash
grep -E "spec.change.store" "$STAGING_LOG" | tail -5
```

**Expected**（PG 接通）:
```
（无输出，或仅有 "spec_change_store wired (PG-backed)" info 行）
```

**FAIL 形态**（PG 缺席）:
```
WARN  spec_change_store disabled — PG pool absent, spec write path will degrade ...
```

**失败诊断**：
- 检查 `DATABASE_URL` 是否配置且 reachable：`psql "$DATABASE_URL" -c 'SELECT 1'`
- 检查 PG pool 初始化日志：`grep -i "pgpool\|postgres" "$STAGING_LOG" | head -10`
- 看是否启动顺序问题（PG 初始化在 SpecChangeStore wire 之前）

✅ 通过判据：grep 结果**不含** `disabled — PG pool absent`。

---

## 2. (b) Spec write path 真写 PG

**目的**：证明 mode=dual 下一次 LLM 调用之后，`hive_spec_changes` 真的有行写入（`UpsertWithCAS` 走通）。

```bash
# 先记录 baseline count
BASELINE=$(psql "$DATABASE_URL" -At -c "SELECT count(*) FROM hive_spec_changes")
echo "baseline=$BASELINE"

# 触发一次 spec session（curl 真 chat endpoint，request 内容随意）
curl -sS -X POST http://localhost:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"smoke-b-'$(date +%s)'","message":"add unit test for parser","stream":false}' \
  | jq -r '.session_id, .status' | head -2

# 等 5 秒让 obs 队列 flush + spec runner 真跑完
sleep 5

# 查新增
psql "$DATABASE_URL" -c \
  "SELECT count(*) FROM hive_spec_changes WHERE updated_at > now() - interval '5 min'"
```

**Expected**:
```
 count
-------
     1   ← 或更多
```

**FAIL 形态**：count = 0

**失败诊断**：
- mode 没切对：`grep "spec_driven" /etc/hive/config.json` 应是 `"mode": "dual"`
- specRunner 没接 LLM：`grep "spec runner" "$STAGING_LOG" | tail -5`
- LLM 调用失败：`SELECT * FROM hive_metrics WHERE name='specdriven.plan_fallback_total' ORDER BY ts DESC LIMIT 5`
- 看 (a) 是否真的过了

✅ 通过判据：count ≥ 1。

---

## 3. (c) CAS 冲突真触发 + 三 scenario 真 emit

**目的**：人为制造 `duplicate_create` 冲突（同 change_id 并发两次 `ExpectRevision=0`），证明 `cas_conflict_total{scenario}` counter 真递增。

```bash
# 用 psql 直接灌——直接造 store 层冲突，不走 LLM
CHANGE_ID="cas-smoke-$(date +%s)"

# 第一次 create（ExpectRevision=0 → 走 INSERT，成功）
psql "$DATABASE_URL" -c "
  INSERT INTO hive_spec_changes (id, revision, payload, status, updated_at)
  VALUES ('$CHANGE_ID', 1, '{}', 'draft', now())
"

# 第二次同 ID create（应触发 duplicate_create scenario）
# 用 master 进程 API 走真实 UpsertWithCAS 路径——直接 SQL 不会走 observer
curl -sS -X POST http://localhost:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"cas-smoke-x","message":"reuse change_id '$CHANGE_ID'","stream":false}' \
  > /dev/null

sleep 3

# 查 metric
psql "$DATABASE_URL" -c "
  SELECT labels->>'scenario' AS scenario, count(*) AS hits
  FROM hive_metrics
  WHERE name = 'specdriven.cas_conflict_total'
    AND ts > now() - interval '2 min'
  GROUP BY 1 ORDER BY 2 DESC
"
```

**Expected**:
```
   scenario     | hits
----------------+------
 duplicate_create |    1
```

**进阶**：要测 `ghost_id` 和 `stale_revision` 三 scenario 全覆盖，运行 `internal/store/spec_store_test.go` 的 PG 集成测试一次：
```bash
TEST_DATABASE_URL="$DATABASE_URL" go test -v -run 'TestSpecStore_CAS' ./internal/store/...
# Expected: 三 case 全绿，对应 PG 端 cas_conflict_total{scenario} 三种 label 都应 +1
```

✅ 通过判据：`cas_conflict_total` 至少 1 行 `duplicate_create`；要求严格则三 scenario 全覆盖。

---

## 4. (d) SLO 三分子三分母 counter 全有数据 + fallback rate ≤ 5%

**目的**：runbook §1 Stage 1 SLO 表里的核心 SLO 公式 `fallback_rate = plan_fallback_total / plan_total ≤ 5%` 必须可计算。

```bash
psql "$DATABASE_URL" <<'SQL'
-- 三 counter 都应该有近 30 分钟数据
SELECT name, count(*) AS samples, sum(value) AS total_count
FROM hive_metrics
WHERE name IN (
  'specdriven.plan_total',
  'specdriven.spec_change_upsert_total',
  'specdriven.plan_fallback_total'
)
  AND ts > now() - interval '30 min'
GROUP BY name
ORDER BY name;

-- 实算 fallback rate
SELECT
  (SELECT coalesce(sum(value),0) FROM hive_metrics
   WHERE name='specdriven.plan_fallback_total' AND ts > now() - interval '30 min')
  /
  NULLIF((SELECT sum(value) FROM hive_metrics
   WHERE name='specdriven.plan_total' AND ts > now() - interval '30 min'), 0)
  AS fallback_rate;
SQL
```

**Expected**:
```
              name              | samples | total_count
--------------------------------+---------+-------------
 specdriven.plan_fallback_total |     X   |     ...
 specdriven.plan_total          |     X   |     ...
 specdriven.spec_change_upsert_total | X   |     ...

 fallback_rate
---------------
 0.0234           ← 必须 ≤ 0.05
```

**FAIL 形态**：
- 任何一行 samples = 0 → 对应 emit 路径未触发；回 (a)/(b)/(c) 排查
- `fallback_rate` 是 NULL → `plan_total` 分母 0；mode=dual 流量太少，加大灰度
- `fallback_rate` > 0.05 → **STOP，不准 promote**；查 `plan_fallback_total{reason}` 分布定位

```bash
# 分布定位
psql "$DATABASE_URL" -c "
  SELECT labels->>'reason' AS reason, sum(value) AS hits
  FROM hive_metrics
  WHERE name='specdriven.plan_fallback_total' AND ts > now() - interval '30 min'
  GROUP BY 1 ORDER BY 2 DESC
"
# reason 取值锁定：schema_invalid / llm_timeout / over_budget / unknown
```

✅ 通过判据：三 counter 全有数据 + fallback_rate ≤ 0.05。

---

## 5. (e) Intake decision dual 占比 ≥ 10%

**目的**：证明 dual mode 下 intake 真的把 ≥ 10% 流量路由到 spec path（即 spec runner 真在跑，没被 downshift 退化成 legacy）。

```bash
psql "$DATABASE_URL" <<'SQL'
WITH totals AS (
  SELECT labels->>'decision' AS decision, sum(value) AS hits
  FROM hive_metrics
  WHERE name='specdriven.intake_decision_total' AND ts > now() - interval '30 min'
  GROUP BY 1
)
SELECT decision, hits,
       round(100.0 * hits / sum(hits) OVER (), 2) AS pct
FROM totals
ORDER BY hits DESC;
SQL
```

**Expected**:
```
   decision    | hits |  pct
---------------+------+-------
 dual          | 1234 | 65.40
 legacy        |  600 | 31.80
 legacy_downshift_<reason> |  53 | 2.80
```

**FAIL 形态**：
- `dual` 占比 < 10% → 大量 downshift；看 `legacy_downshift_*` 分布
- 完全没有 `dual` 行 → mode 没切；回 §0.2

```bash
# downshift reason 定位
psql "$DATABASE_URL" -c "
  SELECT labels->>'decision' AS dec, sum(value)
  FROM hive_metrics
  WHERE name='specdriven.intake_decision_total'
    AND labels->>'decision' LIKE 'legacy_downshift_%'
    AND ts > now() - interval '30 min'
  GROUP BY 1 ORDER BY 2 DESC
"
```

✅ 通过判据：`decision='dual'` 占比 ≥ 10%。

---

## 6. (f) 关 PG → degraded 路径可见

**目的**：证明 N3 修复——PG 缺席不再静默，启动期会 emit 一条 `spec_change_store_disabled=1`，让 dashboard 抓得到降级态。

```bash
# 在另一台 staging 节点（或临时 docker）上跑——别动正在 (a)-(e) 的节点
# 取消 DATABASE_URL 环境变量后启动
DATABASE_URL='' systemctl start hive-degraded-test
# 或: docker run --rm -e DATABASE_URL='' hive:latest

sleep 10

# 检查日志
grep "spec_change_store disabled" /var/log/hive/degraded-test.log
```

**Expected**:
```
WARN  spec_change_store disabled — PG pool absent, spec write path will degrade (cas_conflict_total / spec_change_upsert_total counters will stay at 0). To enable: configure DATABASE_URL and restart.
```

**Metric 端验证**：因为这台节点 PG 不通，`hive_metrics` 表写不进去——必须把 obs 队列指向**另一个**有 PG 的 sink（如果架构允许 cross-node obs 写入）。简化做法：用 in-process metric counter 端点。

```bash
# 如果有 /admin/metrics 端点
curl -sf http://localhost:8080/admin/metrics | grep spec_change_store_disabled
# Expected: spec_change_store_disabled 1
```

**FAIL 形态**：log 里没有 disabled warn 行 → N3 修复回归；查 `internal/bootstrap/server.go:248` 是否被改动

✅ 通过判据：log 出现 disabled warn + (如果可访问) metric=1。**完成后立刻清理这台节点**，不要让降级态 instance 留在生产。

---

## 7. (g) Branch protection required check 已绑

```bash
gh api "repos/$GITHUB_OWNER/$GITHUB_REPO/branches/main/protection" \
  --jq '.required_status_checks.contexts'
```

**Expected** 输出包含：
```
"specdriven gate (race + coverage + SKIP→RED)"
```

**FAIL 形态**：context 列表不含 `specdriven gate` → 回 `spec-driven-rollout.md §0.1` 跑配置命令

✅ 通过判据：grep 命中。

---

## 8. (h) 工程侧二次评审 APPROVE 报告归档

已闭合（2026-04-20）：`~/.gstack/workspace/company/ceo-plans/2026-04-20-harden-spec-driven-phase2-round6-rereview.md`

---

## 9. Sign-Off 表（platform owner 填）

把每条 evidence（命令输出截图或文本块）粘到对应行后归档：

| 项 | 通过判据 | Evidence (paste here) | Owner | Date |
|----|----------|----------------------|-------|------|
| (a) | log 无 `disabled — PG pool absent` | | | |
| (b) | `hive_spec_changes` 5min 内 count ≥ 1 | | | |
| (c) | `cas_conflict_total{scenario}` 至少 1 行 | | | |
| (d) | 三 counter 全有数据 + `fallback_rate ≤ 0.05` | | | |
| (e) | `decision='dual'` 占比 ≥ 10% | | | |
| (f) | log 出现 disabled warn (degraded test instance) | | | |
| (g) | `gh api` 输出含 `specdriven gate` | | | |
| (h) | 已归档（见 §8） | ✅ 2026-04-20 | (eng-side) | 2026-04-20 |

**全 8 项 ✅ → flip task 12.8 父项为 `[x]` → `openspec archive harden-spec-driven-phase2`。**

任意一项 FAIL → 不准 promote，按 `spec-driven-rollback.md` 降档到 `mode=legacy`，开 ticket 走 RCA 复盘四步法。

---

## Appendix: 一键自检脚本（可选）

把以上 (a)/(b)/(d)/(e)/(g) 五项可纯查询的塞进一个脚本：

```bash
#!/usr/bin/env bash
# scripts/spec_driven_acceptance.sh
set -euo pipefail
: "${DATABASE_URL:?must set}"
: "${GITHUB_OWNER:?must set}"
: "${GITHUB_REPO:?must set}"
: "${STAGING_LOG:?must set}"

fail=0
check() { local name="$1" cond="$2"; if eval "$cond"; then echo "PASS  $name"; else echo "FAIL  $name"; fail=1; fi; }

check "(a) PG wired" '! grep -q "spec_change_store disabled" "$STAGING_LOG"'
check "(b) hive_spec_changes recent" '[ "$(psql "$DATABASE_URL" -At -c "SELECT count(*) FROM hive_spec_changes WHERE updated_at > now() - interval '\''10 min'\''")" -ge 1 ]'
check "(d) fallback_rate ≤ 5%" '[ "$(psql "$DATABASE_URL" -At -c "SELECT (coalesce((SELECT sum(value) FROM hive_metrics WHERE name='\''specdriven.plan_fallback_total'\'' AND ts > now() - interval '\''30 min'\''), 0) / NULLIF((SELECT sum(value) FROM hive_metrics WHERE name='\''specdriven.plan_total'\'' AND ts > now() - interval '\''30 min'\''), 0)) <= 0.05")" = "t" ]'
check "(e) dual ratio ≥ 10%" '[ "$(psql "$DATABASE_URL" -At -c "WITH t AS (SELECT labels->>'\''decision'\'' d, sum(value) v FROM hive_metrics WHERE name='\''specdriven.intake_decision_total'\'' AND ts > now() - interval '\''30 min'\'' GROUP BY 1) SELECT (SELECT v FROM t WHERE d='\''dual'\'') / sum(v) >= 0.1 FROM t")" = "t" ]'
check "(g) branch protection bound" 'gh api "repos/$GITHUB_OWNER/$GITHUB_REPO/branches/main/protection" --jq ".required_status_checks.contexts" | grep -q "specdriven gate"'

[ "$fail" -eq 0 ] && echo "ALL PASS — proceed to (c)/(f) manual + sign-off" || { echo "FAIL — see above"; exit 1; }
```

(c)/(f) 必须手动跑（要么造冲突，要么物理关 PG），不进自动脚本。
