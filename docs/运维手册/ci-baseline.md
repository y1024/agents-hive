# CI Baseline — session-scope-regression-matrix

> 状态：**Spike A 首稿（Phase 0.1–0.2 已闭环）**。Spike B（full-stack baseline）待跑。
> 维护规则：harness 在 main 首 7 次 green run 后采 p95 × 1.5 向上取整 → 定稿 `.github/workflows/e2e-session-scope.yml` 的 `timeout-minutes`。

## 0. 本地环境参考规格

- Platform: darwin 24.6.0（Apple Silicon）
- Go: 未采（Spike B 前采集）
- Node/npm: 未采（Spike B 前采集）
- 说明：本地规格仅作**量级参考**，正式 SLO 取 GitHub Actions `ubuntu-latest` runner 的实跑 p95。

## 1. Spike A — grep enforcement 证据（2026-04-20）

### 1.1 脚本：`scripts/ci/check_session_scope.sh` v0

核心断言：
- **R-1** — `eventBus\.Broadcast\(BroadcastMessage` 无紧邻白名单注释 → FAIL
- **R-2（窄化）** — `BroadcastGenericMessage\(EventType(AgentProgress|ToolCall|SkillInstallProgress)` 无紧邻白名单注释 → FAIL
- **白名单语义**：仅 `// no session scope by design` 紧邻前一行生效；跨行 / 函数头注释不豁免

### 1.2 验证矩阵

| # | 场景 | 预期 | 实跑结果 | 证据 |
|---|------|------|---------|------|
| 1 | 干净 baseline（main，post subagent-session-scoping archive） | exit 0 | ✅ exit 0 | `OK: internal/master/ session-scope contract clean (8 files scanned)` |
| 2 | 注入裸 `eventBus.Broadcast(BroadcastMessage{...})` 无注释 | exit 1 + file:line | ✅ exit 1，报 `_tmp_violation_probe.go:6` | R-1 pattern 打中 |
| 3 | 注入 `BroadcastGenericMessage(EventTypeAgentProgress, ...)` 无注释 | exit 1 + file:line | ✅ exit 1，报 `_tmp_violation_probe.go:12` | R-2 pattern 打中 |
| 4 | marker 位于函数头（非紧邻 Broadcast 行） | 不豁免（exit 1） | ✅ 仍 flagged | 确认"紧邻行才豁免"，防函数头注释绕过内部调用 |
| 5 | marker 紧邻 Broadcast（line-1） | 豁免（不 flag） | ✅ 未 flag，violations 3→2 | 白名单正向生效 |
| 6 | 清理 probe 后重跑 | exit 0 回归 | ✅ exit 0 | baseline 回归干净 |

### 1.3 已知 drift / Phase 1 前置

1. **spec R-2 枚举 vs 代码事实 drift**：spec 原文写 `EventType(Agent|Tool|Skill).*`，但 `master.go:518 / master.go:550 / lifecycle.go:157` 3 处 `BroadcastGenericMessage(EventType{AgentCreated|AgentDestroyed|ToolListChanged}, ...)` 为合法全局元数据（均已带 `// no session scope by design` 注释）。Spike A v0 采用窄化枚举 `EventType(AgentProgress|ToolCall|SkillInstallProgress)` 以匹配代码真实 session-scoped 事件族。
   - **闭环归属**：task 1.5（升级 production-ready）时，同步修订 spec R-2 pattern 或在 script 中改用统一白名单语义。二选一，设计 D1 不接受引入 `//nolint:session-scope` 等次级豁免标记。

2. ~~`master_subagent_broadcast_test.go` 编译 drift~~ — **已核实为虚警**（2026-04-20）。
   - lsp 诊断曾报 4 处 `unknown field SessionID` + `too many arguments`，但实跑 `go test -c ./internal/master/...` exit 0 + `go test -run TestMaster_CreateAgentProgressCallback ./internal/master/...` PASS。
   - `ProgressEvent.SessionID` 字段定义在 `internal/subagent/agentloop.go:40`，存在且可访问。lsp 可能是索引未刷新。
   - **教训**：红线二（事实驱动）——lsp/IDE 诊断属于二手信号，结论前必须 `go test -c` + `go build` 实跑验证。

## 2. Spike B — full-stack baseline

### 2.1 本地可采部分（2026-04-20）

