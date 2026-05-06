# M0 · 完整能力盘点(Feature Matrix)

> 目标:站在"**ground-up 构建一个生产级飞书 bot**"的全局视角,穷尽所有可能的能力面,逐条决策"做 / 不做 / 缓做",避免"发现一个再补一个"的打补丁开发。

## 0. 铁律(顶部全局约束)

所有模块文档必须遵守以下三条,**任何新增代码和新能力都要过这三条**。

### 0.1 SDK-only,禁止手写 OpenAPI

**所有**与飞书服务端的交互必须走 `github.com/larksuite/oapi-sdk-go/v3`(当前 v3.5.3)。

- ✅ 允许:`client.Im.Message.Create(...)` / `client.Drive.File.Upload(...)` 等 SDK 方法
- ❌ 禁止:`net/http.NewRequest` 调 `open.feishu.cn` / `open.larksuite.com` 域名
- ❌ 禁止:粘贴 curl 等价 Go 代码做 POST
- ❌ 禁止:引入第三方飞书 wrapper 库

SDK 暂不支持某能力时,按序决策:**升级 SDK 版本 → 等官方发版 → 写进"不做"档并说明理由**。任何情况下**不自己写 HTTP 绕过**。

webhook 接收 HTTP(飞书→我们)不在此约束内。`client.go:440` 的 `https://open.feishu.cn/docx/{id}` 是拼给用户点击的前端 URL,不是 API 调用。

CI gate:
```bash
git diff origin/main -- 'internal/channel/feishu/**' \
  | grep -E '^\+.*"(open\.feishu\.cn|open\.larksuite\.com)/(open-apis|api)' \
  && echo "❌ OpenAPI 直调" && exit 1 || true
```

### 0.2 包依赖叶子化

`internal/imctx` 必须 stdlib-only,不得引入 `internal/master`/`internal/channel`/`internal/tools` 的任何东西。CI:
```bash
go list -deps ./internal/imctx/... | grep -E 'chef-guo/agents-hive/internal/(master|channel|tools)' \
  && echo "❌ imctx 反向依赖" && exit 1 || true
```

### 0.3 所有能力可热开关 + 可观测

每条**做**的能力必须同时提供:
- 一个 config flag(`FeishuConfig.<Module>.<Feature>Enabled`),支持 DB 热重载
- 至少一个 metric(`feishu.<module>.<metric>`)
- 至少一条结构化日志(带 `platform=feishu` + `phase=<阶段>`)

## 1. SDK module 全景(v3.5.3,共 57 个 service)

按与 bot 的相关度分类。每个 module 后标注现状和分档。

### 1.1 核心 IM / 交互(必做基座)

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `im` | `im/v1` | 消息收发、卡片、会话管理、表情、撤回、已读、Pin 消息 | 🟡 部分 | **必做** |
| `cardkit` | `cardkit/v1` | v2 动态卡片(流式 PATCH) | 🟡 已用 renderer | **必做** |
| `auth` | `auth/v3` | tenant_access_token / app_access_token 获取与刷新 | ✅ SDK 自动管理 | **必做**(已内置) |
| `event` | `event/*` | 事件订阅/分发基础设施 | ✅ 已用 larkws/larkcallback | **必做** |

### 1.2 身份与组织

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `contact` | `contact/v3` | 用户/部门/用户组查询 | 🟡 仅 GetUser | **必做** |
| `authen` | `authen/v1` | 用户登录态(OAuth)换 open_id | ❌ | 可选 |
| `tenant` | `tenant/v2` | 当前租户信息 | ❌ | 应做 |
| `directory` | `directory/v1` | 新版组织架构 | ❌ | 可选(与 contact 重叠) |
| `admin` | `admin/v1` | 管理员 API(统计/权限) | ❌ | 不做 |
| `passport` | `passport/v1` | 登录态管理 | ❌ | 不做 |

### 1.3 云文档生态(Agent 读写的核心战场)

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `docx` | `docx/v1` | 新版文档读写、block 粒度 | 🟡 只读 | **必做**(读写全) |
| `docs` | `docs/*` | 旧版文档 | ❌ | 不做(飞书官方迁移中) |
| `drive` | `drive/v1,v2` | 文件元数据、上传下载、权限 | ❌ | **必做** |
| `sheets` | `sheets/v3` | 电子表格读写 | 🟡 工具桩有,SDK 未用 | **必做** |
| `bitable` | `bitable/v1` | 多维表读写 | 🟡 部分 | **必做** |
| `wiki` | `wiki/v1,v2` | 知识库 space/node 操作 | 🟡 token 解析 | **必做**(读取) |
| `board` | `board/v1` | 画板 | ❌ | 不做 |

