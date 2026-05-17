# agents-hive 开发者文档

本文面向需要阅读、调试、扩展和维护 `agents-hive` 的开发者。内容以当前源码为准，重点解释项目结构、启动链路、核心模块、前后端通信、配置来源、数据库迁移、测试命令和常见排障路径。

> 校验日期：2026-05-16  
> 主要依据：`README.md`、`DESIGN.md`、`cmd/*`、`internal/bootstrap`、`internal/api`、`internal/master`、`internal/tools`、`internal/store`、`frontend/src`、`Dockerfile`、`docker-compose.yml`、`config.example.json`。

## 1. 项目定位

`agents-hive` 是一个面向 ReAct Agent 的工程化运行时和质量控制平面。它不是单纯的聊天 UI，也不是简单工具集合，而是把 Web UI、CLI、HTTP API、IM Channel、Master Agent、SubAgent、MCP、Skill、HITL、记忆、执行轨迹、质量评测和运行时配置管理放到同一套链路里。

一句话理解：

```text
agents-hive = Agent Runtime + Agent Harness + Quality Control Plane + Ops Workbench
```

从代码结构看，系统由以下几层组成：

```text
Web UI / CLI / HTTP API / IM Channel
                |
                v
        API Server / Gateway / Auth
                |
                v
          Master Agent SessionLoop
                |
    +-----------+-----------+-----------+
    |           |           |           |
 Tool Runtime  SubAgent    Plan Runtime Memory / KB
 MCP Host      ACP         Todos        Journal / Trace
    |
    v
 Files / Shell / LSP / Web / IM / Skills / Custom Tools

PostgreSQL 保存会话、消息、配置、Prompt、Skill、Memory、
质量治理、Trace、用量、认证、IM 状态、KB、资产 metadata 等数据。
```

## 2. 技术栈

后端：

- Go `1.25.0`，模块名 `github.com/chef-guo/agents-hive`。
- HTTP 路由使用 Go 标准库 `net/http` 的 pattern mux。
- PostgreSQL 通过 `pgx/v5` 和 `pgxpool` 访问。
- LLM SDK 主要通过 `openai-go`，上层由 `internal/llm` 和 `internal/airouter` 封装。
- 日志使用 `zap`，文件滚动使用 `lumberjack`。
- WebSocket 包含 `coder/websocket` 和 `gorilla/websocket` 相关依赖。
- IM 集成包含飞书、钉钉、企业微信、wechatbot。
- Docker sandbox 通过宿主机 Docker daemon 执行隔离命令。

前端：

- React `19`、TypeScript `5.9`、Vite `7`。
- React Router `7`。
- Zustand 做状态管理。
- Tailwind CSS `4`，同时使用少量 `radix-ui`、`lucide-react`、`streamdown`、`shiki`、`mermaid`、`katex`。
- Vite 构建产物直出到 `internal/webui/dist/`，由 Go `embed` 嵌入。

## 3. 快速开始

### 3.1 本地依赖

本地开发至少需要：

- Go 1.25+
- Node.js 22+，仓库跟踪的是 `frontend/package-lock.json`，优先使用 `npm install`
- PostgreSQL 16 或兼容版本
- 如启用 Docker sandbox，需要本机 Docker 可用，并先构建 `hive-sandbox:latest`

### 3.2 后端本地启动

```bash
cp config.example.json config.json
# 修改 config.json，或通过环境变量注入数据库与 LLM 配置

go build -o server ./cmd/server
./server --config config.json
```

常用环境变量：

```bash
export POSTGRES_HOST=localhost
export POSTGRES_PORT=5432
export POSTGRES_DB=claw
export POSTGRES_USER=claw
export POSTGRES_PASSWORD=...
export POSTGRES_SSL_MODE=disable

export CLAW_API_KEY=...
# 或 OPENAI_API_KEY=...
```

### 3.3 前端本地启动

当前源码里 `frontend/vite.config.ts` 的 dev server 端口是 `3000`，并代理 `/api` 到 `http://localhost:8080`。

```bash
cd frontend
npm install
npm run dev
```

然后打开：

```text
http://localhost:3000
```

如果不走 Vite proxy，也可以用 `VITE_API_BASE` 指向后端：

```bash
VITE_API_BASE=http://localhost:8080 npm run dev
```

根 README 与 `frontend/README.md` 已同步为 npm / 3000 端口；如果后续端口变化，以 `frontend/vite.config.ts` 为准。

### 3.4 前端生产构建与 Go embed

```bash
cd frontend
npm run build
```

构建流程：

- `npm run build` 执行 `tsc -b && vite build`。
- Vite `outDir` 是 `../internal/webui/dist`。
- `internal/webui/embed.go` 使用 `//go:embed dist/*` 嵌入该目录。
- 不要手工修改 `internal/webui/dist/`；它是生成产物。

### 3.5 CLI 模式

```bash
go build -o claw ./cmd/claw
./claw -c config.json "分析当前项目结构"
./claw -c config.json -i
./claw -c config.json --acp
```

CLI 入口在 `cmd/claw/main.go`。它通过 `config.LoadCLI` 读取配置，补充 CLI 默认值，然后创建 `internal/cli.App`。`--acp` 模式会以 ACP 协议启动，供 IDE 或外部 Agent 接入。

### 3.6 Docker Compose

Docker Compose 包含：

- `hive`：Go 服务 + 内嵌前端 + Docker CLI。
- `postgres`：PostgreSQL 16。
- `minio` / `minio-init`：S3-compatible 对象存储和 bucket 初始化，用于 KB 图片、聊天附件和 Agent artifact。

首次启动路径：

```bash
cat > .env <<EOF
POSTGRES_PASSWORD=your_strong_password
DOCKER_GID=$(stat -c '%g' /var/run/docker.sock)
TZ=Asia/Shanghai
HIVE_PORT=8080
MINIO_ROOT_USER=minioadmin
MINIO_ROOT_PASSWORD=minioadmin
MINIO_BUCKET=hive-assets
EOF

mkdir -p /opt/hive/workdir/sessions
chmod 0775 /opt/hive/workdir /opt/hive/workdir/sessions
make build-sandbox-image
docker compose up -d
docker compose logs -f hive
```

runtime 镜像会内置 `docker/config.docker.json` 到 `/app/config.json`，并默认以 `--config /app/config.json` 启动；该配置启用 Docker sandbox，并默认使用 Compose 内的 MinIO 作为 `asset.provider=minio`。Docker sandbox 是 DooD 模式：Hive 容器挂载 `/var/run/docker.sock`，在宿主机 Docker daemon 上创建 sandbox 容器。`/opt/hive/workdir` 必须在宿主机和 Hive 容器内路径一致，否则 sandbox bind mount 会失败；该宿主机目录也需要允许容器内非 root `hive` 用户写入。

KB PDF ingest 默认使用 `fileconv.markdown.pdf.provider=mineru`。runtime 镜像构建时会安装 Python、uv 和 `mineru[all]`；服务启动时仍会按配置检查 `mineru` 是否存在。若缺失且 `fileconv.markdown.pdf.install.enabled=true`，默认安装器 `builtin:python-venv-pip` 会在 `install_dir` 创建隔离 venv（优先 `python -m venv`，失败后 fallback 到 `python -m virtualenv`）并安装 `mineru[all]`，然后把运行命令回写到该 venv 下的 `mineru`。需要企业内部镜像源或其他 OCR 工具时，覆盖 `fileconv.markdown.pdf.install.command` 或将 provider 改为 `external`。

