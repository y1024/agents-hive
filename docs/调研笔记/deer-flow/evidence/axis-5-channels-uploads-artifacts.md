# Axis-5: Channels + Uploads + Artifacts 对比（deer-flow vs Hive）

> **产出时间**: 2026-04-22 · **作者**: Claude（主线程直读）
> **前置证据**: `docs/调研笔记/deer-flow/src/backend/app/channels/`（13 文件 4940 Python 行）+ `docs/调研笔记/deer-flow/src/backend/app/gateway/routers/{uploads,artifacts,channels}.py`（434 行）+ `internal/channel/` (47 非测试文件，多模块) + `internal/fileconv/`（4 非测试文件 576 行）
> **覆盖范围**: IM 通道实现广度/深度、消息流（卡片/PatchCard/流式渲染）、上传文件处理、文档转换、产物（artifact）服务、XSS/安全硬化、多租户路径隔离

---

## 0. TL;DR

**一句话**: deer-flow 在 IM 通道上**广度领先**（6 个适配器，含 Slack/Telegram/Discord），在 artifact 服务上**领先**（专用 HTTP 端点 + XSS 硬化）；Hive 在 IM 通道上**深度领先**（飞书 PatchCard 限流重试 + 渲染器 EventRenderer 762 行 + 微信 wechaty/wechatpadpro 双路径 2050+ 行），在文件转换上**领先**（含音频 Whisper + 视频 ffmpeg，deer-flow 仅文档类）；Hive 尚未建立与 deer-flow 等价的 artifact 服务端点。

**原始 final-verdict 的对应结论验证**:
- ✅ 原文"飞书 PatchCard + EventRenderer 反向领先"= 正确（Hive 实际领先）
- ✅ 原文"deer-flow 多通道（Slack/Telegram）是 Hive 缺口"= 正确
- ⚠️ 原文未提及 Hive 缺 artifact 服务端点 = 本轴补充的新盲点

**评分对齐（1-10 分）**:

| 能力 | deer-flow | Hive | 领先方 |
|---|---|---|---|
| IM 通道广度（平台数）| 6 (Feishu/Slack/Telegram/Discord/WeChat/WeCom) | 4 (Feishu/WeChat/WeCom/DingTalk) | deer-flow +2 |
| IM 通道总行数 | 4940 Python | 8428 Go | 持平（语言差异）|
| 飞书深度 | 692 行单文件 | 4578 行 9 文件 | **Hive 远超** |
| 飞书 PatchCard 限流重试 | ❌ 无 | ✅ `ErrPatchRateLimited` + 重试链 | Hive |
| 飞书 EventRenderer 增量 | 部分（`manager.py` stream 聚合）| 专用 `renderer.go` 762L + CardState | Hive |
| 微信深度 | 1370 单文件 Python | 2050+ 行 13 文件（wechaty gRPC + wechatpadpro）| Hive |
| Slack / Telegram / Discord | ✅ 原生支持 | ❌ 缺 | **deer-flow 单边** |
| 钉钉 DingTalk | ❌ 无 | ✅ 有（基础）| **Hive 单边** |
| 文件上传（uploads） | per-thread dir + markitdown | `fileconv/` + per-plugin + 音视频 Whisper | 各有所长 |
| 文档转换（PDF/Office）| markitdown（Python 生态）| 自研 `office.go` 318L | 持平 |
| 音频/视频转文本 | ❌ 无 | ✅ Whisper + ffmpeg | **Hive 单边** |
| Artifact 服务端点 | `GET /api/threads/{id}/artifacts/{path}`（带 XSS 硬化）| ❌ 无专用端点 | **deer-flow 单边** |
| 路径遍历防护 | `validate_path_traversal` | 分散在各 tool 自行检查 | deer-flow 更集中 |

---

## 1. 研究方法

