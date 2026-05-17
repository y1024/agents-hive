# TODOS

## [P1] KB：PageIndex-style 真实样本检索基准集

**What:** 基于真实 PDF/DOCX/Markdown 业务文档建立 KB 检索 golden cases，记录 query、expected `doc_id`、expected `node_ids` / `page_ranges`、expected citation nodes / citation page ranges，并用 `internal/agentquality.ScoreKBPageIndexRetrieval` 统计 retrieval hit rate 和 citation hit rate。

**Why:** 当前代码已经按 PageIndex 核心契约接入 `kb.doc.meta -> kb.doc.structure -> kb.section.text`，并支持 PDF 页锚 `start_page/end_page` 与 `page_ranges` 精确取证；但“准确率达到 PageIndex 水平”不能靠形态判断，必须用业务样本和真实 provider 输出验证。

**Context:**
- 已完成：Markdown tree-mode、页锚提取、`kb.doc.meta` 输出 `page_count/line_count/node_count`、`kb.doc.structure` 输出 `start_page/end_page`、`kb.section.text.page_ranges`、页范围正文切片、页内图片 `asset_refs`、PageIndex-style score helper。
- 样本集至少覆盖：带 TOC PDF、无 TOC PDF、扫描件 OCR、DOCX 标题层级、Markdown 前言、图片章节、跨页答案。
- 验收口径：不把整篇文档作为成功；必须命中 tight node/page range，并在最终 citation 中包含期望节点和期望页范围。
- 该 TODO 是评测/样本补齐，不是新建向量库或旁路 RAG。

**Effort:** M

---

## [P1] 统一对象存储：tenant/system ACL resolver

**What:** 为统一资产层补齐 tenant/system 作用域的业务 ACL resolver，覆盖 `source_kind=chat_attachment`、`kb_document_image`、`agent_artifact` 以外的多租户资产解析场景。

**Why:** 当前对象存储核心、KB 图片 resolver、user-scoped chat/artifact resolve 已落地；但 tenant/system 资产还没有完整业务授权闭环。`asset://` 不是授权凭证，生产多租户场景不能只靠 URI 或 owner 基础字段放行。

**Context:**
- 已完成：`internal/asset`、MinIO/S3-compatible provider、PG metadata、resolve/proxy API、KB 图片 resolver、Agent artifact session 校验、Web/飞书附件持久化。
- 待实施：定义 tenant/system 的 `ResolveContext` 来源、业务 owner 映射、session/agent/domain 约束、审计字段和拒绝指标。
- HTTP resolve 仍必须 fail closed：没有显式 resolver 或 resolver 无法确认授权时拒绝。
- 参考运维入口：`docs/运维手册/unified-asset-storage.md`。
- 已归档计划：`docs/计划与路线/归档/2026-05-16-统一对象存储层计划.md`。

**Effort:** M

---

## [P2] 统一对象存储：multipart / streaming 上传

**What:** 将 `AssetService.Upload([]byte)` 的大文件路径扩展为 reader-based streaming / S3 multipart 上传，避免并发 100MB 对象导致内存峰值和 GC 压力过高。

**Why:** 当前首版硬限制单对象 100MB，聊天入口仍有 25MB 单附件限制，能覆盖首发场景；但 PDF/DOCX、客服资料包、外部 Widget 上传进入生产后，需要流式 hash、分片上传、失败清理和可观测性。

**Context:**
- 已完成：KB Admin/API 上传入口只接受 `multipart/form-data` 文档文件 + 图片资产，并会进入 `internal/kb/asset_ingest.go` 统一写 `internal/asset`；未上线无老客户端兼容负担，JSON/base64 ingest API 已删除，非 multipart 请求返回 415。本 TODO 不是“KB 上传入口缺失”，而是对象存储底层大对象上传仍使用内存 `[]byte`。
- 当前限制：`DefaultMaxUploadBytes = 100MB`。
- 目标接口：保留现有 `Upload` 兼容小文件，新增 reader-based 上传入口，支持 SHA-256 streaming hash、size limit、content-type、取消和超时。
- MinIO/S3 provider 优先使用 SDK multipart；local provider 使用临时文件 + 原子 rename。
- 验收：并发大文件上传内存不随文件大小线性膨胀；失败后无孤儿 multipart；GC 仍能清理历史孤儿对象。

