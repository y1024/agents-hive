# Hive 整改方案（基于 deer-flow 对标）

> **作者**: Claude（主线程合成 + 子 agent 调研 + 蓝军 mutation 校验）
> **日期**: 2026-04-22
> **范围约束**: Hive 只做国内企业 IM（飞书/微信/企微/钉钉），不做海外 IM
> **详细证据**: `evidence/axis-1-tools.md` · `axis-2-memory.md` · `axis-3-skills.md` · `axis-4-mcp-acp.md` · `axis-5-channels-uploads-artifacts.md` · `axis-6-prompts-claude.md` · `axis-6-prompts-codex.md`

---

## §1 必须整改（P0，~2 个月）

| # | 项 | 为什么必须做 | 工期 |
|---|---|---|---|
| P0-1 | `internal/pathguard` 包 | 路径遍历防护散落各处，易漏；P0-2 依赖它 | 1 周 |
| P0-2 | Artifact HTTP 端点 | 前端无法下载 thread 生成物（HTML/图片/PDF） | 2 周 |
| P0-3 | Skill Gateway CRUD API | 运营改 skill 要改代码重启，无管理入口 | 3 周 |
| P0-4 | Prompt Gateway（schema + CI lint + 热 reload） | `strings.NewReplacer` 无占位符校验，漏填会字面泄漏；prompt 改了要重启 | 2 周 |
| P0-5 | execution.md 安全硬化 | destructive 命令禁令仅靠工具层兜底，prompt 层缺显式约束 | 1 周 |

**串行 9 周，2 人并行 6-7 周。**

---

## §2 可选优化（P1，1 个月内）

| # | 项 | 工期 |
|---|---|---|
| P1-1 | Suggestions 路由（follow-up 问题生成） | 3 天 |
| P1-2 | MCP mtime 缓存失效（防 skill 文件变更后老 cache 残留） | 2 天 |
| P1-3 | 搜索/爬取栈：SearXNG + crawl4ai 自部署，包装成 `web_search`/`web_fetch` | 1-2 周 |
| P1-4 | 飞书 PatchCard 模式开源反哺（`ErrPatchRateLimited` 抽 OSS 子库） | 1 周 |
| P1-5 | MCP 规范版本订阅（订 RSS + CI sync，防协议漂移） | 0.5 天 + 持续 |
| P1-6 | RAG top-k 写回 prompt（`<rag_hits>…</rag_hits>` 结构化注入） | 2 天 |
| P1-7 | i18n 切换审计 + en-US locale 实装（依赖 P0-4） | 3 天 |
| P1-8 | safety.md 对照 OWASP LLM Top 10 季度审计 | 持续 |
| P1-9 | markitdown 集成（需先做 Excel/PPT 质量对比 vs 现有 fileconv） | 待定 |

---

## §3 P0 详细规格

### P0-1：`internal/pathguard` 包

**目标**：集中路径遍历防护，替换散落各处的 `filepath.Clean` / `strings.Contains("..")`。
**落地**：新增 50 行 Go + 重构全局调用点。

```go
package pathguard

func SafeJoin(base, userPath string) (string, error)
func ValidateFilename(name string) error
```

**验收标准**：
1. `SafeJoin("/tmp/t1", "../etc/passwd")` 返回 error
2. `SafeJoin("/tmp/t1", "ok/sub/file.txt")` 返回 `/tmp/t1/ok/sub/file.txt`
3. 全项目 grep `filepath.Clean` 和 `strings.Contains` 收敛到 pathguard
4. 单测覆盖 ≥ 20 个 edge case
5. CI lint rule 禁止 `filepath.Clean` 直接调用

---

### P0-2：Artifact HTTP 端点

**目标**：前端能直接下载 thread 生成物。
**落地**：新增 `internal/gateway/methods_artifacts.go` 约 200-300 Go 行。

- 路由：`GET /api/threads/{thread_id}/artifacts/*path`
- XSS 硬化：HTML / XHTML / SVG 强制 `Content-Disposition: attachment`
- MIME 嗅探：image/pdf 走 inline，其他走 download
- 路径校验：走 P0-1 的 `pathguard.SafeJoin`
- 权限：校验 thread 属于请求用户

**验收标准**：
1. curl 请求 HTML 不在浏览器直接执行（force download）
2. 路径 `../../../etc/passwd` 返回 400
3. 陌生 thread_id 返回 404
4. 性能: P99 < 200ms（100MB 文件流式）

---

### P0-3：Skill Gateway CRUD API

**目标**：运营从前端/API 直接管 skill，不改代码、不重启。
**落地**：新增 `internal/gateway/methods_skills.go` 约 500 Go 行。

- `POST /api/skills/install` — 上传 `.skill` ZIP 或 git URL
- `POST /api/skills/{name}/enable` / `{name}/disable`
- `GET /api/skills` — 列出 + 状态
- `GET /api/skills/{name}/history` — 安装/升级历史
- DB 表 `skill_installations` 记录来源 + checksum + enabled_at

**验收标准**：
1. 上传 `.skill` ZIP → 校验 frontmatter → 落盘 → 热 reload
2. disable 后 skill 不再被 router 选中
3. 历史追踪完整（谁装的/何时/校验和）
4. 并发 install 不丢数据

---