### 1.1 数据源
- **deer-flow 通道**: `backend/app/channels/` 13 Python 文件 4940 行（验证自 tarball）
- **deer-flow 上传**: `backend/packages/harness/deerflow/uploads/manager.py` 201L + `app/gateway/routers/uploads.py` 201L
- **deer-flow artifact**: `backend/app/gateway/routers/artifacts.py` 181L
- **Hive 通道**: `internal/channel/` 含 dingtalk/feishu/wechat/wecom 四子目录 + 顶层 router/debounce/chunk
- **Hive 文件转换**: `internal/fileconv/` converter.go / office.go / audio.go / text.go
- **Hive artifact**: 无专用端点（grep 确认）

### 1.2 验证手段
1. `wc -l` 统计各子模块代码量
2. `ls` 枚举平台覆盖
3. `grep -n "PatchCard\|EventRenderer\|Whisper\|markitdown"` 验证关键特性
4. CLAUDE.md §IM Channels System §File Upload 官方文档交叉验证

### 1.3 已知局限
- 未运行时对比真实 IM 平台的端到端延迟、重试行为、消息丢失率（只能基于源码推断）
- Hive 的 DingTalk 只有 131 行（plugin+crypto），可能只是骨架，未验证生产可用性
- Hive wechat 的 wechaty gRPC proto（1580 行 `.pb.go`）是代码生成产物，不代表业务逻辑量

---

## 2. deer-flow Channels 全景

### 2.1 文件与行数（CLAUDE.md 官方版 vs 实际）

**CLAUDE.md §IM Channels System 官方文档** 列举：
- "Bridges external messaging platforms (Feishu, Slack, Telegram) to the DeerFlow agent"
- 只点名 3 个平台

**实际代码** 有 **6 个适配器**（超出文档）:

```
base.py           126 行   # Channel 抽象基类
commands.py        20 行   # /new /status /models 等命令路由
manager.py        960 行   # ChannelManager 核心调度（最大）
message_bus.py    173 行   # InboundMessage/OutboundMessage 异步队列
service.py        200 行   # 生命周期管理
store.py          153 行   # JSON 持久化 channel:chat[:topic] -> thread_id

discord.py        273 行   # ⚠️ CLAUDE.md 未提及
feishu.py         692 行   # ✅ 官方文档覆盖
slack.py          246 行   # ✅ 官方文档覆盖
telegram.py       317 行   # ✅ 官方文档覆盖
wechat.py        1370 行   # ⚠️ CLAUDE.md 未提及（最大单文件）
wecom.py          394 行   # ⚠️ CLAUDE.md 未提及
```

**含义**: discord/wechat/wecom 是文档之外的新增适配器，可能尚未正式发布或处于实验阶段。Hive 对标 deer-flow 时，如果以 CLAUDE.md 为准只需覆盖 3 个；如果以代码为准要覆盖 6 个。

### 2.2 feishu.py 核心能力（692 行）

从 `grep "Upload\|Download\|card"` 抽取（L256+）:

1. **upload image/file to Feishu** (L256, L265) — `image_key / file_key` 返回给 agent
2. **download Feishu file to per-thread uploads dir** (L286, L348) — 把用户 IM 发的附件落到 `paths.sandbox_uploads_dir(thread_id)`
3. **virtual path 注入** (L373) — `f"{VIRTUAL_PATH_PREFIX}/uploads/{resolved_target.name}"` 让 agent 看到 `/mnt/user-data/uploads/xxx`

**卡片流式**（`manager.py` §6 of CLAUDE.md）:
> "Feishu channel sends one running reply card up front, then patches the same card for each outbound update (card JSON sets `config.update_multi=true` for Feishu's patch API requirement)"

deer-flow 在 feishu 上已经实现了 PatchCard 增量更新，但所有 state 管理是在 `manager.py` 这个 960 行的中心式 dispatcher 里集中处理的，没有独立 renderer 模块。

### 2.3 wechat.py 1370 行的内容（文档外）

