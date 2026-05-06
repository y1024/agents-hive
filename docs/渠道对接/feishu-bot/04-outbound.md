# M4 · 消息投递(Outbound)

> 当前 M4 主链路已基本落地:图/文件 upload/download、全局+per-chat 限流、429/5xx 退避重试均已实现。剩余主要是补更完整的 metric 面和文档收口。

## 1. 现状

| 项 | 位置 | 状态 |
|---|---|---|
| Text / 分片 | `plugin.go:59-78` | ✅ 18000 byte 分片 |
| Markdown → 卡片 | `plugin.go:105-108` + `BuildMarkdownCard` | ✅ |
| 原始卡片 | `plugin.go:99-101` | ✅ |
| Reply 失败 fallback | `plugin.go` | ✅ reply 失败后 fallback 到 SendMessage,底层 API 统一带退避重试 |
| PatchCard throttle | `FeishuRendererConfig.ThrottleMs` | ✅ 300ms 默认 |
| EventRenderer 流式 | `renderer.go` | ✅ |
| Legacy 一次性 Send 降级 | `router.go:280` | ✅ |
| Upload/SendImage | `client.go` + `tool_provider.go` | ✅ |
| Upload/SendFile | `client.go` + `tool_provider.go` | ✅ |
| Download 消息里的图片/文件 | `download.go` + `client.go` | ✅ 按需下载 |
| 飞书限流(50 msg/s/app) | `ratelimit.go` | ✅ global + per-chat |
| 429/5xx 指数退避 | `retry.go` | ✅ |

## 2. 方案

### 2.1 Client 新增 API

```go
// internal/channel/feishu/client.go

// UploadImage 上传图片,返回 image_key。
// imageType: "message" | "avatar"(消息图片 vs 头像,飞书参数)
func (c *Client) UploadImage(ctx context.Context, data []byte, imageType string) (string, error)

// UploadFile 上传文件(opus/mp4/pdf 等),返回 file_key。
// fileType: "opus" | "mp4" | "pdf" | "doc" | "xls" | "ppt" | "stream"
func (c *Client) UploadFile(ctx context.Context, data []byte, filename, fileType string) (string, error)

// DownloadMessageResource 拉取消息里的附件字节。
// resourceType: "image" | "file"
// 飞书返回二进制流,需调用方处理 size / mime。
func (c *Client) DownloadMessageResource(ctx context.Context, messageID, fileKey, resourceType string) (
    data []byte, mimeType string, err error)

// SendImage / SendFile 封装 SendMessage(msg_type=image/file)
func (c *Client) SendImage(ctx context.Context, chatID, imageKey string) error
func (c *Client) SendFile(ctx context.Context, chatID, fileKey string) error
```

### 2.2 `OutboundMessage.MsgType` 扩展

```go
// internal/channel/types.go
const (
    MsgTypeText        MsgType = "text"
    MsgTypeMarkdown    MsgType = "markdown"
    MsgTypeInteractive MsgType = "interactive"
    // 新增:
    MsgTypeImage       MsgType = "image"     // Content = image_key(已 upload)
    MsgTypeFile        MsgType = "file"      // Content = file_key
)
```

`plugin.go buildMessageContent` 扩分支;`sendOne` 对 image/file 直接走 `SendImage`/`SendFile`。

### 2.3 Agent 工具补齐

`tools/feishu_tools.go` 增加 action:

| action | 参数 | 说明 |
|---|---|---|
| `upload_image` | `data`(base64) | 返 image_key |
| `upload_file` | `data`(base64), `filename`, `file_type` | 返 file_key |
| `send_image` | `chat_id`, `image_key` | 发图 |
| `send_file` | `chat_id`, `file_key` | 发文件 |
| `download_message_resource` | `message_id`, `file_key`, `resource_type` | 返 base64 字节 + mime |

**文件大小限制**:飞书 API 上限(图片 10MB、文件 30MB)。工具层做 size 预检,超限返 clear error。

### 2.4 消息摄取侧补齐(M1 交叉)

M1 §6 的 `ExtractInboundMessage` 对 `image`/`file` 类型目前只返占位符。一期**不**自动下载(延迟敏感),但 prompt prefix 里要提示 Agent:

```xml
<attachments>
  <attachment kind="image" file_key="xxx" message_id="yyy"/>
  <attachment kind="file" file_key="zzz" file_name="report.pdf" message_id="yyy"/>
</attachments>
```

Agent 看到后若需要处理,主动调 `download_message_resource` 拉字节再走 OCR/分析工具。这样**按需拉取,避免 push-scenario 把每张贴图都耗带宽**。

### 2.5 限流器

飞书文档:单 app `im.message.send` 上限 50 QPS。

在 `Client` 加 chat 级 token bucket + 全局 bucket:

