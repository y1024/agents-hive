# PPT 产品决策

> 上级索引：[2026-05-16-PPT生成工具与HTML预览实施计划.md](../2026-05-16-PPT生成工具与HTML预览实施计划.md)

## 3. 关键产品决策

### 决策 A：DeckSpec 是唯一事实源

不要以 HTML 为唯一源再“转换” PPTX。应定义一份结构化 `DeckSpec`：

```json
{
  "title": "一种新的工作方式",
  "language": "zh-CN",
  "style": "swiss",
  "theme": "ikb",
  "aspect": "16:9",
  "slides": [
    {
      "layout": "S01",
      "title": "一种新的工作方式",
      "subtitle": "AI agent 时代的组织折叠",
      "meta": "2026 / internal sharing"
    }
  ]
}
```

HTML renderer 和 PPTX renderer 都消费这份 spec。这样可以保证：

- Chat 右侧预览和下载 PPTX 内容一致。
- PPTX 是原生文本/形状/图片，能编辑。
- 后端可以做 schema 校验、版式校验、资产权限、大小限制。
- 后续可支持“改第 3 页标题”“换主题色”这类局部编辑。

DeckSpec 必须被持久化为 JSON 资产或 run record，不能只保存 HTML/PPTX。否则二期局部编辑、问题复现、审计和重新导出都会失去事实源。

### 决策 A1：DeckSpec Schema 必须是强类型合同

`generate_ppt` 的输入不能长期保持 `deck: object` 松散形态。实现时必须从 `internal/presentation/spec` 生成或维护一份 JSON Schema，并在工具注册时暴露给模型。

Schema 要求：

- 顶层字段固定：`version/title/language/style/theme/aspect/slides/assets/metadata`。首发 tool schema 只公开 `deck`，但服务层必须预留 `outline -> DeckSpec` 自动排版接口。
- `version` 首发固定为 `1`，未来破坏性变更必须升级版本。
- `slides[*].layout` 使用 discriminated union：不同 layout 有不同 required 字段、最大长度、图片槽位和 notes 限制。
- `style/theme/layout` 的 enum 来自 template registry，不在 prompt 里手写散落。
- `assets` 首发只允许 `asset_uri`、受控内部图片 URL、受限 data URI；不允许本地文件路径，不允许任意外部 HTTPS URL。
- 所有字符串要有最大长度；正文长段落应由 schema 拒绝或由自动排版器拆页，不能让 PPTX renderer 临场缩放到不可读。
- 工具的 validation error 必须返回 JSON pointer，例如 `/slides/3/title`、`/slides/5/images/hero`，让模型可修复。

自动排版接口决策：

- 首发可以不把 `outline` 暴露给模型，但 `internal/presentation/spec` 应定义 `OutlineSpec` 和 `LayoutPlanner` 接口。
- `LayoutPlanner` 输入是标题、受众、页数、要点、图片引用；输出是完整 `DeckSpec`。
- 这样后续从“用户一句话/大纲”生成 PPT 时，不需要重写 tool 合同。

### 决策 A2：PresentationRun 是审计和重试单位

每次生成都必须创建 `PresentationRun`。首发采用 DB 记录作为事实来源，asset tags 只用于下载侧辅助校验和对象查找，不作为唯一状态存储。

固定字段：

```json
{
  "run_id": "prun_...",
  "session_id": "sess_...",
  "turn_id": "trace_...",
  "user_id": "user_...",
  "owner_scope": "user",
  "owner_id": "user_...",
  "domain_id": "generic",
  "style": "swiss",
  "theme": "ikb",
  "slide_count": 8,
  "mode": "editable",
  "deck_spec_asset_uri": "asset://presentations/....json",
  "html_asset_uri": "asset://presentations/....html",
  "pptx_asset_uri": "asset://presentations/....pptx",
  "validation_status": "passed",
  "created_at": "2026-05-16T00:00:00Z"
}
```

