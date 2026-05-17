# PPT 生成工具实施计划 — 工程审核报告

> 审核日期：2026-05-16
> 审核分支：storage-kb
> 审核对象：`docs/计划与路线/2026-05-16-PPT生成工具与HTML预览实施计划.md`

---

## 0. 范围挑战结论

**复杂度：** 57 文件（39 新增 + 18 修改），4 个新服务/模块。触发复杂度检查。

**判定：必要复杂度，不缩减。**

理由：
- PPT 生成是一等内置能力，不是简单工具包装
- DeckSpec 同源架构避免 HTML/PPTX 不一致问题
- 安全、权限、观测、GC 都是生产必需
- Node worker 隔离是稳定性要求
- 没有重建已有模块，没有引入不必要抽象
- 首发明确不做流式预览、visual PPTX 公开、任意远程图片

**已有代码复用：**

| 已有模块 | 复用方式 |
|----------|---------|
| `internal/asset` | 直接使用 Upload/Resolve/Proxy |
| `internal/asset/access.go` ResolveContext | 扩展 presentation 专用 resolver |
| `internal/router/capability_registry.go` | 照搬 generate_image 模式注册 |
| `internal/fileconv/office.go` | 用于 PPTX 验收测试 |
| `frontend/src/components/canvas/renderers/HtmlRenderer.tsx` | 参考 sandbox 模式 |
| `frontend/src/utils/artifactParser.ts` | 扩展 presentation 类型 |

---

## 1. 架构审核

### [P1] 发现 1: Asset 上传与 Run 状态更新的事务边界 (confidence: 9/10)

**问题：** 状态机 `exporting_pptx -> uploading_assets -> succeeded`。如果三个 asset（DeckSpec JSON、HTML、PPTX）上传了两个后崩溃，run 状态停在 `uploading_assets`，租约过期后被另一个 worker 领取重试。已上传的 asset 成为孤儿。

**影响：** 孤儿 asset 占用存储。重试时重复上传。GC 无法安全清理（run 还在 running 状态）。

**推荐：**
1. 三个 asset 上传使用"先上传，最后一步原子更新 run 状态 + 所有 asset URI"模式
2. 重试时先检查已有 asset（通过 `presentation_run_id` tag 查找），跳过已上传的
3. GC 对 `running` 超过 `expires_at` 的 run 的 asset 也要能清理

---

### [P1] 发现 2: 图片 resolve 阶段在状态机中位置不明确 (confidence: 8/10)

**问题：** 状态机定义了 `validating -> rendering_html -> exporting_pptx -> uploading_assets`。但图片 resolve（下载 asset、验证 MIME、转存临时文件）是一个可能耗时且可能失败的操作。它应该在哪个阶段？

- 如果在 `validating`：验证阶段变得很重，可能超时
- 如果在 `rendering_html` 和 `exporting_pptx` 之间：需要新增阶段
- 如果分散在两个 renderer 中：重复下载

**推荐：** 在 `validating` 和 `rendering_html` 之间新增 `resolving_images` 阶段。图片 resolve 是独立的、可失败的、有明确错误分类的操作，值得单独追踪。

```
created -> running(validating) -> running(resolving_images) -> running(rendering_html) 
  -> running(exporting_pptx) -> running(uploading_assets) -> succeeded
```

同时更新 `presentation_runs.stage` CHECK 约束。

---

### [P2] 发现 3: Node Worker 无全局并发控制 (confidence: 8/10)

**问题：** 每次 `generate_ppt` 调用都 spawn 一个新 Node 进程。如果并发 10 个用户同时生成 PPT，就是 10 个 Node 进程同时运行。内存峰值不可控。

**推荐：** 在 Go 侧加一个 semaphore 限制并发 worker 数（默认 2-3）。超过并发上限的请求排队或直接返回 `exporter_busy`。

```go
// internal/presentation/pptx/node_worker.go
var workerSemaphore = make(chan struct{}, 3) // 从 config 读取

func (e *Exporter) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
    select {
    case workerSemaphore <- struct{}{}:
        defer func() { <-workerSemaphore }()
    case <-ctx.Done():
        return ExportResult{}, ctx.Err()
    default:
        return ExportResult{}, ErrExporterBusy
    }
    // ... 现有逻辑
}
```