```go
// internal/channel/feishu/ratelimit.go
type RateLimiter struct {
    global *tokenBucket        // 全局 50/s
    perChat sync.Map           // chatID → *tokenBucket (10/s)
}

func (r *RateLimiter) Wait(ctx context.Context, chatID string) error {
    if err := r.global.Wait(ctx); err != nil { return err }
    b, _ := r.perChat.LoadOrStore(chatID, newTokenBucket(10, 10))
    return b.(*tokenBucket).Wait(ctx)
}
```

`Client.SendMessage` / `ReplyMessage` / `SendCard` / `PatchCard` 入口先 `Wait(ctx, chatID)`。

**renderer 的 PatchCard throttle 与此叠加**:renderer 侧的 throttle 防止同一消息短时间内过多 PATCH,rate limiter 防止跨消息的总 QPS 突刺。两者互补。

### 2.6 重试策略

飞书 API 错误码关键分类:

| code | 含义 | 重试? |
|---|---|---|
| 99991400 / 99991401 / 99991402 | 限流 | ✅ 指数退避,max 3 次 |
| 5xx | 服务端错误 | ✅ 同上 |
| 230xx / 231xx / 234xx | 业务错(消息已失效 / 权限 / 参数) | ❌ 立即失败 |
| 0 | 成功 | — |

统一封装:

```go
// internal/channel/feishu/retry.go
func withRetry(ctx context.Context, op func() error, logger *zap.Logger) error {
    var err error
    for attempt := 0; attempt < 3; attempt++ {
        err = op()
        if err == nil { return nil }
        if !isRetryable(err) { return err }
        backoff := time.Duration(100*math.Pow(2, float64(attempt))) * time.Millisecond // 100, 200, 400ms
        select {
        case <-time.After(backoff):
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    return fmt.Errorf("after 3 attempts: %w", err)
}
```

`Client.sendMessageRaw` 等核心操作全部包一层 `withRetry`。

### 2.7 Reply / Send 降级链

保留现有 `plugin.go:113-125` 的 reply → send fallback,叠加 withRetry:

```
ReplyMessage(withRetry)  ── fail ──>  SendMessage(withRetry)  ── fail ──>  返 error
                                                                         ├─> Router 走 NotifyError 回一条纯文本
                                                                         └─> EventRenderer 若已渲染部分内容,RendererError.LastContent 兜底发送
```

## 3. Config

```go
type FeishuConfig struct {
    ... // 现有字段
    Outbound FeishuOutboundConfig `json:"outbound,omitempty"`
}

type FeishuOutboundConfig struct {
    // 全局每秒最大发送数(不配默认 45,给突刺留 10% 余量)
    GlobalQPS int `json:"global_qps,omitempty"`
    // 单 chat 每秒最大发送数(默认 8)
    PerChatQPS int `json:"per_chat_qps,omitempty"`
    // 最大重试次数(默认 3)
    MaxRetries int `json:"max_retries,omitempty"`
    // 是否启用二进制传输工具(image/file upload/download,默认 false,开了才注册工具)
    EnableBinaryTransfer bool `json:"enable_binary_transfer,omitempty"`
}
```

## 4. 测试

单测:

- Upload/Download API 对飞书返回格式解析正确
- withRetry 对限流码 3 次指数退避后放弃
- withRetry 对 230xx 立即失败(不重试)
- RateLimiter 并发 100 个 chatID 各发 50 条:全局 50/s 不突破
- SendImage 超限(> 10MB)预检失败
- OutboundMessage MsgType=Image 路径:走 SendImage 不走 SendMessage

集成:

- Agent 调 `upload_image` + `send_image` → 对端实际收到图片
- 用户发 PDF → Agent 调 `download_message_resource` 拿到字节 → OCR 工具分析
- 并发打 200 条消息:observed QPS ≤ 45,P99 延迟 < 2s

蓝军:

- 飞书服务端偶发 5xx:withRetry 吃掉,用户无感
- 限流 code=99991400 连续命中 3 次:warn 日志 + 降级到 NotifyError 给用户一个"发送受限"提示
- UploadFile 文件 header 伪造(实际不是声明的类型)→ 飞书 API 返错 → 立即失败 + clear error

## 5. 代码锚点

| 位置 | 作用 |
|---|---|
| `internal/channel/feishu/client.go:41` | `ReplyMessage` 入口,withRetry 包装点 |
| `internal/channel/feishu/client.go:61` | `SendMessage` 入口 |
| `internal/channel/feishu/client.go:814,842,865` | `PatchCard`/`ReplyCard`/`SendCard` 入口 |
| `internal/channel/feishu/client.go` (新文件) | `upload.go` / `download.go` / `ratelimit.go` / `retry.go` |
| `internal/channel/feishu/plugin.go:55-93` | `Send` 分支加 Image/File |
| `internal/channel/types.go:92-96` | `MsgType` 常量新增 |
| `internal/tools/feishu_tools.go:93-110` | action 枚举新增 5 个 |
| `internal/config/config.go:881` | `FeishuOutboundConfig` 新字段 |