首发实现：

- 采用 `presentation_runs` store：记录状态、错误、统计、关联 asset URI。
- 同时上传 `DeckSpec JSON` 为 `presentation_format=deckspec` 的 asset，供审计、重试和二期编辑使用。
- HTML/PPTX/DeckSpec asset 必须共享相同 `presentation_run_id` tag。

`presentation_runs` 表结构：

```sql
CREATE TABLE IF NOT EXISTS presentation_runs (
  id                    TEXT PRIMARY KEY,
  status                TEXT NOT NULL,
  stage                 TEXT NOT NULL DEFAULT '',
  progress_percent      INTEGER NOT NULL DEFAULT 0,
  title                 TEXT NOT NULL DEFAULT '',
  mode                  TEXT NOT NULL DEFAULT 'editable',
  style                 TEXT NOT NULL DEFAULT '',
  theme                 TEXT NOT NULL DEFAULT '',
  slide_count           INTEGER NOT NULL DEFAULT 0,
  owner_scope           TEXT NOT NULL DEFAULT 'user',
  owner_id              TEXT NOT NULL,
  user_id               TEXT NOT NULL DEFAULT '',
  session_id            TEXT NOT NULL DEFAULT '',
  turn_id               TEXT NOT NULL DEFAULT '',
  trace_id              TEXT NOT NULL DEFAULT '',
  tool_call_id          TEXT NOT NULL DEFAULT '',
  domain_id             TEXT NOT NULL DEFAULT '',
  deck_spec_asset_uri   TEXT NOT NULL DEFAULT '',
  html_asset_uri        TEXT NOT NULL DEFAULT '',
  pptx_asset_uri        TEXT NOT NULL DEFAULT '',
  validation_status     TEXT NOT NULL DEFAULT '',
  error_kind            TEXT NOT NULL DEFAULT '',
  error_message         TEXT NOT NULL DEFAULT '',
  error_json_pointer    TEXT NOT NULL DEFAULT '',
  recoverable           BOOLEAN NOT NULL DEFAULT FALSE,
  recoverable_by        TEXT NOT NULL DEFAULT '',
  warnings              JSONB NOT NULL DEFAULT '[]'::jsonb,
  metrics               JSONB NOT NULL DEFAULT '{}'::jsonb,
  claimed_by            TEXT NOT NULL DEFAULT '',
  lease_expires_at      TIMESTAMPTZ,
  expires_at            TIMESTAMPTZ,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT presentation_runs_status_check
    CHECK (status IN ('created','running','succeeded','failed','cancelled')),
  CONSTRAINT presentation_runs_stage_check
    CHECK (stage IN ('','validating','resolving_images','rendering_html','exporting_pptx','uploading_assets')),
  CONSTRAINT presentation_runs_mode_check
    CHECK (mode IN ('editable','visual')),
  CONSTRAINT presentation_runs_recoverable_by_check
    CHECK (recoverable_by IN ('','model','user','operator','none'))
);

CREATE INDEX IF NOT EXISTS idx_presentation_runs_owner_updated
  ON presentation_runs(owner_scope, owner_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_presentation_runs_session
  ON presentation_runs(session_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_presentation_runs_status_lease
  ON presentation_runs(status, lease_expires_at);
```

迁移位置：

- `presentation_runs` 是核心业务表，首发放入 `internal/store/postgres_migrate.go` 的 `pgInitSQL`。
- 如后续字段演进，在同文件添加 `ALTER TABLE presentation_runs ADD COLUMN IF NOT EXISTS ...` 兼容段。
- 不另建零散 migration 文件，避免和当前仓库“一次性初始化 + 兼容 ALTER”的模式冲突。

状态机：