**Effort:** M

---

## [P2] IM 附件：钉钉 / 企微 AttachmentDownloader

**What:** 为钉钉、企微插件实现 `channel.AttachmentDownloader`，把各平台原始附件下载为统一 `channel.Attachment{Data,MimeType}` 后复用资产层。

**Why:** Router 和 Master 已完成平台无关附件下载/持久化链路，飞书和微信已接入；钉钉、企微当前入站层仍只摄取文本，若后续只传 file key / media id 而不下载对象体，就无法进入统一 `internal/asset`，后续 turn 也不能可靠引用。

**Context:**
- 已完成抽象：`channel.AttachmentDownloader`、Router 下载、Master 上传为 `source_kind=chat_attachment`。
- 已完成实现：飞书通过 `Client.DownloadMessageResource` 接入；微信通过官方 wechatbot SDK `Download` 接入，Router 对含附件消息跳过 debounce，避免附件上下文被合并丢失。
- 待实施平台：dingtalk、wecom。必须先在各自 webhook/callback 层把平台媒体事件解析为 `channel.Attachment`，再复用官方 API / 现有 client 下载；不能在 Router 中写平台分支。
- 验收：各平台入站图片/文件能写入 `assets` metadata，消息 metadata 带 `asset_uri` / `content_hash`，附件仍继续走现有 fileconv 给 LLM。

**Effort:** M

---

## [P1] 工具域统一 Phase 7：迁移观察与旧入口降级

**What:** 跟踪工具域统一剩余观察项，并在指标满足后推进旧入口降级：

1. IM legacy/unified 7 天、14 天指标窗口
2. 旧文件工具默认入口是否降级
3. 旧入口是否移除 Core

**Why:** `filesystem.action` 与 `im_api.send_message` 的代码实施已完成，但旧入口降级不能按代码完成时间推进，必须按真实使用指标与事故窗口判断。否则会影响历史 skill/sub-agent、旧会话 replay、batch 并发读性能和 IM 发送稳定性。

**Context:**
- IM 指标：观察 `im_send_legacy_path_total` 与 `im_send_unified_path_total`
- 7 天门槛：legacy 占比低于 20%，且 unified 错误率不高于 legacy
- 14 天门槛：legacy 占比低于 5% 连续 7 天，且无 P0/P1 事故
- 文件工具指标：观察 `hive_tool_call_total`、`hive_tool_error_total`、`hive_filesystem_action_total`、`hive_route_input_denied_total`
- 决策 1：是否把旧 `read_file|grep|glob|ls|write_file|edit|multiedit` 从默认可见入口降级为兼容入口
- 决策 2：是否移除旧入口 Core 标记；删除工具名不在当前范围内
- 归档验收报告：`docs/验收报告/2026-05-16-工具域统一与分层收敛实施验收报告.md`

**Effort:** S（观察与配置调整）→ M（如涉及默认可见性策略变更）

---

## [P1] WeChat 测试覆盖基线

**What:** 为 wechatbot 核心路径补全单元测试，达到关键路径 100% 覆盖。

**Why:** Eng Review 显示 34 条代码/用户流路径当前无任何测试（0%）。4 个关键安全/稳定性缺口（WS 隔离、panic recover、UNIQUE 冲突、Shutdown 超时）均需专项测试。

**Context:**
- 旧非官方个人微信实现已删除，新增测试不得引用旧协议目录。
- 优先：`TestWeChatWSUserIsolation`（安全）、`TestBindExternalIDConflict`（409）、`TestBotRegistryShutdownTimeout`、`TestBotRunPanicRecover`
- 测试文件应放在官方 wechatbot 新包或现有统一 IM 包旁边，例如 `internal/channel/wechatbot/*_test.go`、`internal/channel/router_test.go`、`internal/api/wechat_handlers_test.go`
- 参照：`internal/channel/router_test.go`（mockPlugin + testify）
- `go test -race ./internal/channel ./internal/api ./internal/tools` 无 data race