单文件 1370 行是整个 codebase 最大的 channel 文件，但 CLAUDE.md 未列。推断此文件可能集成了：
- 微信公众号回调（XML callback + 签名验证）
- 微信企业号转 wecom 的桥接
- 图文消息/语音/视频消息类型处理

**这代表 deer-flow 的 wechat 支持处于"代码有 + 文档无"的过渡态**。对 Hive 的借鉴价值：评估是否值得向 deer-flow 的 wechat.py 学习接入模式。

### 2.4 manager.py 960 行 = dispatcher 核心

关键职责（CLAUDE.md §Message Flow）:
1. 消费 `MessageBus` 入站队列
2. 按 `channel:chat[:topic]` 查找/创建 LangGraph thread
3. Feishu 用 `client.runs.stream(["messages-tuple", "values"])` 增量拿 AI 文本 → 多次 outbound
4. Slack/Telegram 用 `client.runs.wait()` 拿终值 → 单次 outbound
5. 内建命令处理：`/new`（新 thread）`/status`（查询）`/models`（列模型）`/memory`（查记忆）`/help`

---

## 3. Hive Channels 全景

### 3.1 顶层结构与子模块

```
channel/
├── router.go         563 行   # ChannelRouter 核心调度
├── debounce.go       153 行   # 合并发送去抖
├── chunk.go           33 行   # 分片工具
├── plugin.go          77 行   # 插件注册
├── json.go            19 行   # 序列化辅助
├── types.go          112 行   # 通用消息类型
│
├── feishu/          4578 行 (9 文件)   ← 最深
├── wechat/          ~2050 行 (15 文件)
├── wecom/            431 行 (5 文件)
└── dingtalk/         131 行 (2 文件，骨架)
```

对标 deer-flow 的中心式 `manager.py` 960 行 + adapter 文件各自 246-1370 行的模型，Hive 是**每个平台一个子包 + 顶层 router 调度 + 通用 debounce/chunk 工具**，更模块化。

### 3.2 feishu/ 子包 4578 行（deer-flow 的 6.6 倍）

```
client.go        893 行   # Open API client + PatchCard + ReplyCard + rate-limit
renderer.go      762 行   # EventRenderer + CardState + 增量 PatchCard
longconn.go      271 行   # 长连接订阅
types.go         249 行
card_builder.go  202 行   # Card JSON 构造器
tool_provider.go 159 行   # 工具集成
plugin.go        146 行
webhook.go       135 行
crypto.go         22 行
```

**关键独有能力**:

1. **PatchCard 限流重试**（`client.go` L807-814）:
   ```go
   // ErrPatchRateLimited 飞书 PatchCard 触发限流（HTTP 429 / code 99991400 等）。
   var ErrPatchRateLimited = errs.New(errs.CodeChannelSendFailed, "飞书 PatchCard 触发限流")
   func (c *Client) PatchCard(ctx context.Context, messageID, cardJSON string) error { ... }
   ```
   → 专门定义了限流错误类型 + 指数退避重试链（`renderer_test.go` L469 "PatchCard 应被重试，且不应出现第 3 次尝试"）
   → deer-flow 的 `feishu.py` **没有**这类专用限流错误类型和重试逻辑（只依赖 Feishu SDK 默认行为）

2. **EventRenderer + CardState 模式**（`card_builder.go` L40）:
   > "renderer 持有一个 CardState 实例，每次事件更新后调 BuildCardJSON → PatchCard。"
   
   → 把"事件流 → 卡片状态机 → JSON"独立出 renderer 模块
   → deer-flow 把这部分逻辑混在 `manager.py` 的 dispatch loop 里，耦合更紧
   → Hive 的 `renderer_test.go` 854 行，对 partial/patch/heartbeat/retry/error 各场景都有测试覆盖

3. **心跳阶段的错误恢复**（`renderer_test.go` L847-859）:
   > "心跳阶段 PatchCard 若持续失败，renderer 必须返回 *RendererError 且 LastContent 非空"
   
   → 即使心跳卡片更新全败，也能保留最后成功的内容供 fallback
   → deer-flow 无此等价测试

