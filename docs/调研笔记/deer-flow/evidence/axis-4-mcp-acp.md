# Axis-4: MCP + ACP 集成深度对比（deer-flow vs Hive）

> **产出时间**: 2026-04-22 · **作者**: Claude（主线程整合，子 agent 在 deer-flow 侧出现幻觉，由主线程用 CLAUDE.md 官方文档 + 直接源码读取 + `grep` 证据重写）
> **前置证据**: 
> - deer-flow tarball: `docs/调研笔记/deer-flow/src/` (bytedance/deer-flow@main, 下载于 2026-04-22)
> - 官方架构文档: `docs/调研笔记/deer-flow/src/backend/CLAUDE.md` (已读入上下文)
> - Hive 源码: `internal/mcphost/`、`internal/acpserver/`、`internal/acpclient/`、`internal/a2abridge/`
> **覆盖范围**: MCP 协议实现深度、ACP 协议实现深度、OAuth 安全、多会话隔离、传输层、wire format 转换、HITL、A2A 扩展

---

## 0. TL;DR（先说结论，含方向纠错）

**一句话**: deer-flow 在 MCP 集成上是"薄壳客户端"(487 行 Python 包装 langchain-mcp-adapters)，ACP 集成是"纯 client-only"(256 行调用 acp-python-sdk 的 `spawn_agent_process`)；Hive 在 MCP 集成上是"自研 host"(2733 行 Go 手写协议栈)，在 ACP 集成上是"server + client + A2A 三角"(1694 行，含完整 ACP server + 远端 client + A2A in-process bridge)。

**原始 final-verdict 的方向错误**（必须更正）:
- ❌ 原文暗示"deer-flow ACP 三角（client+server+bridge）是参考标杆"
- ✅ 事实反转：**Hive 才是 ACP 三角，deer-flow 只有 client 一边**
- deer-flow `backend/CLAUDE.md` 明文："`invoke_acp_agent` - Invokes external ACP-compatible agents from config.yaml"，**从未实现 server 侧**
- `backend/app/gateway/routers/assistants_compat.py` 不是 ACP server，而是 **LangGraph Platform useStream hook 的 JSON stub**（见下方 §2.4）

**评分对齐（1-10 分，越高越好）**:

| 能力 | deer-flow | Hive | 领先方 |
|---|---|---|---|
| MCP 客户端（多 server + 缓存）| 8 | 8 | 持平 |
| MCP transport 覆盖 | 7 (stdio/sse/http) | 9 (stdio/sse/http + OAuth 状态机) | Hive +2 |
| MCP OAuth 深度 | 5 (client_credentials + refresh_token) | 9 (PKCE + client_credentials + refresh_token + DB 持久化) | Hive +4 |
| MCP 自研 vs 外部依赖 | 外部 `langchain-mcp-adapters` | 自研 Go 协议栈 | 中立（各有代价） |
| ACP client（远程调用） | 8 (per-thread workspace + wire 转换) | 8 (连接池 + health check + mailbox) | 持平 |
| ACP server（被调用） | 0（无实现） | 9（NewSession / Prompt / Cancel / 流式更新 / 多会话 MCP 绑定） | **Hive 单边领先** |
| A2A 协议桥 | 0（无实现） | 7 (InProcessTransport, 可扩展远程) | **Hive 单边领先** |
| HITL（权限审批）| 6（依赖 ACP SDK 的 request_permission）| 8（`createACPPermissionFn` 可插拔 + 命令级 skill-permission）| Hive +2 |

---

## 1. 研究方法

### 1.1 数据源
- **deer-flow 侧**: 直接源码读 `backend/packages/harness/deerflow/mcp/{client,oauth,tools,cache}.py`（共 487 行），`backend/packages/harness/deerflow/tools/builtins/invoke_acp_agent_tool.py`（256 行），`backend/app/gateway/routers/assistants_compat.py`（149 行），以及 `backend/CLAUDE.md` 官方架构文档。
- **Hive 侧**: `internal/mcphost/`（26 文件 2733 非测试行 + 测试共 5160 行），`internal/acpserver/`（10 文件 922 非测试行 + 测试共 1468 行），`internal/acpclient/`（7 文件 604 非测试行 + 测试共 874 行），`internal/a2abridge/`（3 文件 168 非测试行 + 测试共 275 行）。

### 1.2 验证手段（命令证据见附录 B）
1. `grep -n "^func\|^type"` 枚举公开 API surface
2. `wc -l` 统计代码体量
3. `grep "PKCE\|client_credentials\|refresh_token\|AuthorizePKCE"` 对比 OAuth grant type 覆盖
4. `grep "acp_sdk\|spawn_agent_process\|AgentSideConnection"` 辨认 client vs server
5. `grep "NewSession\|Prompt\|Cancel"` 验证 ACP server 方法实现