**Effort:** M（human）→ S（CC+gstack）
**Depends on:** WeChat Phase 1 实施完成

---

## [P2] 域 F Phase 2：场景路由测试 + 业务工具实现

**What:** 为已上线的 system/business 场景路由补充端到端测试；实现 Skill 中引用但尚未存在的业务工具。

**Why:** Phase 1 路由逻辑（system/business.md + skill工具 + trigger_keywords）已于 2026-04-12 完成上线。但 Skill 模板中列出的部分业务工具尚未实现，实际执行会失败。

**Context:**
- 已完成：SkillMetadata 路由字段 ✅、business skill 模板 ✅、trigger_keywords 注入 ✅、system/business.md 场景指南 ✅、generate_image 工具 ✅（`internal/tools/image_gen.go`）
- 待实施：
  1. `internal/tools/` 新增 `xiaohongshu_publish` 工具实现
  2. `internal/master/prompt_builder_test.go` 扩展场景路由测试（当前只有 `TestBuildSystemPrompt_SkillListing_DomainMetadata`）
- 实施计划：`docs/optimization-plans/09-agent-routing-impl.md` Phase 2/3

**Effort:** M（human）→ S（CC+gstack）

---

## [P2] WeChat 联系人昵称持久化

**What:** 微信联系人昵称从历史消息中提取并持久化，避免 contacts 列表只有 wxid 没有可读名字。

**Why:** wechatbot SDK 不支持联系人管理 API，`GET /api/wechat/contacts` 的昵称无法直接从 SDK 拉取。联系人列表显示 wxid_xxxx，用户体验差。

**Context:**
- 旧协议的昵称读取路径已删除，官方 wechatbot 实现必须只使用 SDK `IncomingMessage` 中可验证的字段。
- 持久化方案：优先写入 `wechat_conversations.peer_nickname` / `peer_avatar_url`，缺失时再用脱敏 `peer_wxid` 展示。
- contacts API 从 `wechat_conversations` 查询当前用户的会话摘要，不从旧协议联系人 API 拉取。
- 需改动：官方 wechatbot 入站 handler 收到消息时 upsert conversation，`wechat_handlers.go` contacts/conversations 端点从 store 查询。

**Effort:** S

---

## [P2] WeChat 用户注销资源清理

**What:** 用户注销账号时，清理其关联的所有 wechatbot 资源：ChannelManager 实例、凭证文件、IM sessions。

**Why:** 当前只有 `user_external_ids` 表设置了 `ON DELETE CASCADE`，但 `ChannelManager` 中的内存实例、`{DataDir}/wechat/{userID}/credentials.json` 文件、`im-wechatbot-*` sessions 均无清理逻辑。用户注销后其微信 bot 仍在内存中运行，凭证文件残留磁盘。

**Context:**
- 触发时机：用户账号删除事件（需新增 `DeleteUser` 方法，当前 `auth/engine.go` 无此方法）
- 需要调用：`ChannelManager.StopAndRemove(userID)`（停止 goroutine + 从 map 删除，需新增）
- 需要执行：`os.RemoveAll(DataDir + "/wechat/" + userID)`
- sessions 可保留（历史记录）或删除（看产品决策）

**Effort:** S

---

## [P3] generate_image 临时文件清理

**What:** `/tmp/hive-images/` 目录无自动清理，长期运行会积累图片文件。

**Why:** 每次图片生成约 800KB，高频使用场景下磁盘会被耗尽。

**Context:**
- `internal/tools/image_gen.go:102` 创建 `filepath.Join(os.TempDir(), "hive-images")` 但无清理逻辑
- 修复思路：服务启动时清理超过 24h 的旧文件（`filepath.Walk` + `os.Remove`），或引入 TTL 参数
- 注：原 TD-2（serverBaseURL 配置化）已修复 — `config.go:104` 新增 `BaseURL` 字段，`ServerBaseURL()` 方法有 fallback

**Effort:** S

---

## [P3] WebSocket 连接限制配置化

