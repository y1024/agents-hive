# deer-flow phase2 深度代码调研报告（Codex）
调研日期：2026-04-21  
目标仓库路径（任务给定）：`/Users/guoss/workspace/company/vast/deer-flow`  
实际读取路径：`/Users/guoss/workspace/deer-flow`
## 复现说明
1. 任务给定路径不存在：`ls: /Users/guoss/workspace/company/vast/deer-flow: No such file or directory`。
2. 通过 `find /Users/guoss -maxdepth 5 -type d -name 'deer-flow'` 定位到实际仓库：`/Users/guoss/workspace/deer-flow`。
3. 本报告所有 `file:line_range` 均相对 `/Users/guoss/workspace/deer-flow`。
4. 强制深度实测数字：
5. `backend/app/gateway/routers/*.py` 合计 14 个 router 文件，合计 2888 行。
6. `backend/tests` 顶层 `test_*.py` 共 110 个。
7. `frontend/tests/unit/**/*.test.ts` 共 6 个。
8. `frontend/tests/e2e/**/*.ts` 共 6 个文件、498 行。
9. `frontend/src/core/threads/hooks.ts` 为 679 行。
10. `backend/packages/harness/deerflow/runtime/runs/worker.py` 为 381 行。
11. `backend/packages/harness/deerflow/agents/lead_agent/agent.py` 为 358 行。
12. 本报告引用源文件超过 30 个；读取超过 50 行的文件至少包括：`docker/docker-compose.yaml`、`docker/docker-compose-dev.yaml`、`docker/nginx/nginx.conf`、`config.example.yaml`、`Makefile`、`backend/app/gateway/app.py`、`backend/app/gateway/services.py`、`backend/app/gateway/routers/thread_runs.py`、`backend/packages/harness/deerflow/agents/lead_agent/agent.py`、`backend/packages/harness/deerflow/agents/factory.py`、`backend/packages/harness/deerflow/runtime/runs/worker.py`、`frontend/src/core/threads/hooks.ts`、`frontend/tests/e2e/utils/mock-api.ts`。
---
# A. 仓库顶层与部署形态
## A.1 目录
顶层布局由三个主要 deployment units 和若干运行资产组成：
- `backend/`：Python 3.12 monorepo workspace，包含 FastAPI Gateway、LangGraph harness 包、IM channels、tests。
- `frontend/`：Next.js 16 / React 19 前端，使用 LangGraph SDK 与后端流式交互。
- `docker/`：生产与开发 compose、nginx、provisioner。
- `skills/`：默认 skills，运行时挂载给 sandbox。
- `config.example.yaml` 和 `.env.example`：模型、工具、sandbox、memory、checkpointer、channels、tracing 的配置模板。
- `scripts/` 与 `Makefile`：本地、daemon、Docker、production 启动编排入口。
证据锚点：
- `docker/docker-compose.yaml:24-45`
- `docker/docker-compose.yaml:47-66`
- `docker/docker-compose.yaml:67-117`
- `docker/docker-compose.yaml:118-167`
- `docker/docker-compose.yaml:169-199`
- `Makefile:19-52`
- `backend/pyproject.toml:1-20`
- `frontend/package.json:1-20`
## A.2 职责
服务拓扑图（生产 compose 默认）：
```text
Browser
  |
  | http://localhost:${PORT:-2026}
  v
nginx:2026
  |-- /api/langgraph/* --> langgraph:2024 by default
  |-- /api/models,/api/memory,/api/mcp,/api/skills,/api/threads/* --> gateway:8001
  |-- /api/sandboxes --> provisioner:8002 (optional, request-time DNS)
  `-- /* --> frontend:3000
gateway:8001
  |-- FastAPI routers
  |-- embedded LangGraph runtime singletons in gateway-mode
  |-- channel service startup
  |-- Docker socket / .deer-flow / skills mounts
langgraph:2024
  |-- langgraph dev serving graph lead_agent
  |-- checkpointer from backend/langgraph.json
  |-- Docker socket / .deer-flow / skills mounts
frontend:3000
  |-- Next.js production server
  |-- internal URLs to gateway/langgraph
provisioner:8002
  |-- optional Kubernetes sandbox pod/service lifecycle
```
各服务职责：
- `nginx` 是唯一对外入口，端口默认 `2026`，负责路径路由、CORS、SSE proxy buffering 关闭和长超时。
- `frontend` 是 Next.js server，使用内部 `DEER_FLOW_INTERNAL_GATEWAY_BASE_URL=http://gateway:8001` 和 `DEER_FLOW_INTERNAL_LANGGRAPH_BASE_URL=http://langgraph:2024`。
- `gateway` 是 FastAPI Gateway，启动 `uvicorn app.gateway.app:app`，提供模型、memory、mcp、skills、threads、runs、uploads、agents、channels 等 API。
- `langgraph` 是 LangGraph Server，生产 compose 当前仍运行 `uv run langgraph dev`，监听 2024。
- `provisioner` 可选，负责 Kubernetes 模式 sandbox。
- 外部依赖包括 LLM provider API key、搜索/抓取 API key、IM token、Docker socket、可选 Kubernetes kubeconfig、可选 LangSmith/Langfuse。
## A.3 关键代码
`docker/docker-compose.yaml:24-45` 定义外部入口和默认端口：
```yaml
services:
  # ── Reverse Proxy ──────────────────────────────────────────────────────────
  nginx:
    image: nginx:alpine
    container_name: deer-flow-nginx
    ports:
      - "${PORT:-2026}:2026"
    volumes:
      - ./nginx/nginx.conf:/etc/nginx/nginx.conf.template:ro
    environment:
      - LANGGRAPH_UPSTREAM=${LANGGRAPH_UPSTREAM:-langgraph:2024}
      - LANGGRAPH_REWRITE=${LANGGRAPH_REWRITE:-/}
```
`docker/docker-compose.yaml:47-66` 定义前端 deployment unit：
```yaml
  frontend:
    build:
      context: ../
      dockerfile: frontend/Dockerfile
      target: prod
    container_name: deer-flow-frontend
    environment:
      - BETTER_AUTH_SECRET=${BETTER_AUTH_SECRET}
      - DEER_FLOW_INTERNAL_GATEWAY_BASE_URL=http://gateway:8001
      - DEER_FLOW_INTERNAL_LANGGRAPH_BASE_URL=http://langgraph:2024
```
`docker/docker-compose.yaml:67-117` 定义 Gateway：
```yaml
  gateway:
    build:
      context: ../
      dockerfile: backend/Dockerfile
    container_name: deer-flow-gateway
    command: sh -c "cd backend && PYTHONPATH=. uv run uvicorn app.gateway.app:app --host 0.0.0.0 --port 8001 --workers ${GATEWAY_WORKERS:-4}"
    volumes:
      - ${DEER_FLOW_CONFIG_PATH}:/app/backend/config.yaml:ro
      - ${DEER_FLOW_EXTENSIONS_CONFIG_PATH}:/app/backend/extensions_config.json:ro
      - ../skills:/app/skills:ro
      - ${DEER_FLOW_HOME}:/app/backend/.deer-flow
```
`docker/docker-compose.yaml:118-167` 定义 LangGraph Server：
```yaml
  langgraph:
    build:
      context: ../
      dockerfile: backend/Dockerfile
    container_name: deer-flow-langgraph
    command: sh -c 'cd /app/backend && args="--no-browser --no-reload --host 0.0.0.0 --port 2024 --n-jobs-per-worker $${LANGGRAPH_JOBS_PER_WORKER:-10}" && if [ "$${LANGGRAPH_ALLOW_BLOCKING:-0}" = "1" ]; then args="$$args --allow-blocking"; fi && uv run langgraph dev $$args'
    volumes:
      - ${DEER_FLOW_CONFIG_PATH}:/app/backend/config.yaml:ro
      - ${DEER_FLOW_EXTENSIONS_CONFIG_PATH}:/app/backend/extensions_config.json:ro
      - ${DEER_FLOW_HOME}:/app/backend/.deer-flow
```
`docker/nginx/nginx.conf:59-87` 显示 `/api/langgraph/*` 的路由与 SSE 支持：
```nginx
        location /api/langgraph/ {
            rewrite ^/api/langgraph/(.*) ${LANGGRAPH_REWRITE}$1 break;
            proxy_pass http://langgraph;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_buffering off;
            proxy_cache off;
            proxy_set_header X-Accel-Buffering no;
            proxy_read_timeout 600s;
            chunked_transfer_encoding on;
        }
```
`docker/nginx/nginx.conf:153-161` 将 `/api/threads` 交给 Gateway：
```nginx
        location ~ ^/api/threads {
            proxy_pass http://gateway;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }
```
`docker/nginx/nginx.conf:203-214` 为 provisioner 提供可选代理：
```nginx
        location /api/sandboxes {
            set $provisioner_upstream provisioner:8002;
            proxy_pass http://$provisioner_upstream;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }
```
`config.example.yaml:735-765` 显示默认 checkpointer 为 sqlite：
```yaml
# Supported types:
#   memory   - In-process only. State is lost when the process exits. (default)
#   sqlite   - File-based SQLite persistence. Survives restarts.
#   postgres - PostgreSQL persistence. Suitable for multi-process deployments.
checkpointer:
  type: sqlite
  connection_string: checkpoints.db
```
`.env.example:1-8` 和 `.env.example:29-38` 显示外部 API key 类依赖：
```bash
TAVILY_API_KEY=your-tavily-api-key
JINA_API_KEY=your-jina-api-key
INFOQUEST_API_KEY=your-infoquest-api-key
# LANGSMITH_TRACING=true
# LANGSMITH_ENDPOINT=https://api.smith.langchain.com
# LANGSMITH_API_KEY=your-langsmith-api-key
# LANGSMITH_PROJECT=your-langsmith-project
```
## A.4 证据
部署形态结论：
- 生产 compose 有 5 个服务：`nginx`、`frontend`、`gateway`、`langgraph`、`provisioner`，证据为 `docker/docker-compose.yaml:24-199`。
- 对外端口只有 `nginx` 暴露 `${PORT:-2026}:2026`，证据为 `docker/docker-compose.yaml:29-30`。
- Gateway 内部端口是 `8001`，证据为 `docker/docker-compose.yaml:77`。
- LangGraph 内部端口是 `2024`，证据为 `docker/docker-compose.yaml:130`。
- Frontend 内部被 nginx upstream 指向 `frontend:3000`，证据为 `docker/nginx/nginx.conf:33-35`。
- Provisioner 健康检查端口是 `8002`，证据为 `docker/docker-compose.yaml:195-197`。
- 开发 compose 也包含 provisioner/nginx/frontend/gateway/langgraph，并单独定义 bridge subnet `192.168.200.0/24`，证据为 `docker/docker-compose-dev.yaml:16-253`。
- 本地 Makefile 提供 `dev`、`dev-pro`、`start`、`start-pro`、`up`、`up-pro` 等模式，证据为 `Makefile:119-157` 和 `Makefile:205-215`。
## A.5 风险
- 生产 compose 中 `langgraph` 仍用 `langgraph dev`，代码注释也说“TODO switch to langchain/langgraph-api once a license key is available”，证据为 `docker/docker-compose.yaml:118-130`；风险是 dev server 语义与正式 LangGraph API server 的性能、授权、行为不完全一致。
- Gateway 默认 `--workers ${GATEWAY_WORKERS:-4}`，但 `RunManager` 与 `MemoryStreamBridge` 是进程内状态，证据为 `docker/docker-compose.yaml:77`、`backend/packages/harness/deerflow/runtime/runs/manager.py:40-45`、`backend/packages/harness/deerflow/runtime/stream_bridge/memory.py:25-35`；多 worker 下同一 run 的内存事件与 run registry 不跨进程共享。
- Docker socket 直接挂载到 gateway 和 langgraph，证据为 `docker/docker-compose.yaml:83-84` 与 `docker/docker-compose.yaml:137-138`；sandbox 能力强但 host blast radius 大。
- `BETTER_AUTH_SECRET` 是 frontend 环境变量但未在 compose 内强校验，证据为 `docker/docker-compose.yaml:57-62`；如果默认或缺失会影响 session 安全。
- `agents_api.enabled: false` 明确提示需要受信任 admin 边界，证据为 `config.example.yaml:719-725`；开放后涉及 prompt/config 写入风险。
---
# B. Backend 分层
## B.1 目录
Backend 分为两层：
- `backend/app/`：未发布应用层，包含 FastAPI Gateway 和 IM channel integrations。
- `backend/packages/harness/deerflow/`：可作为 harness 包复用的 agent、middleware、runtime、sandbox、skills、mcp、models、tracing、uploads 等逻辑。
关键目录：
- `backend/app/gateway/app.py`：FastAPI app factory 与 lifespan。
- `backend/app/gateway/deps.py`：请求级单例访问器和 lifespan 初始化。
- `backend/app/gateway/services.py`：run lifecycle service，router 复用。
- `backend/app/gateway/routers/*.py`：HTTP router 与 Pydantic models。
- `backend/packages/harness/deerflow/agents/*`：agent factory、state、middleware。
- `backend/packages/harness/deerflow/runtime/*`：RunManager、StreamBridge、serialization、store。
- `backend/packages/harness/deerflow/config/*`：Pydantic 配置模型。
证据锚点：
- `backend/app/gateway/app.py:7-23`
- `backend/app/gateway/app.py:36-74`
- `backend/app/gateway/app.py:77-221`
- `backend/app/gateway/deps.py:19-36`
- `backend/app/gateway/services.py:1-6`
- `backend/packages/harness/pyproject.toml:1-36`
## B.2 职责
分层职责：
- FastAPI 入口：`create_app()` 负责 OpenAPI tags、health、router include；`lifespan()` 加载 config、初始化 LangGraph runtime、启动/停止 IM channel service。
- Router 层：负责请求/响应模型、HTTP 状态、薄包装。
- Service 层：`services.py` 负责 SSE formatting、stream mode/input normalization、agent factory resolution、run config 构建、run 启动、SSE 消费。
- Runtime 层：`RunManager` 管理 in-memory run registry 和 cancel/rollback 策略；`StreamBridge` 保存事件并支持 Last-Event-ID replay。
- Harness domain 层：agent factory、ThreadState、middleware、tools、sandbox、skills、memory 等。
- Repository/Store 层：不是传统 repository class；threads 的 metadata/state 存在 LangGraph Store，checkpoint 存在 checkpointer。
- DI 形态：不是 FastAPI `Depends` 为主，而是 lifespan 创建单例挂到 `app.state`，router/service 用 `get_*` helper 取。
- Pydantic：Gateway router 内大量 `BaseModel`，config 包内也使用 `BaseModel/Field`。
## B.3 关键代码
`backend/app/gateway/app.py:36-53` 是 FastAPI lifespan：
```python
@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Application lifespan handler."""
    try:
        get_app_config()
        logger.info("Configuration loaded successfully")
    except Exception as e:
        error_msg = f"Failed to load configuration during gateway startup: {e}"
        logger.exception(error_msg)
        raise RuntimeError(error_msg) from e
    config = get_gateway_config()
    logger.info(f"Starting API Gateway on {config.host}:{config.port}")
    async with langgraph_runtime(app):
```
`backend/app/gateway/app.py:168-207` 是 router include 列表：
```python
    app.include_router(models.router)
    app.include_router(mcp.router)
    app.include_router(memory.router)
    app.include_router(skills.router)
    app.include_router(artifacts.router)
    app.include_router(uploads.router)
    app.include_router(threads.router)
    app.include_router(agents.router)
    app.include_router(suggestions.router)
    app.include_router(channels.router)
    app.include_router(assistants_compat.router)
    app.include_router(thread_runs.router)
    app.include_router(runs.router)
```
`backend/app/gateway/deps.py:19-36` 是单例初始化：
```python
@asynccontextmanager
async def langgraph_runtime(app: FastAPI) -> AsyncGenerator[None, None]:
    from deerflow.agents.checkpointer.async_provider import make_checkpointer
    from deerflow.runtime import make_store, make_stream_bridge
    async with AsyncExitStack() as stack:
        app.state.stream_bridge = await stack.enter_async_context(make_stream_bridge())
        app.state.checkpointer = await stack.enter_async_context(make_checkpointer())
        app.state.store = await stack.enter_async_context(make_store())
        app.state.run_manager = RunManager()
        yield
```
`backend/app/gateway/services.py:42-55` 是 SSE frame 格式：
```python
def format_sse(event: str, data: Any, *, event_id: str | None = None) -> str:
    payload = json.dumps(data, default=str, ensure_ascii=False)
    parts = [f"event: {event}", f"data: {payload}"]
    if event_id:
        parts.append(f"id: {event_id}")
    parts.append("")
    parts.append("")
    return "\n".join(parts)
```
`backend/app/gateway/routers/thread_runs.py:35-55` 是 run request Pydantic 模型：
```python
class RunCreateRequest(BaseModel):
    assistant_id: str | None = Field(default=None, description="Agent / assistant to use")
    input: dict[str, Any] | None = Field(default=None, description="Graph input (e.g. {messages: [...]})")
    command: dict[str, Any] | None = Field(default=None, description="LangGraph Command")
    metadata: dict[str, Any] | None = Field(default=None, description="Run metadata")
    config: dict[str, Any] | None = Field(default=None, description="RunnableConfig overrides")
    context: dict[str, Any] | None = Field(default=None, description="DeerFlow context overrides (model_name, thinking_enabled, etc.)")
    stream_mode: list[str] | str | None = Field(default=None, description="Stream mode(s)")
```
`backend/packages/harness/deerflow/config/app_config.py:34-58` 证明 config 层是 Pydantic 模型：
```python
class AppConfig(BaseModel):
    model_config = ConfigDict(extra="ignore")
    config_version: int = Field(default=0, description="Config schema version")
    log_level: str = Field(default="info", description="Log level")
    models: list[ModelConfig] = Field(default_factory=list, description="Available model configurations")
    tools: ToolConfig = Field(default_factory=ToolConfig, description="Tool configuration")
```
## B.4 证据
主要 API 路由表（从 `rg '@router\.' backend/app/gateway/routers/*.py` 实测）：
| 模块 | prefix/tags | endpoints 数 | 主要 endpoint |
|---|---:|---:|---|
| `models.py` | `/api`, `models` | 2 | `GET /api/models`, `GET /api/models/token-usage` |
| `mcp.py` | `/api`, `mcp` | 2 | `GET /api/mcp`, `PUT /api/mcp` |
| `memory.py` | `/api`, `memory` | 10 | `GET/POST/DELETE /api/memory/*`、facts CRUD/status |
| `skills.py` | `/api`, `skills` | 10 | global skills, custom skills CRUD/history/rollback |
| `artifacts.py` | `/api`, `artifacts` | 1 | thread artifacts |
| `uploads.py` | `/api/threads/{thread_id}/uploads` | 3 | upload/list/delete |
| `threads.py` | `/api/threads`, `threads` | 8 | create/search/get/patch/delete/state/history |
| `agents.py` | `/api`, `agents` | 8 | list/get/create/update/delete/user profile |
| `suggestions.py` | `/api`, `suggestions` | 1 | suggestions generation |
| `channels.py` | `/api/channels`, `channels` | 2 | status/restart |
| `assistants_compat.py` | `/api/assistants`, `assistants-compat` | 4 | search/get/graph/schemas |
| `thread_runs.py` | `/api/threads`, `runs` | 8 | create/stream/wait/list/get/cancel/join/stream-existing |
| `runs.py` | `/api/runs`, `runs` | 2 | stateless stream/wait |
证据：
- Router include：`backend/app/gateway/app.py:168-207`。
- `thread_runs` Pydantic models 与 endpoint：`backend/app/gateway/routers/thread_runs.py:35-267`。
- `runs` stateless endpoints：`backend/app/gateway/routers/runs.py:26-87`。
- `deps` app.state 单例：`backend/app/gateway/deps.py:19-70`。
- `services.start_run` 做 run lifecycle 编排：`backend/app/gateway/services.py:239-335`。
- `RunManager` 为 in-memory registry：`backend/packages/harness/deerflow/runtime/runs/manager.py:40-45`。
## B.5 风险
- Router 层和 service 层分离只在 runs 路径明显；`memory.py`、`threads.py`、`skills.py` 等 router 文件各自数百行，仍承载业务逻辑，证据为 `wc -l`：`threads.py` 682 行、`memory.py` 353 行、`skills.py` 362 行。
- DI 不使用 FastAPI `Depends`，主要通过 `app.state`，证据为 `backend/app/gateway/deps.py:44-70`；这降低测试替换粒度，但简化 lifecycle。
- `normalize_input()` 对非 human/AI/tool 消息有 TODO，只把其他 role 转为 `HumanMessage`，证据为 `backend/app/gateway/services.py:75-94`；这会影响 system/tool message 的外部 API 兼容性。
- `RunCreateRequest.command/checkpoint/checkpoint_id` 被声明但 `start_run` 当前主要使用 `body.input/config/context`，证据为 `backend/app/gateway/routers/thread_runs.py:35-55` 和 `backend/app/gateway/services.py:283-325`；resume 命令语义并未完整落地。
---
# C. Middleware 栈
## C.1 目录
中间件相关文件集中在：
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py`
- `backend/packages/harness/deerflow/agents/factory.py`
- `backend/packages/harness/deerflow/agents/features.py`
- `backend/packages/harness/deerflow/agents/middlewares/*.py`
- `backend/packages/harness/deerflow/sandbox/middleware.py`
- `backend/packages/harness/deerflow/guardrails/middleware.py`
至少 3 个源文件证据：
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py:205-277`
- `backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:68-135`
- `backend/packages/harness/deerflow/agents/factory.py:155-291`
- `backend/packages/harness/deerflow/agents/features.py:14-62`
- `backend/packages/harness/deerflow/sandbox/middleware.py:21-83`
## C.2 职责
应用主 agent 的真实顺序由 `_build_middlewares()` 与 `build_lead_runtime_middlewares()` 共同决定：
1. `ThreadDataMiddleware`，always-on，lazy init。
2. `UploadsMiddleware`，lead agent always-on。
3. `SandboxMiddleware`，always-on，lazy init。
4. `DanglingToolCallMiddleware`，lead/subagent always-on。
5. `LLMErrorHandlingMiddleware`，always-on。
6. `GuardrailMiddleware`，guardrails config opt-in。
7. `SandboxAuditMiddleware`，always-on。
8. `ToolErrorHandlingMiddleware`，always-on。
9. `DeerFlowSummarizationMiddleware`，config opt-in，默认 config.example enabled。
10. `TodoMiddleware`，runtime `is_plan_mode` opt-in。
11. `TokenUsageMiddleware`，config `token_usage.enabled` opt-in，默认 false。
12. `TitleMiddleware`，always-on。
13. `MemoryMiddleware`，always appended，但内部 `memory.enabled` 决定是否工作。
14. `ViewImageMiddleware`，当前模型 `supports_vision` opt-in。
15. `DeferredToolFilterMiddleware`，`tool_search.enabled` opt-in。
16. `SubagentLimitMiddleware`，runtime `subagent_enabled` opt-in。
17. `LoopDetectionMiddleware`，always-on。
18. custom middlewares，插入在 Clarification 前。
19. `ClarificationMiddleware`，always last。
顺序不变量：
- `ThreadDataMiddleware` 必须早于 `SandboxMiddleware`，证据注释在 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:205-206`。
- `ClarificationMiddleware` 必须最后，证据为 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:275-276`。
- SDK factory 的 extra middleware 插入后会强制把 `ClarificationMiddleware` 移回尾部，证据为 `backend/packages/harness/deerflow/agents/factory.py:282-290`。
- `ToolErrorHandlingMiddleware` 保留 `GraphBubbleUp`，避免吞掉 LangGraph 控制流，证据为 `backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:43-65`。
## C.3 关键代码
`backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:79-95` 是基础链：
```python
    middlewares: list[AgentMiddleware] = [
        ThreadDataMiddleware(lazy_init=lazy_init),
        SandboxMiddleware(lazy_init=lazy_init),
    ]
    if include_uploads:
        from deerflow.agents.middlewares.uploads_middleware import UploadsMiddleware
        middlewares.insert(1, UploadsMiddleware())
    if include_dangling_tool_call_patch:
        from deerflow.agents.middlewares.dangling_tool_call_middleware import DanglingToolCallMiddleware
        middlewares.append(DanglingToolCallMiddleware())
    middlewares.append(LLMErrorHandlingMiddleware())
```
`backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:96-125` 是 guardrail、audit、tool error：
```python
    guardrails_config = get_guardrails_config()
    if guardrails_config.enabled and guardrails_config.provider:
        from deerflow.guardrails.middleware import GuardrailMiddleware
        from deerflow.reflection import resolve_variable
        provider_cls = resolve_variable(guardrails_config.provider.use)
        provider_kwargs = dict(guardrails_config.provider.config) if guardrails_config.provider.config else {}
        provider = provider_cls(**provider_kwargs)
        middlewares.append(GuardrailMiddleware(provider, fail_closed=guardrails_config.fail_closed, passport=guardrails_config.passport))
    middlewares.append(SandboxAuditMiddleware())
    middlewares.append(ToolErrorHandlingMiddleware())
```
`backend/packages/harness/deerflow/agents/lead_agent/agent.py:226-277` 是主链后半段：
```python
    middlewares = build_lead_runtime_middlewares(lazy_init=True)
    summarization_middleware = _create_summarization_middleware()
    if summarization_middleware is not None:
        middlewares.append(summarization_middleware)
    is_plan_mode = config.get("configurable", {}).get("is_plan_mode", False)
    todo_list_middleware = _create_todo_list_middleware(is_plan_mode)
    if todo_list_middleware is not None:
        middlewares.append(todo_list_middleware)
    if get_app_config().token_usage.enabled:
        middlewares.append(TokenUsageMiddleware())
    middlewares.append(TitleMiddleware())
    middlewares.append(MemoryMiddleware(agent_name=agent_name))
```
`backend/packages/harness/deerflow/agents/lead_agent/agent.py:249-276` 继续追加 opt-in 与 last invariant：
```python
    if model_config is not None and model_config.supports_vision:
        middlewares.append(ViewImageMiddleware())
    if app_config.tool_search.enabled:
        from deerflow.agents.middlewares.deferred_tool_filter_middleware import DeferredToolFilterMiddleware
        middlewares.append(DeferredToolFilterMiddleware())
    if subagent_enabled:
        middlewares.append(SubagentLimitMiddleware(max_concurrent=max_concurrent_subagents))
    middlewares.append(LoopDetectionMiddleware())
    if custom_middlewares:
        middlewares.extend(custom_middlewares)
    middlewares.append(ClarificationMiddleware())
```
`backend/packages/harness/deerflow/agents/middlewares/clarification_middleware.py:153-156` 将 clarification 变成 `Command(goto=END)`：
```python
        return Command(
            update={"messages": [tool_message]},
            goto=END,
        )
```
`backend/packages/harness/deerflow/sandbox/middleware.py:51-65` 显示 lazy/eager hook：
```python
    def before_agent(self, state: SandboxMiddlewareState, runtime: Runtime) -> dict | None:
        if self._lazy_init:
            return super().before_agent(state, runtime)
        if "sandbox" not in state or state["sandbox"] is None:
            thread_id = (runtime.context or {}).get("thread_id")
            if thread_id is None:
                return super().before_agent(state, runtime)
            sandbox_id = self._acquire_sandbox(thread_id)
            return {"sandbox": {"sandbox_id": sandbox_id}}
```
## C.4 证据
Hook coverage：
| Middleware | hook | evidence |
|---|---|---|
| `ToolErrorHandlingMiddleware` | `wrap_tool_call`, `awrap_tool_call` | `backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:37-65` |
| `ClarificationMiddleware` | `wrap_tool_call`, `awrap_tool_call` | `backend/packages/harness/deerflow/agents/middlewares/clarification_middleware.py:158-200` |
| `MemoryMiddleware` | `after_agent` | `backend/packages/harness/deerflow/agents/middlewares/memory_middleware.py:45-98` |
| `SandboxMiddleware` | `before_agent`, `after_agent` | `backend/packages/harness/deerflow/sandbox/middleware.py:51-83` |
| `TitleMiddleware` | after-agent title generation | `backend/packages/harness/deerflow/agents/middlewares/title_middleware.py` 已读符号定位，router/store sync 见 `backend/app/gateway/services.py:190-236` |
| `LoopDetectionMiddleware` | tool-loop detection | `backend/packages/harness/deerflow/agents/middlewares/loop_detection_middleware.py` 由 `_build_middlewares` always append，证据 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:268-269` |
Opt-in / always-on：
- Always-on: ThreadData、Uploads、Sandbox、DanglingToolCall、LLMErrorHandling、SandboxAudit、ToolErrorHandling、Title、Memory append、LoopDetection、Clarification。
- Config opt-in: Guardrail、Summarization、TokenUsage、ViewImage、DeferredToolFilter。
- Runtime opt-in: Todo via `is_plan_mode`，SubagentLimit via `subagent_enabled`。
- SDK factory opt-in: `RuntimeFeatures` 中 `memory=False`、`auto_title=False`、`subagent=False`、`vision=False` 默认关闭；这和 app factory 默认并不完全一致，证据为 `backend/packages/harness/deerflow/agents/features.py:14-34`。
## C.5 风险
- `SandboxMiddleware` docstring 说 lazy 时 sandbox 不在 after_agent 释放，但代码 `after_agent` 只要 state 有 sandbox 就 release，证据为 `backend/packages/harness/deerflow/sandbox/middleware.py:21-30` 与 `backend/packages/harness/deerflow/sandbox/middleware.py:67-80`；需要结合 provider 的 ref-count/reuse 语义确认是否矛盾。
- `MemoryMiddleware` 总是 append，但内部读全局 config 后可能 no-op，证据为 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:246-247` 与 `backend/packages/harness/deerflow/agents/middlewares/memory_middleware.py:56-58`；调试“为什么有 middleware 但没记忆”时容易误判。
- `create_deerflow_agent` 的 feature 默认与 app factory 不一致，SDK 默认只有 sandbox + dangling + tool error + clarification 等较小链，证据为 `backend/packages/harness/deerflow/agents/features.py:27-33` 与 `backend/packages/harness/deerflow/agents/factory.py:155-291`；外部用户以为和产品 UI 一致会踩坑。
- 顺序强依赖 append，缺少自动化 snapshot 输出；虽然已有单测文件如 `backend/tests/test_create_deerflow_agent.py`、`backend/tests/test_tool_error_handling_middleware.py`，但生产链具体 opt-in 组合仍可能配置敏感。
---
# D. LangGraph 编排
## D.1 目录
LangGraph 编排入口与状态：
- `backend/langgraph.json`
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py`
- `backend/packages/harness/deerflow/agents/thread_state.py`
- `backend/packages/harness/deerflow/agents/checkpointer/async_provider.py`
- `backend/packages/harness/deerflow/agents/checkpointer/provider.py`
- `backend/packages/harness/deerflow/runtime/runs/worker.py`
- `backend/packages/harness/deerflow/runtime/runs/manager.py`
- `backend/packages/harness/deerflow/runtime/store/async_provider.py`
## D.2 职责
LangGraph 编排有两条入口：
- LangGraph Server：`backend/langgraph.json` 声明 graph `lead_agent` 指向 `deerflow.agents:make_lead_agent`，并声明 checkpointer provider。
- Gateway embedded runtime：Gateway `start_run()` 用 `resolve_agent_factory()` 返回 `make_lead_agent`，在 background `run_agent()` 中创建 graph，手动挂载 checkpointer/store/runtime，再调用 `agent.astream()`。
状态：
- `ThreadState` 继承 LangChain `AgentState`。
- `ThreadState` 扩展 sandbox、thread_data、title、artifacts、todos、uploaded_files、viewed_images。
- `artifacts` reducer 去重合并。
- `viewed_images` reducer 支持空 dict 清空。
Checkpointer/store：
- 支持 memory/sqlite/postgres。
- async provider 给 FastAPI lifespan 使用。
- sync provider 给 graph 编译/CLI/tests 使用。
- store 与 checkpointer 共用 `checkpointer` config section，保证 persistence backend 一致。
Interrupt/resume：
- `ClarificationMiddleware` 通过 `Command(goto=END)` 停止当前执行。
- HTTP API 暴露 run cancel action `interrupt|rollback`。
- `run_agent()` 支持 `interrupt_before` 与 `interrupt_after` 设置到 agent。
- `RunManager.cancel()` 设置 `abort_event` 并取消 asyncio task。
- rollback 会恢复 pre-run checkpoint snapshot。
- “resume from checkpoint/command” 字段在 Pydantic 模型中存在，但 Gateway `start_run()` 未显式将 `body.command` 传给 `agent.astream()`，当前真正落地的是 interrupt/cancel/rollback，而不是完整 LangGraph Command resume。
## D.3 关键代码
`backend/langgraph.json:8-13` 声明 graph 和 checkpointer：
```json
  "graphs": {
    "lead_agent": "deerflow.agents:make_lead_agent"
  },
  "checkpointer": {
    "path": "./packages/harness/deerflow/agents/checkpointer/async_provider.py:make_checkpointer"
  }
```
`backend/packages/harness/deerflow/agents/thread_state.py:21-28` 是 artifacts reducer：
```python
def merge_artifacts(existing: list[str] | None, new: list[str] | None) -> list[str]:
    """Reducer for artifacts list - merges and deduplicates artifacts."""
    if existing is None:
        return new or []
    if new is None:
        return existing
    return list(dict.fromkeys(existing + new))
```
`backend/packages/harness/deerflow/agents/thread_state.py:31-45` 是 viewed images reducer：
```python
def merge_viewed_images(existing: dict[str, ViewedImageData] | None, new: dict[str, ViewedImageData] | None) -> dict[str, ViewedImageData]:
    if existing is None:
        return new or {}
    if new is None:
        return existing
    if len(new) == 0:
        return {}
    return {**existing, **new}
```
`backend/packages/harness/deerflow/agents/thread_state.py:48-55` 是 ThreadState：
```python
class ThreadState(AgentState):
    sandbox: NotRequired[SandboxState | None]
    thread_data: NotRequired[ThreadDataState | None]
    title: NotRequired[str | None]
    artifacts: Annotated[list[str], merge_artifacts]
    todos: NotRequired[list | None]
    uploaded_files: NotRequired[list[dict] | None]
    viewed_images: Annotated[dict[str, ViewedImageData], merge_viewed_images]
```
`backend/packages/harness/deerflow/agents/lead_agent/agent.py:350-358` 构建 graph：
```python
    return create_agent(
        model=create_chat_model(name=model_name, thinking_enabled=thinking_enabled, reasoning_effort=reasoning_effort),
        tools=get_available_tools(model_name=model_name, groups=agent_config.tool_groups if agent_config else None, subagent_enabled=subagent_enabled),
        middleware=_build_middlewares(config, model_name=model_name, agent_name=agent_name),
        system_prompt=apply_prompt_template(
            subagent_enabled=subagent_enabled, max_concurrent_subagents=max_concurrent_subagents, agent_name=agent_name, available_skills=set(agent_config.skills) if agent_config and agent_config.skills is not None else None
        ),
        state_schema=ThreadState,
    )
```
`backend/packages/harness/deerflow/runtime/runs/worker.py:103-120` 在 embedded runtime 中手动注入 Runtime/checkpointer/store：
```python
        runtime = Runtime(context={"thread_id": thread_id}, store=store)
        if "context" in config and isinstance(config["context"], dict):
            config["context"].setdefault("thread_id", thread_id)
        config.setdefault("configurable", {})["__pregel_runtime"] = runtime
        runnable_config = RunnableConfig(**config)
        agent = agent_factory(config=runnable_config)
        if checkpointer is not None:
            agent.checkpointer = checkpointer
        if store is not None:
            agent.store = store
```
`backend/packages/harness/deerflow/runtime/runs/worker.py:122-150` 处理 interrupt nodes 与 stream modes：
```python
        if interrupt_before:
            agent.interrupt_before_nodes = interrupt_before
        if interrupt_after:
            agent.interrupt_after_nodes = interrupt_after
        lg_modes: list[str] = []
        for m in requested_modes:
            if m == "messages-tuple":
                lg_modes.append("messages")
            elif m == "events":
                continue
            elif m in _VALID_LG_MODES:
                lg_modes.append(m)
```
`backend/packages/harness/deerflow/agents/checkpointer/async_provider.py:42-78` 是 async checkpointer factory：
```python
@contextlib.asynccontextmanager
async def _async_checkpointer(config) -> AsyncIterator[Checkpointer]:
    if config.type == "memory":
        from langgraph.checkpoint.memory import InMemorySaver
        yield InMemorySaver()
        return
    if config.type == "sqlite":
        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
        conn_str = resolve_sqlite_conn_str(config.connection_string or "store.db")
        async with AsyncSqliteSaver.from_conn_string(conn_str) as saver:
            await saver.setup()
            yield saver
```
`backend/packages/harness/deerflow/runtime/store/async_provider.py:36-63` mirror checkpointer backend：
```python
@contextlib.asynccontextmanager
async def _async_store(config) -> AsyncIterator[BaseStore]:
    if config.type == "memory":
        from langgraph.store.memory import InMemoryStore
        yield InMemoryStore()
        return
    if config.type == "sqlite":
        from langgraph.store.sqlite.aio import AsyncSqliteStore
        conn_str = resolve_sqlite_conn_str(config.connection_string or "store.db")
        async with AsyncSqliteStore.from_conn_string(conn_str) as store:
            await store.setup()
            yield store
```
## D.4 证据
Stream 执行事实：
- `run_agent()` 使用 `agent.astream()`，不是 `StateGraph` 手写节点，证据为 `backend/packages/harness/deerflow/runtime/runs/worker.py:154-181`。
- `events` mode 被显式跳过，日志说明 Python public API 限制，证据为 `backend/packages/harness/deerflow/runtime/runs/worker.py:10-13` 和 `backend/packages/harness/deerflow/runtime/runs/worker.py:60-65`。
- 多 mode 下会解包 `(mode, chunk)`，证据为 `backend/packages/harness/deerflow/runtime/runs/worker.py:358-381`。
- rollback 捕获 pre-run checkpoint，证据为 `backend/packages/harness/deerflow/runtime/runs/worker.py:71-87`。
- rollback 恢复 checkpoint 与 pending writes，证据为 `backend/packages/harness/deerflow/runtime/runs/worker.py:259-344`。
- `RunManager.create_or_reject()` 支持 `reject|interrupt|rollback`，不支持 enqueue，证据为 `backend/packages/harness/deerflow/runtime/runs/manager.py:126-189`。
- `RunCreateRequest.multitask_strategy` 声明包含 `enqueue`，但 `RunManager` 支持列表没有 enqueue，证据为 `backend/app/gateway/routers/thread_runs.py:52` 与 `backend/packages/harness/deerflow/runtime/runs/manager.py:148-152`。
## D.5 风险
- `RunCreateRequest.command`、`checkpoint_id`、`checkpoint` 看似为 resume 预留，但 `start_run()` 未用 command 作为 graph input，证据为 `backend/app/gateway/routers/thread_runs.py:38-44` 与 `backend/app/gateway/services.py:311-325`；如果前端/SDK 期望 LangGraph Command resume，当前可能不完整。
- `events` stream mode 前端允许传入，但 Gateway run worker 明确跳过，证据为 `frontend/src/core/api/stream-mode.ts:1-11` 与 `backend/packages/harness/deerflow/runtime/runs/worker.py:60-65`；这会造成客户端以为支持 events，服务端静默不返回 events。
- `multitask_strategy="enqueue"` 在 Pydantic 层允许，但 runtime 抛 501，证据为 `backend/app/gateway/routers/thread_runs.py:52` 与 `backend/packages/harness/deerflow/runtime/runs/manager.py:148-152`。
- SQLite checkpointer 默认适合单进程/文件持久化，生产多 worker/multi process 场景应使用 postgres，证据为 `config.example.yaml:744-749`。
---
# E. Frontend 架构
## E.1 目录
Frontend 使用 Next.js App Router，核心文件：
- `frontend/src/app/layout.tsx`
- `frontend/src/app/page.tsx`
- `frontend/src/app/workspace/layout.tsx`
- `frontend/src/app/workspace/page.tsx`
- `frontend/src/app/workspace/chats/page.tsx`
- `frontend/src/components/workspace/chats/use-thread-chat.ts`
- `frontend/src/core/threads/hooks.ts`
- `frontend/src/core/api/api-client.ts`
- `frontend/src/core/api/stream-mode.ts`
- `frontend/src/components/query-client-provider.tsx`
- `frontend/src/core/settings/*`
- `frontend/src/core/config/index.ts`
证据锚点：
- `frontend/package.json:21-93`
- `frontend/src/app/workspace/layout.tsx:1-35`
- `frontend/src/core/threads/hooks.ts:1-679`
- `frontend/src/core/api/api-client.ts:1-44`
- `frontend/src/core/api/stream-mode.ts:1-68`
## E.2 职责
前端职责分层：
- App Router：landing、docs/blog、workspace。
- Workspace layout：提供 `QueryClientProvider`、sidebar、command palette、toaster。
- React Query：查询 thread list、mutations、cache update/invalidation。
- LangGraph SDK：`useStream<AgentThreadState>` 负责流式 run、state history、reconnect。
- API client compatibility layer：包装 LangGraph SDK `runs.stream` 和 `runs.joinStream`，过滤不支持 stream mode。
- Local settings：持久化 UI context（mode、thinking、subagent、agent_name 等）。
- SSE：前端没有手写 `EventSource`，流式交互由 `@langchain/langgraph-sdk/react` 的 `useStream` 执行。
- Re-connect：通过 `reconnectOnMount` + `sessionStorage` metadata storage 交给 SDK 实现。
- 错误处理：stream error 通过 toast；未找到 Next.js `error.tsx` 或 `global-error.tsx` 文件。
## E.3 关键代码
`frontend/src/app/workspace/layout.tsx:25-33` 是 workspace provider 栈：
```tsx
  return (
    <QueryClientProvider>
      <SidebarProvider className="h-screen" defaultOpen={initialSidebarOpen}>
        <WorkspaceSidebar />
        <SidebarInset className="min-w-0">{children}</SidebarInset>
      </SidebarProvider>
      <CommandPalette />
      <Toaster position="top-center" />
    </QueryClientProvider>
  );
```
`frontend/src/components/query-client-provider.tsx:3-19` 创建全局 QueryClient：
```tsx
import {
  QueryClient,
  QueryClientProvider as TanStackQueryClientProvider,
} from "@tanstack/react-query";
const queryClient = new QueryClient();
export function QueryClientProvider({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <TanStackQueryClientProvider client={queryClient}>
      {children}
    </TanStackQueryClientProvider>
  );
}
```
`frontend/src/core/api/api-client.ts:9-30` 包装 LangGraph SDK：
```ts
function createCompatibleClient(isMock?: boolean): LangGraphClient {
  const client = new LangGraphClient({
    apiUrl: getLangGraphBaseURL(isMock),
  });
  const originalRunStream = client.runs.stream.bind(client.runs);
  client.runs.stream = ((threadId, assistantId, payload) =>
    originalRunStream(threadId, assistantId, sanitizeRunStreamOptions(payload))) as typeof client.runs.stream;
  const originalJoinStream = client.runs.joinStream.bind(client.runs);
  client.runs.joinStream = ((threadId, runId, options) =>
    originalJoinStream(threadId, runId, sanitizeRunStreamOptions(options))) as typeof client.runs.joinStream;
```
`frontend/src/core/threads/hooks.ts:204-222` 是 `useStream` 和 reconnect：
```ts
  const thread = useStream<AgentThreadState>({
    client: getAPIClient(isMock),
    assistantId: "lead_agent",
    threadId: onStreamThreadId,
    reconnectOnMount: runMetadataStorageRef.current
      ? () => runMetadataStorageRef.current!
      : false,
    fetchStateHistory: { limit: 1 },
    onCreated(meta) {
      handleStreamStart(meta.thread_id);
      setOnStreamThreadId(meta.thread_id);
```
`frontend/src/core/threads/hooks.ts:83-111` 是 sessionStorage run id 标准化：
```ts
function getRunMetadataStorage(): {
  getItem(key: `lg:stream:${string}`): string | null;
  setItem(key: `lg:stream:${string}`, value: string): void;
  removeItem(key: `lg:stream:${string}`): void;
} {
  return {
    getItem(key) {
      const normalized = normalizeStoredRunId(window.sessionStorage.getItem(key));
      if (normalized) {
        window.sessionStorage.setItem(key, normalized);
        return normalized;
      }
```
`frontend/src/core/threads/hooks.ts:463-508` 发送消息并携带 context：
```ts
        await thread.submit(
          {
            messages: [
              {
                type: "human",
                content: [{ type: "text", text }],
                additional_kwargs: {
                  ...options?.additionalKwargs,
                  ...(filesForSubmit.length > 0 ? { files: filesForSubmit } : {}),
                },
              },
            ],
          },
          {
            threadId: threadId,
            streamSubgraphs: true,
            streamResumable: true,
            config: { recursion_limit: 1000 },
            context: {
```
`frontend/src/core/api/stream-mode.ts:1-11` 定义前端允许的 stream modes：
```ts
const SUPPORTED_RUN_STREAM_MODES = new Set([
  "values",
  "messages",
  "messages-tuple",
  "updates",
  "events",
  "debug",
  "tasks",
  "checkpoints",
  "custom",
] as const);
```
## E.4 证据
Routing 证据：
- Workspace layout 使用 server component 读取 cookie：`frontend/src/app/workspace/layout.tsx:1-23`。
- `useThreadChat()` 从 route params 读取 `thread_id`，处理 `/new`，证据为 `frontend/src/components/workspace/chats/use-thread-chat.ts:8-39`。
- Next.js 版本是 `^16.1.7`，React 是 `^19.0.0`，证据为 `frontend/package.json:72-79`。
- LangGraph SDK 依赖是 `@langchain/langgraph-sdk`，证据为 `frontend/package.json:29-30`。
SSE/reconnect 证据：
- 没有手写 EventSource：已 grep `EventSource|new EventSource`，在 `frontend/src` 未找到实现；仅依赖 SDK。
- 后端支持 `Last-Event-ID` replay，证据为 `backend/app/gateway/services.py:350-364` 和 `backend/packages/harness/deerflow/runtime/stream_bridge/memory.py:51-64`。
- 前端启用 `streamResumable: true`，证据为 `frontend/src/core/threads/hooks.ts:483-487`。
- 前端 reconnectOnMount 用 sessionStorage，证据为 `frontend/src/core/threads/hooks.ts:83-111` 与 `frontend/src/core/threads/hooks.ts:204-210`。
状态管理证据：
- React Query 是主要 server-state 管理，证据为 `frontend/src/core/threads/hooks.ts:4`、`frontend/src/core/threads/hooks.ts:533-679`。
- 未找到 `zustand` 源码使用：grep `zustand|create\(` 仅在 lockfile 和 motion.create 出现，无 store 创建。
- Optimistic messages 使用本地 `useState`，证据为 `frontend/src/core/threads/hooks.ts:299-324` 和 `frontend/src/core/threads/hooks.ts:360-378`。
错误边界证据：
- 未找到 `frontend/src/app/**/error.tsx` 或 `global-error.tsx`：`find frontend/src/app -name error.tsx -o -name global-error.tsx` 输出为空。
- Stream 错误通过 toast：`frontend/src/core/threads/hooks.ts:289-292`。
## E.5 风险
- 前端 stream mode 白名单包含 `events`，但 Gateway 跳过 `events`，证据为 `frontend/src/core/api/stream-mode.ts:1-11` 与 `backend/packages/harness/deerflow/runtime/runs/worker.py:60-65`；UI/SDK 可能以为有 LangChain events。
- 没有 Next.js route-level error boundary，证据为 grep 未找到 `error.tsx/global-error.tsx`；页面级异常可能只走 Next 默认错误页。
- `QueryClient` 没有默认 retry/staleTime 配置，证据为 `frontend/src/components/query-client-provider.tsx:3-19`；所有查询继承 TanStack 默认策略，可能导致错误状态和 refetch 行为不够可控。
- Reconnect 依赖 SDK 内部协议和 sessionStorage 中 `lg:stream:*` key；本地只做 run id 标准化，证据为 `frontend/src/core/threads/hooks.ts:39-111`；如果 SDK 内容格式改变需要回归。
---
# F. Observability / Tracing
## F.1 目录
Observability 相关文件：
- `backend/packages/harness/deerflow/config/tracing_config.py`
- `backend/packages/harness/deerflow/tracing/factory.py`
- `backend/tests/test_tracing_config.py`
- `backend/tests/test_tracing_factory.py`
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py`
- `backend/packages/harness/deerflow/tools/builtins/task_tool.py`
- `backend/packages/harness/deerflow/subagents/executor.py`
- `.env.example`
- `docker/docker-compose.yaml`
- `docker/nginx/nginx.conf`
- `backend/app/gateway/app.py`
- `backend/app/gateway/services.py`
## F.2 职责
Observability 能力分层：
- Logging：全局 `logging.basicConfig`，gateway/run manager/worker/middleware/subagent 多处 logger。
- LangSmith：环境变量启用，构建 `LangChainTracer(project_name=...)`。
- Langfuse：环境变量启用，初始化 `Langfuse(...)` 并返回 LangChain callback handler。
- Run metadata：`make_lead_agent()` 将 agent/model/thinking/mode/tool_groups 写入 `config["metadata"]`，供 LangSmith trace tagging。
- Subagent trace id：`task_tool` 从 metadata 取 `trace_id` 或生成短 UUID，传给 `SubagentExecutor`，用于日志 `[trace=...]`。
- Gateway SSE metadata：每个 run publish `run_id`、`thread_id`。
- HTTP trace propagation：未找到 `traceparent/tracestate/X-Request-ID/X-Trace` 相关传播。
- OTel：依赖锁里有 `opentelemetry-*`，但未找到初始化/导出器配置。
- Metrics：未找到 Prometheus/OpenTelemetry metrics endpoint。
## F.3 关键代码
`backend/packages/harness/deerflow/config/tracing_config.py:107-128` 从环境变量构建 tracing config：
```python
def get_tracing_config() -> TracingConfig:
    global _tracing_config
    if _tracing_config is not None:
        return _tracing_config
    with _config_lock:
        if _tracing_config is not None:
            return _tracing_config
        _tracing_config = TracingConfig(
            langsmith=LangSmithTracingConfig(
                enabled=_env_flag_preferred("LANGSMITH_TRACING", "LANGCHAIN_TRACING_V2", "LANGCHAIN_TRACING"),
                api_key=_first_env_value("LANGSMITH_API_KEY", "LANGCHAIN_API_KEY"),
```
`backend/packages/harness/deerflow/config/tracing_config.py:122-127` 是 Langfuse config：
```python
            langfuse=LangfuseTracingConfig(
                enabled=_env_flag_preferred("LANGFUSE_TRACING"),
                public_key=_first_env_value("LANGFUSE_PUBLIC_KEY"),
                secret_key=_first_env_value("LANGFUSE_SECRET_KEY"),
                host=_first_env_value("LANGFUSE_BASE_URL") or "https://cloud.langfuse.com",
            ),
```
`backend/packages/harness/deerflow/tracing/factory.py:12-29` 构建 provider callbacks：
```python
def _create_langsmith_tracer(config) -> Any:
    from langchain_core.tracers.langchain import LangChainTracer
    return LangChainTracer(project_name=config.project)
def _create_langfuse_handler(config) -> Any:
    from langfuse import Langfuse
    from langfuse.langchain import CallbackHandler as LangfuseCallbackHandler
    Langfuse(secret_key=config.secret_key, public_key=config.public_key, host=config.host)
    return LangfuseCallbackHandler(public_key=config.public_key)
```
`backend/packages/harness/deerflow/tracing/factory.py:32-53` 统一构建 callbacks：
```python
def build_tracing_callbacks() -> list[Any]:
    """Build callbacks for all explicitly enabled tracing providers."""
    validate_enabled_tracing_providers()
    enabled_providers = get_enabled_tracing_providers()
    if not enabled_providers:
        return []
    tracing_config = get_tracing_config()
    callbacks: list[Any] = []
    for provider in enabled_providers:
        if provider == "langsmith":
```
`backend/packages/harness/deerflow/agents/lead_agent/agent.py:323-337` 注入 run metadata：
```python
    if "metadata" not in config:
        config["metadata"] = {}
    config["metadata"].update(
        {
            "agent_name": agent_name or "default",
            "model_name": model_name or "default",
            "thinking_enabled": thinking_enabled,
            "reasoning_effort": reasoning_effort,
            "is_plan_mode": is_plan_mode,
```
`backend/app/gateway/app.py:26-31` 是 logging basicConfig：
```python
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
```
`backend/app/gateway/services.py:89-97` publish run metadata：
```python
        await bridge.publish(
            run_id,
            "metadata",
            {
                "run_id": run_id,
                "thread_id": thread_id,
            },
        )
```
## F.4 证据
LangSmith/Langfuse：
- `.env.example` 中只有 LangSmith 环境变量示例，证据为 `.env.example:29-33`。
- `tracing_config.py` 支持 Langfuse 环境变量，证据为 `backend/packages/harness/deerflow/config/tracing_config.py:122-127`。
- `test_tracing_factory.py` 覆盖 disabled、LangSmith+Langfuse、provider failure、misconfigured provider，证据为 `backend/tests/test_tracing_factory.py:38-125`。
Trace id：
- `task_tool.py` 里有 `metadata.get("trace_id") or str(uuid.uuid4())[:8]`，grep 证据定位为 `backend/packages/harness/deerflow/tools/builtins/task_tool.py:90-125`。
- `subagents/executor.py` dataclass 有 `trace_id`，并在日志中写 `[trace=...]`，grep 证据定位为 `backend/packages/harness/deerflow/subagents/executor.py:43-53`、`backend/packages/harness/deerflow/subagents/executor.py:157-167`、`backend/packages/harness/deerflow/subagents/executor.py:244-305`。
- `make_lead_agent()` 并没有生成 trace id，只写 agent/model metadata，证据为 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:323-337`。
未找到项与 grep 关键词：
- HTTP trace header propagation 未找到。grep 关键词：`traceparent|tracestate|X-Request-ID|X-Trace`，范围：`backend/app backend/packages/harness/deerflow docker/nginx/nginx.conf frontend/src`。
- OTel 初始化未找到。grep 关键词：`OTEL|opentelemetry`，结果主要在 lockfile，不在 app runtime 初始化。
- Metrics endpoint 未找到。grep 关键词：`prometheus|metrics|Counter|Histogram|MeterProvider`。
- Frontend tracing 未找到。grep 关键词：`LangSmith|LANGFUSE|traceparent|opentelemetry`，结果不在 `frontend/src` runtime。
## F.5 风险
- `build_tracing_callbacks()` 只是 factory，当前调研未在 `make_lead_agent()` 或 `run_agent()` 中看到 callbacks 被自动合入 config；需要继续 grep `build_tracing_callbacks` 的调用点，当前只在 tests/factory 文件出现。若无调用，LangSmith/Langfuse 配置可能只是半接入。
- HTTP request id 与 LangSmith/Langfuse trace id 没有统一传播，证据为 grep 未找到 `traceparent/X-Request-ID`；跨 nginx/frontend/gateway/langgraph/subagent 只能靠 run_id/thread_id 和局部 `[trace=...]` 日志拼接。
- OTel 依赖存在但运行时未初始化，风险是“看起来有 OTel 依赖”但没有 spans/metrics/exporter。
- Gateway `logging.basicConfig` 是全局 INFO，没有结构化 JSON 字段，也没有 request id，证据为 `backend/app/gateway/app.py:26-31`。
---
# G. 测试结构
## G.1 目录
测试结构：
- `backend/tests/`：pytest，顶层 110 个 `test_*.py`。
- `frontend/tests/unit/`：Vitest，6 个 `.test.ts`。
- `frontend/tests/e2e/`：Playwright，5 个 spec + 1 个 mock helper，共 498 行。
- `.github/workflows/backend-unit-tests.yml`
- `.github/workflows/frontend-unit-tests.yml`
- `.github/workflows/e2e-tests.yml`
- `.github/workflows/lint-check.yml`
- `backend/Makefile`
- `frontend/Makefile`
- `frontend/playwright.config.ts`
- `frontend/vitest.config.ts`
## G.2 职责
测试分层：
- Backend unit/integration-like：大量 pytest，直接覆盖 middleware、runtime、routers、sandbox、skills、models、tracing、client、channels。
- Backend live/e2e-like：文件名包含 `live` 或 `e2e` 的有 4 个：`test_client_e2e.py`、`test_client_live.py`、`test_create_deerflow_agent_live.py`、`test_sandbox_orphan_reconciliation_e2e.py`。
- Frontend unit：Vitest，主要测试 stream mode sanitization、reasoning trigger、streamdown plugins、threads utils、uploads validation。
- Frontend E2E：Playwright Chromium，mock 后端 API，覆盖 landing/sidebar/chat/agent-chat/thread-history。
- CI：backend unit、frontend unit、frontend e2e、lint/typecheck/build 分离。
## G.3 关键代码
`backend/Makefile:10-15` 定义 backend test/lint：
```makefile
test:
	PYTHONPATH=. uv run pytest tests/ -v
