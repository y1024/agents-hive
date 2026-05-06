# im-streaming-reply Live Smoke SOP

> ⚠️ **降级声明（2026-04-20, session-scope-regression-matrix Phase 4.6）**：
> 自本日起，本 runbook **降级为 CI 故障兜底参考**，不再作为"触及 `feishu/renderer.go` / `react_processor.go` PR"的 on-call 签字合并阻塞。合并准入改由 `.github/workflows/e2e-session-scope.yml`（lint + go-tests + browser 三 job 聚合）物理 CI 证据承担。
> PR template 里对 `feishu/renderer.go` / `react_processor.go` broadcast 路径的 on-call 签字要求同步删除。本 SOP 保留仅用于：(a) CI 出现非 reproducible 假绿时做 live 对照；(b) 历史 session-scope 回归的 forensics 参考。

承接 `openspec/changes/im-streaming-reply/tasks.md` 的 11.4 / 11.5 / 11.7 / 11.8——这四条属于活体验证（需飞书 longconn + 前端浏览器），不在 CI 覆盖范围内，归档时迁出到本 runbook。原 on-call 签字放行合并阻塞已由 `e2e-session-scope.yml` 替代（见上方降级声明）。

## 前置准备

> ⚠️ **UI 截图/选择器警告**：本 runbook 11.8 章节用到的 Chrome DevTools Network/WS Frames 路径，以及任何浏览器截图基线，会在 `openspec/changes/chat-ui-polish/`（amber→light-blue 全站换色 + ToolCallCard 拆分）和 `openspec/changes/chat-ui-migrate-ai-elements/`（`useWebSocket` → `useHiveAgentEvents` hook）上线后**全部失效**。届时本 runbook 必须重录。

### 0.1 环境

- 本机或预发机：`go run ./cmd/server/main.go --config config.json`（**注意：本仓库不编译 `./hive` 二进制——务必走 `go run`，否则会启另一个 PID 卡 8080 端口**）
- 飞书开发者后台 App 已启用 **longconn + webhook 两条订阅路径**（11.4 / 11.5 分别走不同路径，必须都通）
- 浏览器一台（Chrome DevTools 可用）

### 0.2 Config 字段（必查）

| 字段 | 取值 | 验证方式 |
|---|---|---|
| `feishu.renderer.enabled` | `true`（11.4/11.5/11.8）/ `false`（11.7 反例） | `grep -E "renderer\\.enabled" config.json` |
| `feishu.bot.subscribe_mode` | `longconn`（11.4） / `webhook`（11.5） | 启动日志含 `subscribe_mode=<mode>` |
| `master.show_agent_progress` | `true` | 影响 11.8 是否能收到 `agent_progress` 事件 |
| `master.session_scope.strict_filter` | `true` | 启用 spec 12.4 session-scoped event drop 行为 |

### 0.3 Log Level

启动时附加 `--log-level=debug`：
```bash
go run ./cmd/server/main.go --config config.json --log-level=debug > /tmp/hive.log 2>&1 &
```

证据 `grep` 命令依赖 `debug` 级日志才能命中：
- `PATCH.*interactive_card`（11.4 卡片更新）
- `AddReaction`（飞书表情）
- `WebSocket session-mismatch drop`（11.8 反例信号）

### 0.4 session_id 获取

11.8 浏览器步骤需要拼接 URL `http://localhost:8080/sessions/<session_id>`。三种获取方式（任选一）：

1. **前端**：飞书发完消息后看 `/tmp/hive.log` 找 `session_id=im-feishu-<openid>-<chat_id>` 一行
2. **后端 API**：`curl -s http://localhost:8080/api/v1/sessions | jq -r '.[0].id'`（取最新 session）
3. **从 IM EventRenderer 日志反查**：`grep 'EventRenderer.*session_id=' /tmp/hive.log | tail -1`

**约定**：全文出现的 `<session_id>` 占位符均指上述任一方式得到的真实值，不要复制粘贴尖括号本身。

### 0.5 失败兜底

任一前置不满足则**禁止跑后续 11.x 步骤**——记录缺哪一项，回到环境侧补齐再来。前置不齐就跑 = 假阳性，比不跑更危险。

## 11.4 · 飞书 longconn bash 工具链冒烟

```bash
# 启动
go run ./cmd/server/main.go --config config.json > /tmp/hive.log 2>&1 &
```

发消息（飞书机器人对话）：
```
帮我 ls 一下当前目录
```

**证据要求**（附 PR）：
1. 飞书卡片初始状态应出现 💭 / ⚙️ 状态块；工具返回后卡片 PATCH 更新含 bash 结果
2. `grep -E "PATCH.*interactive_card|AddReaction" /tmp/hive.log` 非空
3. 完整 LLM 回复 finish_reason=stop 后，卡片落到 ✅ 终态

## 11.5 · Webhook 路径等价验证

**背景**：历史上一个 Claude 漏过这一项——longconn 表情触发了但 webhook 路径没覆盖。本步是显式反例。

切换飞书后台订阅方式到 webhook（或 config 中关 longconn 留 webhook），重启。发同样的 `帮我 ls 一下当前目录`。

**断言**：
- 飞书 GET 表情（AddReaction）**同样**出现在 webhook 路径
- 卡片 PATCH 节奏与 longconn 路径一致

## 11.7 · Renderer 禁用反例

把 `cfg.Feishu.Renderer.Enabled` 切 `false`，重启，发同样消息。

**断言**：
- 飞书消息**无**卡片（降级为一次性文本）
- `/tmp/hive.log` 无 PATCH / 无 AddReaction / 无 card_builder 记录
- 功能退化到 X-1 之前的 Send 一次性路径 → 向后兼容未破

## 11.8 · 前端跨 surface 验证

飞书发消息的同时，浏览器打开同一会话 `http://localhost:8080/sessions/<session_id>`，DevTools → Network → WS → Frames。

**断言**：
- WS 握手 URL 含 `?session_id=<id>`
- 收到 `input_received` 事件，payload 含 `ChannelMessageID`（证明飞书入口和 WS 订阅都走同一 EventBus）
- 收到 `message` / `tool_call` / `agent_progress` / `error` 任一事件流
- 没有 `WebSocket session-mismatch drop` 警告行

## 失败回退

任一步失败：
1. 不合并 PR
2. 抓完整 `/tmp/hive.log` + 浏览器 DevTools WS frames 截图
3. 回到 `openspec/changes/subagent-session-scoping` 或新开 X-1f change 跟踪