**What:** 全局 WebSocket 和 WeChat WS 端点的连接数限制从硬编码改为可配置。

**Why:**
1. `streaming/websocket.go:47` 的 `maxConnsPerIP = 5` 硬编码，生产环境公司 NAT 后多用户共享 IP 时 5 个连接不够
2. WeChat `/api/wechat/events` WS 端点无连接数限制，恶意用户或客户端 bug 可建立大量连接耗尽文件描述符

**Context:**
- 全局 WS：将 `maxConnsPerIP` 移到配置，支持环境变量覆盖
- WeChat WS：连接建立时检查 `map[userID][]wsConn` 长度，超过阈值返回 `429`
- 或在 middleware 层统一限速（复用现有 rate limiter）

**Effort:** S

---

## [P3] WeChat Phase 4：群聊支持

**What:** wechatbot 多租户架构支持微信群聊消息路由，识别群内 @机器人，路由到对应 Agent Session。

**Why:** Phase 1-3 只支持私聊（1对1）。群聊是微信重要使用场景，但路由逻辑更复杂。

**Context:**
- 旧协议群聊能力已删除，不能以旧非官方 API 作为设计依据。
- 当前官方 wechatbot Go SDK / iLink 文档未暴露稳定 RoomID/ChatroomID/GroupID 或 SendToRoom API。
- 待实现：只有上游 SDK 明确支持群聊后，按 `OwnerUserID + roomID` 进入统一 Router；不得手拼 `im-wechatbot-{room_id}`。
- 需要改动：官方 wechatbot 入站 handler、conversation store、session 列表群聊显示；`enrichCtx` 不接收 PeerWxid 或 roomID 作为系统用户。

**Effort:** XL（human）→ L（CC+gstack）

---

## [P2] P5 灰度推送 + 回滚保护(从 P5 切出,延后)

**What:** 在 P5 自动优化闭环中加入"按 user / tenant / 流量百分比分流"的实时灰度能力,以及配套的回滚保护(指标恶化告警 + 人工执行回滚)。

**Why:** P5 当前(本次 plan-eng-review 调整后)收口在"建议生成 + eval diff + 人工审批 + 离线 A/B 报告",决策依据是 golden cases 上的离线 eval。一旦遇到"必须线上灰度才能验证"的业务场景(例如改动效果只在真实用户分布下显现、golden cases 无法覆盖的长尾、ACP quota / memory governance 阈值这类强依赖真实流量的参数),就需要灰度能力补上。

**当前为什么不做:**
- 业务侧暂无"必须线上灰度才能验证"的硬场景。
- 离线 eval(P3 BatchEvalRunner 在 50-200 个 golden cases 上跑)能支撑大多数 prompt / tool / skill 调整决策。
- 自建一套灰度引擎要 ~500-800 行 Go + 1 张 schema + 1 组 API + 1 个后台 alert worker;同等能力业界有成熟方案(GrowthBook / Unleash / OpenFeature),引入成本未必更高。

**Context:**
- 已写好的延后实现保留在 `docs/research/IMPLEMENTATION/P5-AUTO-OPTIMIZATION-CODE.md` §4(Task 9-10)和 §5(Task 11-12),用 `<details>` 折叠,启动时直接展开即可。
- §6 A/B 报告本期改用离线 eval diff,启动灰度后需补"基于真实灰度流量"的 A/B 报告路径(BaselineStats / TreatmentStats 字段从 rollout metrics 表查,加 Duration 字段)。
- §7 schema、§8 API、§9 前端、§12 灰度分流伪代码都需要一并启用。
- 自建 vs 用 GrowthBook 的取舍要在启动时重新评估;GrowthBook 自带 z-test 显著性检验,正好覆盖 §6 Task 13。

**Pros / cons:**
- 启用收益:能验证离线 eval 看不到的真实用户分布问题、给"必须真线上验证"的改动一条安全通道。
- 启用成本:多一组运维面(rollout 监控、alert 响应 SLA、回滚演练),需明确"何时进灰度、何时直接全量"的决策规则。