lint:
	uvx ruff check .
	uvx ruff format --check .
```
`frontend/package.json:6-19` 定义 frontend scripts：
```json
  "scripts": {
    "build": "next build",
    "check": "eslint . --ext .ts,.tsx && tsc --noEmit",
    "dev": "next dev --turbo",
    "test": "vitest run",
    "test:e2e": "playwright test",
    "typecheck": "tsc --noEmit"
  },
```
`frontend/playwright.config.ts:3-15` 定义 E2E 策略：
```ts
export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? "github" : "html",
  timeout: 30_000,
  use: {
    baseURL: "http://localhost:3000",
    trace: "on-first-retry",
  },
```
`frontend/playwright.config.ts:24-32` E2E 会先 build 再 start：
```ts
  webServer: {
    command: "pnpm build && pnpm start",
    url: "http://localhost:3000",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    env: {
      SKIP_ENV_VALIDATION: "1",
    },
  },
```
`frontend/vitest.config.ts:5-13` 限定 unit test glob：
```ts
export default defineConfig({
  resolve: {
    alias: {
      "@": resolve(__dirname, "src"),
    },
  },
  test: {
    include: ["tests/unit/**/*.test.ts"],
  },
});
```
`frontend/tests/e2e/utils/mock-api.ts:181-183` mock LangGraph stream endpoints：
```ts
  // Run stream — returns a minimal SSE response with an AI message
  void page.route("**/api/langgraph/runs/stream", handleRunStream);
  void page.route("**/api/langgraph/threads/*/runs/stream", handleRunStream);
