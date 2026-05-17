# 业务域 KB 与证据层计划总览

> **日期**：2026-05-16  
> **状态**：已实施并归档的权威总览  
> **来源整合**：已删除的 KB 平台、PageIndex ADR、KB 前端、客服试点历史草案，以及 PageIndex 源码审阅。  
> **执行口径**：P0/P1/P2 已完成并归档；本文作为架构和验收口径留档，后续新增工作只从 `TODOS.md` 或新计划启动。

## 1. 总结论

KB 不是客服 RAG，也不是 memory 的增强字段。它是 Hive 的平台级 **业务域知识与证据层**：

- **Knowledge**：组织、租户、业务域的文档资产，例如 FAQ、SOP、PRD、设计规范、发布记录、实验报告、政策条款。
- **Retrieval**：读取知识的方式。首发采用 PageIndex 风格的 markdown tree-mode，后续可扩展 vector/hybrid。
- **Evidence**：每次回答、工具决策或业务动作使用了哪些文档版本、节点、片段，以及当时是否通过权限过滤。

核心边界：

- `memory` 保存用户/agent 长期记忆、偏好和工作上下文，不承载组织文档。
- `kb` 保存版本化知识资产和引用证据，必须 owner/domain/namespace 隔离。
- `business state` 保存业务对象当前状态，例如工单、roadmap item、experiment，不塞进 KB chunk 或 memory。
- `customer_service` 只是第一个实验域，不拥有 KB 架构。
- PageIndex 值得吸收的是 tree navigation 机制和 PDF 边界校验思想，不是它的 PDF LLM fallback pipeline、LiteLLM 直连、OpenAI Agents SDK demo 或单机 JSON workspace。

## 2. 计划拆分

| 文件 | 目标 | 可独立验收 |
|---|---|---|
| `docs/计划与路线/归档/2026-05-16-KB-P0-Tree-Mode后端闭环实施计划.md` | Markdown tree-mode 后端闭环：模型、存储、tree ingest、三工具、evidence ledger、基础 eval | 是 |
| `docs/计划与路线/归档/2026-05-16-KB-P1-质量观测与最小API前端实施计划.md` | 管理 API、质量事件、Trace/Replay/QualityWorkbench、最小前端 citation 和只读管理面 | 是 |
| `docs/计划与路线/归档/2026-05-16-KB-P2-资产PDF与客服试点实施计划.md` | 对象存储、PDF/DOCX 转 markdown、图片资产、客服业务域试点、webhook/outbox | 是 |

分期原则：

- P0 不做 UI，不做 PDF，不做客服状态机，不做 vector/hybrid。
- P1 不做 PDF，不做客服 webhook，不做完整文档版本 diff。
- P2 才接资产、PDF/DOCX、客服试点和更完整前端。

## 3. 必须复用的现有能力

| 现有能力 | 复用方式 | 禁止事项 |
|---|---|---|
| `internal/router` | 注册 KB capability/tool profile，所有工具可调用性由 `RouteDecision` 与 tool policy 决定 | 禁止用 prompt、关键词、skill 文案授权 namespace |
| `internal/domainpolicy` | domain admission 继续管业务域上线；KB 只读能力和外部写入能力分开准入 | 禁止让只读 KB 查询依赖 `external.send` |
| `internal/airouter` | 摘要、doc description、后续 embedding/rerank 都走 airouter | 禁止引入 LiteLLM 或新 LLM SDK |
| `internal/store` | PostgreSQL migration、PG store 模式沿用现有 store 风格 | 禁止单机 JSON workspace 成为生产状态源 |
| `internal/fileconv` | P2 通过扩展/组合 fileconv 获得 markdown 转换入口 | P0 禁止承诺 PDF 直接 tree ingest |
| `internal/agentquality` | KB 检索和 evidence 违规记录为 quality event，细节放 `Attributes` | 禁止把 doc_id/chunk_id/user_id 做 metrics label |
| `internal/observability` | trace span 和 metric label 走现有观测边界 | 禁止高基数字段污染通用 metrics |
| `internal/qualityworkbench` | 聚合 KB citation miss、ACL block、stale doc、sentinel mismatch | 禁止新建平行质量工作台 |
| `internal/memory` | 上层编排可同时注入 memory 和 KB，但存储、工具、评测完全分离 | 禁止复用 memory 表保存 KB 文档 |
| `frontend/src/components/chat` | P1 citation 和 KB tool 卡片复用现有 MessageBubble/ToolAdapter/streaming 结构 | 禁止先做完整知识库管理产品再验证后端闭环 |