**触发条件(任一满足即启动):**
1. 出现连续 ≥ 3 次"离线 eval 通过但线上指标恶化"的 case(说明 golden cases 覆盖度不够,需要真实流量验证)。
2. 质量改动频率 > 每周 1 次,人工审批 + 直接发版的风险变高。
3. 业务方明确要求"先在 X% 用户上跑 Y 天再决定是否推全量"。

**Depends on / blocked by:** P5 §1-3、§6(已在本期)落地。无外部阻塞。

**Effort:** L(human,~3 天)→ S-M(CC+gstack,~30-60 分钟)。如选 GrowthBook 等现成方案,主要是接入 SDK + 写 rollout 配置同步逻辑,会更短。

---

## [P3] CLI 工具 distribution 管线(从 P2-PROD/P3/P4 切出,延后)

**What:** 给以下 CLI 二进制建立 build / publish / 调度管线:

- `cmd/quality-weekly-report`(P3,周报生成)
- `cmd/quality-batch-eval`(P3,批量 eval 触发)
- `cmd/delegation-eval`(P4,委派对比 eval)
- `cmd/memory-vector-space-migrate`(P2-PROD,memory 向量空间迁移)
- `cmd/acp-spec-check`(P4,ACP spec 漂移检测)

**Why:** 这些 CLI 是质量工作台与 memory 运营的关键工具,本期落地后只能在运维机器上 `go run ./cmd/...`。要给真实运维使用,需要二进制 release / docker image / 调度器接入。

**当前为什么不做:**
- 各 P 计划本期聚焦"功能可用",CLI 能在 dev 与 staging 环境跑通即可。
- 真实 release pipeline 设计依赖于公司 release / k8s 部署约定,与 hive 主线发版捆绑更经济,而非 P3-P5 单独造一套。

**Context:**
- 各 P 文档 NOT in scope 章节已显式标注 distribution 延后。
- 周报已天然支持 `--week=YYYY-MM-DD` flag,可在宿主机 systemd timer / k8s CronJob 起步。
- 真正的 distribution 包括:GoReleaser 配置、Dockerfile + 多架构构建、GitHub Actions release 流程、运维端调度文档。

**Pros / cons:**
- 启用收益:运维不再需要 git clone 仓库 + go build,直接 `docker pull` 或下 release 包即可。
- 启用成本:1-2 天 GoReleaser + Actions + Dockerfile 调试,后续每次新增 CLI 都要更新 release matrix。

**触发条件:**
1. 任一 CLI 进入运维高频使用(每周 ≥ 3 次)。
2. 接入 k8s CronJob / Argo Workflow,需要稳定 image tag。
3. 多人(≥ 3)需要在不同环境跑同一 CLI,统一版本变得必要。

**Depends on / blocked by:** 无外部阻塞。需要决策:GoReleaser vs ko vs 现有 hive 主仓 Dockerfile 复用。

**Effort:** M(human,~1-2 天)→ S(CC+gstack,~30 分钟,主要是写 GoReleaser config + Dockerfile + Actions yaml)。

---

## [P3] 质量数据归档与清理(从 P3 切出,延后)

**What:** `agentquality_version_metrics` / `agentquality_clusters` / `agentquality_replay_queue` / `agentquality_batch_eval_runs` / `agentquality_weekly_reports` 这 5 张表的归档与清理策略。

**Why:** P3 落地后,version_metrics 估算每天写 5K-15K 行 t-digest blob(每 blob 1-3KB),一年 ~5GB;cluster / replay_queue 也单调增长。运营可视化数据通常只看近 30-90 天,陈旧数据应归档冷存储或软删除。

**当前为什么不做:** 上线后头 6 个月数据量在 GB 级,PostgreSQL 完全应付,P3 首发不应背负数据生命周期管理。

**Context:**
- 候选方案:partition by `bucket_date` quarter + drop old partitions / 写归档脚本 dump JSONL 到 S3 → DELETE。
- 周报已经按周聚合,历史周报本身就是陈旧 raw 数据的"摘要快照",原始 version_metrics 早期可丢。
- 启动条件:任一表 > 50GB,或运营反馈查询变慢。