### 1.4 日程与会议

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `calendar` | `calendar/v4` | 日程 CRUD / 空闲时间 | 🟡 工具桩 | 应做 |
| `vc` | `vc/v1` | 视频会议 / 预约 / 纪要 | ❌ | 可选 |
| `meeting_room` | `meeting_room/v1` | 会议室 | ❌ | 不做 |
| `minutes` | `minutes/v1` | 妙记(会议转文字) | ❌ | 可选 |

### 1.5 工作流

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `task` | `task/v1,v2` | 任务管理 | 🟡 工具桩 | 应做 |
| `approval` | `approval/v4` | 审批单 CRUD / 订阅 | 🟡 工具桩 | 应做(与 HITL 联动) |
| `helpdesk` | `helpdesk/v1` | 服务台工单 | ❌ | 可选 |
| `mail` | `mail/v1` | 邮件 | ❌ | 不做(一期) |

### 1.6 AI 增强(谨慎启用,避免与 Agent 能力重复)

| module | 路径 | 能力 | 当前 | 目标档 |
|---|---|---|---|---|
| `optical_char_recognition` | `.../v1` | OCR | ❌ | 不做(Agent 自己的 vision 工具更通用) |
| `speech_to_text` | `.../v1` | 语音转文字 | ❌ | 可选(收到语音消息时转) |
| `translation` | `.../v1` | 翻译 | ❌ | 不做(LLM 自己翻) |
| `document_ai` | `.../v1` | 文档智能(发票/合同识别) | ❌ | 不做 |
| `aily` | `aily/v1` | 智能伙伴(飞书自家 AI) | ❌ | 不做(与本产品竞争) |
| `baike` / `lingo` | | 百科/词典 | ❌ | 不做 |
| `face_detection` / `human_authentication` | | 人脸识别 | ❌ | 不做(合规敏感) |

### 1.7 人力资源域(与 bot 几乎无关)

`hire` / `corehr` / `ehr` / `payroll` / `compensation` / `performance` / `okr` / `attendance` / `moments` / `workplace` / `report` / `apaas` / `mdm` / `acs` / `personal_settings` / `verification` / `security_and_compliance` / `ext` / `block`

→ **全部不做**。除非用户明确要求某个具体场景(如"查本月考勤 → attendance"),否则不纳入 bot tool 集,避免 tool 列表爆炸污染 agent prompt。

## 2. 非 SDK 维度(跨 module 的系统面)

SDK 只给"飞书 API 能做什么",bot 的**完整性**还需要下面这些面。**这部分是之前 7 模块漏得最多的地方**。

### 2.1 安全与合规(🔥 当前完全缺失)

| 能力 | 当前 | 目标 |
|---|---|---|
| encrypt_key 消息体解密 | ❌ 只支持明文 | **必做**(飞书企业版强制加密时不解就挂) |
| verification_token 签名校验 | 🟡 larkcallback 内置但未配 | **必做** |
| PII 日志脱敏 | ❌ 当前日志打真名/open_id 混着 | **必做** |
| 多租户隔离 | ❌ 假设单 app 单租户 | 应做 |
| 审计日志(谁调了什么工具) | ❌ | 应做 |
| Bot 权限撤销感知 | ❌ | 应做 |
| Content sanitizer(禁止 bot 发 @所有人) | ❌ | **必做** |

**新建模块 `M8 · 安全合规`**(下面 §4 列)。

### 2.2 可靠性与一致性(🔥 当前严重不足)

| 能力 | 当前 | 目标 |
|---|---|---|
| `event_id` 跨实例去重 | ⚠️ 仅单进程内存 LRU | **必做**(多副本部署会重复投递) |
| at-least-once 投递 + 幂等 | ⚠️ 依赖单进程 | **必做** |
| longconn 断线期间消息补偿 | ⚠️ SDK 默认丢 | 应做(拉 `im.chat.messages` 补 gap) |
| 飞书服务降级时本地排队 | ❌ | 应做 |
| 消息顺序保证(同 chat 内) | ⚠️ debounce 保证,但多副本下无序 | 应做 |
| 发送失败持久化重放队列 | ❌ | 可选 |