```
`frontend/tests/e2e/utils/mock-api.ts:223-258` 构造最小 SSE：
```ts
 * Build a minimal SSE stream that the LangGraph SDK can parse.
 * The stream returns a single AI message: "Hello from DeerFlow!".
 */
export async function handleRunStream(route: Route) {
  const encoder = new TextEncoder();
  const body = [
    "event: metadata",
    `data: ${JSON.stringify({ run_id: MOCK_RUN_ID, thread_id: MOCK_THREAD_ID })}`,
```
`.github/workflows/backend-unit-tests.yml:34-40` 是 backend CI：
```yaml
      - name: Install backend dependencies
        working-directory: backend
        run: uv sync --group dev
      - name: Run unit tests of backend
        working-directory: backend
        run: make test
```
`.github/workflows/e2e-tests.yml:47-55` 是 E2E CI：
```yaml
      - name: Install Playwright Chromium
        working-directory: frontend
        run: npx playwright install chromium --with-deps
      - name: Run E2E tests
        working-directory: frontend
        run: pnpm exec playwright test
        env:
          SKIP_ENV_VALIDATION: '1'
```
## G.4 证据
实测数字：
- `backend/tests` 顶层 `test_*.py`：110 个。
- `frontend/tests/unit/**/*.test.ts`：6 个。
- `frontend/tests/e2e`：6 个 TypeScript 文件，498 行。
- `backend/app/gateway/routers/*.py`：14 个 router 文件，2888 行，说明 router 层有大量测试目标。
- CI workflow：4 个，分别是 backend unit、frontend unit、e2e、lint/typecheck/build。
Backend 覆盖范围样例：
- Middleware：`test_clarification_middleware.py`、`test_dangling_tool_call_middleware.py`、`test_guardrail_middleware.py`、`test_llm_error_handling_middleware.py`、`test_loop_detection_middleware.py`、`test_summarization_middleware.py`、`test_todo_middleware.py`、`test_token_usage.py`、`test_tool_error_handling_middleware.py`、`test_view_image_middleware.py`。
- Runtime：`test_run_manager.py`、`test_run_worker_rollback.py`、`test_stream_bridge.py`、`test_serialization.py`、`test_sse_format.py`。
- Routers：`test_artifacts_router.py`、`test_memory_router.py`、`test_skills_custom_router.py`、`test_suggestions_router.py`、`test_threads_router.py`、`test_uploads_router.py`。
- Persistence/config/tracing：`test_checkpointer.py`、`test_checkpointer_none_fix.py`、`test_tracing_config.py`、`test_tracing_factory.py`、`test_app_config_reload.py`。
Frontend 覆盖范围：
- Unit：`stream-mode.test.ts` 覆盖 stream mode sanitization；`threads/utils.test.ts` 覆盖 path utils；`uploads/*.test.ts` 覆盖文件校验/转换。
- E2E：`chat.spec.ts` 拦截 `**/runs/stream` 验证发送消息触发 stream；`thread-history.spec.ts` mock 多 thread；`agent-chat.spec.ts` mock agents。
## G.5 风险
- 未发现 coverage 配置或 CI 上传 coverage。grep 关键词：`coverage|pytest-cov|vitest coverage|nyc|codecov`；结果只在 sandbox ignore、lockfile可选依赖和文本中出现，没有 CI coverage gate。
- Frontend E2E 全部 mock 后端 API，证据为 `frontend/tests/e2e/utils/mock-api.ts:42-50` 和 `frontend/tests/e2e/utils/mock-api.ts:181-183`；能测 UI 行为，但不能测真实 Gateway/LangGraph/SSE 兼容。
- Backend `make test` 直接跑全部 tests，CI timeout 15 分钟，证据为 `backend/Makefile:10-11` 与 `.github/workflows/backend-unit-tests.yml:17-20`；随着 live/e2e-like 测试增加可能不稳定。
- `test_client_live.py`、`test_create_deerflow_agent_live.py` 这类 live 文件是否在默认 CI 下跳过依赖实际 marker 逻辑，需要进一步逐文件确认。
- Playwright webServer 每次 `pnpm build && pnpm start`，证据为 `frontend/playwright.config.ts:24-32`；CI 慢但更接近生产 build，开发本地可复用 server。
---
# 全景发现
## 耦合点
1. Gateway 与 LangGraph Server 双路径耦合。`/api/langgraph/*` 默认走 `langgraph:2024`，但 gateway 也实现了 LangGraph-compatible runs lifecycle；证据为 `docker/nginx/nginx.conf:59-65`、`backend/app/gateway/routers/thread_runs.py:1-10`、`backend/app/gateway/services.py:239-335`。
2. Checkpointer 与 Store 共享 `checkpointer` 配置。证据为 `backend/packages/harness/deerflow/runtime/store/async_provider.py:1-8` 和 `config.example.yaml:735-765`。
3. Middleware 与 config/runtime context 强耦合。`_build_middlewares()` 同时读 config、runtime `configurable`、model capabilities、agent_name，证据为 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:215-277`。
4. Frontend `useStream` 依赖 Gateway/LangGraph SSE wire format，包括 `Content-Location`、metadata、Last-Event-ID replay，证据为 `backend/app/gateway/routers/thread_runs.py:101-125`、`backend/app/gateway/services.py:42-55`、`frontend/src/core/threads/hooks.ts:204-222`。
5. Sandbox 依赖 thread_id，从 frontend context、Gateway config、LangGraph runtime 一路传入，证据为 `frontend/src/core/threads/hooks.ts:490-506`、`backend/app/gateway/services.py:287-307`、`backend/packages/harness/deerflow/runtime/runs/worker.py:103-111`。
6. Docker socket 与 skills/user-data mount 同时耦合 sandbox 能力和宿主安全，证据为 `docker/docker-compose.yaml:78-85`、`docker/docker-compose.yaml:131-138`。
## agents-hive 最该借鉴 3 条
1. 借鉴“Gateway compatibility layer + LangGraph Server 标准路径”双入口。它让产品 UI 可以优先用标准 LangGraph SDK，同时 Gateway 可以补 custom API 和兼容层；证据为 `docker/nginx/nginx.conf:59-65` 与 `backend/app/gateway/routers/thread_runs.py:1-10`。
2. 借鉴 middleware 固定顺序和 last invariant。`ClarificationMiddleware` 必须最后，tool errors 保留 `GraphBubbleUp`，这些是 agent runtime 稳定性的关键不变量；证据为 `backend/packages/harness/deerflow/agents/lead_agent/agent.py:205-277` 与 `backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:43-65`。
3. 借鉴 `StreamBridge` 的 bounded event log + `Last-Event-ID` replay。它是断线重连最小可用实现，证据为 `backend/packages/harness/deerflow/runtime/stream_bridge/memory.py:25-64` 和 `frontend/src/core/threads/hooks.ts:204-210`。
## agents-hive 最该警惕 3 个陷阱
1. 不要把进程内 `RunManager/MemoryStreamBridge` 和多 worker 直接组合。DeerFlow Gateway 默认 4 workers，但 run registry/stream bridge 是 in-memory；证据为 `docker/docker-compose.yaml:77`、`backend/packages/harness/deerflow/runtime/runs/manager.py:40-45`、`backend/packages/harness/deerflow/runtime/stream_bridge/memory.py:32-35`。
2. 不要只声明 resume/command 字段而不完成执行语义。DeerFlow `RunCreateRequest` 有 `command/checkpoint`，但 embedded `start_run()` 未把 command 传入 graph；证据为 `backend/app/gateway/routers/thread_runs.py:38-44` 和 `backend/app/gateway/services.py:311-325`。
3. 不要让前端可选 stream modes 超过后端真实支持。DeerFlow 前端允许 `events`，后端跳过 `events`；证据为 `frontend/src/core/api/stream-mode.ts:1-11` 与 `backend/packages/harness/deerflow/runtime/runs/worker.py:60-65`。
## 未解开的疑问
1. `build_tracing_callbacks()` 是否在某个被动态加载的路径里接入 LangGraph config？静态 grep 只看到 factory/tests，没有看到生产调用点。需要进一步跑完整 app 或查 LangChain callbacks 注入机制。
2. `SandboxMiddleware.after_agent()` 释放逻辑与 docstring“Sandbox is NOT released after each agent call”是否冲突？需要读 `SandboxProvider.release()` 的 ref-count/reuse 实现和相关测试确认。
3. LangGraph Server 路径和 Gateway embedded 路径的状态存储是否完全一致？`backend/langgraph.json` 只声明 checkpointer，Gateway lifespan 还创建 store；需要实际跑 `/api/langgraph/*` 与 `/api/threads/*` 交叉验证 thread list/state。
4. `multitask_strategy="enqueue"` 是计划中未实现，还是应由 LangGraph SDK 兼容层另行处理？当前 runtime 返回 501。
5. `events` mode 被前端白名单接受但 worker 跳过，是否有 UI 功能依赖 `onLangChainEvent`？当前前端 `onLangChainEvent` 监听 `on_tool_end`，需要真实流中确认能否收到。
6. Live tests 在 CI 默认 `make test` 中是否全部跳过外部依赖？需要逐个查看 `test_client_live.py`、`test_create_deerflow_agent_live.py` 的 skip/marker 条件。
## “未找到”汇总与 grep 关键词
- 未找到原任务路径：`/Users/guoss/workspace/company/vast/deer-flow`；实际路径为 `/Users/guoss/workspace/deer-flow`。
- 未找到前端手写 SSE/EventSource：grep `EventSource|new EventSource` in `frontend/src`。
- 未找到 Next.js error boundary 文件：find `frontend/src/app -name error.tsx -o -name global-error.tsx` 输出为空。
- 未找到 zustand 源码 store：grep `zustand|create\(` in `frontend/src`，只命中 motion.create。
- 未找到 HTTP trace header propagation：grep `traceparent|tracestate|X-Request-ID|X-Trace` in `backend/app backend/packages/harness/deerflow docker/nginx/nginx.conf frontend/src`。
- 未找到 OTel runtime 初始化：grep `OTEL|opentelemetry` in runtime code，主要命中 lockfile。
- 未找到 metrics endpoint/gate：grep `prometheus|metrics|Counter|Histogram|MeterProvider`。
- 未找到 coverage gate：grep `coverage|pytest-cov|vitest coverage|nyc|codecov` in CI/config。
## 交叉校验建议
1. 用 `rg -n "build_tracing_callbacks" backend` 交叉确认 tracing callbacks 是否生产接入。
2. 用 `PYTHONPATH=. uv run pytest tests/test_create_deerflow_agent.py tests/test_tool_error_handling_middleware.py -v` 验证 middleware invariants。
3. 用 Gateway mode 启动后断开 SSE，再带 `Last-Event-ID` 订阅同 run，验证 `MemoryStreamBridge` replay。
4. 同时启动 `GATEWAY_WORKERS=4` 并发同 thread run，观察 run registry 与 stream join 是否跨 worker 失效。
5. 实测 `stream_mode=["events","values"]`，确认前端 `onLangChainEvent` 是否收到 tool events，若不能收到应从前端白名单删除 `events` 或后端补 `astream_events()` 分支。