**Effort:** S(human,~半天)→ S(CC+gstack,~30 分钟,加一个 cleanup CLI + cron)。

---

## [P2] sessiontodo: spec→todos 自动 intake hook 与产品闭环

**What:** `internal/sessiontodo/sync.go::SyncFromSpec`、`BuildSpecProgressPatch`、`source_change_id/source_revision` 和前端 spec 跳转已存在；剩余是决定什么时候把 spec plan 自动投影到 session todos，以及反向进度 patch 何时写回 specdriven。

**Why:** 当前代码已经具备基础投影和追溯能力，但尚未把 hidden spec planner 和用户可见 todos 的产品语义完全打通。自动 hook 如果触发时机不清，会把后台规划和当前 session 临时计划混在一起。

**Context:**
- 触发策略:session 创建时初始化、spec revision 变化时增量投影、或用户显式要求时投影
- 覆盖策略:保留 agent 已改写 todos、按 `source_revision` 做冲突检测、还是新 revision 只追加新 todo
- 反向策略:仅对 `source=spec_projected` 的 completed todo 生成 spec progress patch，还是允许 agent todo 状态直接推进 spec

**Effort:** M
**Depends on:** specdriven planner 可见化决策

---

## [P2] sessiontodo: 多实例接管策略

**What:** `runtime_epoch + ClaimResume` 已覆盖单实例 resume / auto_continue 并发边界；剩余是多 Master 部署时的主动接管策略，例如 lease、owner、fencing token 或外部调度器。

**Why:** 当前实现能防旧 continuation 误写，但没有定义多个 Master 同时尝试续跑同一 paused plan 时谁拥有执行权。单实例和开发模式足够，多实例生产需要明确 owner 语义。

**Context:**
- 候选方案:PG lease 表、session owner 字段、advisory lock、或统一长任务调度器
- 需要定义失败恢复:owner 进程崩溃后多久可接管、接管后如何广播 snapshot
- 不影响当前 Plan Runtime / Todos 主链路验收

**Effort:** M
**Depends on:** 多实例部署进入生产

---

## [P3] Admin 后台设计 polish(本期 plan-design-review 切出,延后)

**What:** 把本期 plan-design-review 标识但本期未修的设计 polish 项一次性补齐。

**当前状态(2026-04-30 plan-design-review):**
- Pass 1 IA:hero band + Tab 收纳 已加 ✅
- Pass 2 Empty states:主表格 / 主列表 已升级 ✅
- Pass 5 Tokens:`rounded-2xl` card / `rounded-[10px]` button / `bg-card` / `transition-colors duration-150` 已对齐 ✅
- 设计分:Pass 1 7/10、Pass 2 7/10、Pass 4 7/10、Pass 5 9/10

**待补:**

1. **QualityWorkbench / AutoOptimization 拆总览页 + 详情子页**
   - 当前 QualityWorkbench 440 行单文件,AutoOptimization 481 行单文件
   - 总览只放 hero + KPI + 主表格,详情进 `/admin/quality-workbench/clusters/:id`、`/admin/auto-optimization/suggestions/:id`
   - 收益:解决"Don't make me think" 原则深层 IA 问题

2. **AutoOptimization approve → apply 流程提示**
   - approve 成功 toast 加"下一步:执行 apply" 链接 / 弹窗
   - 当前用户 approve 后看不到 apply 按钮要去哪点

3. **触摸目标 ≥ 44px**
   - 当前 button `py-2` ~ 36px 高,iPad 远程不友好
   - 改成 `py-2.5 min-h-11` 但需评估视觉密度

4. **ARIA landmarks**
   - 4 个 admin 页加 `<main role="main">`、heading hierarchy 重审(`h1` → `h2` → `h3` 不要跳级)
   - 纯图标按钮如 `Delete tool rule` 加 `aria-label`

5. **focus-visible 定制**
   - 当前用浏览器默认蓝色 outline
   - 改用 `--accent-600` ring + offset 2px

6. **Loading skeleton**
   - 当前 loading 是"加载中..." 文字
   - 改 skeleton 卡片(灰色块占位),用户感受到结构