配置项：`presentation.max_concurrent_workers` 默认 3。

---

### [P2] 发现 4: HTML 结构是 Go renderer 和前端 parser 之间的隐式合同 (confidence: 8/10)

**问题：** Go 的 `renderhtml/swiss.go` 输出 `<section class="slide" data-slide-id="N" data-layout="Sxx">`，前端 `parseDeck.ts` 用 DOMParser 解析这个结构。但这个 HTML 结构没有版本化。如果 Go renderer 改了输出格式，前端 parser 静默失败，返回 0 slides。

**推荐：**
1. HTML deck 顶部加 `<meta name="hive-deck-version" content="1">` 标记
2. 前端 `parseDeck` 先检查版本，不匹配时返回结构化错误而非空数组
3. 在 Go 测试中断言 HTML 输出能被前端逻辑正确解析（用 Go 的 HTML parser 模拟）

---

### [P2] 发现 5: DeckSpec JSON Schema 与 Go 类型的同步机制未定义 (confidence: 9/10)

**问题：** 计划说 `spec/schema.go` 导出 JSON Schema 给 `generate_ppt` tool schema 引用。但 Go 没有原生的 struct-to-JSON-Schema 生成。计划没有指定是手写 schema、用库生成、还是从 `layouts.json` 动态构建。

**影响：** 如果手写，Go types 和 JSON Schema 会 drift。

**推荐：**
1. 首发用 `layouts.json` 作为 schema 的唯一事实源
2. `schema.go` 在 init 或 startup 时从 `layouts.json` 动态构建 JSON Schema
3. 测试断言：Go validator 和 JSON Schema 对同一份 fixture 的校验结果一致
4. 不要手写两份独立的 schema 定义

---

### [P2] 发现 6: 同步/异步双模式的前端恢复路径缺失 (confidence: 7/10)

**问题：** 工具可能返回 `status:"succeeded"`（同步完成）或 `status:"running"`（需要轮询）。如果网络抖动导致 succeeded 响应丢失，前端无法恢复。

**推荐：**
1. 前端对 `status:"succeeded"` 的工具结果也记录 `runId`
2. 如果 Canvas 打开时 `htmlAssetUri` resolve 失败，fallback 到查询 run 状态
3. 历史消息恢复时，对所有 presentation artifact 都走一次 run 状态确认

---

### [P3] 发现 7: Worker 协议版本不兼容时的行为未定义 (confidence: 7/10)

**问题：** 计划定义 `protocol_version: 1`，startup health check 返回版本。但如果 Go server 升级到 protocol v2 而 worker 还是 v1（部署不同步），会发生什么？

**推荐：** Health check 时记录 worker protocol version。发送请求时，如果 worker version < request version，返回 `exporter_unavailable` 而非发送不兼容请求。这是 2 行代码的防御。

---

### [P3] 发现 8: `presentation_runs.mode` CHECK 约束只有 `editable` (confidence: 9/10)

**问题：** SQL 定义 `CHECK (mode IN ('editable'))`。M3 要加 `visual` 模式时，需要 ALTER TABLE 修改 CHECK 约束。PostgreSQL 修改 CHECK 约束需要 DROP + ADD，在大表上可能锁表。

**推荐：** 首发就把 CHECK 写成 `CHECK (mode IN ('editable', 'visual'))`，但在 Go 层和 tool schema 层限制只允许 `editable`。这样 M3 不需要 DDL 变更。成本为零，收益是避免未来 migration 风险。

---

## 2. 代码质量审核

### [P2] 发现 9: DRY 违反 — 限制值散落多处 (confidence: 9/10)

**问题：** `max_slides: 30`、`max_output_mb: 50`、`max_image_mb: 10` 在以下位置重复定义：
- 配置 JSON
- Go config struct
- DeckSpec JSON Schema (`maxItems: 30`)
- Worker request (`limits.max_slides`)
- 前端可能的 UI 提示

**推荐：** 配置文件是唯一来源。JSON Schema 的 `maxItems` 在 startup 时从 config 动态注入，不硬编码。Worker request 的 `limits` 从 config 读取。前端从 tool schema 或 API 获取限制值。

---

### [P2] 发现 10: ToolResultCard 抽取是前置重构 (confidence: 8/10)