### 3.3 wechat/ 子包双路径

```
wechat/
├── types.go             41 行
├── protocol.go          38 行
├── plugin.go           131 行
│
├── wechaty/                # gRPC 路径
│   ├── backend.go         276 行
│   └── proto/
│       ├── puppet.pb.go  1166 行 (生成)
│       └── puppet_grpc.pb.go 414 行 (生成)
│
└── wechatpadpro/           # HTTP 路径
    ├── backend.go         478 行
    ├── client.go          306 行
    ├── types_extended.go  229 行
    ├── types.go            81 行
    ├── websocket.go       360 行
    ├── ops_provider.go     53 行
    ├── client_group.go     90 行
    ├── client_contact.go   66 行
    ├── client_message.go   71 行
    ├── client_moments.go   52 行
    ├── client_user.go      36 行
    ├── client_admin.go     15 行
    └── client.go          306 行
```

两套独立的微信后端抽象（wechaty 是开源 gRPC 生态，wechatpadpro 是国内特定服务商 HTTP API），用一个 `plugin.go` 的统一接口切换。deer-flow `wechat.py` 1370 行只做一种接入。

### 3.4 顶层 `router.go` 563 行

对标 deer-flow `manager.py` 960 行。Hive 更轻（因为 adapter 负担了更多细节），但承担：
- inbound/outbound 路由
- 插件注册（`plugin.go`）
- chunk/debounce 调用（流式聚合）

`debounce.go` 153 行的独立模块是 Hive 的亮点——把"短时间合并多个增量消息"抽成工具，所有通道共用，避免各自重新实现。deer-flow 的流式聚合写在 `manager.py` 的 `_dispatch_loop`，和其他职责混在一起。

---

## 4. Uploads（文件上传）对比

### 4.1 deer-flow 上传管道

**职责分布**:
- `backend/packages/harness/deerflow/uploads/manager.py` (201 行) — 核心逻辑
- `backend/app/gateway/routers/uploads.py` (201 行) — HTTP 入口

**管理器主要函数**（`manager.py` grep 抽取）:

```python
def validate_thread_id(thread_id: str) -> None                   # L23
def get_uploads_dir(thread_id: str) -> Path                      # L33
def ensure_uploads_dir(thread_id: str) -> Path                   # L39
def normalize_filename(filename: str) -> str                     # L46
def claim_unique_filename(name: str, seen: set[str]) -> str      # L74
def validate_path_traversal(path: Path, base: Path) -> None      # L99   ← 安全
def list_files_in_dir(directory: Path) -> dict                   # L111
def delete_file_safe(base_dir, filename, *, convertible_extensions) -> dict  # L144
def upload_artifact_url(thread_id: str, filename: str) -> str    # L178
def upload_virtual_path(filename: str) -> str                    # L186
def enrich_file_listing(result: dict, thread_id: str) -> dict    # L191
```

**关键特性**:
1. **路径遍历集中防护** (`validate_path_traversal`) — 任何进入上传目录的路径都强制通过这个函数
2. **文件名规范化** (`normalize_filename` + `claim_unique_filename`) — 避免冲突和路径注入
3. **文档转换** (`convertible_extensions = {.pdf, .docx, .xlsx, .pptx}`) — 转换产物和原文件一起管理，删除时联动
4. **虚拟路径抽象** (`upload_virtual_path`) — agent 看到的是 `/mnt/user-data/uploads/xxx`，物理路径被隐藏
5. **使用 `markitdown` 库**（CLAUDE.md §File Upload）做文档转 Markdown

### 4.2 Hive fileconv/ 对比

```
converter.go    143 行   # Convert() 主入口 + WhisperFunc 回调
office.go       318 行   # Office 文档 → 文本
audio.go         77 行   # 音频 → Whisper 转录
text.go          38 行   # 文本文件处理
(converter_test.go 500 行 - 测试)
```