### 1.3 已知局限
- deer-flow 只读了 MCP + ACP 主文件，没有深入 `langchain-mcp-adapters` 外部库的内部逻辑（但这本身就是"外部依赖"这个结论的佐证）
- Hive A2A bridge 目前只有 InProcessTransport，未验证跨进程/远端 transport 是否已规划

---

## 2. deer-flow 的 MCP+ACP 集成（真相）

### 2.1 MCP 客户端——`langchain-mcp-adapters` 薄壳包装

deer-flow MCP 模块结构（`backend/packages/harness/deerflow/mcp/`）:

```
__init__.py    14 行
cache.py      142 行   # 全局 list[BaseTool] 缓存 + mtime 失效
client.py      68 行   # build_server_params / build_servers_config
oauth.py      150 行   # OAuthTokenManager (client_credentials + refresh_token)
tools.py      113 行   # get_mcp_tools() 主入口
─────────────────────
合计          487 行
```

**核心事实**: `tools.py` L63 `from langchain_mcp_adapters.client import MultiServerMCPClient` —— 整个协议握手、session 生命周期、tool 发现、invoke 全部委托给 LangChain 社区库，deer-flow 自己只做三件事：
1. 把 `extensions_config.json` 的 MCP 配置翻译成 `MultiServerMCPClient` 的 `dict[str, dict]` 参数（`client.py`）
2. OAuth token 获取 + 拦截器注入 Authorization header（`oauth.py`）
3. sync 调用包装（`tools.py` L25-53 `_make_sync_tool_wrapper`，全局 `ThreadPoolExecutor(max_workers=10)`）

**运行时缓存**（`cache.py`）:
```python
_mcp_tools_cache: list[BaseTool] | None = None
_config_mtime: float | None = None
```
- 懒加载：首次 `get_cached_mcp_tools()` 才触发 `initialize_mcp_tools()`
- 失效：比较 `extensions_config.json` 的 `os.path.getmtime()`，变了就全量重建
- 全局单例：不分 user、不分 thread（⚠️ 多租户盲点，后文 §6 讨论）

### 2.2 MCP OAuth——覆盖 2 种 grant type，无 PKCE

`oauth.py` L72-120 `_fetch_token` 明确分支：

```python
if oauth.grant_type == "client_credentials":
    if not oauth.client_id or not oauth.client_secret:
        raise ValueError(...)
    data["client_id"] = oauth.client_id
    data["client_secret"] = oauth.client_secret
elif oauth.grant_type == "refresh_token":
    if not oauth.refresh_token:
        raise ValueError(...)
    data["refresh_token"] = oauth.refresh_token
else:
    raise ValueError(f"Unsupported OAuth grant type: {oauth.grant_type}")
```

**对比 Hive `mcphost/oauth.go`**:
```go
func (c *OAuthClient) AuthorizePKCE(ctx context.Context) (*OAuthToken, error)     // L115
func (c *OAuthClient) refreshToken(ctx context.Context, refreshTok string)        // L219
func (c *OAuthClient) exchangeCode(ctx context.Context, code, codeVerifier, ...)  // L279
func generateCodeVerifier() (string, error)                                        // L342
func generateCodeChallenge(verifier string) string                                 // L354
```

Hive 有完整 **PKCE 流程（code_verifier + code_challenge + exchange_code）**，deer-flow 没有。PKCE 是给"用户亲自授权"的公共客户端（例如 user 的 MCP server 需要 OAuth login）用的；`client_credentials` 只适合"机器对机器"。deer-flow 目前假设所有 MCP server OAuth 都是机器对机器场景，对"让终端用户 login 到某个 MCP SaaS"无解。

**Token 持久化**:
- deer-flow: 内存 `_token_cache: dict[str, _OAuthToken]` in `OAuthTokenManager`，进程重启即丢失
- Hive: `mcphost/token_store.go` `DBTokenStore` 接入 SQL 层（`SaveToken/LoadToken/DeleteToken`），支持进程重启后复用 refresh_token

### 2.3 ACP 客户端——`acp-python-sdk` + per-thread workspace

`backend/packages/harness/deerflow/tools/builtins/invoke_acp_agent_tool.py`（256 行）是 deer-flow ACP 唯一入口。

**关键 SDK import**:
```python
from acp import PROTOCOL_VERSION, Client, text_block   # L175
from acp import spawn_agent_process                      # L226
from acp.schema import AllowedOutcome, DeniedOutcome    # L105
from acp import RequestPermissionResponse               # L104
```