## 4. 目录结构

根目录关键文件：

| 路径 | 说明 |
| --- | --- |
| `README.md` / `README.en.md` | 产品定位、快速开始、功能预览 |
| `DEVELOPER_GUIDE.md` | 当前开发者文档 |
| `DESIGN.md` | Web UI 设计系统与设计决策记录 |
| `config.example.json` | 启动配置示例，强调运行时配置存 DB |
| `docker/config.docker.json` | Docker 部署专用配置 |
| `Dockerfile` | 多阶段构建：前端、Go 二进制、runtime 镜像 |
| `docker-compose.yml` | Hive + PostgreSQL + Docker socket 部署 |
| `Makefile` | 常用构建、测试、Docker、前端 embed 命令 |
| `migrations/` | 部分历史/外部 SQL 迁移文件；核心迁移主要在 Go 里 |
| `docs/` | 架构设计、路线、运维手册、验收报告 |
| `openspec/` | 规格文档和变更说明 |
| `tests/regression/` | 回归测试 |
| `skills/` | 仓库内置 Skill 示例/模板 |
| `assets/screenshots/` | README 和展示用截图 |

Go 入口：

| 路径 | 说明 |
| --- | --- |
| `cmd/server` | HTTP Server / Web UI / IM / Gateway 入口 |
| `cmd/claw` | CLI 和 ACP stdio 入口 |
| `cmd/agentquality` | Agent 质量评测相关命令 |
| `cmd/agentquality-benchmark` | benchmark 命令 |
| `cmd/quality-batch-eval` | 批量质量评测 |
| `cmd/quality-weekly-report` | 周报生成 |
| `cmd/memory-eval` / `cmd/memory-nightly-eval` | Memory 评测 |
| `cmd/delegation-eval` | delegation 评测 |

`internal/` 关键包：

| 包 | 职责 |
| --- | --- |
| `internal/bootstrap` | Server 模式组件装配；这是理解启动链路的核心入口 |
| `internal/config` | 配置结构、默认值、环境变量覆盖、配置校验 |
| `internal/api` | REST API、WebSocket 路由、管理端 handler、前端静态资源挂载 |
| `internal/master` | Master Agent、SessionLoop、ReAct、HITL、事件广播、会话状态 |
| `internal/tools` | 内置工具：文件、搜索、shell、patch、web、LSP、task、skill、IM、memory 等 |
| `internal/mcphost` | MCP 工具宿主，注册/执行工具、资源和 prompt |
| `internal/skills` | Skill 注册、发现、DB 覆盖层、工具桥接、按需安装 |
| `internal/subagent` | 固定和动态 SubAgent 框架 |
| `internal/acpserver` / `internal/acpclient` | ACP server/client 和远程 Agent 集成 |
| `internal/llm` | LLM client、provider、Responses/Chat 转换、token 计数 |
| `internal/airouter` | 按任务类型路由 LLM、图片、视频、TTS、STT、Embedding 适配 |
| `internal/store` | PostgreSQL store、迁移、会话/配置/Prompt/Skill 等存储 |
| `internal/auth` | OAuth/LDAP/JWT/user/quota 认证授权 |
| `internal/channel` | IM 抽象路由、去重、重试、renderer、push |
| `internal/channel/feishu` | 飞书插件、长连接、webhook、renderer、重试、chat state |
| `internal/channel/dingtalk` | 钉钉插件 |
| `internal/channel/wecom` | 企业微信插件 |
| `internal/channel/wechatbot` | 官方 wechatbot 个人微信入口 |
| `internal/imcore` | 统一 IM Agent API 适配层 |
| `internal/imctx` | IM 与 master 共享的中立消息上下文，只承载数据 |
| `internal/sandbox` | Local/Docker executor、执行诊断、安全 wrapper |
| `internal/security` | 命令安全策略、解析、脱敏、环境校验 |
| `internal/router` | 工具能力、风险、路由和策略决策 |
| `internal/memory` | 记忆存储、治理、注入、embedding backlog |
| `internal/kb` | KB namespace/document/tree/evidence/binding 服务 |
| `internal/asset` | 统一对象存储；支持 local、MinIO 和 S3-compatible provider，PG 保存 metadata |
| `internal/sessiontodo` | Plan Runtime 的 session-scoped todos |
| `internal/taskboard` | 工作项管理 |
| `internal/journal` | 会话 journal：tool call、file change、decision |
| `internal/observability` | trace、metrics、logs 写入和读取 |
| `internal/trajectory` | step snapshot / session trajectory |
| `internal/agentquality` | 质量用例、评估、优化建议、回滚、shadow eval |
| `internal/qualityworkbench` | 质量工作台：replay、batch eval、report、grouping |
| `internal/specdriven` | spec-driven intake、planner、continuation、eval harness |
| `internal/webui` | 嵌入前端构建产物并提供 SPA fallback |

前端目录：

| 路径 | 说明 |
| --- | --- |
| `frontend/src/App.tsx` | 前端路由总入口 |
| `frontend/src/api` | REST API / node client 封装 |
| `frontend/src/store` | Zustand stores：chat、session、auth、ws、todos、replay 等 |
| `frontend/src/hooks` | WebSocket、主题、语言、微信连接等 hooks |
| `frontend/src/layouts` | `AppShell`、`AdminShell`、Sidebar |
| `frontend/src/pages` | 用户页和管理页 |
| `frontend/src/components/chat` | Chat UI、消息、工具调用、输入框 |
| `frontend/src/components/settings` | 设置页组件 |
| `frontend/src/components/replay` | 会话回放组件 |
| `frontend/src/components/ai-elements` | Chat leaf primitives，业务逻辑不要放进这里 |
| `frontend/src/i18n` | 国际化资源 |
| `frontend/src/utils` | 日期、artifact、shiki、token usage 等工具 |

## 5. Server 启动链路

`cmd/server/main.go` 是 HTTP Server 入口。高层流程：

1. 解析 flags：`--config`、`--model`、`--base-url`、`--api-key`、`--log-level`。
2. `config.Load` 读取 `config.json`，应用环境变量覆盖，调用 `Resolve`。
3. `cfg.ApplyOverrides` 使用 CLI flag 覆盖配置。
4. Server 模式会把默认 `console_level=error` 改为 `info`。
5. 初始化 logger。
6. 调用 `bootstrap.InitServer(cfg, configPath, logger)` 装配所有组件。
7. 创建 `signal.NotifyContext`，启动 `Master.Start(ctx)`。
8. 如有 `PromptLoader`，启动 prompt 文件监听/缓存失效。
9. 开 goroutine 运行 `Master.SessionLoop(ctx)`。
10. 根据配置启用安全环境变量校验。
11. 启动 channel、skill watcher。
12. 创建 `api.NewServer(...)`，注入可选依赖。
13. 启动 HTTP server。
14. 收到 SIGINT/SIGTERM 后优雅关闭 HTTP server，再调用 `sc.Shutdown()`。

### 5.1 `bootstrap.InitServer` 做了什么

`internal/bootstrap/server.go` 是 Server 模式装配中心。当前源码中的主要装配顺序如下：