**新建模块 `M9 · 可靠性与一致性`**。

### 2.3 消息形态完整性(当前只认 4 种)

| 类型 | 当前解析 | 目标 |
|---|---|---|
| `text` | ✅ | 保留 |
| `post`(富文本) | ❌ | **必做**(飞书默认富文本发送就是 post) |
| `image` | 🟡 占位符 | **必做** |
| `file` | 🟡 占位符 | **必做** |
| `audio`(语音) | ❌ | 应做(+ STT 可选) |
| `media`(视频) | ❌ | 应做(占位符 + 下载) |
| `sticker`(表情包) | ❌ | 可选(记 metric 忽略) |
| `location` | ❌ | 可选 |
| `share_chat` / `share_user` | ❌ | 应做(Agent 感知群/人分享) |
| `merge_forward`(合并转发) | ❌ | **必做**(重要信息载体) |
| `system`(系统消息) | ❌ | 应做(审批通过、会议邀请) |
| `interactive`(卡片) | ✅ 发送 | 保留 |

**归入 M1 扩展,新增 `parser.go` 分类型处理表**。

### 2.4 会话治理

| 能力 | 当前 | 目标 |
|---|---|---|
| 群话题 threads(飞书 thread_id) | ❌ | 应做 |
| 一个群绑定不同 agent | ❌ 当前按 chat_id 映射 session,agent 唯一 | 应做 |
| `/reset` / `/debug` / `/status` 管理命令 | ⚠️ 部分在 `handleLegacyCommand` | **必做**(补齐) |
| 命令 ACL(只有群管能 /reset) | ❌ | 应做 |
| Chat 禁用(把 bot 设为"不响应"某群) | ❌ | 应做 |
| 群名/成员变更感知 | ❌ | 可选 |
| 群聊转让 | ❌ | 不做 |

**新建模块 `M10 · 会话治理与命令`**。

### 2.5 运维与灰度

| 能力 | 当前 | 目标 |
|---|---|---|
| Bot 健康自检端点(GET /health 返 bot 身份/token 状态) | ❌ | **必做** |
| 按 chat_id 灰度(whitelist/blacklist) | ❌ | 应做 |
| 按用户灰度 | ❌ | 可选 |
| 按 tenant 灰度 | ❌ | 应做(多租户配套) |
| tenant_access_token 刷新失败告警 | ❌ SDK 静默重试 | **必做** |
| Debug mode(每条消息 echo 原始 payload 到 ops 群) | ❌ | 应做 |

**归入 M7 可观测性扩展**。

### 2.6 国际化

| 能力 | 当前 | 目标 |
|---|---|---|
| Feishu(中国)vs Lark(海外)endpoint 切换 | ⚠️ SDK 支持但未暴露 config | 应做 |
| 时区处理(日程/cron) | ⚠️ 硬编码本地 | 应做 |
| 多语言真名(zh_CN / en_US / ja_JP 字段) | ❌ | 可选 |
| i18n 卡片模板 | ❌ | 可选 |

**归入 M5 身份扩展 + M6 push 模板扩展**。

### 2.7 多租户(ISV 场景)

| 能力 | 当前 | 目标 |
|---|---|---|
| 一个 Hive 实例服务多个飞书 app | ❌ 假设单 app | 应做 |
| tenant_key 路由到正确 client | ❌ | 应做 |
| 每 tenant 独立 config/quota | ❌ | 应做 |

**新建模块 `M11 · 多租户(ISV)`**(一期可 skip,但接口预留)。

## 3. 全能力分档汇总

### 3.1 必做(Phase 1-5,不做 bot 不可用或严重缺陷)

- **M1 消息摄取**:完整形态解析(text/post/image/file/merge_forward/system/share)、引用识别、CDATA
- **M2 卡片回调**:修 HITL 死链
- **M3 生命周期**:bot_added/removed/p2p_created + 欢迎卡 + session 清理
- **M4 消息投递**:image/file upload/download(走 `drive.File` + `im.Image`)、ratelimit、retry
- **M5 身份**:bot open_id 同步、UserCache、真名回填
- **M7 可观测**:metric 闭环、feature flag、Reloadable、健康自检端点
- **M8 安全合规**:encrypt_key 解密、signature 校验、PII 脱敏、content sanitizer(禁 @所有人)
- **M9 可靠性**:event_id 分布式去重(DB 或 Redis)、longconn 断线补偿
- **M10 会话治理**:`/reset` `/debug` `/status` 命令完整化