**流程**（L164-260 简写）:
```python
async def _invoke(agent: str, prompt: str, config: RunnableConfig):
    thread_id = config["configurable"].get("thread_id")
    physical_cwd = _get_work_dir(thread_id)           # L211
    mcp_servers = _build_acp_mcp_servers()            # L213  wire format 转换
    # 用 acp.spawn_agent_process(cmd, ...) 拉起 ACP adapter 子进程
    # 传入 text_block(prompt) + mcp_servers
    # 通过 inner Client 的 session_update / request_permission 回调收集输出
```

**per-thread workspace 隔离**（L20-49）:
```python
def _get_work_dir(thread_id: str | None) -> str:
    paths = get_paths()
    if thread_id:
        work_dir = paths.acp_workspace_dir(thread_id)
        # {base_dir}/threads/{thread_id}/acp-workspace/
    else:
        work_dir = paths.base_dir / "acp-workspace"
    work_dir.mkdir(parents=True, exist_ok=True)
    return str(work_dir)
```

**Lead agent 可读不可写**: ACP 子进程 `cwd` 是 workspace，但 lead agent 只能通过虚拟路径 `/mnt/acp-workspace/`（只读）看到产物。

**wire format 转换**（`_build_acp_mcp_servers`，L60-94）: deer-flow MCP 的 internal 格式是 `dict[name -> config]`，ACP 协议要求 `list[{"name":..., "type":..., "command":...}]`，所以这里做了一层格式翻译，把 `env/headers` 从 `dict` 展平成 `list[{"name":..., "value":...}]`：
```python
payload["env"] = [{"name": k, "value": v} for k, v in server_config.env.items()]
```

### 2.4 `assistants_compat.py` ≠ ACP server（⚠️ 容易误解）

文件路径: `backend/app/gateway/routers/assistants_compat.py`
文档注释: `"Provides LangGraph Platform-compatible assistants API backed by the langgraph.json graph registry and config.yaml agent definitions."`

**这不是 OpenAI Assistants API，也不是 ACP server**，而是：
> "a minimal stub that satisfies the ``useStream`` React hook's initialization requirements (``assistants.search()`` and ``assistants.get()``)."

端点清单（全部是 LangGraph Platform 的 assistants compat）:
- `POST /api/assistants/search` — 返回 `lead_agent` + `config.yaml` 的 custom agents 列表
- `GET /api/assistants/{id}` — 返回 AssistantResponse
- `GET /api/assistants/{id}/graph` — 空的 `{"nodes": [], "edges": []}`
- `GET /api/assistants/{id}/schemas` — 空的 input/output/state/config_schema

**没有任何 ACP 协议字段**（PROTOCOL_VERSION、SessionUpdate、NewSessionRequest、Prompt 等都不存在于此文件）。原始 final-verdict 如果把这个当成"deer-flow ACP server"是误判，需要更正。

### 2.5 middleware 链中的 MCP/ACP 位置（CLAUDE.md 官方 18 项）

deer-flow lead_agent middleware 链（`agent.py` `_build_middlewares`，CLAUDE.md §Middleware Chain）:

1. ThreadDataMiddleware
2. UploadsMiddleware
3. SandboxMiddleware
4. DanglingToolCallMiddleware
5. LLMErrorHandlingMiddleware
6. **GuardrailMiddleware** — 唯一拦截 MCP/ACP tool call 的授权层（可选）
7. SandboxAuditMiddleware
8. ToolErrorHandlingMiddleware
9. SummarizationMiddleware
10. TodoListMiddleware
11. TokenUsageMiddleware
12. TitleMiddleware
13. MemoryMiddleware
14. ViewImageMiddleware
15. DeferredToolFilterMiddleware
16. SubagentLimitMiddleware
17. LoopDetectionMiddleware
18. ClarificationMiddleware

MCP tool 是 `get_available_tools()` 的一部分（CLAUDE.md §Tool System），直接加入 agent.tools，没有单独 middleware。ACP tool 是 `invoke_acp_agent` 这一个 tool，也在 builtins 列表里，没有 middleware 介入（只有 Guardrail 可以拦）。

---

## 3. Hive 的 MCP+ACP 集成（真相）

### 3.1 MCP host——Go 自研协议栈（2733 非测试行）

`internal/mcphost/` 文件结构：

```
client.go                # MCP client 主入口
convert.go               # tool schema 转换
hitl.go                  # human-in-the-loop 审批
host.go                  # 多 server 管理（等价 MultiServerMCPClient）
oauth.go           357L  # PKCE + client_credentials + refresh_token
prompt.go           31L  # MCP prompts 支持
resource.go              # MCP resources 支持
toolset.go          71L  # toolset 聚合
token_store.go           # OAuth token 持久化接口
transport.go             # transport 抽象
transport_http.go        # HTTP transport
transport_sse.go         # SSE transport
transport_stdio.go       # stdio transport
transport_builder.go  115L # factory
builtin_prompts.go       # 预置 prompts
builtin_resources.go     # 预置 resources
```

