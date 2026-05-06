# Code Audit Evidence 2026-04-28

> **目的**：支撑 `docs/research/FINAL-PLAN.md` 的优先级判断。
>
> **结论**：Hive 不是能力缺失型系统。当前主要瓶颈是质量闭环、治理一致性、失败归因和回归门禁。

## 1. 审计范围

本轮审计覆盖：

- 后端：`cmd`、`internal` 下约 690 个 Go 文件，约 169520 行。
- 前端：`frontend/src` 下约 129 个 TS/TSX 文件。
- 文档：`docs/research/FINAL-PLAN.md`、`ROADMAPS/*`、`EVIDENCE/*`。

重点阅读模块：

- `cmd/server`
- `internal/bootstrap`
- `internal/master`
- `internal/tools`
- `internal/skills`
- `internal/security`
- `internal/sandbox`
- `internal/memory`
- `internal/compaction`
- `internal/specdriven`
- `internal/subagent`
- `internal/acpclient`
- `internal/acpserver`
- `internal/channel`
- `internal/gateway`
- `internal/controlplane`
- `internal/accounting`
- `internal/airouter`
- `internal/auth`
- `internal/api`
- `internal/observability`
- `frontend/src/pages/admin`
- `frontend/src/components`

## 2. 系统能力现状

### 2.1 Bootstrap 已装配大量成熟能力

`internal/bootstrap/server.go` 已装配：

- Spec-driven runner / store。
- PromptLoader。
- AI Router 和 image/video/TTS/STT/embedding adapter。
- CostTracker。
- AuthEngine / quota。
- SubAgents。
- Memory。
- Journal。
- Observability。
- TaskBoard。
- Builtin tools。
- skill_install / skill_search。

代码证据：

- `internal/bootstrap/server.go:371`：通过 `AIRouter.GetLLMClient(airouter.TaskPlanning)` 接 spec-driven RealRunner。
- `internal/bootstrap/server.go:399`：初始化 PromptLoader，三层优先级。
- `internal/bootstrap/server.go:403`：注册 AI 服务适配器。
- `internal/bootstrap/server.go:412`：初始化成本追踪。
- `internal/bootstrap/server.go:420`：注册 SubAgents。
- `internal/bootstrap/server.go:423`：初始化 memory。
- `internal/bootstrap/server.go:426`：初始化 journal。
- `internal/bootstrap/server.go:429`：初始化 observability。
- `internal/bootstrap/server.go:432`：初始化 TaskBoard。
- `internal/bootstrap/server.go:440`：注册内置工具。
- `internal/bootstrap/server.go:445`：按需注册 skill_install / skill_search。

判断：

- 不能再把 Hive 当作“底座空白”系统。
- 计划应优先复用和治理现有模块。

### 2.2 API 和 Admin surface 已经较完整

`internal/api/routes.go` 已有：

- agents、skills、skill metrics、health、capabilities。
- HITL input/command/pending。
- session create/list/get/update/delete/message/clear/fork/revert/regenerate/stop/star/tags。
- journal stats/replay。
- model list/switch。
- tools invoke。
- channel config/reload/push/schedule。
- auth/login/me/refresh。
- admin users/quota/logins/usage/auth providers/prompts/skills/LLM providers/models。

代码证据：

- `internal/api/routes.go:13` 到 `internal/api/routes.go:124`。

前端已有：

- `frontend/src/pages/admin/PromptManager.tsx`
- `frontend/src/pages/admin/UsageStats.tsx`
- `frontend/src/pages/admin/AuthProviders.tsx`
- `frontend/src/pages/admin/LLMProviders.tsx`
- `frontend/src/pages/admin/UserList.tsx`
- `frontend/src/pages/SessionReplay.tsx`
- `frontend/src/pages/ReplayGallery.tsx`
- `frontend/src/components/hitl/ApprovalCard.tsx`

判断：

- `CAPABILITY-SURFACE` 不应继续以“补页面/补产品面”为主。
- 近期应把这些 surface 接入质量调试、危险操作边界和运营闭环。

## 3. 主 Agent 现状