现有系统兼容补充：

- `internal/config/defaults.go` 的 `defaultToolPolicyConfig()` 当前只默认装配 `fs/runtime/web/lsp/agent/discovery` 组；新增 `kb` 组后必须同步纳入 `master_direct` 或通过明确的 `tool_search`/recall 路径可见，否则会出现工具已注册但模型不可见或运行时 profile 不放行。
- 前端 API 方法应落在 `frontend/src/api/node-client.ts` 的 `NodeClient` / `LocalNodeClient` 抽象上；`frontend/src/api/client.ts` 只是底层 HTTP client，不应成为 KB 业务 API 的唯一入口。
- citation 不能由前端从 tool output 推断。服务端必须从 evidence ledger 汇总本轮引用，并通过 assistant message metadata、WebSocket payload 和 `/sessions/{id}/messages` API 透出。
- `internal/asset` 已落地为统一对象存储层；KB P2 必须复用该层，不能在 KB 或 fileconv 下再造一套平行对象存储。后续对象存储遗留项跟踪在 `TODOS.md`，原实施计划已归档到 `docs/计划与路线/归档/2026-05-16-统一对象存储层计划.md`。

## 4. Capability 口径

当前代码已有 `kb_search`，并且挂在 `customer_service` 下。新口径必须调整为平台级能力：

| 能力 | 用途 | 首发 |
|---|---|---|
| `kb.read` | 平台级只读 KB 能力，覆盖 tree/vector/hybrid 读取 | P0 |
| `kb.doc.meta` | 列出已授权 namespace/document 元数据，用于选文档 | P0 |
| `kb.doc.structure` | 返回剥离原文的 tree structure，用于导航 | P0 |
| `kb.section.text` | 返回节点原文和服务端 evidence token，用于回答取证 | P0 |
| `kb_search` | vector-mode 或兼容 façade | P1/P2 后再决定 |
| `customer_service.escalate` | 客服人工升级，外部写入能力 | P2 |

规则：

- tree-mode 首发走 `kb.doc.meta`、`kb.doc.structure`、`kb.section.text` 三工具，不以 `kb_search` 为主路径。
- `customer_service` 只能通过 KB binding 配置可用 namespace 和工具组合，不拥有 KB capability。
- 只读 KB 不触发 HITL；外部写入仍走 policy/HITL/outbox。

## 5. PageIndex 吸收范围

照搬/改造：

- Markdown heading 提取：跳过 code block，识别 `#` 到 `######`。
- PageIndex Markdown 实现只识别 triple backtick code fence，不识别 `~~~` fence；Hive P0 要么显式支持两种 fence，要么把 `~~~` 作为测试边界并拒绝或降级，不能假装完全兼容 Markdown。
- PageIndex 会丢弃首个 heading 之前的正文；Hive ingest 必须明确处理策略：默认合成 `0000` 前言节点或拒绝这类文档，不能静默丢内容。
- Stack 建树：按 heading level 构建稳定树。
- PageIndex 兼容 `node_id`：最终对外 `node_id` 按 thinning 后的 preorder 从 `0000` 开始，4 位 zero padded；它只在同一 document version 内稳定。
- Tree thinning：默认关闭；显式开启时先合并小节点再分配 `node_id`，避免被删除节点留下可引用 ID。
- `summary` / `prefix_summary` 双层摘要：叶子节点摘要和分支节点概览分开。
- Summary short-circuit：短节点直接用原文做摘要，不调 LLM。
- Structure 去原文：导航阶段只给 title/node_id/path/summary，不给 text。
- Retrieval contract：PageIndex demo 是 `meta -> structure -> page_content` 三段式；Hive 改造为 `kb.doc.meta -> kb.doc.structure -> kb.section.text`，Markdown 用 `node_id` 精确取节点，不沿用 PageIndex 的 line-number-as-page。
- Sentinel/evidence 思想：引用只能来自本轮 tool 返回的证据集合；生产以服务端 evidence ledger 为准，`<kb_ref>` 只做展示/模型约束。
- PDF 边界校验思想：P2 转换阶段吸收 page count、空文本、页码越界、降级状态这些校验；若未来做智能 PDF tree，再参考 PageIndex 的 `verify_toc`、页码偏移估计、错误项重修和大节点递归拆分。