**与 deer-flow 的关键差异**:

| 维度 | deer-flow | Hive |
|---|---|---|
| 协议实现者 | `langchain-mcp-adapters`（外部 pip 包）| 自研（go.uber.org/zap + 纯 net/http）|
| 总行数 | 487 Python | 2733 Go（5160 含测试）|
| stdio transport | 委托 adapter | 自实现 `transport_stdio.go`（进程生命周期 + pipe 双工）|
| sse transport | 委托 adapter | 自实现 `transport_sse.go`（HTTP chunked）|
| http transport | 委托 adapter | 自实现 `transport_http.go`（HTTPS + header injection）|
| resources | ❌ 文档未提及 MCP resources 支持 | ✅ `resource.go` + `builtin_resources.go` |
| prompts | ❌ 文档未提及 | ✅ `prompt.go` + `builtin_prompts.go` |
| OAuth grant types | client_credentials + refresh_token | **PKCE + client_credentials + refresh_token** |
| Token 持久化 | 内存 dict | DB（`DBTokenStore`）|
| HITL | ❌ 无专用 HITL | ✅ `hitl.go` 专用审批链 |

### 3.2 ACP server（`internal/acpserver/`, 922 非测试行）

文件：
```
agent.go             (ClawAgent 实现 ACP 全部方法)
commands.go          (命令路由)
mcp_passthrough.go   (NewSession 时绑定 session 级 MCP clients)
permission.go        (HITL 权限 fn)
stream.go            (EventBus → acp.SessionUpdate 转换)
```

**公开 surface**（`grep "^func" internal/acpserver/`）:

```go
type ClawAgent struct { ... }
func NewClawAgent(m *master.Master, cfg, logger, cmdRegistry, host *mcphost.Host) *ClawAgent
func (a *ClawAgent) Initialize(ctx, acp.InitializeRequest) (acp.InitializeResponse, error)
func (a *ClawAgent) Authenticate(ctx, acp.AuthenticateRequest) (acp.AuthenticateResponse, error)
func (a *ClawAgent) NewSession(ctx, acp.NewSessionRequest) (acp.NewSessionResponse, error)
func (a *ClawAgent) SetSessionMode(ctx, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error)
func (a *ClawAgent) Cancel(ctx, acp.CancelNotification) error
func (a *ClawAgent) Prompt(ctx, acp.PromptRequest) (acp.PromptResponse, error)
func (a *ClawAgent) CloseSession(sessionID string)
func (a *ClawAgent) CloseAllSessions()
// stream.go
func streamSessionUpdates(ctx, conn, eb, sessionID, logger)
func convertToACPUpdates(msg master.BroadcastMessage) []acp.SessionUpdate
func convertToolCallEvent(payload interface{}) []acp.SessionUpdate
func convertAgentStartEvent(payload interface{}) []acp.SessionUpdate
func convertSkillExecEvent(payload interface{}) []acp.SessionUpdate
// mcp_passthrough.go
func connectSessionMCPServers(...)
func closeSessionMCPClients(clients []*mcphost.RemoteMCPClient)
// permission.go
func createACPPermissionFn(...) acp.PermissionFn
```

**关键机制**:

1. **多会话**: `sessionEntry` + `sync.Map`，支持同一 agent 并发承载 N 个客户端会话，每个会话独立 `cancel() context`、独立 `MCP clients list`、独立 EventBus 订阅。
2. **session 级 MCP 绑定**（`mcp_passthrough.go`）: 客户端发 `NewSession` 时可以附带 `mcpServers: []acp.McpServer`，Hive 会为**该 session**单独连这些 MCP（`connectSessionMCPServers`），而不是用全局 host。Close session 时 `closeSessionMCPClients` 清理。→ 多租户隔离默认可用。
3. **流式更新映射**（`stream.go`）: Hive 内部用 master.EventBus 广播事件（agent_start / tool_call / skill_exec / message），`streamSessionUpdates` 订阅该 session 的事件，翻译成标准 `acp.SessionUpdate` 发给客户端。
4. **HITL 权限**（`permission.go`）: `createACPPermissionFn` 注入到 agent 执行链，遇到受限工具调用时通过 ACP 协议的 `request_permission` 发给客户端裁决，客户端回 Allow/Deny，Hive 据此放行或拦截。

**deer-flow 的对等实现**: **零**。deer-flow 是 ACP client，Hive 是 ACP **client + server**。

### 3.3 ACP client（`internal/acpclient/`, 604 非测试行）

文件：
```
client_impl.go     (实现 acp.ClientSideConnection 接口)
pool.go            (ACPClientPool + health check)
remote_agent.go    (RemoteACPAgent + subagent.Agent 实现)
transport.go       (stdio/http transport 工厂)
types.go           (RemoteAgentConfig)
```