### 3.2 应做(Phase 6-8,体验完整化)

- **M6 主动推送**:HTTP API + 模板 + scheduled
- **云文档深度**:docx 写入、sheets 读写、bitable 读写、wiki 节点读
- **M3 扩展**:群名/成员变更感知
- **审批/任务/日程**:工具层对接 approval / task / calendar(Agent 按需调)
- **灰度**:按 chat/tenant whitelist
- **多语言真名字段**

### 3.3 可选(Phase 9+,按需启用)

- `speech_to_text`(收到语音转字)
- `minutes`(妙记接入)
- `helpdesk`(服务台工单)
- `vc`(会议预约)
- `authen`(用户 OAuth 登录)

### 3.4 明确不做(写进来防止未来"再来一轮重构")

| 能力 | 理由 |
|---|---|
| `aily` / `document_ai` / `baike` / `lingo` / `translation` | 与 Agent 自身能力重复或竞争 |
| `face_detection` / `human_authentication` | 合规风险高,场景不刚需 |
| `hire` / `corehr` / `ehr` / `payroll` / `compensation` / `performance` / `okr` / `attendance` | HR 域,bot 场景不直接 |
| `moments` / `workplace` / `report` | 与 IM 交互场景远 |
| `mdm` / `acs` / `personal_settings` / `admin` / `passport` / `verification` | 管理员/基础设施域 |
| `apaas` / `board` / `ext` / `block` / `security_and_compliance` | 过于专用或特殊 |
| `docs`(旧版文档) | 飞书官方迁移中,统一用 `docx` |
| `meeting_room` | 与 bot 交互场景远 |
| `optical_char_recognition` | Agent 自己的 multimodal/vision tool 更通用 |
| `mail` | 一期不接邮件,留给后续 MailChannel 独立通道 |
| Feishu OpenAPI 直调 | 铁律 §0.1 |
| 第三方 Feishu wrapper | 铁律 §0.1 |
| 自建重试/token 管理绕过 SDK | SDK 已管理,绕过造成分叉 |
| 整个 Plugin 重建式配置更新 | 会丢 session/dedup/cache 状态 |

## 4. 基于 Matrix 的新模块切分(相对旧 7 模块的调整)

原 7 模块 + 新增 4 个系统模块 = **11 模块**:

| 模块 | 文档 | 是否新增 |
|---|---|---|
| M0 | `00-feature-matrix.md` | **新增**(本文) |
| M1 消息摄取 | `01-inbound-parse.md` | 已有,将扩展消息形态分类表 |
| M2 交互回调 | `02-interaction-callback.md` | 已有 |
| M3 生命周期 | `03-lifecycle.md` | 已有 |
| M4 消息投递 | `04-outbound.md` | 已有,扩展 drive.File upload |
| M5 身份 | `05-identity.md` | 已有,扩展多语言 |
| M6 主动推送 | `06-push.md` | 已有 |
| M7 可观测性 | `07-observability.md` | 已有,加健康端点 |
| **M8 安全合规** | `08-security.md` | **新增** |
| **M9 可靠性** | `09-reliability.md` | **新增** |
| **M10 会话治理** | `10-session-governance.md` | **新增** |
| **M11 多租户(ISV)** | `11-multi-tenant.md` | **新增**(一期接口预留) |

## 5. Gap 深度清单(按文件维度)

### 5.1 client.go

