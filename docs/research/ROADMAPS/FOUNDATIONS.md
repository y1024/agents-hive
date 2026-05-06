# Foundations Roadmap

> **定位**：治理与运行底座
>
> **优先级**：P1
>
> **目标**：让 P0 质量主线可测、可控、可运营、可回滚
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 代码前提

这条路线不是补空白底座。

代码里已经有：

- `internal/observability`：PG trace/metric/log writer，nil safe，异步写入。
- `internal/master/session_loop.go`、`internal/master/react_processor.go`：已有 session/tool/LLM/span/metric 打点。
- `internal/security`：内置危险规则、AST parser、LLM classifier、安全执行策略。
- `internal/skills/permission.go`：permission rule、session grants、plugin hook、LLM classifier、HITL。
- `internal/sandbox`：Local/Docker executor，Docker 支持 no-new-privileges、cap-drop、read-only、network、seccomp。
- `internal/accounting` + `internal/auth`：cost tracker、usage_records、quota、Admin 用量统计。
- `internal/gateway`：RPC/WebSocket gateway、token scope、rate limit、body size limit。
- `internal/controlplane`：session pool、rate limiter、binding store。
- `internal/airouter`：按任务类型选择模型，支持 provider/model DB 配置和热重载。
- `internal/subagent/factory.go`：已有 per-session 隔离、maxPerSession、maxGlobal、maxSpawnDepth、工具白名单。
- `internal/acpserver` / `internal/acpclient`：已有 ACP session、auth、permission bridge、stream、remote agent 包装。

所以本路线图不是“补 observability/security/cost”，而是把这些已有能力统一成可执行治理策略。

## 2. 近期最高风险

### 2.1 Observability 模型过松

当前 `observability.Metric.Labels` 是 `map[string]any`，多处直接写 `session_id`、`user_id` 等高基数字段。继续这样扩张会导致指标不可聚合、存储膨胀、看板不可用。

必须做：

- 定义 metric 命名规范。
- 定义允许 label 白名单。
- 高基数字段进入 trace/log attributes，不进入通用 metric labels。
- 建 quality event schema，服务 `AGENT-QUALITY.md`。

### 2.2 危险操作边界跨入口不一致

`internal/master/lifecycle.go` 的核心原则应是最小打扰：普通工具和安全 shell 命令默认放行，只有删除、删库、force push、硬重置等不可逆危险操作进入 HITL 或拒绝。跨入口问题不是“增加审批”，而是危险操作不能因为 Web/API/IM/ACP 入口不同而静默执行。

必须做：

- Web/API/IM/ACP 路径统一 dangerous operation semantics。
- 普通工具、普通 shell、只读操作不得触发审批。
- 危险/不可逆操作必须进入 HITL 或直接拒绝。
- tool profile/filter 只用于系统级工具可见性控制，不作为频繁用户审批机制。

### 2.3 Timeout 和 capacity 分散

代码里已有多个 timeout 和 limit：

- ReAct task。
- tool execution。
- task/spawn_agent 30 分钟兜底。
- Docker executor 默认 120 秒。
- Gateway rate limit。
- ControlPlane session/rate limit。
- AgentFactory maxPerSession/maxGlobal/maxSpawnDepth。

必须做：

- 建统一 runtime policy。
- 每个 timeout/limit 有配置、默认值、metric、错误码。
- 过载和超时能回放到具体 session/task/tool。

### 2.4 Cost / quota 已有但未进入质量闭环

已有 usage_records、cost tracker、quota、Admin usage，但还需要和质量事件关联。

必须做：

- 每次 LLM 调用记录 task type、prompt version、quality case。
- 统计每类失败的 token 成本。
- 让 over-budget、quota exceeded、model route 进入失败分类。

### 2.5 SubAgent / ACP 运行边界未进入统一治理

本地 subagent 和远程 ACP 已经是系统能力，不应等生态化后再治理。它们会直接影响任务质量和风险边界。

必须做：

- 父任务、子任务、远程 ACP session 之间有 trace linkage。
- 子 Agent 继承 userID、workspace、quota、cost budget、危险操作边界。
- `spawn_agent`、`task`、`parallel_dispatch` 的 timeout、max turns、max depth、工具白名单纳入 runtime policy。
- ACP permission bridge 不能绕过本地 dangerous operation semantics。
- ACP cancel、disconnect、timeout、reconnect、partial result 都进入质量事件和 journal/replay。

## 3. 施工顺序

### 3.1 指标与事件治理

先定义四类事件：