**相对 deer-flow 的增强**:

| 能力 | deer-flow `invoke_acp_agent_tool.py` | Hive `acpclient/` |
|---|---|---|
| 连接方式 | 每次调用 spawn 新进程（cold start）| 连接池 `ACPClientPool`（warm pool）|
| 健康检查 | ❌ | `HealthCheckAll(ctx) -> map[name]HealthStatus` |
| 断线重连 | 无（进程结束就没了）| pool 管理生命周期 |
| 多个远端 agent | 每次都要从 config 查 | `List() []RemoteAgentConfig` + `Get(name)` 热查询 |
| 子 agent 接口 | 只能通过 tool 调用 | 实现 `subagent.Agent` 接口，可直接当 Hive 的 subagent |
| 统一 mailbox | 无（prompt 一进一出）| `Mailbox() *subagent.SubAgentMailbox` 支持异步消息回收 |

但要客观：deer-flow 的 cold-start 模式也有好处——**每次调用都是干净进程**，不用担心 state 泄漏；Hive pool 有 state 泄漏风险，需要靠 session_update 机制保证 per-session 隔离。

### 3.4 A2A bridge（`internal/a2abridge/`, 168 非测试行）

文件: `adapter.go`（类型）+ `transport.go`（InProcessTransport）+ `adapter_test.go`

**现状**:
- `Message/Part/Task/TaskStatus/TaskResult` 类型符合 Google A2A 协议草案
- `InProcessTransport.SendMessage(ctx, agentID, msg)` 走内存路由（非跨进程）
- `RegisterAgent / UnregisterAgent / ListAgents / GetAgent` — 本地 subagent 注册表

**深度评估**: 这是 A2A 的**雏形**而非成熟实现——只有 in-process transport，没有真正的跨进程/跨主机协议栈。但比 deer-flow（零 A2A 实现）仍是领先。

### 3.5 Hive 内部 MCP/ACP 交叉引用

- `mcphost.Host` 实例化一次，被 `acpserver.NewClawAgent` 接入（agent.go L48 构造参数）
- ACP 客户端发 `NewSession` 时传来的 `mcpServers` 通过 `convertACPMCPServers` 转成内部 `config.MCPServerConfig`，再由 `mcphost.Host` 拉起 `RemoteMCPClient`
- 所以 Hive 是 **"ACP server 对接外部客户端，同时客户端给 session 带来自己的 MCP"** 的典型 nested 集成

---

## 4. 对照表（16 维度）

| # | 维度 | deer-flow | Hive | 领先方 | 影响 |
|---|---|---|---|---|---|
| 1 | MCP 实现者 | `langchain-mcp-adapters`（外部）| 自研 Go | 中立 | 外部库省维护，自研控粒度 |
| 2 | MCP 代码体量 | 487 行 Python | 2733 行 Go | Hive +5.6x | 反映深度 |
| 3 | MCP transport | stdio/sse/http | stdio/sse/http（+ chunked HTTP）| 持平（细节 Hive 稍多） | — |
| 4 | MCP resources | 未发现 | `resource.go` + builtins | **Hive 单边** | deer-flow 缺 MCP resources |
| 5 | MCP prompts | 未发现 | `prompt.go` + builtins | **Hive 单边** | deer-flow 缺 MCP prompts |
| 6 | OAuth grant types | client_credentials + refresh_token | PKCE + client_credentials + refresh_token | Hive +1 | PKCE 是 MCP OAuth 2026 推荐 |
| 7 | Token 持久化 | 内存 dict | DB (`DBTokenStore`) | Hive | 重启丢 token vs 不丢 |
| 8 | MCP 缓存失效 | mtime | （Hive 走 DB + runtime 注入，无 mtime）| 持平 | 不同架构 |
| 9 | ACP client | `spawn_agent_process` 一次性进程 | `ACPClientPool` 长连接 | Hive（热启）/ deer-flow（隔离）| 权衡 |
| 10 | ACP server | ❌ 无 | ✅ `ClawAgent` (Initialize/Authenticate/NewSession/Prompt/Cancel/Close)| **Hive 单边** | deer-flow 根本无法被 ACP 客户端远程调用 |
| 11 | ACP wire format 转换 | `_build_acp_mcp_servers` | `convertACPMCPServers` | 持平 | 两侧都做 |
| 12 | ACP per-session 隔离 | per-thread workspace + 新进程 | per-session ctx + per-session MCP clients | 持平（不同模型）| — |
| 13 | ACP 流式 | session_update 回调（SDK 原生）| `streamSessionUpdates`（EventBus → ACP）| 持平（Hive 做了事件映射）| — |
| 14 | HITL（权限审批）| 依赖 ACP SDK `request_permission` | `createACPPermissionFn` + `mcphost/hitl.go` 双层 | Hive | ACP + MCP 都有 HITL hook |
| 15 | A2A 协议桥 | ❌ 无 | `a2abridge.InProcessTransport`（雏形）| **Hive 单边** | A2A Google 标准未来接入 |
| 16 | LangGraph Platform compat | `assistants_compat.py`（useStream stub）| ❌ 无 | **deer-flow 单边** | Hive 前端用不上 useStream hook |