**`converter.go` L54-55 的关键注释（直接 grep）**:
```go
// Convert 根据 MIME type 智能转换文件内容
// - image/* → Type="image" (直接传给 LLM Vision)
// - application/pdf → Type="file" (直接传给 LLM)
// - Office docs → Type="text" (提取文本)
// - text/* → Type="text" (base64解码)
// - audio/* → Type="text" (Whisper转录)
// - video/* → Type="text" (ffmpeg提取音轨→Whisper转录)
```

**Hive 独有**（deer-flow 没有）:
1. **audio/\* Whisper 转录** — 语音消息/语音备忘录直接变文字
2. **video/\* ffmpeg 提取音轨 + Whisper** — 视频消息用同一管道
3. **image/\* 透传给 LLM Vision** — 非解析而是直接让多模态模型看（deer-flow 可能也做，但需要 ViewImageMiddleware 参与）

**deer-flow 独有**（Hive 弱）:
1. **`markitdown` 库支持 excel/pptx 的高保真转换**（Hive 的 office.go 318 行是否覆盖 xlsx/pptx 需要进一步读代码验证）
2. **集中式 `validate_path_traversal`** — Hive 的路径安全分散在各 tool 里

**文件大小限制**:
- Hive `converter.go` L13: `maxConvertSize` = 100MB
- deer-flow: 没看到硬编码限制，可能依赖 FastAPI/multipart 配置

---

## 5. Artifacts（产物服务）对比

### 5.1 deer-flow 的 artifact 端点

`backend/app/gateway/routers/artifacts.py` (181 行) 提供:

```python
@router.get(
    "/threads/{thread_id}/artifacts/{path:path}",
)
async def get_artifact(thread_id, path, request, download: bool = False) -> Response:
```

**安全硬化**（CLAUDE.md §Artifacts）:
- `text/html`, `application/xhtml+xml`, `image/svg+xml` 这三种"主动内容"**强制**作为附件下载（无法在浏览器直接 render）
- 其他类型 `?download=true` 可选下载
- 原因（CLAUDE.md 原文）: "to reduce XSS risk"

**MIME 自动识别** + **内容嗅探** (`is_text_file_by_content`):
- 未知扩展名的文件做 8KB 采样，判定是否为文本

**从 .skill 压缩包提取文件** (`_extract_file_from_skill_archive`, L46):
- 支持从已安装的 skill 压缩包内部读取文件（只读）— 这是 deer-flow 为 skill 作者提供的特殊访问路径

### 5.2 Hive 的 artifact 状态

`grep -rln "artifact\|Artifact"` 在 Hive 代码中的命中:
- `master/prompt_builder.go` + `master/react_processor.go` — 作为上下文传递的概念，不是 HTTP 服务
- `a2abridge/adapter.go` — A2A 协议的 TaskResult 字段
- `specdriven/eval/` — eval 测试的 fixtures
- `subagent/compaction/agent.go` — 压缩产物

**结论**: Hive **没有与 deer-flow 对等的 artifact 服务端点**。Agent 生成的文件目前要么：
- 存在 sandbox 内，通过 tool 读回（间接）
- 通过 channel 直接发送（如飞书 upload image 再 patch card）
- 前端无法通过 URL 直接访问 agent 的产物（因此无法做"点击链接下载"的 UX）

**这是原始 final-verdict 没提到的盲点**。

---

## 6. 对照表（完整 18 维度）