| 缺失能力 | SDK 方法 | 所属 M |
|---|---|---|
| 图片上传 | `larkim.NewCreateImageReqBuilder` + `client.Im.Image.Create` | M4 |
| 文件上传 | `larkim.NewCreateFileReqBuilder` + `client.Im.File.Create` | M4 |
| 消息资源下载 | `client.Im.MessageResource.Get` | M4 |
| 发送图片消息 | `client.Im.Message.Create` with `msg_type=image` | M4 |
| 发送文件消息 | `client.Im.Message.Create` with `msg_type=file` | M4 |
| 撤回 bot 自己发的消息 | `client.Im.Message.Delete` | M10 |
| Pin 消息 | `client.Im.Pin.Create` | 可选 |
| 获取群聊信息 | `client.Im.Chat.Get` | 🟡 部分,扩展 |
| 列出群成员 | `client.Im.ChatMembers.Get` | M10 ACL |
| 获取部门成员 | `client.Contact.Department.Children` + `User.FindByDepartment` | M5 扩展 |
| Drive 文件元数据 | `client.Drive.File.Meta` | 云文档深度 |
| Drive 上传 | `client.Drive.File.UploadAll` | M4(大文件走 drive) |
| Drive 下载 | `client.Drive.File.Download` | M4 |
| Docx 块级读取 | `client.Docx.DocumentBlock.List` | 云文档深度 |
| Docx 创建文档 | `client.Docx.Document.Create` | 云文档深度 |
| Sheets 范围读 | `client.Sheets.SpreadsheetSheet.Get` + `SheetRowColumn.Get` | 云文档深度 |
| Sheets 范围写 | `client.Sheets.SpreadsheetSheet.BatchUpdate` | 云文档深度 |
| Wiki space list | `client.Wiki.Space.List` | 云文档深度 |
| Wiki node 拉取 | `client.Wiki.SpaceNode.List` | 云文档深度 |
| Calendar 查空闲 | `client.Calendar.Freebusy.List` | Phase 6 |
| Approval 查实例 | `client.Approval.Instance.Get` | Phase 6 |
| Task 创建 | `client.Task.Task.Create` | Phase 6 |

### 5.2 webhook.go

| 缺失 | 所属 M |
|---|---|
| encrypt_key 解密 | **M8** |
| signature 校验(larkcallback 已内置,需 wire 进来) | **M8** |
| event_type 分发(非 message 事件不能全丢) | M2 / M3 |
| event_id 分布式去重 | **M9** |

### 5.3 longconn.go

| 缺失 | 所属 M |
|---|---|
| `OnP2CardActionTriggerV1` | M2 |
| `OnP2ChatMemberBotAddedV1/DeletedV1` | M3 |
| `OnP1P2PChatCreatedV1` 非空实现 | M3 |
| 断线 gap 拉取补偿 | **M9** |
| tenant_access_token 刷新失败告警 | M7 / M8 |

### 5.4 plugin.go

| 缺失 | 所属 M |
|---|---|
| 消息形态分类表(post/audio/video/share/merge_forward) | M1 扩展 |
| content sanitizer(禁 @所有人) | **M8** |

### 5.5 router.go

| 缺失 | 所属 M |
|---|---|
| tenant_key 路由 | **M11** |
| chat 级禁用 flag | **M10** |
| 命令 ACL 钩子 | **M10** |

## 6. 接下来要做的文档改动

1. `00-feature-matrix.md`(本文)✅ 写完
2. `README.md` — 顶部加 §0 铁律 + 更新 11 模块导航表
3. `ROADMAP.md` — 按 11 模块重切 Phase,调整优先级(M8 安全、M9 可靠前置到 Phase 1-2)
4. `08-security.md` — **新建**
5. `09-reliability.md` — **新建**
6. `10-session-governance.md` — **新建**
7. `11-multi-tenant.md` — **新建**(接口预留,一期不实现)
8. `01-inbound-parse.md` — 补消息形态分类表(post/audio/video/share/merge_forward/system)
9. `04-outbound.md` — 补 drive.File 上传(大文件 >10MB 走 drive 非 im.File)
10. `05-identity.md` — 补多语言真名字段 / Feishu vs Lark endpoint
11. `07-observability.md` — 加健康自检端点、token 刷新失败告警

## 7. 完整性验收标准

本 matrix 的**存在目的**是"以后不再重构"。验收它自己合格的标准:

- [ ] SDK 全部 57 个 module 每个都有明确分档(必做/应做/可选/不做)
- [ ] "不做"档每条都有理由,不是"忘了列"
- [ ] 安全 / 可靠性 / 会话治理 / 多租户 / 国际化 这 5 个跨模块维度**至少每条列一行**
- [ ] 每个"必做"能力都有明确 SDK 方法名(避免"找不到就手写 HTTP")
- [ ] 每条"不做"都可以被未来提问"为什么这个没做"时用一句话回答

以上 5 条打勾 → 本 matrix 可以 freeze,后续 session 基于它推进而非重切。