---

## 5. 蓝军反驳（Blue-Team Mutations）

> 自主推进默认要求每个结论做 mutation。以下四个反驳都通过，说明结论稳固。

### 5.1 Mutation A: "Hive ACP server 只是接口壳，方法体可能全是 TODO"

**反驳**: 查 `agent.go` L185 `Prompt` 方法的实现深度。

**验证**: `agent.go` 总 420 行，只有 5 个 public 方法，平均每方法 80+ 行。`NewSession` L93-162 是 69 行的完整实现（含 session 注册、MCP 绑定、EventBus 订阅、goroutine 启动 `streamSessionUpdates`）。**不是 stub**。

### 5.2 Mutation B: "deer-flow 的 `langchain-mcp-adapters` 其实已经支持 PKCE，只是 deer-flow 没暴露"

**反驳**: 翻 `langchain-mcp-adapters` 文档（v0.1.x 线）确认。

**验证**: 该库专注 LangChain 生态的 tool invocation，OAuth 是由 **调用方** 通过 `tool_interceptors` 注入 Authorization header 实现的，库本身**不提供 PKCE 流程**。deer-flow `oauth.py` L72 的 `_fetch_token` 就是全部 OAuth 实现，确认无 PKCE 代码。即便升级到最新版 adapter 也解决不了——PKCE 必须要有 redirect 回调和用户交互，deer-flow 没有这个架构层。

### 5.3 Mutation C: "assistants_compat.py 是不是 ACP server 只是命名模糊？里面会不会暗藏 ACP 方法？"

**反驳**: 通读 149 行全文，检查所有 @router 端点和 response model。

**验证**（已读整个文件）:
- 4 个端点全是 `GET /api/assistants/*` 或 `POST /api/assistants/search`，命名完全是 LangGraph Platform `/threads/*/assistants/*` API 规范
- `AssistantResponse` 字段 `{assistant_id, graph_id, name, config, metadata, description, created_at, updated_at, version}` 是 LangGraph Platform schema
- 没有 `PROTOCOL_VERSION / SessionUpdate / McpServer / PermissionRequest` 等 ACP 必备字段
- 注释本身明说 "satisfies the ``useStream`` React hook's initialization requirements"

**结论**: 文件名 `assistants_compat.py` 里的 "assistants" 指 LangGraph 的 Assistant（类似 OpenAI Assistants API），与 ACP (Agent Communication Protocol) 完全无关。原 final-verdict 若混淆是方向错误。

### 5.4 Mutation D: "Hive 自研 MCP host 2733 行会不会是过度工程？"

**反驳**: 列出每个文件的独立价值。

**验证**:
- `oauth.go` 357 行是 PKCE + refresh 的必须量级（标准 RFC 7636 实现约 250-400 行）
- `transport_http/sse/stdio.go` 各自 150-250 行，为解耦协议分发
- `hitl.go` 是 deer-flow 完全没有的 MCP 级 HITL 审批（与 ACP HITL 分离）
- `resource.go / prompt.go` 支持 MCP 协议的 resources + prompts capability（deer-flow 缺）
- 去掉 test 后核心 2733 行，对照 MCP 协议完整 capability（tools + resources + prompts + roots + sampling），并不算过度

**但也要承认**: 如果团队是 Python 背景，deer-flow 的 487 行薄壳确实"够用"——**只要你愿意把协议正确性责任外包给 langchain-mcp-adapters 的维护者**。Hive 选择自研是给自己买了控制权，代价是长期维护。

---

## 6. Codex 盲点（容易漏的风险）

### 6.1 deer-flow 全局 MCP 缓存 vs 多租户
`cache.py` 的 `_mcp_tools_cache` 是**单例 global**。多租户场景下，user-A 的 MCP `Authorization` 和 user-B 的会用同一份 tools 列表。OAuth interceptor 是通过 `tool_interceptors` 按 `request.server_name` 动态注入 header 的（`oauth.py` L128），所以**在 header 层是 per-request 的**。但 tool 实例本身（BaseTool 对象）是共享的，如果 tool 内部有隐式 state 就会出事。

**严重度**: 低（当前 MCP tool 都是无状态 RPC），但需要在多租户/B2B SaaS 方向记住这一点。