1. 校验 feature flag 与 skill 配置。`on_demand_enabled=true` 但没有 marketplace URL 会 fail-fast。
2. 初始化 Skill：`OverlayRegistry`、`Finder`、`Discovery`。默认扫描 `.claude/skills`、`$HOME/.claude/skills`、`skills`，也会合并配置中的 public/personal skill 目录。
3. 初始化 LLM 模型注册表并启动后台刷新。
4. 创建 MCP Host，注册内置 MCP resources/prompts。
5. 创建 Skill ToolBridge，并接入插件管理器。
6. 创建 SubAgent registry。
7. 初始化 PostgreSQL Store。Server 模式下 PostgreSQL 初始化失败会 fatal。
8. 首次启动时将 config 中 LLM 等配置 seed/migrate 到 DB，再从 DB 覆盖回内存配置。DB 是运行时配置的主要真相源。
9. 初始化 AuthEngine、AssetService、KBService。
10. 如果 PG 可用，启用 Skill DB 覆盖层、Agent Quality 候选池、优化建议存储。
11. 如 Plan Runtime 开启，初始化 session todo store；PG 不可用时回退内存。
12. 初始化 sandbox executor，并注入到 `tools`。
13. 根据 DB 覆盖后的 MCP 配置连接外部 MCP servers。
14. 初始化 AIRouter，并得到用户默认 LLM client。
15. 创建 Master，并注入 executor、MCPHost、SessionTodoStore、KB reader、HITL emitter、spec runner、spec change store。
16. 初始化 PromptLoader。Prompt 加载优先级是 `DB > 文件目录 > go:embed`。
17. 注册图片、视频、TTS、STT、Embedding 适配器。
18. 初始化 CostTracker 和用量清理 worker。
19. 注入 AuthEngine 到 Master。
20. 注册固定 SubAgent 和动态 Agent 工厂。
21. 初始化 Memory、Journal、Observability、TaskBoard。
22. 注册内置工具、按需 Skill 工具、TaskBoard 工具、AI 媒体工具。
23. 初始化远程 ACP Agents。
24. 初始化 IM Channel Router 和各平台插件。
25. 注册 IM 相关工具、飞书工具、统一 `im_api` 工具。
26. 如 `gateway.enabled=true`，初始化 Gateway RPC/WebSocket。

### 5.2 关闭顺序

`ServerComponents.Shutdown()` 的顺序很重要：

1. 取消 PG NOTIFY / 模型刷新 / 用量清理 / embedding backlog。
2. 停止 Channel Router。
3. 关闭远程 MCP clients。
4. 关闭 ACP pool。
5. `Master.Stop()`，会保存会话，因此必须在 DB 关闭前。
6. 关闭 Memory store，embedding worker 也依赖 DB。
7. 最后关闭 DB pool 和 executor。

## 6. Master Agent 与会话执行

`internal/master` 是运行时核心。`Master` 负责：

- 维护 session 状态和会话持久化。
- 接收用户输入并进入 ReAct loop。
- 通过 EventBus 广播消息、工具调用、HITL、agent 状态、todo snapshot 等事件。
- 调用 MCP Host 执行工具。
- 管理 HITL 审批和权限请求。
- 调度 SubAgent、task、parallel dispatch、spawn agent。
- 注入 Memory、KB evidence、PromptLoader、Journal、Trace、CostTracker。
- 执行上下文压缩和 spec-driven intake。
- 支持 per-session 串行执行和全局 worker pool。

### 6.1 `SessionLoop`

`Master.SessionLoop(ctx)` 的核心行为：

- 启动时尝试从 store 恢复最近活跃会话。
- 若无历史会话，创建 `main` 会话。Auth 启用时不持久化无主会话，避免所有用户共享可见。
- 启动 journal worker 和 observability worker。
- 如有 store，启动后台 session sync。
- 根据 `runtime_policy.global_workers` 或 `MaxConcurrentTasks` 创建 worker pool；默认兜底为 50。
- Dispatcher 从 `sessionMgr.requestCh` 接收请求。
- 轻量 session command 同步处理，例如 revert。
- 普通任务进入 per-session semaphore，保证同一个 session 同时只有一个任务执行。
- worker 调用 `processTask`，执行完整任务。
- 退出时保存所有会话并结束 journal。

### 6.2 事件类型

Master 对外广播的主要事件类型定义在 `internal/master/master.go`：

| 事件 | 用途 |
| --- | --- |
| `input_received` | 用户输入已到达 master，IM renderer 可做 ack |
| `message` | 流式或最终消息 |
| `tool_call` | 工具调用 start/success/error |
| `input_request` | HITL 审批或人工输入请求 |
| `input_response` | HITL 响应 |
| `agent_status` | thinking/tool_calling/completed/error 等状态 |
| `task_group` / `task_progress` | 并行任务组进度 |
| `agent_progress` | SubAgent 工具级进度 |
| `todo_snapshot` | Plan Runtime todos 快照 |
| `plan_mode_changed` | Plan mode 状态变化 |
| `spec_continuation_ambiguous` | spec-driven continuation 需要用户确认 |

Web UI 通过 WebSocket 消费这些事件，IM Channel renderer 也订阅同一个 EventBus。

### 6.3 ReAct 与工具执行

`internal/master/react_processor.go` 负责主要 ReAct 处理路径。执行工具时：

- LLM 输出 tool calls。
- Master 对工具做可见性、权限、策略、ActionGuard 等检查。
- 工具通过 `mcphost.Host.ExecuteTool` 调用。
- 工具结果转回 LLM message。
- 过程写入事件、journal、trace、metrics、cost tracking。
- 开启 `EnableStreamingExecutor` 后，标记为 concurrency safe 的工具可并发执行，unsafe 工具保持串行。

## 7. 工具、MCP、Skill 与扩展

### 7.1 MCP Host

`internal/mcphost.Host` 是统一工具宿主。核心结构：

- `ToolDefinition`：工具名、描述、input/output schema、是否 core、是否并发安全、来源 server、trusted、annotations。
- `ToolExecutor`：`func(ctx context.Context, input json.RawMessage) (*ToolResult, error)`。
- `RegisterTool` / `UnregisterTool` / `ExecuteTool` / `ListTools`。
- 支持资源和 prompt 注册。
- `OnToolListChanged` 用于工具列表变化通知。
- HITL emitter 由 bootstrap 在 Master 创建后注入。

外部 MCP server 由 `config.MCP.Servers` 或 DB 运行时配置决定。`connectMCPServers` 支持 stdio、SSE、HTTP 和 OAuth 配置，最多重试 3 次。

### 7.2 内置工具

`tools.RegisterBuiltinTools` 注册内置工具。当前主要包含：

- 文件：`read_file`、`write_file`、`edit`、`multiedit`、`apply_patch`、`filesystem`。
- 搜索：`glob`、`grep`，底层优先使用 doublestar 和 ripgrep executor。
- 运行时：`bash`。
- Web：`websearch`、`webfetch`、`browser_interact`。
- LSP：`lsp_definition`、`lsp_references`、`lsp_hover`、`lsp_symbols`、`lsp_diagnostics`、`lsp_rename`、`lsp_code_action`、`lsp_format`、`lsp_completion`，条件是 `cfg.LSP.Enabled`。
- 协作：`question`、`task`、`parallel_dispatch`、`spawn_agent`。
- Plan Runtime：`todo_write`、`enter_plan_mode`、`exit_plan_mode`、`finish_plan`、`create_handoff_summary`。
- TaskBoard：`promote_todos_to_taskboard`、`taskboard`。
- Skill：`skill`，按需开启时还有 `skill_install`、`skill_search`。
- Memory：`memory`，条件是 Memory store 可用。
- KB：`kb.doc.meta`、`kb.doc.structure`、`kb.section.text`。
- IM：`send_im_message`、`im_api`，以及飞书专用工具。
- AI 能力：图片生成、视频生成、TTS。
- 自定义工具：从 `custom_tools_dir` 加载。