```text
created/status -> running(stage=validating)
running(stage=validating) -> running(stage=resolving_images)
running(stage=resolving_images) -> running(stage=rendering_html)
running(stage=rendering_html) -> running(stage=exporting_pptx)
running(stage=exporting_pptx) -> running(stage=uploading_assets)
running(stage=uploading_assets) -> succeeded(stage='')
running(stage=validating|resolving_images|rendering_html|exporting_pptx|uploading_assets) -> failed(stage=失败阶段)
```

约束：

- `status` 只表示生命周期：`created/running/succeeded/failed/cancelled`。
- `stage` 只表示当前执行阶段：空字符串或 `validating/resolving_images/rendering_html/exporting_pptx/uploading_assets`。
- `mode` 数据库层预留 `visual`，避免 M3 增加视觉版时修改 CHECK 约束；首发 Go validator、tool schema 和公开 UI 仍只允许 `editable`。
- `recoverable` 保留为前端兼容布尔值；`recoverable_by` 是强语义，取值为 `model/user/operator/none`，用于决定是否让模型自动重试、提示用户换输入，还是要求运维处理。
- 进度百分比由后端阶段推进生成，不能由模型输入。
- `succeeded` 和 `cancelled` 必须把 `stage` 清空；`failed` 保留失败阶段用于排查。

首发生成模式：

- M1 薄片可以在工具调用内等待结果，但执行语义仍必须以 `presentation_runs` 为中心。
- M2 起固定使用“先创建 run，再由 `PresentationRunWorker` 执行，tool call 最多等待 `sync_timeout_seconds`”的模式。
- 如果 worker 在 `sync_timeout_seconds` 内完成，工具返回 succeeded 和 asset URI。
- 如果 worker 未完成，工具返回 `status:"running"`、`run_id`、`stage/progress`，前端轮询 run 状态。
- 禁止在 HTTP/tool 请求线程里直接跑长任务后“超时返回 running”。否则请求 context 取消、进程重启或客户端断开都会导致后台状态丢失。

### 决策 A3：异步 Run API 和后台 worker 必须闭合

一旦工具返回 `status:"running"`，后端必须有真实后台执行语义，不能只让前端进入 pending 状态。

Run 查询 API：

```http
GET /api/v1/presentation/runs/{run_id}
```

权限规则：

- 必须登录。
- `run.owner_scope == "user"` 且 `run.owner_id == auth.UserIDFrom(ctx)`。
- 如果请求带 `session_id` 且 run 有 `session_id`，两者必须匹配。
- 请求不带 `session_id` 时，同 owner 允许查询，用于历史消息恢复和资产列表；记录审计字段 `presentation_run_read_without_session=true`。

返回结构：

```json
{
  "kind": "presentation_run",
  "run_id": "prun_...",
  "status": "running",
  "progress": {
    "stage": "exporting_pptx",
    "percent": 70
  },
  "title": "一种新的工作方式",
  "mode": "editable",
  "style": "swiss",
  "theme": "ikb",
  "slide_count": 8,
  "deck_spec_asset_uri": "asset://presentations/....json",
  "html_asset_uri": "asset://presentations/....html",
  "pptx_asset_uri": "",
  "warnings": [],
  "error": null,
  "updated_at": "2026-05-16T00:00:10Z"
}
```

当 `status:"succeeded"` 时，Run API 必须返回和工具成功结果同构的字段，前端才能把 pending artifact 原地升级为可预览/可下载 artifact：

```json
{
  "kind": "presentation",
  "run_id": "prun_...",
  "status": "succeeded",
  "title": "一种新的工作方式",
  "mode": "editable",
  "style": "swiss",
  "theme": "ikb",
  "slide_count": 8,
  "deck_spec_asset_uri": "asset://presentations/....json",
  "html_asset_uri": "asset://presentations/....html",
  "pptx_asset_uri": "asset://presentations/....pptx",
  "warnings": []
}
```

失败返回中的 `error`：

```json
{
  "error_kind": "pptx_validation_failed",
  "message": "editable pptx validation failed",
  "recoverable": false,
  "recoverable_by": "operator",
  "json_pointer": ""
}
```