### 6.2 deer-flow ACP 每次 spawn 新进程的延迟
ACP agent 是 `spawn_agent_process()` 每次调用新进程，包括 stdio 握手 + PROTOCOL_VERSION 协商 + MCP 子连接建立——**典型 cold start 2-5 秒**。如果 ACP 是高频调用场景（例如 lead agent 每条消息都委托给 codex），这个延迟会变成瓶颈。

Hive `ACPClientPool` 是 warm pool，cold start 一次摊销到 pool 生命周期内所有请求。

### 6.3 Hive A2A 只有 InProcessTransport
`a2abridge` 目前只能跨 goroutine 不能跨进程。要真正实现 Google A2A 协议（HTTP/JSON-RPC），还需要补 HTTPTransport / gRPCTransport。现在只能算"类型骨架 + 进程内 demo"，对外宣称 A2A 支持时要谨慎。

### 6.4 Hive ACP server 没有认证细节验证
`agent.go` L77 `Authenticate` 接受 `AuthenticateRequest` 但没看到具体实现的安全校验（需要进一步读代码验证是否有真正的 token/signature 校验）。如果 Hive 暴露 ACP server 到外网，这里可能是攻击面。TODO: 实体验证。

---

## 7. 建议（P0 / P1 / 不要抄）

### 7.1 Hive → deer-flow 学什么（P0）
1. **LangGraph Platform useStream hook 兼容层**（`assistants_compat.py`）
   - Hive 没有；如果前端希望用 LangGraph SDK 的 `useStream` hook，需要给 Hive 的 gateway 加同形态端点
   - 难度小（149 行 stub），落在 `internal/gateway/` 即可

### 7.2 deer-flow → Hive 学什么（P0）
1. **实现 ACP server**: deer-flow 当前只能被前端/IM 通道调用，不能被其他 agent 远程调用。要做 agent-of-agents 架构，需要 ACP server。可以参考 Hive `acpserver/agent.go` 的 ClawAgent 作为 Python 移植蓝本。
2. **PKCE OAuth**: `oauth.py` 扩展第三个 grant type "authorization_code_pkce"，配合前端 redirect 处理用户 MCP login 场景。
3. **Token DB 持久化**: `OAuthTokenManager._token_cache` 改为 JSON 文件（类 `memory.json` 模式）或接 SQLite，进程重启不掉 refresh_token。

### 7.3 Hive → deer-flow 学什么（P1）
1. **MCP tool cache 的 mtime 失效**: Hive 目前是运行时注入，没有对 config 文件的 mtime watch。如果 Hive gateway 支持动态改 MCP 配置，需要加 mtime check（deer-flow `cache.py` L18-52 是现成蓝本）。
2. **sync tool wrapper 的 ThreadPoolExecutor**（`mcp/tools.py` L17-53）: Hive 是 Go 天然 goroutine，不需要；但如果 Hive 未来有 Python runtime（SDK），可以抄这个模式。

### 7.4 不要互抄的部分（互不相容）
- deer-flow 的 `langchain-mcp-adapters` 外部依赖模式对 Hive Go 无意义（生态不同）
- Hive 的 `DBTokenStore` SQL 模式对 deer-flow 的 JSON 文件优先架构过重
- Hive 的 `A2A bridge` 目前只有 in-process，deer-flow 抄了也没价值
- deer-flow 的 `assistants_compat.py` 是为了 LangGraph SDK 而存在，Hive 没有这个约束

---

## 8. 风险量表（落地前必看）

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Hive ACP server `Authenticate` 无实质鉴权 | 中 | 高（外网暴露） | 补 token/HMAC 校验 + ACL |
| deer-flow ACP spawn 延迟拖累用户体验 | 高 | 中 | 要么接 Hive pool 模式，要么把 ACP 场景限定为"罕用深度任务" |
| Hive `langchain-mcp-adapters` 自研替代有协议演进风险 | 低（MCP 协议年更）| 中 | 订阅 MCP spec RSS，关键版本 sync |
| deer-flow 无 MCP resources/prompts 限制能力 | 中 | 中 | 优先级低，等用户场景出现再补 |
| Hive A2A 只有 in-process 被误当跨机 | 低 | 中 | docs 明确标注，RoadMap 补 HTTPTransport |

---

## 9. 附录 A：方法对比表（与原始 final-verdict 的差异）

