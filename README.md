# agents-hive

> 生产级多 Agent 系统，基于 ReAct 架构，支持多会话管理、IM Channel 集成和工具调用

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## 快速开始

### 生产部署（Ubuntu 服务器）

> 前提：镜像已在本地构建并推送至镜像仓库（见下方[本地构建镜像](#本地构建镜像)）。

**在服务器上执行：**

```bash
# 1. 克隆仓库（仅需 docker-compose.yml，不需要源码）
git clone https://github.com/chef-guo/agents-hive.git
cd agents-hive

# 2. 创建 .env（只有 POSTGRES_PASSWORD 必填，LLM Key 启动后可在 Web UI 配置）
cat > .env <<EOF
POSTGRES_PASSWORD=your_strong_password
DOCKER_GID=$(stat -c '%g' /var/run/docker.sock)   # 查询宿主机 docker.sock gid
TZ=Asia/Shanghai
HIVE_PORT=8080
EOF

# 3. 创建宿主机工作目录（sandbox 挂载路径，必须与 docker-compose.yml 一致）
mkdir -p /opt/hive/workdir/sessions

# 4. 拉取镜像并启动
docker compose pull
docker compose up -d

# 5. 查看日志
docker compose logs -f hive
```

浏览器访问 `http://<服务器IP>:8080`

---

### 本地构建镜像

```bash
# 克隆源码
git clone https://github.com/chef-guo/agents-hive.git
cd agents-hive

# 构建主服务镜像（多阶段：前端 → Go 二进制）
docker build -t your-registry/hive:latest .

# 构建 sandbox 镜像（agent 代码隔离执行用）
docker build -t your-registry/hive-sandbox:latest -f docker/sandbox/Dockerfile .

# 推送至镜像仓库
docker push your-registry/hive:latest
docker push your-registry/hive-sandbox:latest
```

> 将 docker-compose.yml 中的 `image: hive:latest` 和 sandbox 配置改为你的仓库地址。

### 本地开发（不使用 Docker）

```bash
# 克隆并编译
git clone https://github.com/chef-guo/agents-hive.git
cd agents-hive
go build -o claw ./cmd/claw
go build -o server ./cmd/server

# 配置
cp config.example.json config.json
# 编辑 config.json，填入 API Key 和数据库连接信息

# CLI 模式（单次任务）
./claw "请分析 main.go 的代码结构"

# 交互式会话模式
./claw -i

# 服务器模式
./server
```

---

## 架构概览

```
┌─────────────────────────────────────────────────────────────────┐
│                         用户界面                                 │
│           CLI / HTTP API / WebSocket / IM Channel               │
└────────────────┬──────────────────────┬─────────────────────────┘
                 │                      │
    ┌────────────▼────────────┐  ┌──────▼──────────┐
    │    Gateway (JSON-RPC)   │  │  Channel Router  │
    │  HTTP + WebSocket RPC   │  │  钉钉/飞书/微信   │
    └────────────┬────────────┘  └──────┬──────────┘
                 │                      │
    ┌────────────▼──────────────────────▼─────────────────────────┐
    │                   Master Agent                               │
    │  SessionLoop · ReAct · HITL · Security · ContextCompress   │
    └────────┬───────────────┬───────────────┬────────────────────┘
             │               │               │
        ┌────▼────┐    ┌────▼────────┐  ┌───▼──────┐
        │ Skills  │    │  SubAgents  │  │ Plugins  │
        │Registry │    │   Explore   │  │ (子进程)  │
        └─────────┘    └─────────────┘  └──────────┘
        ┌─────────┐
        │  Tools  │
        │ (MCP)   │
        └─────────┘
```

**技术栈**: Go 1.25+ / openai-go v1.12 / MCP Go SDK / ACP Go SDK / zap / pgx

---

## 核心特性

- **15+ 内置工具**: read_file、write_file、edit、glob、grep、bash、web_search、web_fetch、LSP (9 种操作)、batch、multiedit、apply_patch 等
- **SubAgent 体系**: Explore Agent (快速探索)、Title、Summary、Compaction
- **多会话管理**: 创建、切换、Fork、Revert，PostgreSQL 持久化
- **Skills 系统**: 声明式 Markdown 指令包，支持动态上下文 (`!`command``)、脚本执行、生命周期 hooks
- **IM Channel 集成**: 钉钉、飞书、企业微信、微信 (WeChatPadPro/Wechaty)
- **权限策略（SafeExecutor-first）**: shell 家族工具走 `MatchPolicy` (Allow/Ask/Deny)；非 shell 工具在 minimal 模式默认放行；IM 通道自动允许 Ask 但 Deny 不可绕过；`strict` 模式一键回滚到全量 HITL。详见 [docs/架构设计/安全权限模型.md](docs/架构设计/安全权限模型.md)
- **9+ LLM Provider**: OpenAI、Anthropic、Google、Azure、Groq、Mistral、Bedrock、DeepSeek、自定义
- **安全执行系统**: 命令白名单 (allow/ask/deny)、环境变量监控
- **ACP 控制平面**: 多会话限流、绑定管理
- **上下文压缩**: LLM 摘要 + tiktoken 精确计数，自动降级
- **Plan Runtime + Session Todos**: 长任务自动 checkpoint / resume,session-scoped todos 实时 UI 可见,Plan Runtime Guard 解耦"LLM 一轮结束"与"任务完成"。默认开启 (`agent.plan_runtime.enabled=true`)。`enter_plan_mode` / `todo_write` / `finish_plan` / `exit_plan_mode` 工具供 agent 自主进出计划态。详见 [docs/计划与路线/Agent-计划状态与Todos实时化重构计划.md](docs/计划与路线/Agent-计划状态与Todos实时化重构计划.md)
- **System Prompt 治理**: 7 段独立 prompt (`base/execution/plan_runtime/business/code_editing/safety/reply`),PromptLoader 三层优先级 (DB override > 文件 > `//go:embed` 内置),管理台支持热更新与 smoke eval。详见 [docs/计划与路线/Agent-System-Prompt重整方案.md](docs/计划与路线/Agent-System-Prompt重整方案.md)

---

## 配置

Docker 部署使用纯环境变量，无需 config.json。本地开发使用 config.json（参见 `config.example.json`）。

### 环境变量一览

优先级: CLI 标志 > 环境变量 > config.json > 默认值

| 环境变量 | 说明 | 默认值 |
|---------|------|--------|
| `DATABASE_URL` | PostgreSQL 完整 DSN（优先于下方拆分字段） | — |
| `POSTGRES_HOST` | 数据库地址 | `localhost` |
| `POSTGRES_PORT` | 数据库端口 | `5432` |
| `POSTGRES_DB` | 数据库名 | `claw` |
| `POSTGRES_USER` | 数据库用户 | `claw` |
| `POSTGRES_PASSWORD` | 数据库密码 | — |
| `POSTGRES_SSL_MODE` | SSL 模式 | `disable` |
| `SESSIONS_DIR` | 会话存储目录 | `~/.claw/sessions` |
| `CUSTOM_TOOLS_DIR` | 自定义工具目录 | `.claw/tools` |
| `CLAW_PROVIDER` / `CLAW_MODEL` / `CLAW_API_KEY` / `CLAW_BASE_URL` | LLM 配置（可选，也可启动后在 Web UI 配置） | — |
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` | OpenAI 备用配置（可选，也可启动后在 Web UI 配置） | — |
| `CLAW_LOG_FILE` / `CLAW_LOG_LEVEL` / `CLAW_CONSOLE_LEVEL` | 日志配置 | — |

---

## CLI 使用

### 命令行标志

| 标志 | 简写 | 说明 |
|------|------|------|
| `--config <file>` | `-c` | 指定配置文件 |
| `--interactive` | `-i` | 交互式模式 |
| `--verbose` | `-v` | 详细日志输出 |

### 交互式命令

| 命令 | 说明 |
|------|------|
| `/session new [name]` | 创建会话 |
| `/session list` | 列出会话 |
| `/session switch <id>` | 切换会话 |
| `/model` / `/model <name>` | 列出/切换模型 |
| `/skills` | 列出技能 |
| `exit` / `quit` | 退出 |

---

## HTTP API

基础 URL: `http://localhost:8080/api/v1`

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/sessions` | 创建会话 |
| `GET` | `/sessions` | 列出会话 |
| `POST` | `/sessions/{id}/messages` | 发送消息 |
| `GET` | `/ws` | WebSocket 实时推送 |

WebSocket 事件类型: `input_request`、`message`、`agent_status`、`tool_call`、`agent_start`、`skill_exec`、`error`、`ping`

---

## 项目结构

```
agents-hive/
├── cmd/
│   ├── claw/              # CLI 入口
│   └── server/            # HTTP Server 入口
├── internal/
│   ├── master/            # Master Agent (ReAct 核心)
│   ├── subagent/          # SubAgent 框架
│   ├── skills/            # Skills 系统
│   ├── tools/             # 内置工具集 (MCP)
│   ├── llm/               # LLM 客户端 (多 Provider)
│   ├── lsp/               # LSP 集成
│   ├── store/             # 持久化 (PostgreSQL)
│   ├── memory/            # 记忆系统 (向量搜索)
│   ├── channel/           # IM Channel (钉钉/飞书/微信)
│   ├── gateway/           # JSON-RPC 网关
│   ├── security/          # 安全执行系统
│   ├── controlplane/      # ACP 控制平面
│   ├── acpserver/         # ACP 服务端
│   ├── acpclient/         # ACP 客户端
│   ├── a2abridge/         # A2A 协议桥接
│   ├── config/            # 配置管理
│   ├── errs/              # 结构化错误
│   ├── i18n/              # 国际化提示
│   ├── mcphost/           # MCP 工具宿主
│   ├── streaming/         # WebSocket 流
│   ├── fileconv/          # 文件格式转换
│   └── plugin/            # 插件运行时
├── frontend/              # Web 前端 (React + Vite)
├── skills/                # 技能定义
└── config.example.json    # 配置示例
```

---

## 开发指南

### 代码规范

- **所有注释和日志使用中文**（第三方依赖除外）
- 错误使用 `errs.New(code, msg)` 创建结构化错误
- 日志使用 `zap.Logger` 依赖注入
- Channel 通信替代共享内存
- 表驱动测试（table-driven tests）

### 测试

```bash
go test ./... -v          # 运行所有测试
go test -race ./...       # 竞态检测
go test -cover ./...      # 覆盖率
```

### 构建

```bash
go build -o claw ./cmd/claw
go build -o server ./cmd/server
```

---

## Skills On-Demand（v2.4+ 新特性，灰度）

运行期按需从 marketplace 拉取并安装 skill，区分 public / personal scope。

快速启用（最小配置）：

```yaml
agent:
  skills:
    on_demand_enabled: true             # 灰度主开关（默认 false，向后兼容）
    marketplace_urls:
      - https://skills.example.com/
```

详见 `docs/架构设计/skills/Skill-按需加载总览.md`。其他文档入口：

- 协议规格：`docs/架构设计/Skill-市场协议.md`
- scope 语义：`docs/架构设计/skills/Skill-Scope与覆盖关系.md`
- 安全模型：`docs/架构设计/skills/Skill-安装安全模型.md`
- Feature flag 矩阵：`docs/架构设计/skills/Skill-Feature-Flag矩阵.md`
- SubAgent 身份继承：`docs/subagent-identity-inheritance.md`

---

## Roadmap

以下为后续规划，按优先级排列：

| 优先级 | 事项 | 说明 |
|--------|------|------|
| P2 | CI/CD 流水线 | GitHub Actions 自动化测试 + Codecov 覆盖率报告 |
| P3 | API 文档 | OpenAPI/Swagger 规范、CONTRIBUTING.md、后端排错手册 |
| P3 | Agent 市场 | Agent 上传/下载、评分评论、版本管理 |
| P3 | Skill checksum 强制化 | `index.json.checksum` 强校验 + 签名机制（skills-on-demand follow-up）|

---

## 许可证

MIT License

## 联系方式

- **Issues**: https://github.com/chef-guo/agents-hive/issues