后台 worker 语义：

- `PresentationRunWorker` 从 `presentation_runs` 中领取 `status='created'` 或 `status='running' AND lease_expires_at < now()` 的任务。
- 领取必须使用单条原子 SQL，配合 `FOR UPDATE SKIP LOCKED` 或等价机制，写入 `status='running'`、`claimed_by` 和新的 `lease_expires_at`，防止多进程重复执行。
- 每个阶段更新 `status/stage/progress/updated_at`。
- worker 每 10 秒续租一次；如果进程退出，租约过期后其他 worker 可恢复执行。
- worker context 不直接继承 tool call context；必须从 run 记录恢复 `RuntimeContext` 所需的 user/owner/session/domain/turn facts。
- 进程重启后，`lease_expires_at` 过期的 running run 可被重新领取。
- 单个 run 总超时默认 5 分钟；超过后标记 `failed`，`error_kind="timeout"`。
- 用户停止当前 chat turn 不取消后台 run；后续可增加显式取消 API。
- `cancelled` 首发只作为保留状态，不提供用户取消 API；除非后续新增显式取消接口，否则普通 stop chat 不得写入 `cancelled`。
- 清理临时文件在 `defer/finally` 中执行，清理失败只记录 warning。

领取 SQL 形态固定为：

```sql
WITH candidate AS (
  SELECT id
  FROM presentation_runs
  WHERE
    (status = 'created' OR (status = 'running' AND lease_expires_at < NOW()))
    AND (expires_at IS NULL OR expires_at > NOW())
  ORDER BY created_at ASC
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE presentation_runs r
SET
  status = 'running',
  claimed_by = $1,
  lease_expires_at = NOW() + INTERVAL '30 seconds',
  updated_at = NOW()
FROM candidate
WHERE r.id = candidate.id
RETURNING r.*;
```

必须新增 API 测试：

- owner 查询 running run 通过。
- owner 查询 succeeded run 返回 asset URI。
- 错 owner 查询 403。
- 带错 session 查询 403。
- 不带 session 同 owner 查询通过并记录审计。
- failed run 返回结构化 error。
- failed run 返回 `recoverable_by`，并且 `invalid_spec=model`、`quota_exceeded=user`、`exporter_unavailable=operator`、`pptx_validation_failed=operator`。

### 决策 A4：图片解析和 asset 上传必须幂等

图片 resolve 和 asset 上传都是容易失败且会产生外部副作用的阶段，必须单独建模，不能隐藏在 renderer 内。

图片 resolve 阶段：

- 固定发生在 `validating` 之后、`rendering_html` 之前，对应 `stage='resolving_images'`。
- Go 侧一次性解析 DeckSpec 中所有图片引用，生成 `resolved_assets` 供 HTML renderer 和 PPTX worker 共用。
- Renderer 不得自行下载图片，Node worker 不得访问网络。
- 图片解析结果写入 `metrics.resolved_assets`，至少包含 `slot/id/mime/width/height/bytes/source_kind`。
- 失败策略由 layout registry 决定：必填图片失败返回 `image_resolution_failed`；可选图片失败可替换为占位 shape，并写 warning。

asset 上传幂等：