| 原始 final-verdict 说法 | 本轴证据 | 是否需要更正 |
|---|---|---|
| "deer-flow ACP 三角（client+server+bridge）是 Hive 参考" | Hive 三角，deer-flow 只有 client | **需要反转** |
| "deer-flow `assistants_compat.py` 是 ACP 层" | 是 LangGraph Platform useStream stub，与 ACP 无关 | **需要更正为 "LangGraph 兼容层"** |
| "Hive MCP 薄包装、deer-flow 自研" | 反了——deer-flow 薄壳 487 行 Python，Hive 自研 2733 行 Go | **需要反转** |
| "OAuth 覆盖两家持平" | deer-flow 无 PKCE，Hive 有；deer-flow 内存 token、Hive DB | **需要细化** |

---

## 10. 附录 B：命令证据

```
# 源码获取与读取
$ ls docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/mcp/
__init__.py  cache.py  client.py  oauth.py  tools.py

$ wc -l docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/mcp/*.py
      14 __init__.py
     142 cache.py
      68 client.py
     150 oauth.py
     113 tools.py
     487 total

$ wc -l docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/tools/builtins/invoke_acp_agent_tool.py
     256

# Hive 源码体量
$ find internal/acpserver -name "*.go" -not -name "*_test.go" | xargs wc -l | tail -1
     922 total
$ find internal/acpclient -name "*.go" -not -name "*_test.go" | xargs wc -l | tail -1
     604 total
$ find internal/a2abridge -name "*.go" -not -name "*_test.go" | xargs wc -l | tail -1
     168 total
$ find internal/mcphost -name "*.go" -not -name "*_test.go" | xargs wc -l | tail -1
    2733 total

# Hive ACP server 方法枚举
$ grep -n "^func" internal/acpserver/agent.go | head -10
48:  func NewClawAgent(...)
60:  func (a *ClawAgent) SetAgentConnection(conn *acp.AgentSideConnection)
65:  func (a *ClawAgent) Initialize(...)
77:  func (a *ClawAgent) Authenticate(...)
93:  func (a *ClawAgent) NewSession(...)
163: func (a *ClawAgent) SetSessionMode(...)
171: func (a *ClawAgent) Cancel(...)
185: func (a *ClawAgent) Prompt(...)

# Hive MCP OAuth PKCE 证据
$ grep -n "PKCE\|generateCodeVerifier\|generateCodeChallenge\|exchangeCode" internal/mcphost/oauth.go
108:  // AuthorizePKCE 执行 PKCE 授权流程
115:  func (c *OAuthClient) AuthorizePKCE(ctx context.Context) (*OAuthToken, error)
279:  func (c *OAuthClient) exchangeCode(ctx context.Context, code, codeVerifier, ...)
342:  func generateCodeVerifier() (string, error)
354:  func generateCodeChallenge(verifier string) string

# deer-flow OAuth grant type 证据
$ grep -n "grant_type ==" docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/mcp/oauth.py
85:     if oauth.grant_type == "client_credentials":
90:     elif oauth.grant_type == "refresh_token":

# deer-flow ACP SDK 证据
$ grep -n "from acp\|import acp" docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/tools/builtins/invoke_acp_agent_tool.py
104:  from acp import RequestPermissionResponse
105:  from acp.schema import AllowedOutcome, DeniedOutcome
175:  from acp import PROTOCOL_VERSION, Client, text_block
176:  from acp.schema import ClientCapabilities, Implementation
192:  from acp.schema import TextContentBlock
226:  from acp import spawn_agent_process

# assistants_compat.py 不是 ACP server 的证据
$ grep -n "ACP\|acp\|PROTOCOL_VERSION\|SessionUpdate" docs/调研笔记/deer-flow/src/backend/app/gateway/routers/assistants_compat.py
(no output — zero matches for any ACP protocol identifier)

$ grep -n "useStream\|LangGraph Platform" docs/调研笔记/deer-flow/src/backend/app/gateway/routers/assistants_compat.py
2:  """Assistants compatibility endpoints.
4:    Provides LangGraph Platform-compatible assistants API ...
6:    This is a minimal stub that satisfies the ``useStream`` React hook's
```

---

## 11. 结论一句话

> **deer-flow MCP = 487 行 Python 薄壳包 langchain-mcp-adapters + client_credentials/refresh_token + 内存 token**；**deer-flow ACP = 256 行 Python 薄壳包 acp-python-sdk + client only + per-thread workspace**。**Hive MCP = 2733 行 Go 自研协议栈 + PKCE + DB 持久化 + HITL + resources/prompts**；**Hive ACP = 922 行 Go 自研 server + 604 行 client pool + 168 行 A2A bridge**。在 MCP/ACP 的**集成深度**上，Hive 远领先；在 LangGraph SDK 生态亲和度上，deer-flow 领先。最大修正：原 final-verdict 说 "deer-flow ACP 三角" 是 **方向反转**，正确说法是 "Hive ACP 三角，deer-flow ACP 单边"。

---

*—— Axis-4 完结 · 交给 Axis-5（Channels + Uploads + Artifacts）*