**问题：** 计划说"首发必须先抽到 `frontend/src/components/chat/ToolResultCard.tsx`，再接 presentation"。这是一个重构步骤，会影响现有 `MessageBubble.tsx` 的所有工具结果渲染。如果 storage-kb 分支上有其他人在改 MessageBubble，会冲突。

**推荐：**
1. ToolResultCard 抽取作为独立 commit，先合入
2. 或者首发 presentation 直接在 MessageBubble 内部加一个 `renderPresentationResult` 分支，M2 再做抽取
3. 考虑到当前分支是 `storage-kb`，presentation 应该在独立分支开发

---

### [P2] 发现 11: 错误分类的 `recoverable` 语义需要更精确 (confidence: 8/10)

**问题：** 计划定义 `recoverable: true/false`，但"可恢复"对谁而言？
- `invalid_spec` recoverable=true → 模型可以修改 DeckSpec 重试
- `image_resolution_failed` → 模型可以换图或去掉图
- `exporter_unavailable` recoverable=false → 没人能修

但如果模型连续 3 次生成 invalid_spec，是否应该停止重试？计划没有定义重试上限。

**推荐：**
1. `recoverable` 改为 `recoverable_by: "model" | "user" | "operator" | "none"`
2. 或者保持 boolean，但在 tool description 中明确告诉模型"最多重试 2 次"
3. 在 `presentation_runs` 中记录同一 session 的连续失败次数，超过 3 次返回 `recoverable=false`

---

## 3. 测试审核

### 测试覆盖图

```
CODE PATHS                                              USER FLOWS
[+] internal/presentation/spec/validate.go              [+] 用户请求生成 PPT
  ├── [PLANNED] valid DeckSpec 通过                       ├── [PLANNED] 模型调用 generate_ppt → succeeded
  ├── [PLANNED] invalid layout 拒绝                      ├── [PLANNED] 模型调用 generate_ppt → running → poll → succeeded
  ├── [PLANNED] 超长标题拒绝                              ├── [GAP] 模型调用 generate_ppt → running → poll → failed
  ├── [PLANNED] 错误图片槽位拒绝                          ├── [GAP] [→E2E] 用户点预览 → Canvas 打开 → 翻页
  └── [PLANNED] JSON pointer 错误定位                     ├── [GAP] [→E2E] 用户点下载 → PPTX 文件保存
                                                         ├── [GAP] 用户刷新页面 → 历史消息恢复 presentation artifact
[+] internal/presentation/pptx/node_worker.go            └── [GAP] 错 session 用户尝试下载 → 403
  ├── [PLANNED] 正常生成 PPTX
  ├── [PLANNED] worker timeout                          [+] 错误状态
  ├── [PLANNED] stdout 非 JSON                            ├── [PLANNED] exporter_unavailable → Chat 不崩溃
  ├── [PLANNED] output 超限                               ├── [GAP] asset service 不可用 → 结构化错误
  ├── [GAP] worker 进程 crash (exit code != 0)            ├── [GAP] 网络断开时轮询 → 指数退避 → 停止
  └── [GAP] worker stdin 写入失败                         └── [GAP] [→E2E] 部分失败（HTML 有，PPTX 无）→ 预览可用，下载 disabled

[+] internal/presentation/service.go                    [+] 安全边界
  ├── [PLANNED] 完整 happy path                           ├── [PLANNED] 错 owner 下载 → 403
  ├── [PLANNED] 状态流转 created→succeeded                ├── [PLANNED] 错 session 下载 → 403
  ├── [PLANNED] 状态流转 created→failed                   ├── [PLANNED] 普通 purpose 不能 resolve presentation
  ├── [PLANNED] worker 领取原子性                         ├── [GAP] proxy URL 篡改 filename → 签名失败
  ├── [PLANNED] 租约续期                                  └── [GAP] 超大 DeckSpec (30 slides × max text) → 不 OOM
  ├── [PLANNED] 租约过期恢复
  ├── [PLANNED] 并发 worker 不重复执行                   [+] 移动端/响应式
  ├── [GAP] 图片 resolve 失败后的降级路径                  ├── [GAP] 移动端 DeckRenderer 不重叠
  └── [GAP] asset upload 部分成功后的清理                  └── [GAP] 缩略图条折叠

[+] internal/store/presentation_runs.go
  ├── [PLANNED] CRUD 基本操作
  ├── [PLANNED] 并发 running 上限
  ├── [PLANNED] GC dry-run
  ├── [GAP] GC 不删未过期 run 的 asset
  └── [GAP] expires_at 边界条件

[+] tools/presentation-exporter/src/exporter.mjs
  ├── [PLANNED] 每个 layout 生成 PPTX
  ├── [PLANNED] XML 验收（文本 shape 存在）
  ├── [PLANNED] editable 不是全页截图
  ├── [GAP] 中文字体 fallback 不乱码
  └── [GAP] notes 写入 speaker notes

[+] frontend DeckRenderer
  ├── [GAP] parseDeck 正常解析
  ├── [GAP] parseDeck 空 HTML → 错误
  ├── [GAP] 键盘翻页 (←→/PageUp/PageDown/Home/End)
  ├── [GAP] 全屏演示
  └── [GAP] running → succeeded 按钮状态变化

COVERAGE: 计划明确覆盖 ~22/45 路径 (49%)  |  GAP: 23 路径
QUALITY: ★★★:0 ★★:22(planned) ★:0  |  GAPS: 23 (4 E2E, 0 eval)
```