### 3.1 ReAct 主循环已有多层护栏

`internal/master/react_processor.go` 已有：

- ReAct 迭代。
- loop detector。
- terminal tool error cache。
- approval cache。
- user quota check。
- per-session cost budget check。
- orphaned tool call repair。
- context compression。
- streaming response。
- assistant structural lock。
- required tool guard。
- stop + tool_calls 优先执行。

代码证据：

- `internal/master/react_processor.go:232`：loop detector。
- `internal/master/react_processor.go:235`：terminal tool error cache。
- `internal/master/react_processor.go:239`：approval cache。
- `internal/master/react_processor.go:243`：quota check。
- `internal/master/react_processor.go:256`：cost budget check。
- `internal/master/react_processor.go:299`：消息修复和压缩前处理。
- `internal/master/react_processor.go:665`：assistant 持久化 structural lock。
- `internal/master/react_processor.go:713`：tool_calls 优先于 finish_reason。
- `internal/master/react_processor.go:741`：循环检测 hard stop / warn。

判断：

- P0 不应重写主循环。
- P0 应围绕现有主循环补 golden tasks、质量事件、失败分类和回归门禁。

### 3.2 Session worker 和 observability 已存在但指标模型需治理

`internal/master/session_loop.go` 已有 worker pool、per-session semaphore、trace/span/metric。

代码证据：

- `internal/master/session_loop.go:232`：投递到 worker pool。
- `internal/master/session_loop.go:286`：任务完成后释放 per-session semaphore。
- `internal/master/session_loop.go:290`：用户上下文传递给 taskCtx。
- `internal/master/session_loop.go:309`：写 `hive.sessions.active` metric，label 含 `session_id`。
- `internal/master/session_loop.go:329`：写 session process span。

判断：

- Observability 不是空白。
- 风险在于自由 labels、高基数字段和缺少 typed quality event。

## 4. Prompt 现状

### 4.1 PromptLoader 已支持三层优先级和热更新

`internal/i18n/prompt_loader.go` 已支持：

- DB > 文件 > go:embed > hardcoded default。
- DB TTL cache。
- 文件缓存和 mtime 轮询。
- Admin 更新后失效 DB cache。

代码证据：

- `internal/i18n/prompt_loader.go:32`：三层优先级描述。
- `internal/i18n/prompt_loader.go:93`：Load 入口。
- `internal/i18n/prompt_loader.go:103`：DB 层。
- `internal/i18n/prompt_loader.go:115`：文件层。
- `internal/i18n/prompt_loader.go:127`：go:embed 层。
- `internal/i18n/prompt_loader.go:148`：InvalidateDBCache。

### 4.2 PromptBuilder 已有模块化段落和工具 prompt 注入

代码证据：

- `internal/master/prompt_builder.go:156`：buildSystemPrompt。
- `internal/master/prompt_builder.go:163` 到 `internal/master/prompt_builder.go:170`：加载 `system/base`、`execution`、`business`、`code_editing`、`safety`、`reply`。
- `internal/master/prompt_builder.go:174`：核心工具提示。
- `internal/master/prompt_builder.go:185`：外部 MCP 工具提示。
- `internal/master/prompt_builder.go:203`：外部 MCP 工具专属 prompt。

判断：

- 不应重建 Prompt 系统。
- 应补 prompt 版本、eval、灰度、回滚和改动门禁。

## 5. Security / Permission / Sandbox 现状

### 5.1 已有多层权限和执行防护

代码证据：

- `internal/skills/permission.go:72`：PermissionManager。
- `internal/skills/permission.go:140`：permission grants 持久化加载。
- `internal/skills/permission.go:178`：热更新权限规则。
- `internal/skills/permission.go:194`：CheckPermission。
- `internal/sandbox/docker.go:74` 到 `internal/sandbox/docker.go:84`：Docker executor 设置 read-only、no-new-privileges、cap-drop、CPU/memory/pids/network/user。
- `internal/sandbox/docker.go:87`：seccomp profile。
- `internal/sandbox/docker.go:134`：执行超时。
- `internal/sandbox/docker.go:142`：容器丢失自愈重建。