不吸收：

- PageIndex PDF -> tree 的 LLM fallback pipeline。
- PageIndex 开源仓库没有 MinerU/OCR/图片资产输出实现；Hive PDF ingest 必须走 `internal/fileconv` 的 `MarkdownProvider` 抽象，默认 MinerU，可替换 external OCR/解析工具/模型 provider。provider 不可用时显式失败，不生成降级文档。配置为 MinerU 时，启动期必须做 binary 自检；缺失且允许安装时自动执行配置的安装命令，默认使用 `builtin:python-venv-pip` 在 `install_dir` 创建隔离 venv（优先 `python -m venv`，失败后 fallback 到 `python -m virtualenv`）安装 `mineru[all]`，安装失败则 fail-fast。
- LLM-as-judge completeness check。
- LiteLLM 直连。
- OpenAI Agents SDK demo。
- `_meta.json` + workspace JSON 存储。

## 6. P0 目标架构

```text
Markdown
  -> tree_builder       # heading regex + stack tree，零 LLM
  -> tree_thinning      # token 阈值合并，零 LLM
  -> tree_summary       # 走 airouter，短节点 short-circuit
  -> pg_store           # namespace/document/tree_nodes/evidence_ledger
  -> router tools       # kb.doc.meta / kb.doc.structure / kb.section.text
  -> master ReAct loop  # 现有工具调用链路
  -> evidence extractor # 从服务端 ledger 生成 citation
```

P0 只回答一个问题：Hive 是否能在现有 router/tool/quality 边界内，可靠地从 markdown KB 中取证回答，并证明没有跨 owner/domain/namespace 泄漏。

## 6.1 系统兼容方案

KB 兼容 Hive 的方式是“内嵌能力域”，不是“旁路 RAG 服务”：

| 系统边界 | 兼容方式 |
|---|---|
| Router / Capability | 新增平台级 `kb.read` 和 `kb.doc.meta` / `kb.doc.structure` / `kb.section.text` 三个 read-only tool profile；保留现有 `kb_search`，后续作为 vector/hybrid façade，不作为 tree-mode 主入口 |
| Tool Runtime | KB 工具走现有 MCP host / tool wrapper / ReAct tool-call 链路；wrapper 只解析可选 `namespace_id` narrowing、`doc_id`、`node_ids`，不接受模型传入 owner/domain/session/agent 授权字段 |
| Auth / Scope | owner、user、session 从 `auth.UserFrom(ctx)`、`toolctx.GetSessionID(ctx)`、tool trace/turn context 和 route/domain runtime context 派生；store 层再次 fail closed |
| Store / Migration | 复用 `internal/store/postgres_migrate.go` 和 PG store 模式；新增 KB 独立表，不复用 memory 表，也不使用 PageIndex `_meta.json` workspace |
| LLM / Summary | 摘要、doc description、后续 rerank/embedding 全走 `internal/airouter`；不引入 LiteLLM 或 OpenAI Agents SDK |
| Domain Policy | `kb.read` 是只读能力，不依赖 `external.send`；`customer_service` 只通过 KB binding 获得可用 namespace 和工具组合，人工升级仍走 external-write 门禁 |
| Quality / Trace | KB retrieval、evidence violation 进入 `agentquality.Event.Attributes`、observability trace 和 QualityWorkbench；doc/node/user 等高基数字段不进 metrics label |
| File / Asset | P0 只吃 Markdown；P2 扩展现有 `internal/fileconv`，并复用统一对象存储计划落地后的 `internal/asset`，不另建转换和资产服务 |
| Frontend | P1 复用现有 chat message、ToolAdapter、admin shell、`NodeClient`/`LocalNodeClient`，只增加最小 KB 管理页、tool card、citation card |

兼容迁移顺序：