### 关键测试缺口（必须补入计划）

1. **[P1] Worker 进程 crash 处理** — `exec.CommandContext` 返回 non-zero exit 且 stdout 为空时的错误分类
2. **[P1] Asset upload 部分成功清理** — 上传 2/3 asset 后失败，已上传的如何处理
3. **[P1] 历史消息恢复** — 页面刷新后，presentation artifact 如何从 run 状态恢复
4. **[P2] Proxy URL 签名验证** — 篡改 disposition/filename 后 HMAC 失败
5. **[P2] 前端 parseDeck 单测** — 正常 HTML、空 HTML、缺少 data-slide-id 的 HTML
6. **[P2] 中文字体 fallback** — 至少一个 fixture 包含中文，验证 PPTX 中字体声明正确
7. **[P2] 图片 resolve 失败降级** — 图片下载失败时，是空图、占位符还是拒绝整个 deck
8. **[P3] GC 不删未过期 run 的 asset** — 验证 GC 逻辑正确识别 expires_at
9. **[P3] 移动端 DeckRenderer 不重叠** — 缩略图条、notes 面板在小屏幕上的布局

### 推荐补入的 E2E 测试

- **完整用户流程：** 请求 PPT → 工具返回 → 预览 → 下载 → 验证 PPTX 内容
- **错误恢复流程：** running → failed → 用户看到错误信息和 run id
- **权限边界：** 错 session 用户尝试下载 → 403
- **部分失败：** HTML 生成成功但 PPTX 失败 → 预览可用，下载 disabled

---

## 4. 性能审核

### [P2] 发现 12: 每次生成 spawn Node 进程的冷启动开销 (confidence: 8/10)

**问题：** Node 进程启动 + require PptxGenJS + 解析 stdin JSON + 生成 PPTX + 写文件。对于 8 页 deck，预计 500ms-2s。但 Node 启动本身 ~200ms，PptxGenJS import ~100ms。

**推荐：** 首发可接受。M3 如果需要优化，可以改为长驻 worker 进程 + JSON-RPC。当前 per-request spawn 的优势是隔离性好、无内存泄漏累积。

---

### [P2] 发现 13: HTML preview inline 最大 512KB 可能撑大 tool result (confidence: 7/10)

**问题：** 工具返回 `html_preview` 最大 512KB。这个 JSON 会进入 chat message 存储和 LLM 上下文。8 页 Swiss deck 的 HTML 可能 50-100KB，但如果有 data URI 图片，可能接近 512KB。

**推荐：**
1. `html_preview` 不应包含 data URI 图片（图片走 asset resolve）
2. 如果 HTML > 100KB，只返回 `html_asset_uri`，不 inline
3. 100KB 阈值比 512KB 更合理，避免 LLM 上下文被 HTML 污染

---

## 5. 失败模式分析

