# P4 Multi-agent And ACP Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> P4 已进入代码实施阶段。本文件记录当前 multi-agent/ACP 能力、入口、验证和边界,不再保留旧的扩张路线。

## 最终能力

当前能力面:

- delegation decision/eval, 对 direct vs delegate 做离线成本和延迟比较。
- orchestration primitives, 支持 sequential/parallel/fanout-fanin。
- agent tree 构建, 支持 Session Replay 侧展示父子 agent 结构。
- quota breaker, 限制 delegation depth/total/concurrency/acquire timeout。
- ACP client safe fs surface, 只允许 workspace 内读文件和创建新文件。
- ACP client terminal safe surface, 只允许只读白名单命令。
- ACP permission request 对危险请求保守拒绝或选择 reject option。
- ACP SessionBridge 组件通过 user/token/TTL 防止裸 session id 复用。
- Admin multi-agent 页面通过 Gateway RPC 查看本地 agent、远程 ACP agent、健康和运行时权限。

## 当前实现入口

- Delegation eval: `internal/evaluation/delegation.go`, `cmd/delegation-eval`
- Orchestration: `internal/orchestration/orchestrator.go`
- Quota breaker: `internal/quota/circuit_breaker.go`
- Agent tree UI: `frontend/src/components/replay/AgentTreeView.tsx`
- ACP client safe surface: `internal/acpclient/client_impl.go`
- ACP client transport: `internal/acpclient/transport.go`
- ACP remote agent wrapper: `internal/acpclient/remote_agent.go`
- ACP server bridge/validation/drift: `internal/acpserver/session_bridge.go`, `validation.go`, `spec_drift.go`
- Admin frontend: `frontend/src/pages/admin/MultiAgentEcosystem.tsx`
- Admin data path: `frontend/src/api/node-client.ts` -> Gateway RPC `remote_agents.*`, `config.get`
- Route: `/admin/multi-agent`

## ACP 当前安全语义

File system:

- `ReadTextFile` 只能读取 workspace 内绝对路径。
- `WriteTextFile` 只能创建 workspace 内新文件。
- 路径逃逸拒绝。
- 覆盖/截断已有文件拒绝,需要未来 HITL bridge 才能支持。

Terminal:

- 只允许命令名,拒绝带路径命令。
- 只读允许列表: `pwd`, `ls`, `cat`, `head`, `tail`, `wc`, `rg`, 以及 `git status/diff/log/show/branch`。
- 拒绝 shell、重定向、管道、写入、删除、网络、包管理、脚本解释器等危险命令。
- 可获取 terminal output / wait / release / kill,但没有交互 stdin 写入。

Permission:

- 危险请求优先选择 reject option。
- 普通只读请求优先选择 allow option。
- 无可用 option 时取消。

SessionBridge:

- `Bind(acpSessionID, internalSessionID, userID)` 返回随机 token。
- `Resolve` 必须同时匹配 session id、user id、token。
- 支持 token rotate 和 idle TTL cleanup。
- 当前是 server-side 组件级安全能力和测试覆盖,不代表完整 ACP server 产品协议面已经接入认证/授权/租户全链路。

Admin multi-agent:

- 前端不走独立 `/api/v1/admin/*` REST handler,而是调用 Gateway RPC。
- `remote_agents.list/health/connect/disconnect` 依赖 `ACPClientPool` 注入。
- `config.get` 依赖 Gateway `Config` 注入,用于展示运行时权限配置。
- 注入缺失时页面会展示错误或空态,不是 ACP/agent 能力不存在。

Transport:

- `stdio` 继续走子进程 stdin/stdout。
- `http` 会真实 POST line-delimited JSON-RPC,注入配置 headers,并把 JSON/NDJSON/SSE 响应桥回 SDK reader。

Spec drift:

- `SupportedACPMethods()` 暴露当前 validation 层支持的方法集。
- `CheckACPMethodDrift()` 对本地 fixture 做 missing/extra/invalid/duplicate 检查。
- 当前 fixture 是离线快照,用于防止本仓 validation 支持面退化,不是联网拉取上游 spec 的认证。

## 当前测试

```bash
go test ./internal/evaluation ./internal/orchestration ./internal/quota -count=1
go test ./internal/acpclient ./internal/acpserver -count=1
go test ./cmd/delegation-eval -count=1
cd frontend && npm run build
```

代表性测试:

- `TestACPClientImplWriteTextFileCreatesNewFileAndRejectsPathEscapeAndOverwrite`
- `TestACPClientImplCreateTerminalRunsSafeReadOnlyCommand`
- `TestACPClientImplCreateTerminalRejectsDangerousCommand`
- `TestHTTPTransportPostsJSONRPCAndStreamsSSEResponse`
- `TestSessionBridgeRejectsBareSessionHijack`
- `TestACPValidatedMethodFixtureMatchesSupportedMethods`
- `TestCircuitBreakerConcurrentLimitRaceSafeAndReleaseIdempotent`
- `TestOrchestratorFanoutFaninAggregatesSuccessfulOutputs`

## 明确边界

- 当前已有 validation method fixture drift checker,但不是完整上游 ACP spec 认证。
- 当前 server-side ACP handler 不是完整产品化协议面。
- Context isolation 当前是拷贝 shared values,不是完整 auth/cancel/token 传播策略。
- Multi-agent 页面是观测面,不是远程 agent marketplace。
- delegation eval 是离线比较工具,不是生产自动委派决策器。

## 待办

- 如果要做完整 ACP server,补全上游 spec method fixture、HITL fs/write bridge、terminal HITL bridge、token 过期 e2e。
- 如果要做生产自动委派,先把 eval 指标和 P3/P5 的质量门禁接起来,不要只按成本启发式上线。
- 如果要扩展 context isolation,必须从 parent context derive,保留 cancel/auth,不能使用裸 `context.Background()`。