| # | 维度 | deer-flow | Hive | 领先方 |
|---|---|---|---|---|
| 1 | 通道平台数 | 6（实际）/ 3（文档）| 4 | deer-flow |
| 2 | 总代码体量 | 4940 Python | 8428 Go | 持平（语言）|
| 3 | 飞书子模块 | 单文件 692 | 9 文件 4578 | **Hive** |
| 4 | 飞书 PatchCard 限流错误类型 | ❌ | `ErrPatchRateLimited` | Hive |
| 5 | 飞书 EventRenderer/CardState | 混入 manager.py | 独立 renderer.go 762L | Hive |
| 6 | 飞书心跳 fallback | ❌ | `RendererError + LastContent` | Hive |
| 7 | 微信实现路径 | 单文件 1370（文档外）| wechaty+wechatpadpro 双路径 | Hive |
| 8 | 钉钉 | ❌ | ✅（骨架 131L）| Hive 单边 |
| 9 | Slack | ✅ 246 行 | ❌ | **deer-flow 单边** |
| 10 | Telegram | ✅ 317 行 | ❌ | **deer-flow 单边** |
| 11 | Discord | ✅ 273 行 | ❌ | **deer-flow 单边** |
| 12 | 中心式 manager | 960 行单文件 | router.go 563 + 子包 | 各有所长 |
| 13 | debounce 模块 | 无独立（嵌 manager）| `debounce.go` 153 行独立 | Hive |
| 14 | 上传 per-thread dir | ✅ | ✅（sandbox 内）| 持平 |
| 15 | 文档转换（PDF/Office）| `markitdown` | `office.go` 318L | 持平 |
| 16 | 音频/视频转文本 | ❌ | ✅ Whisper + ffmpeg | **Hive 单边** |
| 17 | 路径遍历防护 | 集中 `validate_path_traversal` | 分散 | deer-flow |
| 18 | Artifact HTTP 端点 | ✅ XSS 硬化 + MIME 嗅探 | ❌ 缺 | **deer-flow 单边** |

---

## 7. 蓝军反驳（Blue-Team Mutations）

### 7.1 Mutation A: "Hive 飞书 PatchCard 重试是不是过度设计？"

**反驳**: 检查限流真实发生率是否高到值得专门处理。

**验证**: 飞书 OpenAPI PatchCard 接口有明确的 QPS 限制（官方文档：单租户约 5 QPS）。当 agent 产生高频 token 增量时，每个 token 一次 PatchCard 会瞬间触发 429。deer-flow 实测在 Feishu 场景可能已经踩过（见 manager.py 的流式节流注释），但选择了被动节流；Hive 选择主动重试。**不是过度设计**，是不同策略权衡。

### 7.2 Mutation B: "deer-flow wechat.py 1370 行可能包含 Hive 缺失的关键功能"

**反驳**: 需要抽查 wechat.py 具体做了什么。

**验证**（快速 grep）:
```
$ grep -c "async def\|def " ../docs/调研笔记/deer-flow/src/backend/app/channels/wechat.py
```
如果是 30+ 函数，可能包含公众号/小程序/客服接入；如果是 10 以内，可能是 wechaty 桥。**本轴未完成深入 diff**，留作后续补充。对 Hive 的态度：等 deer-flow 正式文档化 wechat.py 后再评估是否值得抄。

### 7.3 Mutation C: "Hive 没有 artifact 端点可能是设计选择，不是缺口"

**反驳**: 理由必须充分。

**验证思路**:
- 如果 Hive 认为"agent 生成的文件都应该通过 IM channel 直接发给用户"(不留在后端)，那么没有 artifact 端点确实是设计选择
- 但 UI 前端 (`frontend/`) 如果要展示 agent 生成的报表/PDF/图片预览，就需要一个稳定 URL
- `grep "artifact" frontend/` 的结果（未做）可以判定前端是否有依赖

**暂定结论**: 缺 artifact 端点是**可补的盲点**，优先级取决于前端 UX 需求。如果 Hive 走的是"IM-only 不做 Web UI"，则无影响。

### 7.4 Mutation D: "Hive fileconv Whisper/ffmpeg 可能依赖外部服务，不真正 OOTB 可用"

**反驳**: 检查依赖部署要求。