工具注册时会初始化：

- `ShellPool`
- `ReadTracker`：读后改约束
- `FileTracker`：检测外部修改
- LSP manager
- 全局 sandbox executor
- 插件 hook
- HTTP 工具域名白名单

### 7.3 工具策略与风险

`internal/router` 维护工具能力与风险分类：

- `capability_registry.go` 声明内置工具的 domain、risk、read-only、side-effect、capabilities。
- `tool_policy.go` 中 `EvaluateToolPolicy` 是工具路由、展示和运行时守卫共享的策略入口。

风险分类包括：

- read-only
- routine side effect
- privileged side effect
- destructive
- runtime exec
- unknown

常见规则：

- 只读工具通常 allow。
- runtime exec 默认 ask 或 deny，依赖意图和配置。
- 外部发送、特权副作用、危险结构化动作需要 side-effect intent 或审批。
- unknown/open-world/destructive 工具 fail-closed 到 ask/deny。

### 7.4 Skill 系统

`internal/skills` 实现 Skill：

- Skill 是 Markdown 指令包，frontmatter 定义元数据。
- `Finder` 负责从本地目录和 marketplace 发现。
- `Registry`/`OverlayRegistry` 管理注册结果。
- `ToolBridge` 让 Skill 能调用 MCP 工具。
- PG 可用时启用 `SkillStore` 和 `SkillService`，支持 DB 覆盖与热重载。
- 按需安装由 `agent.skills.on_demand_enabled` 控制；开启时要求 marketplace URL，否则启动 fail-fast。

默认扫描路径：

- `.claude/skills`
- `$HOME/.claude/skills`
- `skills`
- `agent.skills.public_skills_dir`
- `agent.skills.personal_skills_dir`

### 7.5 插件与自定义工具

插件系统由 `internal/plugin` 管理，只有 `plugin.enabled=true` 时启用。默认插件目录是 `$HOME/.claw/plugins`，也可通过配置指定。

自定义工具目录由 `custom_tools_dir` 或 `CUSTOM_TOOLS_DIR` 指定，默认 CLI 模式是 `.claw/tools`。内置工具注册后会加载该目录中的工具定义。

## 8. 配置模型

### 8.1 启动配置与运行时配置

项目明确区分两类配置：

- 启动配置：服务监听、日志、数据库连接、资产 provider 等启动前必须知道的参数。
- 运行时配置：LLM、Prompt、Skill、Channel、MCP、权限、Memory 等，启动后主要存储在 PostgreSQL，并可通过 Web UI/API 修改。

`config.example.json` 只包含引导配置示例，并提示运行时配置会写入数据库。

### 8.2 加载优先级

Server 模式：

```text
config.Default()
  -> 读取 config.json
  -> 环境变量覆盖
  -> Resolve()
  -> CLI flags 覆盖
  -> bootstrap seed/migrate 到 DB
  -> 从 DB 加载运行时配置覆盖内存 cfg
```

CLI 模式：

```text
config.Default()
  -> CLIDefaults()
  -> 读取配置
  -> 环境变量覆盖
  -> Resolve()
  -> CLI flags 覆盖
```

### 8.3 重要环境变量

LLM：

- `CLAW_PROVIDER`
- `CLAW_MODEL`
- `CLAW_API_KEY`
- `OPENAI_API_KEY`
- `CLAW_BASE_URL`
- `OPENAI_BASE_URL`
- `CLAW_REASONING_EFFORT`
- `CLAW_INTERACTIVE_SERVICE_TIER`
- `CLAW_PROMPT_CACHE_KEY_ENABLED`
- `GOOGLE_API_KEY`
- `CLAW_GOOGLE_API_KEY`
- `AZURE_OPENAI_API_KEY`
- `CLAW_AZURE_API_KEY`
- `AZURE_DEPLOYMENT`
- `CLAW_AZURE_DEPLOYMENT`
- `AZURE_OPENAI_ENDPOINT`
- `CLAW_AZURE_ENDPOINT`

数据库：

- `DATABASE_URL` 优先级最高。
- `POSTGRES_HOST`
- `POSTGRES_PORT`
- `POSTGRES_DB`
- `POSTGRES_USER`
- `POSTGRES_PASSWORD`
- `POSTGRES_SSL_MODE`

日志：

- `CLAW_LOG_LEVEL`
- `CLAW_LOG_FILE`
- `CLAW_CONSOLE_LEVEL`

路径和资产：

- `SESSIONS_DIR`
- `CUSTOM_TOOLS_DIR`
- `ASSET_PROVIDER`
- `ASSET_LOCAL_BASE_PATH`
- `MINIO_ENDPOINT`
- `MINIO_ACCESS_KEY`
- `MINIO_SECRET_KEY`
- `MINIO_BUCKET`
- `MINIO_REGION`
- `MINIO_USE_SSL`
- `S3_ENDPOINT`
- `S3_ACCESS_KEY`
- `S3_SECRET_KEY`
- `S3_BUCKET`
- `S3_REGION`
- `S3_USE_SSL`
- `FILECONV_PDF_PROVIDER`
- `FILECONV_PDF_BIN`
- `FILECONV_PDF_ARGS`
- `FILECONV_PDF_INSTALL_ENABLED`
- `FILECONV_PDF_INSTALL_DIR`

IM API：

- `IM_API_ENABLED`
- `IM_API_PREFERRED_OVER_LEGACY`
- `IM_API_FORCE_DRY_RUN`

提示词：

- `CLAW_PROMPT_LANGUAGE`

### 8.4 `Resolve()` 的重要副作用

`config.Resolve()` 会：

- 根据 Provider 推断或填充 BaseURL、默认模型、JSON mode 兼容性。
- 解析 `llm.models` 中的 provider/base URL/API key。
- `webui.enabled=true` 时自动启用 `hitl.websocket_enabled`，因为前端依赖 WebSocket。
- 修正部分非法负值。
- 规范化 Asset 和 ToolRecall 配置。

## 9. HTTP API、WebSocket 与前端静态资源

### 9.1 API Server

`internal/api.NewServer` 创建 HTTP server：

- 创建 `StreamHandler` 和 `WSHandler`。
- 挂载 REST 路由。
- 应用 middleware。
- 默认 ReadTimeout/WriteTimeout 为 0，避免影响 WebSocket/SSE 长连接。
- IdleTimeout 为 60 秒。

代码包装顺序：

```text
securityHeaders
cors
auth（如果 authEngine != nil）
logging
recovery
tracing（最外层）
```

实际请求执行顺序是反向的：`tracing -> recovery -> logging -> auth -> cors -> securityHeaders -> handler`。

安全头里设置了 CSP，目前 `connect-src 'self'`。如果前端通过不同 origin 直连后端，需要同时关注 CORS 和 CSP。

### 9.2 路由概览

基础：

- `GET /api/v1/health`
- `GET /api/v1/agents`
- `GET /api/v1/skills`
- `GET /api/v1/metrics/skills`
- `GET /api/v1/capabilities`