1. P0 先上线平台 KB 三工具，验证 markdown tree-mode、ACL、evidence ledger。
2. `kb_search` 保持兼容，不删除、不抢 tree-mode 主路径。
3. 现有 `customer_service.kb.read` 口径逐步迁移到平台 `kb.read` + KB binding；客服域只保留 `customer_service.escalate` / `customer_service.escalation.cancel` 等业务动作能力。
4. P1 再补 API、前端和质量观测。
5. P2 才接 PDF/DOCX、asset 和客服真实试点。

实施时必须以 P0 的“细节红线清单”为准，尤其是：

- 工具 schema 不暴露 owner/domain/session。
- `kb.doc.structure` 不返回原文。
- `kb.section.text` 限制节点数量和输出大小。
- Evidence token 绑定 session/turn/trace/document/version/node/scope。
- `DomainCustomerService` disabled 或缺 `external.send` 不影响平台 `kb.read`。
- `hostToolGroups` 新增 `kb` 组，不把 KB 三工具塞进 `customer_service` 组。
- `hostToolPolicyProfiles` 和 `defaultToolPolicyConfig()` 必须同步包含 `kb` 组或显式说明只通过召回使用；默认实现优先让 `master_direct` 使用 `group:kb`。
- `AllowedToolInputs` 只是 runtime 输入收窄，不是 KB ACL；owner/domain/namespace/doc/node 权限必须在 KB service/store SQL 再过滤。
- `namespace_id` 对模型和 API 只是可选 narrowing，不是授权凭证；省略时使用服务端 `KBBindingResolver` 解析出的 allowed namespace set。

## 6.2 用户使用流与 Agent 接入流

普通用户不能被要求理解 namespace、document id、node id 或 PageIndex tree。KB 对用户的产品形态是“Agent 回答时自动使用已绑定知识，并给出可核验引用”。

管理员使用流：

1. 在管理后台创建 namespace，例如 `refund_policy`、`product_faq`、`engineering_sop`。
2. 上传 Markdown；P2 后可上传 PDF/DOCX，由 `internal/fileconv` 转成 Markdown 后进入同一 ingest pipeline。
3. 在 KB 管理页把 namespace 绑定到 Agent、业务域、会话模板、租户或用户。
4. 用预览会话验证：同一问题在绑定和未绑定上下文下的工具调用、citation、no-evidence 行为是否符合预期。
5. 灰度上线：只调整 binding，不改 Agent prompt、不复制文档、不为每个业务域重新做 RAG。

终端用户使用流：

1. 用户正常发问，例如“这个订单超过 7 天还能退吗？”。
2. Agent 根据工具描述和运行时 KB hint 自动调用 `kb.doc.meta -> kb.doc.structure -> kb.section.text`。`kb.doc.meta` 返回 `page_count`、`line_count`、`node_count`，用于先判断文档尺度；Markdown/DOCX 优先用 `node_ids`；PDF 或由 OCR/layout provider 转出的 Markdown 如果结构树带 `start_page/end_page`，优先选择 tight `page_ranges`，例如 `["5-7"]`。
3. 回答正文下方展示服务端生成的 citation；用户不看 namespace，也不直接操作 KB 工具。
4. 如果绑定知识中没有证据，Agent 必须拒答、降级为泛化说明或触发业务升级；不能编造来源。

Agent runtime 流：

```text
user/session/agent/domain/tenant
  -> KBBindingResolver
  -> allowed namespace set
  -> prompt KB hint              # 只包含可用知识的元信息，不包含授权秘密
  -> kb.doc.meta                 # namespace_id 可省略，默认查 bound namespaces
  -> kb.doc.structure            # doc_id 必填，namespace_id 可选
  -> kb.section.text             # doc_id + node_ids/page_ranges，namespace_id 可选
  -> service/store ACL filter    # owner/domain/bound namespace/status/time SQL fail closed
  -> evidence ledger
  -> assistant citations
```

PageIndex-style accuracy 闭环：