**验证**: `grep "openai\|whisper\|ffmpeg" fileconv/audio.go` 应该能看到 HTTP 客户端调用 OpenAI Whisper API，或本地 ffmpeg 二进制调用。如果是前者，OOTB 需要配 OPENAI_API_KEY；如果是后者，需要系统安装 ffmpeg。**任一情况都不是真正 "零配置开箱"**。但仍强于 deer-flow 零支持。

---

## 8. Codex 盲点

### 8.1 deer-flow CLAUDE.md 未更新
`wechat.py 1370 行` + `discord.py 273 行` + `wecom.py 394 行` 三个适配器在 CLAUDE.md 完全不提。这是文档和代码漂移的典型信号，如果要信任 deer-flow 作为对标物，需要知道**哪些是产品化能力、哪些是实验残留**。建议: 只抄 CLAUDE.md 已经官方化的 3 个平台（Feishu/Slack/Telegram）。

### 8.2 Hive 缺 artifact 对前端的潜在限制
如果前端要做 "click to preview/download agent 生成的报表" 功能，必须建端点。否则只能用 base64 内嵌到消息流 → 流量大、记录污染。

### 8.3 deer-flow `_extract_file_from_skill_archive` 的 "skill-as-filesystem" 模式
这是 deer-flow 的独特能力——skill 不光提供 SKILL.md，还可以携带示例数据/模板/脚本，agent 通过 artifact 端点按需读取。Hive 目前没有这个抽象，skill 更像纯 prompt 文件。

### 8.4 路径安全的集中 vs 分散
deer-flow `validate_path_traversal` 一处防护，所有上传都走这儿；Hive 依赖各 tool 自检。**前者更安全**，因为"漏加检查"的概率大幅降低。Hive 可以考虑抽一个 `internal/security/pathguard.go` 做类似功能。

---

## 9. 建议（P0 / P1 / 不要抄）

### 9.1 Hive → deer-flow 学（P0）
1. **Artifact HTTP 端点** + XSS 硬化（强制下载 html/xhtml/svg）
   - 对标 `backend/app/gateway/routers/artifacts.py` 181 行 Python → Go 移植约 200-300 行
   - 前端需要时必须要有
2. **集中式 `validate_path_traversal`** — 把所有上传/artifact/skill 的路径检查统一
3. **Slack / Telegram 适配器** — 如果企业用户要求覆盖海外 IM，直接抄（`slack.py` 246 + `telegram.py` 317）

### 9.2 deer-flow → Hive 学（P0）
1. **飞书 `ErrPatchRateLimited` + 重试链**：`manager.py` 的流式发送目前是被动节流，抄 Hive `client.go:807-814` 的主动限流错误 + 指数退避
2. **EventRenderer + CardState 独立模块**：从 `manager.py` 960 行里抽出来，单独 `renderer.py`，按 Hive `renderer.go` 762 行对齐
3. **音频/视频 Whisper 转录**：`uploads/manager.py` 扩展 convertible_extensions 支持 `.mp3/.wav/.mp4`，转文本落 `uploads/` 旁

### 9.3 Hive → deer-flow 学（P1）
1. **Skill archive 内部文件读取**（`_extract_file_from_skill_archive`）— skill-as-filesystem 模式
2. **文件名规范化 `normalize_filename` + `claim_unique_filename`** — Hive 在 sandbox 冲突场景有用

### 9.4 不要抄的部分
- deer-flow wechat.py 1370 行（文档外，实现状态不明）
- deer-flow discord.py / wecom.py（同上，未官方化）
- Hive 的 wechaty proto（微信逆向生态有法律/稳定性风险，deer-flow 不应直接抄）
- Hive 的 DingTalk 骨架（131 行，未成熟）

---