### P0-4：Prompt Gateway（占位符 schema + CI lint + 热 reload）

**目标**：阻断"漏填占位符字面泄漏"的隐性 bug；给运营端 prompt 管理留扩展口。
**落地**：新增 `internal/prompt/gateway.go` 约 300 Go 行 + `prompts/*.schema.json` + CI lint。

- 每个 `.md` 配套 `.schema.json`，列出必填占位符（如 `{{CONVERSATION}}` / `{{MAX_LENGTH}}`）
- Render-time 校验：替换完后扫残留 `{{[A-Z_]+}}` → 返回 error
- CI lint：对 `prompts/*.md` 扫占位符集合，与 `.schema.json` 双向校验
- 热 reload：dev 走 mtime 监听；prod 走 admin API
- 复用 `PromptStoreReader` 接口，为 P1-7 多语种铺路

**验收标准**：
1. 故意漏替换 `{{CONVERSATION}}` → `Build()` 返回 error，不进入 LLM
2. CI 发现 schema 与 `.md` 占位符不一致 → pipeline 失败
3. 改 prompt 文件无需重启（dev ≤ 2s，prod 一次 admin call）
4. 单测 ≥ 15 个 edge case
5. `PromptStoreReader` 提供 DB mock 实现

---

### P0-5：execution.md 安全硬化

**目标**：destructive 操作禁令从工具层兜底上升为 prompt 显式约束，双层防御。
**落地**：改 `internal/i18n/prompts/execution.md` 约 +80 行 + 红军 mutation 测试。

- 明示 destructive 清单：`rm -rf /` / `git push --force main` / `git commit --no-verify` / `pkill -9` / DB `DROP TABLE`
- Escalation 规则：触发清单时停止并请求用户 confirm，不得直接执行
- 红军 mutation：10 个危险 prompt，每个必须触发拒绝或确认路径

**验收标准**：
1. 10 个红军指令全部触发 refusal / confirmation
2. prompt 体积膨胀 ≤ 20%
3. happy-path regression ≤ 1%
4. 工具层与 prompt 层双层防御交叉测试

---

## §4 时间表

| 阶段 | 周数 | 备注 |
|---|---|---|
| P0-1 pathguard | 1 | 先行，P0-2 依赖 |
| P0-2 Artifact 端点 | 2 | 依赖 P0-1 |
| P0-3 Skill CRUD | 3 | DB 迁移 + API + 前端管理页 |
| P0-4 Prompt Gateway | 2 | 可与 P0-2/P0-3 并行 |
| P0-5 execution.md 硬化 | 1 | 可与 P0-3 末段并行 |
| **P0 串行（5 项）** | **9 周** | |
| **P0 并行（2 人）** | **6-7 周** | |
| P1 总计 | 5-9 | 视优先级 |

---

## §5 风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Artifact 端点缺失导致前端继续受限 | 高 | 中 | P0-2 两周内落地 |
| pathguard 重构 grep 漏改残留旧代码 | 中 | 中 | CI lint 禁 `filepath.Clean` 直接调用 |
| Skill CRUD 并发 install 竞态丢数据 | 中 | 高 | 验收 4 必须通过压测 |
| Prompt Gateway schema 维护成本 | 中 | 低 | CI lint 强制，不靠人 |
| execution.md 硬化误伤 happy path | 中 | 中 | 红军 mutation + happy-path regression 双跑 |
| MCP spec 年更导致自研协议过时 | 低 | 中 | P1-5 订阅 + 版本 sync |

---

## §6 验证方法学

每个 P0 落地必须通过**蓝军 mutation + 命令输出证据**：
1. **蓝军 mutation**：每个 P0 规格提 3-4 个假想 mutation，先写对抗用例再实现
2. **命令证据**：不接受口头汇报，必须附 `curl` / `go test -v` / `psql` 输出
3. **性能基线**：P0-2 和 P0-3 给 P99 latency 数字
4. **独立评审**：每个 P0 落地前跑一次独立 code review

---

## §7 待确认决策

1. **P0 顺序 1→2→3→4→5**（pathguard 先行）。接受?
2. **P0-4 Prompt Gateway 接受?** 核心是 schema 校验 + CI lint；热 reload 可 MVP 仅 dev。接受 / 降级 P1 / 拒绝?
3. **P0-5 execution.md 硬化接受?** 若认为工具层已兜底不必重复，可降级 P1（蓝军认为双层防御必要）。接受 / 降级?
4. **P1-9 markitdown**：先做 Excel/PPT 质量对比还是直接跳过?
5. **P1-7 en-US 实装**：依赖 P0-4。是否等 P0-4 完成后立即启动?

---

## §8 文件索引

**本方案**: `docs/调研笔记/deer-flow/PLAN.md`

**支撑证据**: `evidence/` 目录
- `axis-1-tools.md` — 工具对比
- `axis-2-memory.md` — Memory/RAG
- `axis-3-skills.md` — Skills
- `axis-4-mcp-acp.md` — MCP + ACP
- `axis-5-channels-uploads-artifacts.md` — Channels/Uploads/Artifacts
- `axis-6-prompts-claude.md` + `axis-6-prompts-codex.md` — Prompt 双方蓝军报告

**历史溯源**: `archive/` 目录（不再引用）

*— End of PLAN —*