- 开源 PageIndex 的核心检索契约是 document meta、structure tree、page content 三段式；Hive 对应为 `kb.doc.meta`、`kb.doc.structure`、`kb.section.text`，并复用现有 router、tool runtime、evidence ledger、统一对象存储。
- PDF 页锚来自 MinerU 或 external provider 输出的 Markdown。当前 ingest 支持 `<physical_index_5>`、`<page_5>`、`<!-- page: 5 -->`、`[[page=5]]` 等标记，写入 `start_page/end_page` 后可由 `page_ranges` 检索。
- `kb.section.text` 通过 `page_ranges` 命中的节点仍会返回服务端 evidence token，并只返回页范围内的 `asset_refs`；图片不会进入 tool output base64。聊天页展示这些 `asset_refs` 时必须走 `purpose=kb_section_text` 并携带当前 `session_id/domain_id`，由 KB binding resolver 校验后再获取短时 URL。
- `internal/agentquality.ScoreKBPageIndexRetrieval` 用于真实样本回归：按 expected node/page range、actual retrieval、citation node/page range 分别统计命中率。不能只凭实现形态声明“达到 PageIndex 准确率”；必须用业务样本 eval 证明。

绑定解析原则：

- `KBBindingResolver` 是服务端组件，输入来自 authenticated user、tenant、session、agent id、domain id、session template 和系统配置；不读取模型输出里的授权字段。
- prompt KB hint 只是可用知识提示，不是 ACL。真正授权必须在 KB service/store 层用 owner/domain/bound namespace/status/time 过滤。
- scope 建议支持 `system`、`tenant`、`user`、`agent`、`domain`、`session_template`、`session`；P0 优先实现 `tenant/user + agent/domain/session_template/session` 的 union allow-list。
- 多个有效 binding 取并集；禁用或过期 binding 立即失效。显式 deny/revoke 可后置，但 `enabled=false` 必须 P0 支持。
- 工具参数里的 `namespace_id` 只做 narrowing：传入值必须属于 resolver 产出的 allowed namespace set，否则返回 empty/not found，不能扩大权限。
- 未解析到 binding 时，`kb.doc.meta` 返回可恢复的 “no KB bound” 结果，并记录 quality event；不得回退到全量 namespace。

建议 P0/P1 数据表：

```text
kb_bindings(
  id,
  owner_scope,
  owner_id,
  domain_id,
  namespace_id,
  binding_type,       # agent | domain | session_template | session | tenant | user | system
  binding_target,
  enabled,
  effective_at,
  expires_at,
  created_by,
  created_at,
  updated_at
)

kb_binding_audit(
  id,
  binding_id,
  action,             # create | enable | disable | update | delete
  actor_user_id,
  before_json,
  after_json,
  created_at
)
```

P0 可只落 `kb_bindings`；P1 管理 API 落审计或最少记录操作人和时间。

## 6.3 Markdown 图片与资产存储口径

上传文档里的图片不能留在原始本地路径，也不能把 base64 或二进制塞进 Markdown、`kb_tree_nodes.text` 或 PostgreSQL 大字段。统一口径如下：

1. P0/P1 的 Markdown tree-mode 只保证文本证据闭环；如果上传 Markdown 包含本地图片引用、data URI 图片或远程图片，而资产层尚未启用，ingest 必须拒绝或标记 degraded 且不静默 active。
2. P2 启用 `internal/asset` 后，所有图片二进制进入统一对象存储：local 开发环境是配置的 `asset.local.base_path`，生产是 MinIO/S3 bucket，例如 `hive-assets`。
3. PostgreSQL 只保存资产元数据和关系：`assets` 表保存 owner、namespace、content_hash、mime、size、object key；KB 侧增加 `kb_node_assets` 或等价关系表保存 document/node 与 asset 的关联。
4. Markdown 原文在 ingest 后被重写：`![alt](./images/a.png)`、`![alt](data:image/png;base64,...)` 或已下载归档的远程图，统一替换为 `![alt](asset://kb/{owner_scope}/{owner_id}/{namespace_id}/{document_id}/{content_hash}.png)` 或等价 opaque `asset://` URI。
5. `asset://` 不是公开 URL，也不是授权凭证。前端展示时调用 `/api/v1/assets/resolve?uri=...` 获取短时 signed URL，服务端按 owner/domain/binding/document 权限校验后才返回；聊天附件和 Agent artifact 还必须带匹配的 `session_id`。
6. `kb.section.text` 返回节点文本时保留 `asset://` 占位，同时返回结构化 `asset_refs`，包括 `asset_uri`、`alt_text`、`caption`、`mime_type`、`content_hash`、`line`、`page` 和可选的 OCR/vision extracted text。按 `page_ranges` 取证时只返回页范围内图片；聊天页 resolve 必须使用运行态 `purpose=kb_section_text`，不能使用管理态 `kb_management`。
7. 只有图片本身不能算文本证据。没有 OCR/vision 提取结果时，Agent 可以引用“该节点包含图片”，但不能声称图片内容支持某个事实；要么降级，要么要求人工确认。