| 失败场景 | 有测试？ | 有错误处理？ | 用户可见？ | 严重度 |
|----------|---------|-------------|-----------|--------|
| Worker crash mid-generation | ❌ GAP | ✅ timeout + lease recovery | ✅ 超时后显示失败 | P1 |
| Asset upload 部分成功 | ❌ GAP | ❌ GAP | ❌ 静默（孤儿 asset） | **P1 CRITICAL** |
| 图片 resolve DNS 超时 | ❌ GAP | ✅ image_resolution_failed | ✅ 结构化错误 | P2 |
| PPTX 超 50MB | ✅ planned | ✅ output size check | ✅ 错误返回 | P3 |
| 前端 HTML parse 失败 | ❌ GAP | ❌ GAP | ❌ 空白预览 | **P1 CRITICAL** |
| Run 轮询 403 | ✅ planned | ✅ 停止轮询 | ✅ 权限错误 | P3 |
| Proxy URL 签名篡改 | ❌ GAP | ✅ HMAC 校验 | ✅ 403 | P2 |

**Critical gaps（无测试 + 无错误处理 + 静默失败）：**
1. **Asset upload 部分成功 → 孤儿 asset** — 必须修复
2. **前端 HTML parse 失败 → 空白预览无提示** — 必须修复

---

## 6. 并行化策略

计划的实施步骤可以分为以下并行 lanes：

| Lane | 步骤 | 模块 | 依赖 |
|------|------|------|------|
| A | Phase 0-1 | DeckSpec + schema + validator | — |
| A | Phase 2 | HTML renderer | Phase 1 |
| B | Phase 3 | Node PPTX exporter | — |
| C | Phase 4 | Go service + store | Phase 1 |
| D | Phase 5 | Asset 权限 + tool 注册 | Phase 4 |
| E | Phase 6 | 前端 presentation artifact | — |
| — | Phase 7 | 端到端集成 | A+B+C+D+E |

**并行执行建议：**
- **第一波：** Lane A + B + C + E 并行启动（4 个独立 worktree）
- **第二波：** Lane D 等待 Phase 4 完成
- **第三波：** Phase 7 等待所有 lane 完成

**冲突风险：**
- Lane D 和 Lane E 都会修改 `frontend/src/components/chat/MessageBubble.tsx`（ToolResultCard 抽取）
- 建议 Lane E 先做 ToolResultCard 抽取，合入后 Lane D 再接

---

## 7. NOT in scope（计划已明确排除）

- ✅ 任意 HTML → 可编辑 PPTX 通用转换
- ✅ WebGL 动效导出
- ✅ 多人协作编辑
- ✅ 企业模板母版导入
- ✅ 复杂图表自动生成
- ✅ 全部 22 个 Swiss 版式（首发 8 个）
- ✅ 流式/增量预览
- ✅ Visual PPTX 公开入口
- ✅ 任意远程 HTTPS 图片

---

## 8. 完成总结

| 审核维度 | 发现数 | P1 | P2 | P3 |
|----------|--------|----|----|-----|
| 架构 | 8 | 2 | 4 | 2 |
| 代码质量 | 3 | 0 | 3 | 0 |
| 测试 | 23 gaps | 3 | 6 | 3 |
| 性能 | 2 | 0 | 2 | 0 |
| **总计** | **36** | **5** | **15** | **5** |

**Lake Score:** 9/10 推荐选择完整版本（计划已是完整版）

**状态：** CLEARED WITH CONCERNS

**必须修复的 P1 问题（5 个）：**
1. Asset 上传与 run 状态的事务边界
2. 图片 resolve 阶段在状态机中位置不明确
3. Worker 进程 crash 处理测试缺失
4. Asset upload 部分成功清理测试缺失
5. 前端 HTML parse 失败处理缺失

**推荐修复的 P2 问题（15 个）：** 见上文各节

**可选修复的 P3 问题（5 个）：** 见上文各节

---

## 9. 下一步行动

1. **立即修复 P1 问题** — 在 Phase 0 契约冻结时补充
2. **评估 P2 问题** — 决定哪些进入首发，哪些推迟到 M2
3. **补充测试计划** — 将 23 个测试缺口加入各 Phase 的测试要求
4. **更新状态机定义** — 加入 `resolving_images` 阶段
5. **更新 SQL schema** — `mode` CHECK 约束改为 `('editable', 'visual')`