### 5.2 重要治理点：危险操作边界跨入口一致

`internal/master/lifecycle.go` 的当前实现已把 `PolicyAsk` 统一送入 HITL，不再为 IM session 静默放行危险操作。P1 仍必须把这件事固化为回归测试，避免 Web/API/IM/ACP 入口再次出现危险操作语义分叉。

代码证据：

- `internal/master/lifecycle.go`：`PolicyAsk` 分支直接调用 `requestHITLPermission`。
- `internal/master/lifecycle.go`：`PolicyDeny` 直接拒绝。
- `internal/security/builtin_rules.go`：内置危险规则覆盖删除、删库、force push、硬重置、K8s/Docker 删除、`chmod 777` 等操作。

判断：

- 这是 P1 基础治理项，不是未来探索。
- 需要用回归测试保证 Web/API/IM/ACP 的 dangerous operation semantics 一致。

## 6. Memory / Context 现状

### 6.1 Memory 已有 PG、向量、混合检索、抽取、注入

代码证据：

- `internal/memory/pg_store.go:23`：PostgresMemoryStore。
- `internal/memory/pg_store.go:43`：Save。
- `internal/memory/pg_store.go:81`：异步生成 embedding。
- `internal/memory/hybrid.go:13`：HybridSearcher。
- `internal/memory/hybrid.go:36`：Search。
- `internal/memory/hybrid.go:91`：RRF 融合。
- `internal/memory/extractor.go:27`：从 summary 提取 memory。
- `internal/memory/extractor.go:84`：关键词分类。
- `internal/memory/injector.go:41`：InjectContext。
- `internal/memory/injector.go:48`：优先混合搜索。
- `internal/memory/injector.go:90`：格式化注入。

判断：

- 不从零建 memory。
- 缺口是来源证据、置信度、生命周期、冲突、注入污染控制、检索 eval、embedding 一致性。

## 7. Spec-driven 现状

### 7.1 已有 runner、planner、eval harness，但主执行仍非近期替代主线

代码证据：

- `internal/specdriven/ingress/runner.go:46`：Runner 接口。
- `internal/specdriven/ingress/runner.go:90`：RealRunner。
- `internal/specdriven/ingress/runner.go:143`：调用 `planner.Generate`。
- `internal/master/session_loop_specdriven.go:16`：applySpecDrivenIntake。
- `internal/master/session_loop_specdriven.go:23`：注释说明 Phase 2 MVP 下 path 只是 intake 决策。
- `internal/master/session_loop_specdriven_react.go:7`：ReAct 入口只读 specCtx 打诊断日志。
- `internal/specdriven/eval/harness.go:15`：eval Harness。
- `internal/specdriven/eval/harness.go:43`：required fixtures fm01-fm08。

判断：

- spec-driven 应保留孵化。
- 近期不作为主 Agent 质变主线。
- 可把它的 eval 经验复用到主 ReAct eval。

## 8. Multi-agent / ACP 现状

### 8.1 本地 subagent 已存在

代码证据：

- `internal/subagent/factory.go:38`：AgentFactory。
- `internal/subagent/factory.go:52`：per-session dynamic agents。
- `internal/subagent/factory.go:76` 到 `internal/subagent/factory.go:78`：maxPerSession/maxGlobal/maxSpawnDepth。
- `internal/subagent/factory.go:180`：要求 ctx 中包含 sessionID。
- `internal/subagent/factory.go:196`：per-session 数量限制。
- `internal/subagent/factory.go:201`：global 数量限制。
- `internal/tools/task.go:30`：TaskExecutor。
- `internal/tools/task.go:39`：maxDepth。
- `internal/tools/task.go:68`：caller 限制。
- `internal/tools/task.go:96`：system Agent denylist。
- `internal/tools/spawn_agent.go:68`：Master-only。
- `internal/tools/spawn_agent.go:120`：执行后销毁动态 Agent。

### 8.2 ACP server/client 已存在

代码证据：