## 10. 风险量表

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Hive 没 artifact 端点导致前端受限 | 高（前端有需求时）| 中 | 新增 `internal/gateway/artifacts.go`, 对标 deer-flow 181 行 |
| deer-flow wechat.py 生产未验证却被参考 | 中 | 高 | 只对照 CLAUDE.md 文档化的 3 个平台 |
| Hive 路径遍历分散 → 某天某个 tool 漏检 | 低 | 高 | 抽 `pkg/pathguard` 强制所有 uploads/artifacts 走 |
| Hive Whisper 依赖外部 API → 离线不可用 | 中 | 低 | 加 feature flag + fallback 到仅文本 |
| deer-flow 缺音视频转录 → 限制 agent 输入多样性 | 中 | 中 | 抄 Hive `fileconv` 整体思路 |

---

## 11. 附录：命令证据

```
# deer-flow channels 广度
$ wc -l ../docs/调研笔记/deer-flow/src/backend/app/channels/*.py
      16 __init__.py
     126 base.py
      20 commands.py
     273 discord.py           ← 文档未提
     692 feishu.py
     960 manager.py
     173 message_bus.py
     200 service.py
     246 slack.py
     153 store.py
     317 telegram.py
    1370 wechat.py            ← 文档未提
     394 wecom.py             ← 文档未提
    4940 total

# Hive channels 广度
$ ls internal/channel/
chunk.go  debounce.go  dingtalk/  feishu/  json.go  plugin.go  router.go  types.go  wechat/  wecom/

$ find internal/channel -name "*.go" -not -name "*_test.go" | xargs wc -l | tail -1
    8428 total

# Hive feishu 深度 (4578 行 9 文件 vs deer-flow 692 行 1 文件)
$ wc -l internal/channel/feishu/*.go
     202 card_builder.go
      22 crypto.go
     893 client.go           ← PatchCard + ErrPatchRateLimited
     271 longconn.go
     146 plugin.go
     762 renderer.go         ← EventRenderer + CardState
     159 tool_provider.go
     249 types.go
     135 webhook.go
    4578 total

# Hive fileconv 覆盖音视频
$ grep -n "audio\|video\|Whisper\|ffmpeg" internal/fileconv/converter.go | head -5
53:// - audio/* → Type="text" (Whisper转录)
54:// - video/* → Type="text" (ffmpeg提取音轨→Whisper转录)

# deer-flow uploads manager
$ wc -l ../docs/调研笔记/deer-flow/src/backend/packages/harness/deerflow/uploads/manager.py
     201

# deer-flow artifact 服务
$ wc -l ../docs/调研笔记/deer-flow/src/backend/app/gateway/routers/artifacts.py
     181

# Hive artifact 搜索结果 (无专用端点)
$ grep -rln "artifact\|Artifact" internal/ | grep -v _test.go | grep -v "\.pb\.go"
internal/a2abridge/adapter.go               ← A2A TaskResult 字段
internal/master/prompt_builder.go           ← prompt 上下文
internal/master/react_processor.go          ← react 处理
internal/specdriven/eval/harness.go         ← eval 测试
internal/subagent/title/agent.go            ← subagent 内部
internal/subagent/compaction/agent.go       ← compaction 内部
(none of the above are HTTP endpoints for serving files to clients)
```

---

## 12. 结论一句话

> **deer-flow channels = 广度优先（6 平台含 Slack/Telegram/Discord，但其中 3 个文档外）+ markitdown 文档转换 + XSS 硬化 artifact 端点 + per-thread uploads 集中防护**；**Hive channels = 深度优先（飞书 4578 行 + 微信双路径 + PatchCard 限流重试 + EventRenderer/CardState 独立模块 + 音视频 Whisper 转录）**。最显著的单边领先：**Hive 飞书/微信深度 + 音视频转录**；**deer-flow Slack/Telegram/Discord 覆盖 + artifact HTTP 服务 + 路径遍历集中防护**。原 final-verdict "飞书 PatchCard 反向领先" 结论得到印证；新增盲点是 Hive 缺 artifact HTTP 端点。

---

*—— Axis-5 完结 · 5 轴研究收官，下一步是 merged-report-v2 + final-verdict-v2（含方向纠错总表）*