7. **Partial state 显示**
   - QualityWorkbench KPI 当前只有 open count
   - 加 promoted_verified / promoted_regressed 细分(横向 stacked 微 bar)

8. **Hexagon brand texture**
   - DESIGN.md 强调"Hexagonal motifs for brand texture",当前 admin 完全没用
   - empty state 卡片 / hero band 背景加 SVG hexagon outline,使 admin 不像 generic SaaS

9. **partial state ARIA live region**
   - 后台 worker 异步完成 replay job 时,当前需要刷新页面才看到结果
   - 加 `<div role="status" aria-live="polite">` 自动更新

10. **Cookie-cutter section rhythm 解构**
    - 4 admin 页都是"H1 + 刷新 + 4 KPI + sections"完全相同
    - 至少给每个能力域一个 distinctive 视觉签名(MultiAgent 用 hex grid、AutoOpt 用 timeline、Memory 用 nested rings)

**Why:** 本期设计分提到 7/10 已经显著好于 4/10 起点,但要冲到 9-10/10 需要这一组工作。本期 ship 在即,这些是非阻塞 polish。

**触发条件:** 给客户/领导 demo 后收到"看起来像 SaaS 模板"的 specific 反馈,或运营反馈"找不到下一步该干啥"。

**Effort:** L(human,~3-4 天)→ M(CC+gstack,~1-2 小时)。建议拆 3 个 PR:IA 重构 / 触摸 + a11y / 视觉品牌签名。

## [P2] system prompt: LLM eval(prompt 改动效果验证)

**What:** 为 `Agent-System-Prompt重整方案.md` 上线提供 before/after eval pair,验证模型行为改动符合预期。

**Why:** Plan §6 测试计划全是结构性单测(prompt 包不包含某关键词),无模型行为 eval。Prompt 改动是软改动,效果不可证 = 盲改。

**Context:**
- Eval cases 范围:50-100 个 golden prompts 覆盖 (a) 长任务 → 是否进 plan_mode (b) 业务场景 → 是否调对 Skill (c) artifact 输出格式稳定性 (d) 简单问答 → 是否不冗余创建 todos
- 走现有 `internal/agentquality` BatchEvalRunner
- 上线前必跑;上线后 7 天再跑一次确认无静默回归

**Effort:** M(human,~2 天)→ S(CC+gstack,~1 小时)
**Depends on:** 本期 prompt 重整 PR

---

## [P3] system prompt: reply XML 示例删除回归 eval

**What:** 验证 §4.7 删除 artifact `<artifact>` 完整 XML 示例后,模型在中文环境仍能稳定输出正确格式。

**Why:** Plan §8 风险栏明示"如果回归失败,可恢复一个极短单行示例"——承认有反弹风险,但本期未做验证。中文 prompt 环境 LLM 对"具体示例"的模仿强于对"自然语言描述"的语义理解。

**Context:**
- Eval cases:20-30 个生成长文档/代码文件/HTML/PPT 的 golden prompts
- 比较改动前后的 artifact 标签 well-formed 率(type/title/language 字段完整、闭合标签正确、内容前后空行)
- 如果 well-formed 率下降 > 5%,补回单行 XML 示例

**Effort:** S
**Depends on:** 本期 prompt 重整 PR

---

## [P2] system prompt: 上线回退手册

**What:** 为 prompt 重整 PR 上线提供回退路径文档。

**Why:** Plan §8 提"DB override 优先级高于代码改动",但没说"如果生产回归怎么回退"。文件改了 + hardcoded 改了,但 DB 旧 override 仍生效,回退路径不清晰。

**Context:**
- 文档落点:`docs/runbook/system-prompt-rollback.md`
- 内容:(a) git revert 代码改动 (b) 检查 hive_prompts 表 DB override (c) admin 管理台手动恢复旧 prompt 版本步骤 (d) 验证回退后单测通过 (e) 监控 prompt smoke eval
- 触发时机:生产 prompt smoke eval 失败 / 业务路由命中率掉超过基线 10%

**Effort:** S
**Depends on:** 本期 prompt 重整 PR

---