- DeckSpec JSON、HTML、PPTX 三类 asset 必须先全部上传成功，再用一次 store 更新把 `deck_spec_asset_uri/html_asset_uri/pptx_asset_uri/status=succeeded/stage=''` 写入 run。
- 每次上传 asset 时 tag 必须包含 `presentation_run_id`、`presentation_format`、`presentation_asset_state=pending|committed`。
- 上传成功但最终 run 未 succeeded 的 asset 保留 `pending` 状态；重试时先按 `presentation_run_id + presentation_format` 查已有 pending/committed asset，能复用则复用，不能重复生成无限副本。
- 最终 run succeeded 后，三类 asset 必须进入 committed 语义。Phase 4 必须扩展 `AssetMetaStore`：按 namespace/tags 列出 presentation asset，并支持 metadata/tag 更新，把 `presentation_asset_state` 从 `pending` 更新为 `committed`；如果底层 provider 暂不支持 tag 更新，则 run 表中的 committed URI 是授权事实源，同时 GC 只能删除未出现在 run committed URI 字段里的 pending asset。
- 如果上传 2/3 后第三个失败，run 标记 `failed(stage=uploading_assets)`，错误为 `asset_unavailable`，已上传 asset 由 retry/GC 处理，不允许静默遗留不可追踪孤儿。
- GC 必须能清理过期 failed/running run 下 `pending` presentation assets。

必须新增测试：

- 图片 resolve 只发生一次，HTML/PPTX 共用同一份 resolved asset。
- 必填图片 resolve 失败返回 `image_resolution_failed` 和正确 JSON pointer。
- 可选图片失败时输出 warning，PPTX 用占位 shape，不触发下载成功假象。
- 上传 2/3 asset 后失败，run failed，已上传 pending asset 可被重试复用或被 GC dry-run 列出。
- 重试同一 run 不重复上传已存在的 DeckSpec/HTML/PPTX asset。

### 决策 B：HTML 是预览产物，不是任意输入

支持用户看到 HTML deck，但 HTML 应由受控 renderer 生成。首发不要开放“模型传入任意 HTML -> 工具打包 PPTX”，原因：

- 任意 HTML/CSS 到可编辑 PPTX 没有稳定通用映射。
- 任意 `<script>` / 外链 / iframe 会扩大安全面。
- 复杂 CSS、WebGL、动画无法自然映射到 PowerPoint 可编辑对象。

如需保留极高视觉还原，M3 可增加 fallback：生成“图片版 PPTX”。首发不在用户 UI 暴露该能力；后续如暴露，UI 必须明确标记为“视觉版，不可编辑文本”。

### 决策 B1：首发 deck 翻页由 React 控制，不依赖 iframe 脚本

现有 `HtmlRenderer` 已经支持 `srcDoc` + sandbox。为了缩小安全面，presentation 首发应让 `DeckRenderer` 在 React 层控制当前页、键盘、全屏、notes 和缩略图；iframe 只渲染单页 HTML，不需要 deck 内脚本处理翻页。

允许脚本的情况：

- 仅限 `trustedGenerated=true` 且 HTML 由后端 renderer 生成。
- 仅限后续确实需要页面内动画或图表时开启。
- 不允许外部 `<script src>`，不允许 `allow-same-origin`。截图子系统如需 DOM 访问，应使用服务端 Playwright 的受控页面，而不是放宽用户 Canvas iframe。

因此，`renderhtml` 首发输出应是无脚本 HTML；不要从 `template-swiss.html` 直接搬完整翻页脚本。

### 决策 C：首发公开 Swiss，Editorial 只做候选模板研究

首发公开支持：

- `style: "swiss"`：优先，适合 AI/产品/技术/数据汇报，结构化程度高。
- `style: "editorial"`：不进入首发公开 schema，只保留 template registry、fixture 和二期候选研究。若要提前内测，必须 behind feature flag，并且不能影响 `generate_ppt` 对普通用户只暴露 Swiss 的合同。

Swiss 首发不必一次实现 S01-S22 的全部 PPTX renderer。固定实现 8 个高频版式：

- `S01` Cover
- `S03` Split Statement
- `S04` Six Cells
- `S08` Duo Compare
- `S11` Horizontal Timeline
- `S15` Matrix / Multi Image Grid
- `S19` Four Cards
- `S22` Image Hero

HTML renderer 可以先覆盖更多版式，PPTX renderer 首发只对受支持版式输出可编辑对象。未支持版式在 spec 校验阶段拒绝，避免静默降级。