上传入口同样走统一口径：Admin KB 导入页和 API 只接受 `multipart/form-data`，文档文件字段为 `file`，图片字段为重复 `assets`，服务端把文件字节交给 `internal/fileconv.MarkdownRegistry` 和 `internal/kb/asset_ingest.go`；系统未上线，无老客户端兼容负担，JSON/base64 ingest API 已删除，非 multipart 请求返回 415。

因此，Markdown 中的图片“存在”三处：对象体在统一对象存储，元数据在 `assets` 表，Markdown/tree node 中只保留可审计的 `asset://` 引用。

## 7. Evidence 口径

不要把 LLM 输出当作唯一证据源。生产口径是：

1. `kb.section.text` 返回节点原文时，服务端生成本轮 `EvidenceToken`，写入 evidence ledger。
2. 最终回答结束前，服务端从本轮 tool-call ledger 汇总已返回的 evidence，生成 assistant message 的 `citations` metadata，并同步放入 WebSocket 最终消息和历史消息 API。
3. 如果 assistant 引用了不存在或本轮未返回的 evidence，记录 `quality.kb_evidence_violation`。
4. 用户可见 citation 可以裁剪；内部 evidence payload 必须包含 document/version/node/path/owner/domain/namespace。

`<kb_ref>` tag 可以作为展示和模型约束，但不能替代服务端 ledger。

## 8. 数据策略

P0 必须有独立表：

- `kb_namespaces`
- `kb_documents`
- `kb_tree_nodes`
- `kb_bindings`
- `kb_evidence_events` 或等价 evidence ledger 表

后续 vector-mode 才增加：

- `kb_chunks`
- embedding / vector index 字段

所有查询默认 fail closed：

- 缺 owner 不返回。
- 缺 domain 不返回。
- 没有 effective binding 不返回。
- namespace 未授权不返回。
- document 非 active 不返回。
- effective/expires 不满足不返回。

## 9. 验收门禁

P0 最低门禁：

- `go test ./internal/kb ./internal/router ./internal/tools ./internal/domainpolicy -run '(KB|Evidence|Tree|Namespace|ACL)' -count=1`
- Markdown heading 建树稳定。
- `kb.doc.structure` 不返回原文。
- `kb.section.text` 只返回授权节点。
- 跨 owner/domain/namespace 查询返回空或 404。
- revoked/archived/expired document 不进入工具结果。
- citation 只能来自本轮 evidence ledger。

P1 最低门禁：

- `go test ./internal/api ./internal/agentquality ./internal/observability ./internal/qualityworkbench -run '(KB|Evidence|Citation|Label|Workbench)' -count=1`
- `cd frontend && npm run lint`
- `cd frontend && npm run build`

P2 最低门禁：

- 对象存储 local/minio 实现通过去重和 owner scope 测试。
- PDF/DOCX 转 markdown 失败可观测，不能静默 active；PDF provider 缺失、关闭或执行失败时必须显式报错，不能 stub。
- KB 文档和图片上传路径必须是 multipart；JSON/base64 ingest API 已删除，非 multipart 请求返回 415。
- `customer_service` 只读 KB 不需要外发权限，人工升级需要 external write。
- webhook outbox 至少一次投递，最终失败进入 owner-scoped DLQ。

## 10. 历史草案处理

旧计划文件已删除，避免后续执行者从过时入口开工。其有效内容已合入本文和 P0/P1/P2：

- KB 平台概念：已合入本文。
- PageIndex 技术研究：已合入本文的 PageIndex 吸收范围和 P0 tree-mode 计划。
- 前端完整愿景：已拆到 P1/P2，P1 只做最小 API/前端闭环。
- 客服实验域：已拆到 P2，不再作为首发前置。