| 项目 | 耗时 | 备注 |
|------|------|------|
| `go build ./cmd/server/` 冷编译 | **8.19s** | 产物 72 MB |
| `go test -c ./internal/master/...` 测试编译 | **5.93s** | regression tests 未加入前基线 |
| 两项合计量级锚点 | **~14s** | 仅后端编译，无 server 启动 / playwright |

### 2.2 本地阻塞点（无法采完整 cold → first test baseline）

1. **本地无 PostgreSQL**（`pg_isready NOT available`）—— `cmd/server/main.go` 依赖 PG 后端启动。本地不装 PG 的话，server 无法完成启动序列；因此"冷启动到 HTTP port listen 耗时"本地采不到。
2. **Playwright 未安装**（`frontend/package.json` 无 playwright 依赖；无 `frontend/playwright/` 目录）。装好 playwright + 下载浏览器需要 ~500 MB 网络传输，且本地数字不等于 CI runner 数字。
3. **mock 飞书 longconn stub 尚未实现**——Phase 4 task 4.2 正式交付（`httptest.Server` 覆盖 handshake + event push 两个 API），本次 Spike B 不预写。

### 2.3 结论

**本地 Spike B 完整数字采不到是预期的**——design D3 明确："baseline 需要真实 CI 环境跑出来，本地 POC 只是量级参考"。本地已采量级锚点 ~14s（go build + test compile），证明后端编译不是 CI 时长瓶颈。真实冷启动到首 case 总耗时，必须等 Phase 4 `.github/workflows/e2e-session-scope.yml` 首 run 在 ubuntu-latest runner 上实测。

### 2.4 CI 实测采集协议（Phase 4 启用）

harness 首 run 时 YAML 每个 step 加 `id:` + `::set-output name=duration::${SECONDS}`，在 workflow summary 里 aggregate：
- `go-build` 耗时
- `go-test` (regression matrix) 耗时
- `browser` (playwright)：0 spec 时应快速 "no tests found"，有 spec 后按 spec 数量增长
- `lint` (`check_session_scope.sh`) 耗时（预期 < 1s）
- 总 wall-clock

连跑 7 次 green run 后取 p95 × 1.5 向上取整 → 填入 workflow `timeout-minutes`。

## 2.5 本地 de-risk 验证（2026-04-20 workflow 落地后补采）

在 push workflow 到 GHA 之前做了三项**本地预演**，把"blocked on CI"的 first-run 失败风险前移到本地：

| 动作 | 工具 | 结果 | 意义 |
|------|------|------|------|
| `actionlint e2e-session-scope.yml` | github.com/rhysd/actionlint | **0 issues** | workflow YAML 语法层过关，不会因低级错误在首 run 红 |
| 本地跑 browser job `detect` step 逻辑 | `find frontend/playwright -type f -name '*.spec.ts'` | `has_specs=false` | no-specs short-circuit 路径按设计触发，不 fail workflow |
| 本地 cold `go test ./tests/regression/... -race -count=1` | `time` | **8.15s wall（5.93s user + 1.39s sys）** | go-tests step 本地底座，加 CI overhead 也远低于当前 `timeout-minutes: 15` 的占位 |

**结论**：当前 workflow `timeout-minutes` 占位（lint:5 / go-tests:15 / browser:15 / summary:2）与本地量级对比属于**宽松上限**——即便 CI overhead 是本地 10×，也远不会触发超时假红。task 4.5 的 7-run baseline 做的是"紧一点"的 refine，不是 loose → strict 的必要修正。

## 3. CI 首 7 green run 采集位（待填）

| Run # | Date | Total (min) | go-tests (min) | browser (min) | lint (s) | 备注 |
|-------|------|-------------|----------------|---------------|----------|------|
| 1 | TBD | | | | | |
| 2 | TBD | | | | | |
| 3 | TBD | | | | | |
| 4 | TBD | | | | | |
| 5 | TBD | | | | | |
| 6 | TBD | | | | | |
| 7 | TBD | | | | | |

**采齐后**：计算 `timeout-minutes = ceil(p95 × 1.5)`，更新 `.github/workflows/e2e-session-scope.yml`。

## 4. 跨 change 依赖

- `frontend-ws-handshake-regression` Phase 2 playwright spec 依赖本 change Phase 4 提供的 harness（`.github/workflows/e2e-session-scope.yml` + `config.test.json` + mock longconn stub）。
- 本 change Phase 4 workflow YAML **严禁硬编码任何 FE spec 文件名**——仅通过 `frontend/playwright/**/*.spec.ts` glob 拾取，保证 0 spec 存在时 step PASS。