- `internal/acpclient/remote_agent.go:18`：RemoteACPAgent 包装远程 ACP 为本地 subagent。
- `internal/acpclient/remote_agent.go:131`：handleTask 转 ACP Prompt。
- `internal/acpclient/remote_agent.go:177`：SendTask。
- `internal/acpserver/agent.go:31`：ClawAgent 实现 ACP Agent。
- `internal/acpserver/agent.go:75`：Authenticate。
- `internal/acpserver/agent.go:92`：NewSession。
- `internal/acpserver/agent.go:128`：ACP permission bridge 注入 Master。
- `internal/acpserver/agent.go:184`：Prompt 转发给 Master。

判断：

- 不能再说 ACP client/server 空白。
- 近期应做门禁、评测、恢复、权限、会话隔离，不应扩生态。

## 9. Channel 现状

`internal/channel` 已有成熟抽象和重型飞书实现：

- `ChannelPlugin`
- `EventRenderer`
- `InboundController`
- router dedup/debounce/retry/event bus
- 飞书 renderer/gap_fetch/reconnect/governance/acl/audit/hitl_bridge/health/reload/longconn
- push service 和 schedules

判断：

- 不新建 ChannelAdapter。
- 其它 channel 应复用飞书样板和现有 router，而不是另起抽象。

## 10. AI Router / Cost / Quota / Gateway 现状

### 10.1 AI Router

代码证据：

- `internal/airouter/selector.go:14`：按 task type 选模型。
- `internal/airouter/selector.go:41`：planning 走便宜 JSON/tools 模型。
- `internal/airouter/router.go`：从 DB reload providers/models，支持非 LLM provider。

判断：

- 模型调度已有骨架。
- 近期要把模型 route 和质量/成本关联。

### 10.2 Cost / quota

代码证据：

- `internal/accounting/pg_tracker.go:22`：Record usage_records。
- `internal/accounting/pg_tracker.go:39`：GetTotalCost。
- `internal/accounting/pg_tracker.go:86`：GetCostByUser。
- `internal/auth/engine.go:398`：CheckQuota。
- `internal/auth/engine.go:411`：IncrementTokenUsage。
- `internal/bootstrap/server.go:976`：initCostTracker。

判断：

- capacity/cost 不是空白。
- 需要纳入质量失败成本分析。

### 10.3 Gateway

代码证据：

- `internal/gateway/gateway.go:48`：Gateway。
- `internal/gateway/gateway.go:94`：IP rate limit。
- `internal/gateway/gateway.go:115`：body size limit。
- `internal/gateway/gateway.go:192`：auth scope。
- `internal/gateway/methods.go`：RegisterAllMethods 覆盖 health/session/agent/skill/HITL/config/channel/plugin/MCP/resource/remote agent。

判断：

- control surface 已有。
- 需要统一 auth/session/user 语义和 audit。

## 11. 对现有证据文档的修正

`docs/research/EVIDENCE/GAP-INVENTORY.md` 是历史输入，存在过期项：

- 把 Task/todos 写成缺失，但当前已有 `internal/taskboard` 和 `internal/tools/taskboard_tools.go`。
- 把 ACP client 写成缺失，但当前已有 `internal/acpclient`。
- 把 ask user question 写成缺失，但当前已有 `internal/tools/question.go`。
- 把 skill tool 写成缺失，但当前已有 `internal/tools/skill.go`。
- 把能力面作为大缺口，但当前代码已经有大量 Admin/API/frontend/channel surface。

因此该文件不能作为当前施工依据，只能作为历史对标参考。

## 12. 最终优先级依据

基于代码现状，正确排序是：

1. P0 Agent 质量闭环：eval、quality event、prompt/tool/skill/context 回归。
2. P1 治理与运行底座：observability label、permission consistency、runtime policy、cost/quality 关联。
3. P2 Memory / Context 深治理：来源、置信度、生命周期、注入污染、embedding 一致性。
4. P3 能力 surface 最小推进：只服务调试、治理和质量提升。
5. P4 Multi-agent / ACP 受控孵化：门禁、恢复、权限、会话隔离。
6. P5 未来研究：GEPA、自我改进、自动 skill、spec-driven 主执行替换。