会话：

- `POST /api/v1/sessions`
- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{id}`
- `PATCH /api/v1/sessions/{id}`
- `DELETE /api/v1/sessions/{id}`
- `POST /api/v1/sessions/{id}/messages`
- `GET /api/v1/sessions/{id}/messages`
- `POST /api/v1/sessions/{id}/clear`
- `POST /api/v1/sessions/{id}/fork`
- `POST /api/v1/sessions/{id}/fork-from-step`
- `POST /api/v1/sessions/{id}/revert`
- `POST /api/v1/sessions/{id}/regenerate`
- `POST /api/v1/sessions/{id}/stop`
- `PATCH /api/v1/sessions/{id}/star`
- `PATCH /api/v1/sessions/{id}/tags`
- `GET /api/v1/sessions/{id}/todos`
- `POST /api/v1/sessions/{id}/todos/resume`
- `GET /api/v1/sessions/{id}/trace`
- `GET /api/v1/sessions/{id}/trajectory/{step}`
- `GET /api/v1/sessions/{id}/journal`
- `GET /api/v1/journal/stats`

HITL：

- `POST /api/v1/tasks/{id}/input`
- `POST /api/v1/tasks/{id}/command`
- `GET /api/v1/tasks/{id}/pending-input`

模型和配置：

- `GET /api/v1/models`
- `PUT /api/v1/model`
- `POST /api/v1/config/save`
- `POST /api/v1/config/channels/feishu/reload`

工具：

- `POST /api/v1/tools/invoke`

资产与 KB：

- `GET /api/v1/assets/resolve`
- `GET /api/v1/assets/proxy`
- `GET /api/v1/kb/namespaces`
- `POST /api/v1/kb/namespaces`
- `GET /api/v1/kb/namespaces/{id}/documents`
- `POST /api/v1/kb/namespaces/{id}/documents:ingest-markdown`
- `GET /api/v1/kb/documents/{id}/tree`
- `GET /api/v1/kb/documents/{id}/nodes/{node_id}`
- `POST /api/v1/kb/documents/{id...}`
- `GET /api/v1/kb/evidence`
- KB binding 相关路由：`/api/v1/kb/bindings`、`/api/v1/kb/effective-bindings`

IM 和定时任务：

- `POST /api/v1/channels/push`
- `POST /api/v1/channels/push/schedules`
- `GET /api/v1/channels/push/schedules`
- `DELETE /api/v1/channels/push/schedules/{id}`
- `POST /api/v1/scheduled-tasks`
- `GET /api/v1/scheduled-tasks`
- `GET /api/v1/scheduled-tasks/{id}`
- `PUT /api/v1/scheduled-tasks/{id}`
- `DELETE /api/v1/scheduled-tasks/{id}`
- `POST /api/v1/scheduled-tasks/{id}/toggle`
- `POST /api/v1/scheduled-tasks/{id}/run-now`
- `GET /api/v1/scheduled-tasks/{id}/runs`
- `GET/POST /api/v1/channel/wecom/webhook`
- `POST /api/v1/channel/dingtalk/webhook`
- `POST /api/v1/channel/feishu/webhook`

Wechatbot：

- `GET /api/v1/wechat/status`
- `POST /api/v1/wechat/login`
- `POST /api/v1/wechat/relogin`
- `POST /api/v1/wechat/logout`
- `GET /api/v1/wechat/events`
- `GET /api/v1/wechat/conversations`

Auth 启用后才注册：

- `GET /api/v1/auth/providers`
- `GET /api/v1/auth/login`
- `GET /api/v1/auth/callback`
- `POST /api/v1/auth/login`
- `GET /api/v1/auth/me`
- `POST /api/v1/auth/refresh`
- 多个 `/api/v1/admin/*` 管理端路由，受 `auth.AdminOnly` 限制。

Gateway 启用后才注册：

- `POST /api/v1/rpc`
- `/api/v1/rpc/ws`

### 9.3 WebSocket

如果 `hitl.websocket_enabled=true`，注册：

```text
GET /api/v1/ws
```

当前 `webui.enabled=true` 时 `Resolve()` 会自动启用 WebSocket。WebSocket 支持：

- Origin 校验，默认允许本地开发端口 `3000`、`5173`、`8080` 和 server 自身端口。
- `hitl.websocket_token` token 认证。
- Auth 启用时 JWT 认证。
- 单 IP 最大连接数配置。
- ping interval 来自 `agent.ws_ping_interval`。

前端 `useWebSocket` 处理：

- `input_request` 写入 HITL store 和 inline approval。
- `message` 处理 partial/final/user/tool 消息。
- `agent_status` 更新 streaming 和错误状态。
- `tool_call` 更新工具执行状态。
- `task_group` / `task_progress` / `agent_progress` 更新任务进度。
- `todo_snapshot` 更新 todos。

### 9.4 Web UI 静态资源

`internal/webui/embed.go`：

- 嵌入 `internal/webui/dist/*`。
- `/api/` 路径不处理。
- 静态资源不存在且有扩展名时返回 404，避免 JS/CSS MIME 错误。
- 无扩展名路径 fallback 到 `index.html`，支持 React Router SPA。

## 10. 前端架构

### 10.1 路由

`frontend/src/App.tsx` 使用 lazy routes：

公开路由：

- `/login`
- `/auth/callback`

普通受保护路由，包裹 `AuthGuard` 和 `AppShell`：

- `/`：Chat landing
- `/sessions/:id`：Chat
- `/replay`：Replay gallery
- `/guide`
- `/settings`

独立全屏：

- `/sessions/:id/replay`

管理后台，包裹 `AdminGuard` 和 `AdminShell`：

- `/admin`
- `/admin/agents`
- `/admin/scheduled-tasks`
- `/admin/skills`
- `/admin/settings`
- `/admin/guide`
- `/admin/users`
- `/admin/usage`
- `/admin/auth-providers`
- `/admin/prompts`
- `/admin/quality-candidates`
- `/admin/quality-workbench`
- `/admin/memory-governance`
- `/admin/auto-optimization`
- `/admin/multi-agent`
- `/admin/llm`

旧路由 `/agents` 和 `/skills` 会重定向到管理后台。

### 10.2 API Client

`frontend/src/api/client.ts`：

- `BASE_URL = import.meta.env.VITE_API_BASE || ''`。
- 默认超时 30 秒。
- `postLong` 用于 LLM 等长请求，无超时。
- 自动附加 `Authorization: Bearer <auth_token>`。
- 非 auth 路径遇到 401 时会尝试 `refreshToken()`，失败后清理 auth 并跳转 `/login`。
- 204 返回 `undefined`。

### 10.3 状态管理

主要 Zustand stores：

| Store | 职责 |
| --- | --- |
| `store/auth.ts` | token、当前用户、刷新逻辑 |
| `store/chat.ts` | 消息、发送、streaming、工具状态、inline approval |
| `store/session.ts` | 会话列表和当前会话 |
| `store/ws.ts` | WebSocket 状态 |
| `store/hitl.ts` | HITL 请求 |
| `store/todos.ts` | Plan Runtime todos |
| `store/taskProgress.ts` | task group / agent progress |
| `store/agentActivity.ts` | Sidebar 状态点 |
| `store/replay.ts` | Replay 状态 |
| `store/canvas.ts` | Canvas artifact |
| `store/scheduledTasks.ts` | 定时任务 |
| `store/app.ts` | 主题、语言等应用级设置 |

### 10.4 Chat UI 分层

`DESIGN.md` 明确了 Chat UI 分层：

- `components/ai-elements/` 是 leaf primitives，例如 streamdown、Tool、CodeBlock、基础 UI 原语。
- `components/chat/` 是 Hive 业务壳，包括 MessageBubble、MessageList、ChatInput、TaskProgressPanel、ArtifactCard、ToolAdapter、HITL 等。

原则：业务逻辑、store 订阅、i18n、滚动聚合等放在业务壳；不要把 Hive 业务逻辑塞进 `ai-elements/`。

### 10.5 设计系统约束

做 UI 改动前读 `DESIGN.md`。当前设计方向：

- 工业/实用、控制台感，避免通用 AI SaaS 套板。
- 品牌主色是 light-blue，warning 保持 amber/orange。
- 使用 Geist / DM Sans / JetBrains Mono。
- 支持暗色模式。
- 业务工具 UI 应紧凑、可扫描、面向重复操作。
- Chat 页面采用业务壳 + 叶子原语分层。

## 11. 数据库与迁移

### 11.1 PostgreSQL 是唯一后端

`internal/store/doc.go` 写明当前使用 PostgreSQL 作为唯一数据库存储后端。`NewPostgresStore` 会：

1. 解析 DSN。
2. 创建 pgx pool。
3. Ping。
4. 执行 `pgMigrate`。
5. 启动 LISTEN 协程。

Server 模式下初始化失败会 fatal。

### 11.2 迁移来源

核心迁移在 `internal/store/postgres_migrate.go` 的 SQL 字符串里，使用大量 `CREATE TABLE IF NOT EXISTS` 和 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 做幂等迁移。

`migrations/` 目录也有飞书相关 SQL 文件，但当前主启动路径是 Go 里的 `pgMigrate`。

部分包自管理表：

- `internal/journal/pg_journal.go`
- `internal/taskboard/pg_taskboard.go`
- `internal/asset/pg_schema.go`
- `internal/sessiontodo/pg_store.go`

### 11.3 主要表域

核心会话：

- `sessions`
- `messages`
- `permission_grants`
- `oauth_tokens`
- `configs`
- `channel_configs`
- `mcp_servers`
- `external_resources`

LLM 与配置：

- `llm_providers`
- `llm_models`
- `hive_prompts`
- `hive_skills`

Memory / KB / Asset：

- `memories`
- `embedding_backlog`
- `memory_governance_policies`
- `kb_namespaces`
- `kb_documents`
- `kb_tree_nodes`
- `kb_bindings`
- `kb_evidence_events`
- `kb_node_assets`
- `assets`

质量与可观测：

- `usage_records`
- `hive_traces`
- `hive_metrics`
- `hive_logs`
- `hive_step_snapshots`
- `journal_sessions`
- `journal_tool_calls`
- `journal_file_changes`
- `journal_decisions`
- `agentquality_candidates`
- `agentquality_optimization_suggestions`
- `agentquality_shadow_eval_results`
- `optimization_eval_diffs`
- `optimization_approvals`
- `optimization_rollback_alerts`
- `optimization_rollbacks`
- `optimization_rollouts`
- `agentquality_grouping_rules`
- `qualityworkbench_replay_jobs`
- `qualityworkbench_batch_eval_runs`
- `qualityworkbench_weekly_reports`

Auth：

- `auth_providers`
- `users`
- `user_external_ids`
- `login_history`
- `user_quotas`
- `user_session_prefs`

IM / 定时任务：

- `scheduled_pushes`
- `scheduled_task_runs`，按周分区
- `feishu_event_dedup`
- `feishu_outbound_retry_queue`
- `feishu_chat_state`
- `wechat_conversations`
- `hive_session_todos`

## 12. Auth、Admin 与配额

Auth 只有在 `auth.enabled=true` 且 PG pool 可用时初始化。

初始化行为：

- 校验 `auth.frontend_url` 必须是 `http://` 或 `https://`。
- 解析或生成 JWT secret。
- 从配置 seed auth providers 到 DB。
- 从 DB 加载 OAuth/Credential providers。
- 没有 provider 时只打 warn，不会阻止启动。

用户行为：

- OAuth 登录时按 provider + external ID 查找或创建用户。
- 第一个创建的用户自动成为 `admin`。
- 用户状态必须是 `active` 才能使用。
- Admin API 通过 `auth.AdminOnly` 包装。
- IM 用户关联不会自动创建用户，只会关联已经通过 Web 登录注册的用户。

配额：

- AuthEngine 注入 Master 后，运行时可做 quota check 和 usage record。
- `usage_records` 由 CostTracker 记录，并有 90 天清理 worker。

## 13. IM Channel

### 13.1 Router

`internal/channel.Router` 管理：

- 平台插件。
- `platform:tenantKey:chatID -> sessionID` 绑定。
- 消息去重。
- 消息 debounce。
- retry queue。
- event claim。
- EventRenderer 订阅。
- IM sender 到内部用户 context 的关联。

默认 dedup backend 是内存实现；PG 可用时飞书路径会装配 PostgreSQL 版去重和重试队列。

### 13.2 平台插件

当前 bootstrap 注册：

- DingTalk：`cfg.Channel.DingTalk.Enabled` 时注册。
- Feishu：`cfg.Channel.Feishu.Enabled` 时注册，包含 webhook/longconn、chat state、renderer、retry worker、context resolver 等。
- WeCom：`cfg.Channel.WeCom.Enabled` 时注册。
- WeChatBot：总会注册插件和 service，但实际是否允许扫码/运行由 `cfg.Channel.WeChatBot.Enabled` 等配置决定。

### 13.3 Renderer

Master 的 EventBus 同时服务 WebSocket 和 IM renderer。飞书 renderer 是首个主要实现：

- 订阅本 session 的事件流。
- 收到 `input_received` 后做 ack。
- 把 message/tool_call/input_request 等事件增量 PATCH 到同一张卡片。
- 失败时回落 legacy `Plugin.Send`。

### 13.4 IM 工具

IM 相关工具包含：

- `send_im_message`
- `im_api`
- 飞书工具集合

`im_api` 由 `agent.im_api.enabled` 控制，可配置 dry run 和是否优先于 legacy。

## 14. Sandbox 与命令执行

`internal/sandbox.Executor` 是统一命令执行接口：

```go
type Executor interface {
    Execute(ctx context.Context, req ExecRequest) (ExecResult, error)
    Close() error
}
```

Server 模式由 `bootstrap.initExecutor` 创建 executor：

- `sandbox.enabled=false`：使用 LocalExecutor。
- `sandbox.type=docker`：先检查 Docker 可用；不可用时 fatal。
- 其他值或 local：使用 LocalExecutor。
- 最外层统一包一层 `SafeExecutorWrapper`，安全 checker 延迟由 Master 注入。

Docker sandbox 配置包括 image、CPU、内存、pids、tmpfs、network、seccomp、workdir 是否只读等。

命令安全相关：

- `internal/security` 提供 rule-based allow/ask/deny。
- `internal/router` 提供工具级能力和风险策略。
- `tools.SetExecutorChecker` 把 Master 安全策略接到 executor 主路径。
- HITL 审批桥由 `tools.SetApprovalBridge` 注入。

## 15. Memory、KB、Asset

### 15.1 Memory

Memory 只有 `cfg.Memory.Enabled=true` 且 DB 是 PostgreSQL 时启用。

启用后：

- 创建 `memory.PostgresMemoryStore`。
- 注入 metrics recorder。
- 创建 injector，并注入 Master。
- 可选启用 embedding 搜索和 backlog worker。
- 关闭时必须在 DB pool 关闭前完成。

Memory 管理 API 主要在 admin 路由下，包括 governance、prune、export/import、injection explain、promotion、vector-space plan、backlog stats、production metrics。

### 15.2 KB

PG pool 可用时 bootstrap 创建 `kb.Service`。KB 当前包含：

- namespace
- markdown document ingest
- tree nodes
- evidence events
- bindings
- node assets

KB 文档导入接口是 `POST /api/v1/kb/namespaces/{id}/documents:ingest-markdown`，只接受 `multipart/form-data`。字段约定：

- `file`：Markdown/TXT/DOCX/PDF 文档文件，最多一个。
- `assets`：Markdown 引用的图片文件，可重复。
- `asset_path` / `asset_alt_text` / `asset_caption`：与 `assets` 同序的可选元数据。
- `markdown` / `content`：不上传文件时的直接 Markdown 文本。

非 multipart 请求返回 415。系统未上线，无老客户端兼容负担；不要重新引入 JSON/base64 ingest API。Markdown 中的 data URI 图片可以作为文档内容输入，但必须进入 KB asset ingest 并重写为 `asset://`，不能保存为 base64。

Master 可通过 KB evidence reader 在回复中使用证据。`kb.section.text` 返回节点文本和结构化 `asset_refs`；按 `node_ids` 取证时返回命中节点图片，按 `page_ranges` 取证时只返回带页码且落在页范围内的图片。前端展示资产时通过 `/api/v1/assets/resolve` 获取短时 URL。

KB 的 PageIndex 对齐点在运行时工具契约，而不是引入 PageIndex workspace 或向量库：`kb.doc.meta -> kb.doc.structure -> kb.section.text`。`kb.doc.meta` 返回 `page_count`、`line_count`、`node_count`，用于对齐 PageIndex 的 document metadata 第一步；`kb.doc.structure` 不返回正文，但会暴露 `start_page/end_page`；当文档由 MinerU/external PDF provider 产出的 Markdown 保留页锚时，Agent 可以用 `page_ranges` 调 `kb.section.text`，服务端会按页范围重查命中的 tree nodes、切出对应页正文、记录 evidence，并返回该页范围内的 `asset_refs`。质量验收可用 `internal/agentquality.ScoreKBPageIndexRetrieval` 对 expected node/page range、retrieved node/page range、citation node/page range 进行 PageIndex-style 命中率统计。

### 15.3 Asset

AssetService 支持 `local`、`minio` 和 `s3` provider：

- `local` 对象体写入本地 `asset.local.base_path`。
- `minio` / `s3` 通过同一个 S3-compatible store 写入对象存储；`minio` 用显式 endpoint，`s3` 可用 AWS 默认 endpoint 或其他 S3-compatible endpoint。
- PG 保存 metadata。
- `asset://` 是内部 URI。
- HTTP resolve 会做 metadata 和权限校验后返回短时 URL；local provider 会改写为同源 proxy URL，MinIO/S3 返回 presigned GET URL。
- Docker Compose 默认 `asset.provider=minio`，`minio-init` 会创建 `MINIO_BUCKET`；生产可以使用 `asset.provider=s3` 接 AWS S3、百度 BOS 等 S3-compatible 服务，HTTP endpoint 需要显式设置 `s3.use_ssl=false` / `S3_USE_SSL=false`。

当前 access resolver 默认允许 user owner scope 且 `owner_id == user_id` 的普通资产访问；`source_kind=kb_document_image` 会走 KB resolver 校验 owner/domain/binding/document/node，聊天页展示 `kb.section.text` 资产时使用 `purpose=kb_section_text` 并带当前 `session_id/domain_id`，不能用 `kb_management` 绕过运行态 binding；`source_kind=agent_artifact` 和 `source_kind=chat_attachment` 都必须带匹配的 `session_id` 才能 resolve。

## 16. Quality Control Plane

质量治理相关包：

- `internal/agentquality`：候选用例、评估、建议、approval、rollback、shadow eval。
- `internal/qualityworkbench`：replay job、batch eval、weekly report、grouping rules、dashboard。
- `internal/journal`：开发过程 journal。
- `internal/observability`：trace/metric/log。
- `internal/trajectory`：step snapshot。

Server 初始化时：

- PG 可用时创建 Agent Quality PG stores。
- API server 默认先创建 in-memory stores，检测到 PG store 后替换为 PG stores。
- Master 注入 shadow eval runner，默认 sampler 配置在 `cmd/server/main.go` 中是 enabled、5% 采样、最大并发 2。
- CostTracker 记录 usage，Admin 用量 API 使用。

质量工作台 Admin 路由覆盖：

- candidates
- prompt smoke
- replay jobs
- batch evals
- grouping rules
- fanout
- version diff
- reports
- dashboard series/snapshot
- optimization suggestions / approvals / rollbacks / eval diffs

## 17. Spec-driven 与 Plan Runtime

### 17.1 Spec-driven

`config.SpecDrivenConfig` 默认：

- `mode=legacy`
- `continuation.default=off`
- `planner.token_budget=800`

含义：

- `legacy`：spec 路径短路，零成本。
- `dual`：spec + legacy 双跑，响应以 legacy 为准。
- `spec`：spec 为 primary，legacy fallback。

如果 `subagent_mode` 或 `skills_semantic_routing` 开启但 `mode=legacy`，启动期会 fail-fast。

### 17.2 Plan Runtime

`agent.plan_runtime.enabled` 默认开启。启用后：

- bootstrap 初始化 `SessionTodoStore`。
- 注册 `todo_write`、plan mode 工具、handoff summary 工具。
- Master 广播 `todo_snapshot` 和 `plan_mode_changed`。
- 前端 `store/todos.ts` 和相关组件展示 todos。

PG 可用时 todos 持久化到 `hive_session_todos`，否则回退内存。

## 18. Gateway 与 ACP

### 18.1 Gateway

`gateway.enabled=true` 时创建 `internal/gateway.Gateway`：

- token 认证来自 `gateway.tokens`。
- 注册 `/api/v1/rpc` 和 `/api/v1/rpc/ws`。
- Gateway 方法依赖 Master、SkillRegistry、ChannelRouter、PluginLoader、MCPHost、ACP pool、Store、AIRouter 和热加载函数。

### 18.2 ACP

CLI `--acp` 模式会通过 `internal/cli.App.RunACP` 以 ACP 协议运行。

Server 模式下：

- `internal/acpclient` 根据 `remote_agents` 配置连接远程 ACP Agent。
- 成功连接后注册到 Master。
- `internal/acpserver` 提供 ACP server 相关实现和测试。

## 19. 开发命令

后端：

```bash
go build -o claw ./cmd/claw
go build -o server ./cmd/server
go test ./... -v
go test -race ./...
go test -cover ./...
```

Makefile：

```bash
make test
make test-specdriven
make frontend-build
make frontend-embed
make hive-build
make hive-run
make build-sandbox-image
make docker-build
make docker-up
make docker-down
make docker-logs
make validate-skills
```

前端：

```bash
cd frontend
npm install
npm run dev
npm run build
npm run lint
npm test
npm run test:e2e
```

前端命令以 `package-lock.json`、`package.json`、`vite.config.ts` 为准；当前使用 npm，开发端口为 3000。

## 20. 测试与 CI 辅助脚本

Go 测试：

- 单包测试优先放在对应 package 旁边。
- 回归测试在 `tests/regression/`。
- Spec-driven 关键测试由 `make test-specdriven` 跑 `./internal/specdriven/...`、`./internal/master/...`、`./internal/store/...`，带 race 和覆盖率检查。

前端测试：

- Vitest 测试散落在 `frontend/src/**/__tests__` 和同目录 `.test.ts(x)`。
- E2E 使用 Playwright，命令是 `npm run test:e2e`。

辅助脚本：

- `scripts/check_specdriven_coverage.sh`
- `scripts/check_write_contract_boundaries.sh`
- `scripts/check_run_quality_foundation_guard.sh`
- `scripts/ci/check_imctx_leaf.sh`
- `scripts/ci/check_no_fail_open.sh`
- `scripts/ci/check_pii_safe_sender.sh`
- `scripts/ci/check_session_scope.sh`
- `scripts/ci/check_webhook_handler_nil_err.sh`
- `scripts/ci/check_feishu_sdk_only.sh`
- `scripts/ci/check_feishu_phantom_symbols.sh`

## 21. 开发约定

Go：

- 使用 `gofmt`。
- 包名小写。
- 按仓库约定，注释和日志倾向中文，错误保持结构化。
- 测试优先表驱动。
- 不要绕开 `internal/errs`、结构化日志和现有错误模式。

前端：

- TypeScript 2 空格缩进、分号、单引号。
- 组件 PascalCase，hooks/utilities camelCase。
- 不绕过 `frontend/eslint.config.js`。
- UI 改动前读 `DESIGN.md`。
- `internal/webui/dist/` 是生成产物，不能手工编辑。

Git / PR：

- 近期提交使用 Conventional Commits，例如 `feat(chat): ...`、`fix(sidebar): ...`。
- PR 应说明行为变化、影响区域、测试命令、相关 issue、UI 截图。

安全：

- 不提交 `config.json` 中的真实密钥。
- 不提交 `.env`。
- `OPENAI_API_KEY`、`CLAW_API_KEY` 等通过环境变量或运行时配置注入。

## 22. 新功能开发建议

### 22.1 新增 API

1. 在 `internal/api/*_handlers.go` 中实现 handler。
2. 在 `internal/api/routes.go` 注册路由。
3. 如果需要 Admin 权限，放在 `authEngine != nil` 块内并包 `auth.AdminOnly`。
4. 如果依赖 DB store，提供 nil 时的 503 或合理 fallback。
5. 添加 handler 单元测试。
6. 前端在 `frontend/src/api` 或 node client 中补方法。

### 22.2 新增工具

1. 在 `internal/tools` 中实现注册函数和执行逻辑。
2. 通过 `mcphost.ToolDefinition` 给出准确 schema、描述、Core、并发安全标记。
3. 在 `RegisterBuiltinTools` 或条件注册点接入。
4. 在 `internal/router/capability_registry.go` 声明风险和能力。
5. 如有危险动作，在 `ToolActionRiskRule` 或 mixed action rule 中补策略。
6. 添加单元测试，尤其是权限/错误/边界输入。

### 22.3 新增运行时配置

1. 在 `internal/config.Config` 或子结构中加字段。
2. 补默认值和 normalize/resolve 逻辑。
3. 如果是运行时配置，补 DB seed/migrate/load/save。
4. 如前端可配，补 API handler 和管理页。
5. 注意 config 文件、环境变量、DB 覆盖之间的优先级。

### 22.4 新增前端页面

1. 在 `frontend/src/pages` 或 `pages/admin` 新建页面。
2. 在 `App.tsx` lazy import 并挂路由。
3. 如需布局，接入 `AppShell` 或 `AdminShell`。
4. 如需权限，确认是否在 `AuthGuard` / `AdminGuard` 下。
5. 复用现有 components/store/api 模式。
6. 运行 `npm run lint` 和 `npm run build`。

## 23. 常见排障

### 23.1 PostgreSQL 启动失败

现象：Server fatal，日志出现 PostgreSQL ping 或 migrate 失败。

检查：

- `DATABASE_URL` 是否覆盖了拆分字段。
- `POSTGRES_*` 是否正确。
- 数据库用户是否有建表/改表权限。
- `ssl_mode` 是否符合本地/生产环境。

### 23.2 前端连不上后端

检查：

- Vite dev server 当前端口是 `3000`。
- 后端是否在 `8080`。
- 如果使用 proxy，前端请求应走 `/api/...`。
- 如果使用 `VITE_API_BASE`，确认 CORS 和 CSP。
- Auth 启用时 token 是否过期，前端是否跳 `/login`。

### 23.3 WebSocket 无消息

检查：

- `/api/v1/ws` 是否注册。`webui.enabled=true` 通常会自动启用。
- Origin 是否在允许列表。
- Auth/JWT 或 `hitl.websocket_token` 是否匹配。
- 前端当前 session id 是否与消息 payload session id 匹配，`useWebSocket` 会过滤其他 session。

### 23.4 Docker sandbox 不工作

检查：

- 是否先构建 `hive-sandbox:latest`。
- Hive 容器是否挂载 `/var/run/docker.sock`。
- `DOCKER_GID` 是否等于宿主机 docker.sock gid。
- `/opt/hive/workdir` 在宿主机和容器内路径是否一致。
- `sandbox.type=docker` 时 Docker 不可用会导致 Server fail-fast。

### 23.5 前端构建后页面 404 或 JS MIME 错误

检查：

- 是否运行了 `cd frontend && npm run build`。
- 产物是否在 `internal/webui/dist/`。
- 访问前端路由时应由 Go SPA fallback 返回 `index.html`。
- 静态资源带扩展名不存在时会返回 404，不会 fallback。

### 23.6 Auth 开启后 Admin 页面不可访问

检查：

- `auth.enabled=true` 且 PG 可用。
- 是否已配置 OAuth/LDAP provider。
- 第一个注册用户会自动成为 admin。
- 用户 `status` 是否 active，`role` 是否 admin。

### 23.7 IM 消息重复或丢失

检查：

- 飞书 PG dedup/retry 表是否正常。
- `feishu_event_dedup`、`feishu_outbound_retry_queue`、`feishu_chat_state` 是否存在。
- dedup backend 故障时路由会 fail-closed，可能进入 retry queue。
- renderer 失败会尝试 legacy send fallback。

### 23.8 Prompt 修改不生效

PromptLoader 优先级是 `DB > 文件 > go:embed`。

检查：

- 是否通过 Admin API 写入了 DB prompt。
- PG NOTIFY 是否正常。
- `PromptLoader.InvalidateDBCache` 是否被触发。
- 如果使用文件 prompt，`prompts_dir` 是否设置正确。

## 24. 当前注意事项

这些是阅读源码时确认到、开发时仍应留意的事项：

- `ServerComponents.StartChannels` 当前是空函数，实际 channel 插件在 `initChannels` 阶段注册/启动。
- `migrations/` 目录不是唯一迁移来源；核心迁移在 `internal/store/postgres_migrate.go`。
- `config.example.json` 只展示启动配置子集，很多运行时默认值由 DB seed/migrate 提供。