- `quality.agent_turn`：每轮 ReAct 质量事件。
- `quality.tool_decision`：工具选择与参数事件。
- `quality.context_build`：prompt/context/memory/attachment 构成事件。
- `quality.permission_decision`：危险操作、HITL、sandbox 决策事件。
- `quality.delegation`：本地 subagent、parallel dispatch、remote ACP 的委派输入、父子 trace、结果和失败类型。

每类事件必须区分：

- 低基数 labels：route、tool_name、failure_type、decision、status。
- 高基数 attributes：session_id、user_id、trace_id、prompt_hash、tool_args_hash。

### 3.2 危险操作边界一致性

优先处理：

- 普通操作默认放行，避免审批打扰。
- 删除、删库、force push、硬重置等 `PolicyAsk` 危险操作不能静默执行。
- HITL approve scope 最小化，只记住同 actor/workspace/tool/input fingerprint 的危险操作。
- Sandbox executor 和 SafeExecutorWrapper 的覆盖检查。
- bash 绕过路径的 regression case。

验收：

- 安全命令和普通工具不会弹审批。
- 相同危险 tool/input 在 Web/API/IM/ACP 下得到一致策略，除非配置显式声明差异。
- 危险操作没有审批 UI 时不能静默执行。

### 3.3 Runtime policy

建立一份运行策略表，覆盖：

- LLM call timeout。
- tool timeout。
- task timeout。
- spawn_agent timeout。
- subagent max turns。
- subagent max depth。
- ACP prompt timeout。
- ACP reconnect timeout。
- per-session 并发。
- global worker pool。
- Gateway rate limit。
- Docker resource limits。
- cost budget。
- user quota。

每项必须有：

- 配置来源。
- 默认值。
- 错误码。
- metric。
- 用户可见错误文案。
- 回滚方式。

### 3.4 Admin/Gateway 运营闭环

基于现有 Admin 和 Gateway：

- Admin UsageStats 接入质量失败成本。
- PromptManager 保存时展示 eval 风险。
- LLM Provider 管理显示 task route 和 fallback。
- Gateway methods 纳入同一 auth/session/user 语义。
- 管理操作写 audit log。
- Admin 质量视图展示 subagent/ACP 的失败成本、超时、取消、危险操作决策。
- Gateway/ACP/API/IM 入口的危险操作决策输出相同 low-cardinality policy/status labels。

### 3.5 SubAgent / ACP 最小治理

这部分不是 P4 生态扩张，而是 P0 质量闭环的运行前提。

近期必须做：

- `spawn_agent` 和 `task` 创建的子 Agent 记录 parent session、parent trace、agent id、tool whitelist、spawn depth、max turns。
- `parallel_dispatch` 记录 group id、每个子任务状态、失败类型、partial result。
- `AgentFactory.DestroyAgent` 失败必须进入 runtime failure event，避免配额泄漏不可见。
- `acpserver.ClawAgent.NewSession`、`Prompt`、`Cancel`、`CloseSession` 记录 ACP session 与 Master session 绑定关系。
- `acpserver.createACPPermissionFn` 只承接危险操作确认，不成为绕过本地策略的后门。
- `acpclient.RemoteACPAgent` 记录 remote prompt、stop reason、cancel/refusal、transport failure。

## 4. 不做什么

- 不重写 observability 存储。
- 不为看板美化做大平台。
- 不把所有 runtime 配置一次性产品化。
- 不在质量 schema 未稳定前引入复杂 BI。
- 不绕开现有 security/permission/sandbox 体系另建一套。
- 不把 subagent/ACP 治理理解为完整多 agent 产品化。
- 不为了 ACP 生态化新增大批 surface 或 marketplace 能力。

## 5. 验收指标

P1 完成的标志：

- P0 golden tasks 能输出稳定质量事件。
- metric labels 通过基数检查。
- 普通操作默认放行，危险操作才审批或拒绝。
- timeout/capacity/cost/quota 都有统一错误分类。
- Admin 能看到质量失败与成本、prompt、tool、model 的关联。
- 危险操作边界相关回归 case 有本地验收命令。
- subagent/ACP 的委派、取消、超时、危险操作 bridge 有质量事件和 replay 定位。
- 子 Agent 不会绕过父 session 的 userID、quota、tool whitelist、危险操作边界。

## 6. 与其他路线图的关系

- 直接支撑 `AGENT-QUALITY.md`。
- 为 `MEMORY-AND-CONTEXT.md` 提供注入、压缩、检索的可观测底座。
- 限制 `CAPABILITY-SURFACE.md` 的扩张，任何新 surface 必须纳入治理。
- 为 `MULTI-AGENT-AND-ACP.md` 提供并发、危险操作边界、事件、恢复基础。
